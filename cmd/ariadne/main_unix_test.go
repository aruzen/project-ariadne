//go:build darwin || linux

package main

import (
	"bytes"
	"testing"
)

func TestDaemonStatusReportsStoppedWithoutAutoStart(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	socketPath := "/tmp/ariadne-cli-status-definitely-missing.sock"
	if err := run([]string{"-socket", socketPath, "daemon", "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if stdout.String() != "stopped\n" || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestCLIValueHelpers(t *testing.T) {
	paneID, err := exactlyOnePaneID([]string{"42"})
	if err != nil || paneID != 42 {
		t.Fatalf("Pane ID = %d, %v", paneID, err)
	}
	if _, err := exactlyOnePaneID([]string{"0"}); err == nil {
		t.Fatal("zero Pane ID accepted")
	}
	if got := formatArgv([]string{"sh", "-c", "echo value", "plain"}); got != `sh -c "echo value" plain` {
		t.Fatalf("formatArgv = %q", got)
	}
}
