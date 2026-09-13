package core

import "testing"

func TestPanePresentationValidation(t *testing.T) {
	snapshot := DefaultSnapshot()
	snapshot.Panes = []Pane{{
		ID: 1, WindowID: 1, Kind: PaneFixed,
		Presentation: PanePresentation{Chrome: PaneChromeNone},
	}}
	snapshot.Windows[0].Layout = &LayoutNode{Kind: LayoutPane, PaneID: 1}
	snapshot.NextPaneID = 2
	if err := ValidateSnapshot(snapshot); err != nil {
		t.Fatalf("ValidateSnapshot borderless pane: %v", err)
	}
	snapshot.Panes[0].Presentation.Chrome = PaneChrome("unknown")
	if err := ValidateSnapshot(snapshot); err == nil {
		t.Fatal("ValidateSnapshot accepted unknown Pane chrome")
	}
}
