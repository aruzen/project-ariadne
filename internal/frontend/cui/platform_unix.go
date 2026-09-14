//go:build darwin || linux

package cui

import (
	"os"
	"os/exec"
	"syscall"
)

func configureDetachedProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func defaultShell() []string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return []string{shell}
	}
	return []string{"/bin/sh"}
}

func defaultEditor() []string {
	if editor := os.Getenv("VISUAL"); editor != "" {
		return []string{editor}
	}
	if editor := os.Getenv("EDITOR"); editor != "" {
		return []string{editor}
	}
	return []string{"vi"}
}
