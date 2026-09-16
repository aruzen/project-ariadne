package tui

import (
	"reflect"
	"testing"

	"github.com/aruzen/ariadne/internal/config"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/vt/libghostty"
)

func TestMouseParsing(t *testing.T) {
	for _, test := range []struct {
		sequence string
		want     MouseEvent
	}{
		{"\x1b[<0;4;5M", MouseEvent{X: 3, Y: 4, Button: 1}},
		{"\x1b[<20;4;5m", MouseEvent{X: 3, Y: 4, Button: 1, Action: 1, Mods: 3}},
		{"\x1b[<32;4;5M", MouseEvent{X: 3, Y: 4, Button: 1, Action: 2}},
		{"\x1b[<64;1;1M", MouseEvent{Button: 4, Wheel: -1}},
	} {
		actual, ok := parseMouse([]byte(test.sequence))
		if !ok || actual != test.want {
			t.Fatalf("mouse=%+v want=%+v", actual, test.want)
		}
	}
	for _, s := range []string{"\x1b[<0;0;1M", "\x1b[<999;1;1M", "\x1b[<1;1M", "\x1b[<1;-1;2M"} {
		if _, ok := parseMouse([]byte(s)); ok {
			t.Fatal(s)
		}
	}
}

func TestMouseSelectionAndApplicationCapture(t *testing.T) {
	terminal, err := libghostty.NewTerminal(10, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	_ = terminal.Write([]byte("hello world\r\nline2"))
	id := core.TerminalID(1)
	pane := core.Pane{ID: 1, WindowID: 1, Kind: core.PaneTerminal, Terminal: &core.TerminalInstance{ID: &id, State: core.TerminalRunning}}
	s := session{window: 1, focus: 1, width: 10, height: 4, presentation: config.Default().TUI, paneFrame: PaneFrameSplit, renderers: defaultPaneRendererRegistry(), views: map[core.PaneID]*paneView{1: {paneID: 1, terminalID: id, content: &terminalPaneContent{terminal: terminal, cols: 10, rows: 3}}}, snapshot: core.Snapshot{Workspaces: []core.Workspace{{ID: 1, WindowIDs: []core.WindowID{1}}}, Windows: []core.Window{{ID: 1, Layout: &core.LayoutNode{Kind: core.LayoutPane, PaneID: 1}}}, Panes: []core.Pane{pane}}}
	s.relayout()
	s.processInput([]byte("\x1b[<0;1;1M\x1b[<32;5;1M\x1b[<0;5;1m"))
	if !s.copyMode || s.gesture != nil || s.copyX != 4 {
		t.Fatalf("copy=%t capture=%+v x=%d", s.copyMode, s.gesture, s.copyX)
	}
	if _, err := terminal.SelectionText(); err != nil {
		t.Fatal(err)
	}
	s.leaveCopyMode()
	_ = terminal.Write([]byte("\x1b[?1002h\x1b[?1006h"))
	s.processInput([]byte("\x1b[<0;1;1M"))
	if s.gesture == nil || s.gesture.route != "app" || s.copyMode {
		t.Fatal("application did not capture gesture")
	}
	s.processInput([]byte("\x1b[<36;5;1M"))
	if s.copyMode || s.gesture.route != "app" {
		t.Fatal("shift rerouted captured gesture")
	}
	s.width = 11
	s.relayout()
	if s.gesture != nil {
		t.Fatal("resize did not cancel gesture")
	}
	s.processInput([]byte("\x1b[<0;5;1m"))
	if s.gesture != nil || s.copyMode {
		t.Fatal("orphan release started a new selection")
	}
	s.processInput([]byte("\x1b[<0;1;1M"))
	s.zoom = true
	s.relayout()
	if s.gesture != nil {
		t.Fatal("zoom did not cancel gesture")
	}
}

func TestSeparatorDragPreviewPreservesCoreAndCancelsOnResize(t *testing.T) {
	root := &core.LayoutNode{Kind: core.LayoutSplit, SplitID: 1, Direction: core.SplitHorizontal, Weights: []uint32{1, 1}, Children: []core.LayoutNode{{Kind: core.LayoutPane, PaneID: 1}, {Kind: core.LayoutPane, PaneID: 2}}}
	s := session{window: 1, focus: 1, width: 21, height: 4, paneFrame: PaneFrameSplit, presentation: config.Default().TUI, renderers: defaultPaneRendererRegistry(), views: make(map[core.PaneID]*paneView), snapshot: core.Snapshot{Workspaces: []core.Workspace{{ID: 1, WindowIDs: []core.WindowID{1}}}, Windows: []core.Window{{ID: 1, Layout: root}}, Panes: []core.Pane{{ID: 1, Kind: core.PaneTool}, {ID: 2, Kind: core.PaneTool}}}}
	s.relayout()
	boundary, ok := findSplitBoundary(*root, Rect{W: 21, H: 3}, 10, 1, PaneFrameSplit)
	if !ok {
		t.Fatal("missing separator")
	}
	g := s.capture("split", 0, Rect{}, 1)
	g.boundary = boundary
	s.previewSplitDrag(MouseEvent{X: 20, Y: 1, Action: 2}, g)
	if s.dragLayout == nil || !reflect.DeepEqual(root.Weights, []uint32{1, 1}) || s.placements[1].Rect.W < 2 {
		t.Fatalf("preview=%+v placements=%+v", s.dragLayout, s.placements)
	}
	s.width = 22
	s.relayout()
	if s.gesture != nil || s.dragLayout != nil || root.Weights[0] != 1 {
		t.Fatal("drag not cancelled safely")
	}
	s.closeViews()
}

func TestCopySelectionPreservesWideGrapheme(t *testing.T) {
	s := NewSurface(4, 1, Style{})
	s.Text(0, 0, 4, "界ab", Style{})
	drawCopySelection(s, Rect{W: 4, H: 1}, 1, 0, 1, 0)
	if s.At(0, 0).Text != "界" || s.At(0, 0).Width != 2 || s.At(1, 0).Width != 0 || s.At(0, 0).Style != s.At(1, 0).Style {
		t.Fatalf("selection=%+v", s.Cells)
	}
}
