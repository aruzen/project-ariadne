//go:build windows

package cui

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func configureDetachedProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
		HideWindow:    true,
	}
}

func defaultShell() []string {
	if shell := os.Getenv("COMSPEC"); shell != "" {
		return []string{shell}
	}
	return []string{"cmd.exe"}
}
