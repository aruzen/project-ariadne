//go:build windows

package localipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const pipePrefix = `\\.\pipe\`

type pipeListener struct {
	net.Listener
	peerSID string
}

func DefaultEndpoint() (string, error) {
	sid, err := currentProcessSID()
	if err != nil {
		return "", err
	}
	return pipePrefix + "ariadne-" + sid, nil
}

func Listen(endpoint string) (Listener, error) {
	if !validPipeEndpoint(endpoint) {
		return nil, ErrInvalidEndpoint
	}
	sid, err := currentProcessSID()
	if err != nil {
		return nil, err
	}
	listener, err := winio.ListenPipe(endpoint, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")",
		InputBufferSize:    64 << 10,
		OutputBufferSize:   64 << 10,
	})
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_PIPE_BUSY) ||
			errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) ||
			errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("localipc: listen named pipe: %w", err)
	}
	return &pipeListener{Listener: listener, peerSID: sid}, nil
}

func (listener *pipeListener) Accept() (net.Conn, error) {
	for {
		connection, err := listener.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if err := verifyPeerSID(connection, true, listener.peerSID); err == nil {
			return connection, nil
		}
		_ = connection.Close()
	}
}

func (listener *pipeListener) Cleanup() error { return nil }

func DialContext(ctx context.Context, endpoint string) (net.Conn, error) {
	if ctx == nil || !validPipeEndpoint(endpoint) {
		return nil, ErrInvalidEndpoint
	}
	connection, err := winio.DialPipeContext(ctx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("localipc: dial named pipe: %w", err)
	}
	sid, err := currentProcessSID()
	if err == nil {
		err = verifyPeerSID(connection, false, sid)
	}
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	return connection, nil
}

func IsAbsent(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_PATH_NOT_FOUND)
}

func validPipeEndpoint(endpoint string) bool {
	return len(endpoint) > len(pipePrefix) && strings.EqualFold(endpoint[:len(pipePrefix)], pipePrefix) &&
		!strings.ContainsRune(endpoint, 0)
}

func currentProcessSID() (string, error) {
	return processSID(windows.CurrentProcess())
}

func processSID(process windows.Handle) (string, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return "", fmt.Errorf("localipc: open process token: %w", err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("localipc: get token user: %w", err)
	}
	return user.User.Sid.String(), nil
}

func verifyPeerSID(connection net.Conn, accepted bool, expected string) error {
	handleOwner, ok := connection.(interface{ Fd() uintptr })
	if !ok {
		return ErrInvalidPeer
	}
	var processID uint32
	var err error
	if accepted {
		err = windows.GetNamedPipeClientProcessId(windows.Handle(handleOwner.Fd()), &processID)
	} else {
		err = windows.GetNamedPipeServerProcessId(windows.Handle(handleOwner.Fd()), &processID)
	}
	if err != nil {
		return fmt.Errorf("localipc: query peer process: %w", err)
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, processID)
	if err != nil {
		return fmt.Errorf("localipc: open peer process: %w", err)
	}
	defer windows.CloseHandle(process)
	actual, err := processSID(process)
	if err != nil {
		return err
	}
	if actual != expected {
		return ErrInvalidPeer
	}
	return nil
}
