//go:build darwin || linux || windows

package cui

import (
	"bytes"
	"errors"
	"testing"

	platformterminal "github.com/aruzen/ariadne/internal/platform/terminal"
)

func TestDaemonStatusReportsStoppedWithoutAutoStart(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	socketPath := missingEndpoint(t)
	if err := Run([]string{"-socket", socketPath, "daemon", "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if stdout.String() != "stopped\n" || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestOpenRejectsNonTTYBeforeConnecting(t *testing.T) {
	var output bytes.Buffer
	err := Run([]string{"-socket", missingEndpoint(t), "open", "--", "sh"}, &output, &output)
	if !errors.Is(err, platformterminal.ErrNotTerminal) {
		t.Fatalf("open error = %v", err)
	}
}

func TestUnknownCommandDoesNotConnect(t *testing.T) {
	var output bytes.Buffer
	err := Run([]string{"-socket", missingEndpoint(t), "unknown"}, &output, &output)
	if err == nil || err.Error() != `unknown command "unknown"` {
		t.Fatalf("unknown command error = %v", err)
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
