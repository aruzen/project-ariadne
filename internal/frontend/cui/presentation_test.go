//go:build darwin || linux || windows

package cui

import (
	"testing"

	"github.com/aruzen/ariadne/internal/core"
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
