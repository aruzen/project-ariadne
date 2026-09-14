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

func TestMinimumAxisSizeIncludesNestedSplitSeparators(t *testing.T) {
	node := core.LayoutNode{
		Kind: core.LayoutSplit, SplitID: 1, Direction: core.SplitHorizontal, Weights: []uint32{1, 1},
		Children: []core.LayoutNode{{Kind: core.LayoutPane, PaneID: 1}, {Kind: core.LayoutPane, PaneID: 2}},
	}
	if got := minimumAxisSize(node, core.SplitHorizontal, PaneFrameSplit); got != 3 {
		t.Fatalf("split minimum = %d, want 3", got)
	}
	if got := minimumAxisSize(node, core.SplitHorizontal, PaneFrameFull); got != 6 {
		t.Fatalf("full-frame minimum = %d, want 6", got)
	}
	if got := minimumAxisSize(node, core.SplitVertical, PaneFrameFull); got != 3 {
		t.Fatalf("orthogonal minimum = %d, want 3", got)
	}
}
