package tui

import (
	"strings"
	"testing"

	"github.com/aruzen/ariadne/internal/config"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/vt/libghostty"
)

func titleSession(t *testing.T) (*session, *libghostty.Terminal) {
	t.Helper()
	terminal, err := libghostty.NewTerminal(38, 5)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(terminal.Close)
	pane := core.Pane{ID: 1, WindowID: 1, Kind: core.PaneTerminal, Terminal: &core.TerminalInstance{Launch: core.LaunchSpec{Argv: []string{"/bin/sh"}}}}
	s := &session{window: 1, focus: 1, width: 40, height: 8, paneFrame: PaneFrameFull, presentation: config.Default().TUI, renderers: defaultPaneRendererRegistry(), views: map[core.PaneID]*paneView{1: {content: &terminalPaneContent{terminal: terminal}}}, snapshot: core.Snapshot{Workspaces: []core.Workspace{{ID: 1, WindowIDs: []core.WindowID{1}}}, Windows: []core.Window{{ID: 1, WorkspaceID: 1, Layout: &core.LayoutNode{Kind: core.LayoutPane, PaneID: 1}}}, Panes: []core.Pane{pane}}}
	s.presentation.Status.Left = []string{"pane"}
	s.presentation.Status.Right = nil
	s.theme, _ = resolveTheme(s.presentation.Theme)
	s.statusBar = s.configuredStatusBar()
	s.relayout()
	return s, terminal
}

func TestOSCTitlePreferenceAndFallback(t *testing.T) {
	s, terminal := titleSession(t)
	pane := s.snapshot.Panes[0]
	if title := s.displayPaneTitle(pane); title != "sh" {
		t.Fatal(title)
	}
	if err := terminal.Write([]byte("\x1b]2;ビルド中\x07")); err != nil {
		t.Fatal(err)
	}
	if title := s.displayPaneTitle(pane); title != "ビルド中" {
		t.Fatal(title)
	}
	pane.Title = "manual"
	if title := s.displayPaneTitle(pane); title != "manual" {
		t.Fatal(title)
	}
	s.presentation.PaneTitle = config.PaneTitleTerminal
	if title := s.displayPaneTitle(pane); title != "ビルド中" {
		t.Fatal(title)
	}
	s.presentation.PaneTitle = config.PaneTitlePane
	if title := s.displayPaneTitle(pane); title != "manual" {
		t.Fatal(title)
	}
	pane.Title = ""
	if title := s.displayPaneTitle(pane); title != "sh" {
		t.Fatal(title)
	}
	s.presentation.PaneTitle = config.PaneTitleAuto
	if err := terminal.Write([]byte("\x1b]0;\x1b\\")); err != nil {
		t.Fatal(err)
	}
	if title := s.displayPaneTitle(pane); title != "sh" {
		t.Fatal(title)
	}
	pane.Kind, pane.Terminal, pane.Title = core.PaneTool, nil, "tool"
	if title := s.terminalTitle(pane); title != "" {
		t.Fatal("terminal title leaked into ToolPane")
	}
	if title := s.displayPaneTitle(pane); title != "tool" {
		t.Fatal(title)
	}
	if s.snapshot.Panes[0].Title != "" {
		t.Fatal("OSC title renamed the core Pane")
	}
}

func TestOSCTitleAppearsInFrameAndStatus(t *testing.T) {
	s, terminal := titleSession(t)
	if err := terminal.Write([]byte("\x1b]2;build-界é\x1b\\")); err != nil {
		t.Fatal(err)
	}
	surface, _, err := s.render()
	if err != nil {
		t.Fatal(err)
	}
	row := func(y int) string {
		var b strings.Builder
		for x := 0; x < surface.Width; x++ {
			b.WriteString(surface.At(x, y).Text)
		}
		return b.String()
	}
	for _, y := range []int{0, 7} {
		if !strings.Contains(row(y), "build-界é") {
			t.Fatalf("title missing at row %d: %q", y, row(y))
		}
	}
	s.presentation.Status.Widgets["pane"] = config.WidgetOptions{Format: "{title}|{pane_title}|{terminal_title}"}
	s.statusBar = s.configuredStatusBar()
	s.snapshot.Panes[0].Title = "manual"
	surface, _, err = s.render()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(row(7), "manual|manual|build-界é") {
		t.Fatalf("separate title placeholders missing: %q", row(7))
	}
}

func TestPaneTitleSanitizesControlSequences(t *testing.T) {
	s, _ := titleSession(t)
	pane := s.snapshot.Panes[0]
	pane.Title = "hello\x1b[31m\nworld\x1b]52;c;aGk=\x07"
	if title := s.displayPaneTitle(pane); title != "hello world" {
		t.Fatalf("unsafe title: %q", title)
	}
}
