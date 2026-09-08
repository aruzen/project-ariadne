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
	if err := Run(socketPath, []string{"daemon", "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if stdout.String() != "stopped\n" || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestOpenRejectsNonTTYBeforeConnecting(t *testing.T) {
	var output bytes.Buffer
	err := Run(missingEndpoint(t), []string{"open", "--", "sh"}, &output, &output)
	if !errors.Is(err, platformterminal.ErrNotTerminal) {
		t.Fatalf("open error = %v", err)
	}
}

func TestUnknownCommandDoesNotConnect(t *testing.T) {
	var output bytes.Buffer
	err := Run(missingEndpoint(t), []string{"unknown"}, &output, &output)
	if err == nil || err.Error() != `unknown command "unknown"` {
		t.Fatalf("unknown command error = %v", err)
	}
}

func TestDaemonCommandIsValidatedBeforeConnecting(t *testing.T) {
	var output bytes.Buffer
	for _, test := range []struct {
		arguments []string
		expected  string
	}{
		{arguments: []string{"daemon"}, expected: "daemon subcommand is required"},
		{arguments: []string{"daemon", "invalid"}, expected: `unknown daemon subcommand "invalid"`},
	} {
		err := Run(missingEndpoint(t), test.arguments, &output, &output)
		if err == nil || err.Error() != test.expected {
			t.Fatalf("Run(%q) error = %v, want %q", test.arguments, err, test.expected)
		}
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
