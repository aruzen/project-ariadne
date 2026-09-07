//go:build windows

package main

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsPlatformDefaults(t *testing.T) {
	t.Setenv("COMSPEC", `C:\Windows\System32\cmd.exe`)
	if got := defaultShell(); len(got) != 1 || got[0] != `C:\Windows\System32\cmd.exe` {
		t.Fatalf("defaultShell = %q", got)
	}
	if got := daemonExecutableName(); got != "ariadned.exe" {
		t.Fatalf("daemonExecutableName = %q", got)
	}
	command := exec.Command("ariadned.exe")
	configureDetachedProcess(command)
	if command.SysProcAttr == nil || !command.SysProcAttr.HideWindow ||
		command.SysProcAttr.CreationFlags&(windows.CREATE_NEW_PROCESS_GROUP|windows.DETACHED_PROCESS) !=
			windows.CREATE_NEW_PROCESS_GROUP|windows.DETACHED_PROCESS {
		t.Fatalf("detached SysProcAttr = %+v", command.SysProcAttr)
	}
}
