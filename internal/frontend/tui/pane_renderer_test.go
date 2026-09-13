package tui

import (
	"testing"

	"github.com/aruzen/ariadne/internal/core"
)

func TestPaneChromeControlsContentRect(t *testing.T) {
	rect := Rect{X: 2, Y: 3, W: 20, H: 10}
	renderer := terminalPaneRenderer{}
	bordered := chromeFor(core.Pane{Kind: core.PaneTerminal}, renderer).ContentRect(rect)
	if bordered != (Rect{X: 3, Y: 4, W: 18, H: 8}) {
		t.Fatalf("bordered content = %+v", bordered)
	}
	borderless := chromeFor(core.Pane{
		Kind: core.PaneTerminal, Presentation: core.PanePresentation{Chrome: core.PaneChromeNone},
	}, renderer).ContentRect(rect)
	if borderless != rect {
		t.Fatalf("borderless content = %+v", borderless)
	}
}

func TestNoChromeDoesNotModifySurface(t *testing.T) {
	surface := NewSurface(4, 2, Style{})
	before := append([]Cell(nil), surface.Cells...)
	noChrome{}.Draw(surface, Rect{W: 4, H: 2}, "hidden", true)
	for index := range before {
		if surface.Cells[index] != before[index] {
			t.Fatalf("cell %d changed", index)
		}
	}
}

func TestRendererRegistrySeparatesTerminalAndToolPanes(t *testing.T) {
	registry := defaultPaneRendererRegistry()
	terminal, err := registry.renderer(core.Pane{Kind: core.PaneTerminal})
	if err != nil || !terminal.UsesTerminal() {
		t.Fatalf("terminal renderer = %T, %v", terminal, err)
	}
	tool, err := registry.renderer(core.Pane{Kind: core.PaneTool})
	if err != nil || tool.UsesTerminal() {
		t.Fatalf("tool renderer = %T, %v", tool, err)
	}
}

func TestSyncViewsCreatesBorderlessToolContentWithoutTerminal(t *testing.T) {
	session := session{
		snapshot: core.Snapshot{Panes: []core.Pane{{
			ID: 1, Kind: core.PaneTool,
			Presentation: core.PanePresentation{Chrome: core.PaneChromeNone},
		}}},
		placements: []Placement{{PaneID: 1, Rect: Rect{W: 20, H: 8}}},
		views:      make(map[core.PaneID]*paneView),
		renderers:  defaultPaneRendererRegistry(),
	}
	session.syncViews()
	view := session.views[1]
	if view == nil || view.cols != 20 || view.rows != 8 || view.attachment != nil {
		t.Fatalf("view = %+v", view)
	}
	if _, ok := view.content.(emptyPaneContent); !ok {
		t.Fatalf("content = %T", view.content)
	}
	session.closeViews()
}
