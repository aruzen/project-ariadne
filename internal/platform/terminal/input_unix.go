//go:build darwin || linux

package terminal

import (
	"context"
	"golang.org/x/sys/unix"
	"io"
	"os"
)

// ReadContext leaves the descriptor and its terminal mode intact. There must be
// only one reader of the descriptor, including while cancellation completes.
func ReadContext(ctx context.Context, file *os.File, buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	fd := int(file.Fd())
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		count, err := unix.Poll(poll, 50)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return 0, err
		}
		if count == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n, err := unix.Read(fd, buffer)
		if err == unix.EINTR {
			continue
		}
		if n == 0 && err == nil {
			err = io.EOF
		}
		return n, err
	}
}
