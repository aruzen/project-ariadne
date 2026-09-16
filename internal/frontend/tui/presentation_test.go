package tui

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aruzen/ariadne/internal/config"
	"github.com/aruzen/ariadne/internal/core"
)

func TestPromptModeKeysAndFragmentedUnicode(t *testing.T) {
	maps := config.DefaultKeymaps()
	maps.Prompt["ctrl-g"] = "cancel"
	decoder, err := modeDecoder(maps.Prompt, false)
	if err != nil {
		t.Fatal(err)
	}
	s := session{promptDecoder: decoder, inputMode: inputModePrompt, promptLead: ":"}
	for _, b := range []byte("界é👩‍💻") {
		s.processInput([]byte{b})
	}
	if s.prompt != "界é👩‍💻" {
		t.Fatal(s.prompt)
	}
	s.processInput([]byte{0x7f})
	if s.prompt != "界é" {
		t.Fatal(s.prompt)
	}
	s.processInput([]byte{0x7f})
	if s.prompt != "界" {
		t.Fatal(s.prompt)
	}
	s.processInput([]byte{7})
	if s.inputMode != inputModeNormal {
		t.Fatal("custom cancel failed")
	}
	if _, err := modeDecoder(config.Keybindings{"x": "focus left"}, true); err == nil {
		t.Fatal("copy mode accepted normal action")
	}
	if _, err := modeDecoder(config.Keybindings{"x": "left", "x y": "right"}, true); err == nil {
		t.Fatal("copy mode accepted ambiguous prefix")
	}
}

func TestFragmentedPromptArrowsAndEscape(t *testing.T) {
	decoder, err := modeDecoder(config.DefaultKeymaps().Prompt, false)
	if err != nil {
		t.Fatal(err)
	}
	s := session{promptDecoder: decoder, inputMode: inputModePrompt, promptLead: ":", promptHistory: []string{"zoom on"}, promptHistoryIndex: 1}
	for _, b := range []byte("\x1b[A") {
		s.processInput([]byte{b})
	}
	if s.prompt != "zoom on" || s.inputMode != inputModePrompt {
		t.Fatalf("prompt=%q mode=%v", s.prompt, s.inputMode)
	}
	s.processInput([]byte{0x1b})
	s.inputFrames.last = time.Now().Add(-time.Second)
	s.flushInputFrames()
	s.modeKeyTime = time.Now().Add(-time.Second)
	s.flushModeEscape()
	if s.inputMode != inputModeNormal {
		t.Fatal("escape did not cancel")
	}
}

func TestPasteCannotExecuteBindingsAndHasLimit(t *testing.T) {
	decoder, _ := newInputDecoder(nil)
	promptDecoder, _ := modeDecoder(config.DefaultKeymaps().Prompt, false)
	s := session{inputDecoder: decoder, promptDecoder: promptDecoder, inputMode: inputModePrompt, promptLead: ":", clipboardMax: 100}
	for _, b := range []byte("\x1b[200~\x01:delete pane\n界\x1b[201~") {
		s.processInput([]byte{b})
	}
	if s.inputMode != inputModePrompt || !strings.Contains(s.prompt, "delete pane") || !strings.Contains(s.prompt, "界") {
		t.Fatalf("paste=%q mode=%v", s.prompt, s.inputMode)
	}
	s.clipboardMax = 3
	before := s.prompt
	s.processInput([]byte("\x1b[200~12345\x1b[201~"))
	if s.prompt != before || !strings.Contains(s.message, "limit") {
		t.Fatalf("overflow prompt=%q message=%q", s.prompt, s.message)
	}
}

func TestCompactLayoutRestoresCoreLayoutAndExplicitZoom(t *testing.T) {
	root := &core.LayoutNode{Kind: core.LayoutSplit, SplitID: 1, Direction: core.SplitHorizontal, Weights: []uint32{1, 1}, Children: []core.LayoutNode{{Kind: core.LayoutPane, PaneID: 1}, {Kind: core.LayoutPane, PaneID: 2}}}
	s := session{window: 1, focus: 2, width: 4, height: 2, paneFrame: PaneFrameSplit, renderers: defaultPaneRendererRegistry(), snapshot: core.Snapshot{Workspaces: []core.Workspace{{ID: 1, WindowIDs: []core.WindowID{1}}}, Windows: []core.Window{{ID: 1, Layout: root}}, Panes: []core.Pane{{ID: 1, Kind: core.PaneTerminal}, {ID: 2, Kind: core.PaneTerminal}}}}
	s.relayout()
	if !s.compact || s.zoom || len(s.placements) != 1 || s.placements[0].PaneID != 2 || s.snapshot.Windows[0].Layout != root {
		t.Fatalf("compact=%t zoom=%t placements=%v", s.compact, s.zoom, s.placements)
	}
	s.width, s.height = 80, 24
	s.relayout()
	if s.compact || len(s.placements) != 2 || len(s.separators) != 1 {
		t.Fatal("layout not restored")
	}
	s.zoom = true
	s.width = 2
	s.relayout()
	s.width = 80
	s.relayout()
	if !s.zoom || len(s.placements) != 1 {
		t.Fatal("explicit zoom was lost")
	}
}

func TestCWDResolutionAndRemoteFallback(t *testing.T) {
	dir := t.TempDir()
	startup := t.TempDir()
	path := filepath.ToSlash(dir)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	uri := &url.URL{Scheme: "file", Host: "localhost", Path: path}
	if got := localOSC7Directory(uri.String()); got != dir {
		t.Fatalf("cwd=%q", got)
	}
	uri.Host = "remote.invalid"
	if got := localOSC7Directory(uri.String()); got != "" {
		t.Fatal(got)
	}
	s := session{focus: 1, cwd: startup, presentation: config.Default().TUI, snapshot: core.Snapshot{Panes: []core.Pane{{ID: 1, Kind: core.PaneTerminal, Terminal: &core.TerminalInstance{Launch: core.LaunchSpec{CWD: dir}}}}}}
	if got := s.widgetCWD(""); got != dir {
		t.Fatal(got)
	}
	if got := s.widgetCWD("startup"); got != startup {
		t.Fatal(got)
	}
	s.presentation.CWD.Kinds = map[string]string{"terminal": "startup"}
	if got := s.widgetCWD(""); got != startup {
		t.Fatal(got)
	}
}

func TestWidgetSanitizationAndStaleResult(t *testing.T) {
	if got := sanitizeWidgetText("a\x1b[31m赤\x1b[0m\n\x1b]52;c;evil\a b\x00"); got != "a赤 b" {
		t.Fatal(got)
	}
	runner := newWidgetRunner(context.Background(), map[string]config.WidgetOptions{"x": {CWD: "startup"}})
	runner.states["x"] = &widgetState{cwd: "/tmp", pane: 1, generation: 2, running: true, text: "old"}
	runner.active = 1
	s := session{focus: 1, cwd: "/tmp", widgets: runner}
	s.applyWidgetResult(widgetResult{name: "x", generation: 1, text: "stale"})
	if runner.states["x"].text != "old" {
		t.Fatal("stale result accepted")
	}
	runner.active = 1
	s.applyWidgetResult(widgetResult{name: "x", generation: 2, err: errors.New("failed")})
	if runner.states["x"].text != "old" || !runner.states["x"].err {
		t.Fatal("failed widget lost last good output")
	}
}

func TestWidgetsNeverRunMoreThanFourAndCancelOnCWDChange(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	options := make(map[string]config.WidgetOptions)
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		options[name] = config.WidgetOptions{Command: []string{executable, "-test.run=^TestWidgetHelper$"}, TimeoutMS: 2000}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := newWidgetRunner(ctx, options)
	defer runner.close()
	s := session{focus: 1, cwd: t.TempDir(), widgets: runner, env: append(os.Environ(), "ARIADNE_WIDGET_HELPER=1")}
	s.refreshWidgets(time.Now())
	if runner.active != 4 {
		t.Fatalf("active=%d", runner.active)
	}
	s.refreshWidgets(time.Now())
	if runner.active != 4 {
		t.Fatal("overlapping widget execution")
	}
	s.focus = 2
	s.refreshWidgets(time.Now())
	for runner.active > 0 {
		select {
		case result := <-runner.results:
			s.applyWidgetResult(result)
		case <-time.After(3 * time.Second):
			t.Fatal("cancelled widgets did not stop")
		}
	}
	for _, state := range runner.states {
		if state.text != "" {
			t.Fatal("old focus output leaked")
		}
	}
}

func TestWidgetHelper(t *testing.T) {
	if os.Getenv("ARIADNE_WIDGET_HELPER") == "1" {
		time.Sleep(5 * time.Second)
		os.Exit(0)
	}
}
