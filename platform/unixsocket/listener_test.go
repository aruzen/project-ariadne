//go:build darwin || linux

package unixsocket

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testSocketPath(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "ariadne-socket-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return filepath.Join(directory, "run", "ariadned.sock")
}

func TestDefaultPathPrecedenceAndValidation(t *testing.T) {
	environment := map[string]string{"XDG_RUNTIME_DIR": "/runtime", "TMPDIR": "/temporary"}
	lookup := func(key string) (string, bool) {
		value, exists := environment[key]
		return value, exists
	}
	path, err := defaultPath(lookup, "/fallback", 42)
	if err != nil {
		t.Fatalf("defaultPath XDG: %v", err)
	}
	if path != "/runtime/ariadne/ariadned.sock" {
		t.Fatalf("XDG path = %q", path)
	}
	delete(environment, "XDG_RUNTIME_DIR")
	path, err = defaultPath(lookup, "/fallback", 42)
	if err != nil || path != "/temporary/ariadne/ariadned.sock" {
		t.Fatalf("TMPDIR path = %q, error=%v", path, err)
	}
	delete(environment, "TMPDIR")
	path, err = defaultPath(lookup, "/fallback", 42)
	if err != nil || path != "/fallback/ariadne-42/ariadned.sock" {
		t.Fatalf("fallback path = %q, error=%v", path, err)
	}
	environment["XDG_RUNTIME_DIR"] = "relative"
	if _, err := defaultPath(lookup, "/fallback", 42); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("relative XDG error = %v", err)
	}
}

func TestListenSecuresSocketChecksPeerAndCleansUp(t *testing.T) {
	path := testSocketPath(t)
	listener, err := Listen(path, os.Getuid())
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	directoryInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat directory: %v", err)
	}
	if directoryInfo.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode = %#o, want 0700", directoryInfo.Mode().Perm())
	}
	socketInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if socketInfo.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %#o, want 0600", socketInfo.Mode().Perm())
	}

	accepted := make(chan net.Conn, 1)
	acceptErrors := make(chan error, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			acceptErrors <- err
			return
		}
		accepted <- connection
	}()
	dialContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client, err := DialContext(dialContext, path, os.Getuid())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()
	select {
	case connection := <-accepted:
		_ = connection.Close()
	case err := <-acceptErrors:
		t.Fatalf("Accept: %v", err)
	case <-time.After(time.Second):
		t.Fatal("Accept timed out")
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("Close removed socket before Cleanup: %v", err)
	}
	if err := listener.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket remains after Cleanup: %v", err)
	}
}

func TestListenRejectsRunningDaemonAndRecoversStaleSocket(t *testing.T) {
	path := testSocketPath(t)
	first, err := Listen(path, os.Getuid())
	if err != nil {
		t.Fatalf("first Listen: %v", err)
	}
	if _, err := Listen(path, os.Getuid()); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Listen error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// The first listener deliberately leaves its path until Cleanup. It is now
	// a confirmed same-owner stale socket and may be replaced.
	second, err := Listen(path, os.Getuid())
	if err != nil {
		t.Fatalf("recover stale Listen: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := second.Cleanup(); err != nil {
		t.Fatalf("second Cleanup: %v", err)
	}
	if err := first.Cleanup(); !errors.Is(err, os.ErrNotExist) && !errors.Is(err, ErrSocketReplaced) && err != nil {
		t.Fatalf("first Cleanup: %v", err)
	}
}

func TestCleanupDoesNotRemoveReplacement(t *testing.T) {
	path := testSocketPath(t)
	listener, err := Listen(path, os.Getuid())
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove socket: %v", err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatalf("write replacement: %v", err)
	}
	if err := listener.Cleanup(); !errors.Is(err, ErrSocketReplaced) {
		t.Fatalf("Cleanup error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "replacement" {
		t.Fatalf("replacement was removed: data=%q err=%v", data, err)
	}
}

func TestListenRejectsOverlongPath(t *testing.T) {
	path := "/" + string(make([]byte, maxSocketPathBytes))
	if _, err := Listen(path, os.Getuid()); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("Listen error = %v", err)
	}
}
