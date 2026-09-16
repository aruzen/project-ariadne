//go:build darwin || linux || windows

package cui

import (
	"bytes"
	"strings"
	"testing"
)

func TestEveryCommandHasHelp(t *testing.T) {
	for _, command := range []string{
		"init", "tui", "new", "open", "attach", "list", "delete", "restart", "run", "kill", "dismiss",
		"stash", "restore", "tool", "attention", "daemon",
	} {
		var output bytes.Buffer
		if err := WriteHelp([]string{command}, &output); err != nil {
			t.Fatalf("WriteHelp(%q): %v", command, err)
		}
		if !strings.Contains(output.String(), "Usage:\n") {
			t.Fatalf("help for %q has no usage: %q", command, output.String())
		}
	}
}

func TestHandleHelpIgnoresSpawnedCommandArguments(t *testing.T) {
	var output bytes.Buffer
	handled, err := HandleHelp([]string{"new", "--", "--help"}, &output)
	if handled || err != nil || output.Len() != 0 {
		t.Fatalf("handled=%t error=%v output=%q", handled, err, output.String())
	}
}

func TestUnknownHelpTopic(t *testing.T) {
	var output bytes.Buffer
	err := WriteHelp([]string{"unknown"}, &output)
	if err == nil || !strings.Contains(err.Error(), "ariadne --help") {
		t.Fatalf("WriteHelp error = %v", err)
	}
}
