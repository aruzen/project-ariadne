package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/aruzen/ariadne/internal/core"
)

func TestCalculateLayoutUsesWeights(t *testing.T) {
	root := &core.LayoutNode{
		Kind: core.LayoutSplit, Direction: core.SplitHorizontal,
		Children: []core.LayoutNode{{Kind: core.LayoutPane, PaneID: 1}, {Kind: core.LayoutPane, PaneID: 2}},
		Weights:  []uint32{1, 3},
	}
	placements := CalculateLayout(root, Rect{W: 80, H: 24})
	if len(placements) != 2 || placements[0].Rect.W != 20 || placements[1].Rect.X != 20 || placements[1].Rect.W != 60 {
		t.Fatalf("placements = %+v", placements)
	}
}

func TestCalculateSplitLayoutUsesOneInternalCellAndNoOuterFrame(t *testing.T) {
	root := &core.LayoutNode{
		Kind: core.LayoutSplit, Direction: core.SplitHorizontal,
		Children: []core.LayoutNode{{Kind: core.LayoutPane, PaneID: 1}, {Kind: core.LayoutPane, PaneID: 2}},
		Weights:  []uint32{1, 1},
	}
	result := CalculateSplitLayout(root, Rect{W: 80, H: 24})
	if len(result.Placements) != 2 || len(result.Separators) != 1 {
		t.Fatalf("layout = %+v", result)
	}
	if result.Placements[0].Rect != (Rect{W: 39, H: 24}) ||
		result.Separators[0] != (Separator{Rect: Rect{X: 39, W: 1, H: 24}, Direction: core.SplitHorizontal}) ||
		result.Placements[1].Rect != (Rect{X: 40, W: 40, H: 24}) {
		t.Fatalf("layout = %+v", result)
	}
}

func TestCalculateSplitLayoutSinglePaneUsesAllCells(t *testing.T) {
	root := &core.LayoutNode{Kind: core.LayoutPane, PaneID: 1}
	result := CalculateSplitLayout(root, Rect{X: 2, Y: 3, W: 40, H: 10})
	want := Placement{PaneID: 1, Rect: Rect{X: 2, Y: 3, W: 40, H: 10}}
	if len(result.Placements) != 1 || result.Placements[0] != want || len(result.Separators) != 0 {
		t.Fatalf("layout = %+v, want placement %+v", result, want)
	}
}

func TestDrawSplitSeparatorsJoinsNestedSplits(t *testing.T) {
	root := &core.LayoutNode{
		Kind: core.LayoutSplit, Direction: core.SplitHorizontal,
		Children: []core.LayoutNode{
			{Kind: core.LayoutPane, PaneID: 1},
			{Kind: core.LayoutSplit, Direction: core.SplitVertical, Children: []core.LayoutNode{
				{Kind: core.LayoutPane, PaneID: 2},
				{Kind: core.LayoutPane, PaneID: 3},
			}},
		},
	}
	result := CalculateSplitLayout(root, Rect{W: 9, H: 5})
	surface := NewSurface(9, 5, Style{})
	drawSplitSeparators(surface, result.Separators, result.Placements, 3)
	if surface.At(0, 0).Text != " " || surface.At(4, 0).Text != "│" || surface.At(4, 2).Text != "├" || surface.At(5, 2).Text != "─" {
		t.Fatalf("separator cells = %q %q %q %q", surface.At(0, 0).Text, surface.At(4, 0).Text, surface.At(4, 2).Text, surface.At(5, 2).Text)
	}
	if !surface.At(5, 2).Style.Bold {
		t.Fatal("separator adjacent to focused Pane is not highlighted")
	}
}

func TestStatusBarKeepsRightWidgetsVisible(t *testing.T) {
	surface := NewSurface(30, 2, Style{})
	bar := DefaultStatusBar()
	bar.Draw(surface, 1, StatusContext{Workspace: "a-very-long-workspace", Window: "main", PaneID: 9, Now: time.Date(2026, 1, 1, 12, 34, 0, 0, time.UTC)})
	var row string
	for x := 0; x < surface.Width; x++ {
		row += surface.At(x, 1).Text
	}
	if len([]rune(row)) != 30 || row[len(row)-6:] != "12:34 " {
		t.Fatalf("status row = %q", row)
	}
}

func TestEncodeFramePositionsVisibleCursor(t *testing.T) {
	surface := NewSurface(2, 1, Style{})
	frame := string(EncodeFrame(surface, Cursor{X: 1, Y: 0, Visible: true}))
	if !containsAll(frame, "\x1b[1;2H", "\x1b[?25h") {
		t.Fatalf("frame = %q", frame)
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
