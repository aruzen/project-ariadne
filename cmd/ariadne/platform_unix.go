//go:build darwin || linux

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func configureDetachedProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func daemonExecutableName() string { return "ariadned" }

func daemonCandidateUsable(info os.FileInfo) bool {
	return !info.IsDir() && info.Mode()&0o111 != 0
}

func defaultShell() []string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return []string{shell}
	}
	return []string{"/bin/sh"}
}
