//go:build darwin || linux || windows

package cui

import (
	"io"
	"testing"

	ariadneconfig "github.com/aruzen/ariadne/internal/config"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/frontend/tui"
)

func TestParsePanePresentation(t *testing.T) {
	for _, test := range []struct {
		input string
		want  core.PaneChrome
	}{
		{input: "auto", want: core.PaneChromeAuto},
		{input: "border", want: core.PaneChromeBorder},
		{input: "none", want: core.PaneChromeNone},
	} {
		presentation, err := parsePanePresentation(test.input)
		if err != nil || presentation.Chrome != test.want {
			t.Fatalf("parsePanePresentation(%q) = %+v, %v", test.input, presentation, err)
		}
	}
	if _, err := parsePanePresentation("rounded"); err == nil {
		t.Fatal("parsePanePresentation accepted unknown chrome")
	}
}

func TestParseTUIOptions(t *testing.T) {
	options, err := parseTUIOptions([]string{"--pane-frame", "split"}, ariadneconfig.TUIOptions{PaneFrame: ariadneconfig.TUIFrameFull}, io.Discard)
	if err != nil {
		t.Fatalf("parseTUIOptions: %v", err)
	}
	if options.PaneFrame != tui.PaneFrameSplit {
		t.Fatalf("PaneFrame = %q", options.PaneFrame)
	}
	if _, err := parseTUIOptions([]string{"--pane-frame", "unknown"}, ariadneconfig.TUIOptions{PaneFrame: ariadneconfig.TUIFrameFull}, io.Discard); err == nil {
		t.Fatal("parseTUIOptions accepted unknown mode")
	}
}
