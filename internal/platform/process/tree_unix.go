//go:build darwin || linux

package process

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func prepare(command *exec.Cmd) (func() error, func(), error) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	kill := func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	command.Cancel = kill
	return func() error { return nil }, func() { _ = kill() }, nil
}
