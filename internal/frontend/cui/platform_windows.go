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

func defaultEditor() []string {
	if editor := os.Getenv("VISUAL"); editor != "" {
		return []string{editor}
	}
	if editor := os.Getenv("EDITOR"); editor != "" {
		return []string{editor}
	}
	return []string{"notepad.exe"}
}
