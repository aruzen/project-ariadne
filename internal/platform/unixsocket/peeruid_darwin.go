//go:build darwin

package unixsocket

import (
	"net"

	"golang.org/x/sys/unix"
)

func peerUID(connection *net.UnixConn) (int, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return 0, err
	}
	var uid int
	var controlErr error
	if err := raw.Control(func(fileDescriptor uintptr) {
		credential, err := unix.GetsockoptXucred(int(fileDescriptor), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			controlErr = err
			return
		}
		uid = int(credential.Uid)
	}); err != nil {
		return 0, err
	}
	return uid, controlErr
}
