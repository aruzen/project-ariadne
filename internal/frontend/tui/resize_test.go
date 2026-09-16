package tui

import (
	"testing"

	"github.com/aruzen/ariadne/internal/core"
)

func TestLayoutPathChoosesNearestDirectionalAncestor(t *testing.T) {
	root := core.LayoutNode{
		Kind: core.LayoutSplit, SplitID: 1, Direction: core.SplitHorizontal, Weights: []uint32{1, 1},
		Children: []core.LayoutNode{
			{
				Kind: core.LayoutSplit, SplitID: 2, Direction: core.SplitVertical, Weights: []uint32{1, 1},
				Children: []core.LayoutNode{{Kind: core.LayoutPane, PaneID: 1}, {Kind: core.LayoutPane, PaneID: 2}},
			},
			{Kind: core.LayoutPane, PaneID: 3},
		},
	}
	path, found := layoutPath(root, Rect{W: 81, H: 25}, 2, true, nil)
	if !found || len(path) != 2 || path[0].node.SplitID != 1 || path[1].node.SplitID != 2 || path[1].childIndex != 1 {
		t.Fatalf("path = %+v, found=%v", path, found)
	}
	if path[0].rect.W != 81 || path[1].rect.W != 40 || path[1].rect.H != 25 {
		t.Fatalf("path rects = %+v", path)
	}
}

func TestMinimumSizeIncludesContentAndSplitSeparators(t *testing.T) {
	node := core.LayoutNode{
		Kind: core.LayoutSplit, SplitID: 1, Direction: core.SplitHorizontal, Weights: []uint32{1, 1},
		Children: []core.LayoutNode{{Kind: core.LayoutPane, PaneID: 1}, {Kind: core.LayoutPane, PaneID: 2}},
	}
	s := session{paneFrame: PaneFrameSplit, renderers: defaultPaneRendererRegistry()}
	if w, h := s.nodeMinimum(node); w != 5 || h != 1 {
		t.Fatalf("split minimum = %dx%d, want 5x1", w, h)
	}
	s.paneFrame = PaneFrameFull
	s.snapshot.Panes = []core.Pane{{ID: 1, Kind: core.PaneTerminal}, {ID: 2, Kind: core.PaneTerminal}}
	if w, h := s.nodeMinimum(node); w != 8 || h != 3 {
		t.Fatalf("full-frame minimum = %dx%d, want 8x3", w, h)
	}
}
