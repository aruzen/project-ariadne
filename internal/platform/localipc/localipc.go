// Package localipc provides authenticated per-user local IPC for the daemon.
package localipc

import (
	"errors"
	"net"
)

var (
	ErrInvalidEndpoint = errors.New("localipc: invalid endpoint")
	ErrInvalidPeer     = errors.New("localipc: invalid peer")
	ErrAlreadyRunning  = errors.New("localipc: daemon already running")
)

// Listener is a net.Listener whose endpoint resources can be cleaned after
// the daemon has stopped accepting connections.
type Listener interface {
	net.Listener
	Cleanup() error
}
