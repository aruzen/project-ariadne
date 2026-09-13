package statefile

import (
	"testing"

	"github.com/aruzen/ariadne/internal/core"
)

func TestPanePresentationRoundTrip(t *testing.T) {
	snapshot := core.DefaultSnapshot()
	snapshot.Panes = []core.Pane{{
		ID: 1, WindowID: 1, Kind: core.PaneTool,
		Presentation: core.PanePresentation{Chrome: core.PaneChromeNone},
	}}
	snapshot.Windows[0].Layout = &core.LayoutNode{Kind: core.LayoutPane, PaneID: 1}
	snapshot.NextPaneID = 2
	data, err := Encode(snapshot)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	restored, err := Decode(data, DefaultMaxBytes)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got := restored.Panes[0].Presentation.Chrome; got != core.PaneChromeNone {
		t.Fatalf("Chrome = %q", got)
	}
}
