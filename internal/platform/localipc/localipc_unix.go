//go:build darwin || linux

package localipc

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"

	"github.com/aruzen/ariadne/internal/platform/unixsocket"
)

func DefaultEndpoint() (string, error) {
	return unixsocket.DefaultPath()
}

func Listen(endpoint string) (Listener, error) {
	listener, err := unixsocket.Listen(endpoint, os.Getuid())
	if errors.Is(err, unixsocket.ErrAlreadyRunning) {
		return nil, ErrAlreadyRunning
	}
	return listener, err
}

func DialContext(ctx context.Context, endpoint string) (net.Conn, error) {
	connection, err := unixsocket.DialContext(ctx, endpoint, os.Getuid())
	if err != nil {
		return nil, err
	}
	return connection, nil
}

func IsAbsent(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED)
}
