//go:build windows

package terminal

import (
	"context"
	"golang.org/x/sys/windows"
	"io"
	"os"
	"runtime"
	"time"
)

var cancelSynchronousIO = windows.NewLazySystemDLL("kernel32.dll").NewProc("CancelSynchronousIo")

// ReadContext cancels this reader's synchronous console/pipe read without
// closing the shared input handle or affecting another thread's I/O.
func ReadContext(ctx context.Context, file *os.File, buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	type result struct {
		n   int
		err error
	}
	thread := make(chan windows.Handle, 1)
	done := make(chan result, 1)
	release := make(chan struct{})
	defer close(release)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		current, _ := windows.GetCurrentThread()
		process := windows.CurrentProcess()
		var handle windows.Handle
		err := windows.DuplicateHandle(process, current, process, &handle, 0, false, windows.DUPLICATE_SAME_ACCESS)
		thread <- handle
		if err != nil {
			done <- result{err: err}
			return
		}
		defer windows.CloseHandle(handle)
		var count uint32
		err = ctx.Err()
		if err == nil {
			err = windows.ReadFile(windows.Handle(file.Fd()), buffer, &count, nil)
		}
		if count == 0 && err == nil {
			err = io.EOF
		}
		done <- result{int(count), err}
		// Keep this OS thread reserved until the caller stops issuing cancellation.
		<-release
	}()
	handle := <-thread
	select {
	case r := <-done:
		return r.n, r.err
	case <-ctx.Done():
		for {
			select {
			case <-done:
				return 0, ctx.Err()
			default:
			}
			_, _, _ = cancelSynchronousIO.Call(uintptr(handle))
			select {
			case <-done:
				return 0, ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
}
