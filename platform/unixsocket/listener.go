//go:build darwin || linux

// Package unixsocket provides the secured per-user Unix listener used by the
// Ariadne daemon.
package unixsocket

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

var (
	ErrInvalidPath       = errors.New("unixsocket: invalid path")
	ErrInvalidOwner      = errors.New("unixsocket: invalid owner")
	ErrAlreadyRunning    = errors.New("unixsocket: daemon already running")
	ErrStaleNotConfirmed = errors.New("unixsocket: cannot confirm stale socket")
	ErrSocketReplaced    = errors.New("unixsocket: socket path was replaced")
)

type Listener struct {
	listener *net.UnixListener
	path     string
	uid      int
	bound    fs.FileInfo

	cleanupOnce sync.Once
	cleanupErr  error
}

func Listen(path string, uid int) (*Listener, error) {
	if path == "" || !filepath.IsAbs(path) || len(path) >= maxSocketPathBytes || uid < 0 {
		return nil, ErrInvalidPath
	}
	if err := ensureDirectory(filepath.Dir(path), uid); err != nil {
		return nil, err
	}
	listener, err := listen(path)
	if err != nil && errors.Is(err, syscall.EADDRINUSE) {
		if err := removeConfirmedStale(path, uid); err != nil {
			return nil, err
		}
		listener, err = listen(path)
	}
	if err != nil {
		return nil, fmt.Errorf("unixsocket: listen: %w", err)
	}
	failed := true
	defer func() {
		if failed {
			_ = listener.Close()
			_ = os.Remove(path)
		}
	}()
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("unixsocket: chmod: %w", err)
	}
	bound, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("unixsocket: stat bound socket: %w", err)
	}
	if !isOwnedSocket(bound, uid) {
		return nil, ErrInvalidOwner
	}
	failed = false
	return &Listener{listener: listener, path: path, uid: uid, bound: bound}, nil
}

func listen(path string) (*net.UnixListener, error) {
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	listener.SetUnlinkOnClose(false)
	return listener, nil
}

func ensureDirectory(path string, uid int) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("unixsocket: create directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("unixsocket: stat directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || ownerUID(info) != uid {
		return ErrInvalidOwner
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("unixsocket: chmod directory: %w", err)
	}
	return nil
}

func removeConfirmedStale(path string, uid int) error {
	initial, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("unixsocket: stat conflicting path: %w", err)
	}
	if !isOwnedSocket(initial, uid) {
		return ErrInvalidOwner
	}
	connection, dialErr := net.DialTimeout("unix", path, 100*time.Millisecond)
	if dialErr == nil {
		_ = connection.Close()
		return ErrAlreadyRunning
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, fs.ErrNotExist) {
		return fmt.Errorf("%w: %v", ErrStaleNotConfirmed, dialErr)
	}
	current, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if !os.SameFile(initial, current) || !isOwnedSocket(current, uid) {
		return ErrSocketReplaced
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("unixsocket: remove stale socket: %w", err)
	}
	return nil
}

func isOwnedSocket(info fs.FileInfo, uid int) bool {
	return info.Mode()&os.ModeSocket != 0 && ownerUID(info) == uid
}

func ownerUID(info fs.FileInfo) int {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return -1
	}
	return int(stat.Uid)
}

func (listener *Listener) Accept() (net.Conn, error) {
	for {
		connection, err := listener.listener.AcceptUnix()
		if err != nil {
			return nil, err
		}
		uid, err := peerUID(connection)
		if err == nil && uid == listener.uid {
			return connection, nil
		}
		_ = connection.Close()
	}
}

// Close stops Accept but deliberately leaves the pathname until Cleanup, so
// the daemon can remove it as the final graceful-shutdown step.
func (listener *Listener) Close() error {
	return listener.listener.Close()
}

func (listener *Listener) Addr() net.Addr {
	return listener.listener.Addr()
}

func (listener *Listener) Cleanup() error {
	listener.cleanupOnce.Do(func() {
		current, err := os.Lstat(listener.path)
		if errors.Is(err, fs.ErrNotExist) {
			return
		}
		if err != nil {
			listener.cleanupErr = err
			return
		}
		if !os.SameFile(listener.bound, current) {
			listener.cleanupErr = ErrSocketReplaced
			return
		}
		if err := os.Remove(listener.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			listener.cleanupErr = err
		}
	})
	return listener.cleanupErr
}
