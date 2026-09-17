package tui

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aruzen/ariadne/internal/client"
	ariadneconfig "github.com/aruzen/ariadne/internal/config"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/plugin/external"
	"github.com/aruzen/ariadne/internal/vt/libghostty"
)

type PaneFrameMode string

const (
	PaneFrameFull  PaneFrameMode = "full"
	PaneFrameSplit PaneFrameMode = "split"
	PaneFrameNone  PaneFrameMode = "none"
)

type Options struct {
	PaneFrame    PaneFrameMode
	Keybindings  ariadneconfig.Keybindings
	CopyKeys     ariadneconfig.Keybindings
	PromptKeys   ariadneconfig.Keybindings
	Presentation ariadneconfig.TUIOptions
	Shell        []string
	Editor       []string
	CWD          string
	Env          []string
	Clipboard    ariadneconfig.ClipboardOptions
	PluginLimits external.Config
}

func DefaultOptions() Options {
	defaults := ariadneconfig.Default()
	return Options{PaneFrame: PaneFrameFull, Keybindings: defaults.Keybindings.Normal, CopyKeys: defaults.Keybindings.Copy, PromptKeys: defaults.Keybindings.Prompt, Presentation: defaults.TUI}
}

func (options Options) validate() error {
	if _, err := options.PluginLimits.Normalize(); err != nil {
		return err
	}
	if mode := options.Presentation.PaneTitle; mode != "" && !mode.Valid() {
		return fmt.Errorf("tui.pane_title must be auto, pane, or terminal")
	}
	presentation := options.Presentation
	if presentation.Theme.Base.Foreground == "" {
		presentation = ariadneconfig.Default().TUI
	}
	if _, err := resolveTheme(presentation.Theme); err != nil {
		return err
	}
	for _, copy := range []bool{true, false} {
		keys := options.PromptKeys
		if copy {
			keys = options.CopyKeys
		}
		if keys == nil {
			maps := ariadneconfig.DefaultKeymaps()
			keys = maps.Prompt
			if copy {
				keys = maps.Copy
			}
		}
		if _, err := modeDecoder(keys, copy); err != nil {
			return err
		}
	}
	if err := validateFrontendCommand("shell", options.Shell); err != nil {
		return err
	}
	if err := validateFrontendCommand("editor", options.Editor); err != nil {
		return err
	}
	switch options.PaneFrame {
	case PaneFrameFull, PaneFrameSplit, PaneFrameNone:
		_, err := newInputDecoder(options.Keybindings)
		return err
	default:
		return fmt.Errorf("invalid TUI Pane frame mode %q", options.PaneFrame)
	}
}

func validateFrontendCommand(name string, argv []string) error {
	if len(argv) != 0 && strings.TrimSpace(argv[0]) == "" {
		return fmt.Errorf("invalid TUI %s command", name)
	}
	for _, argument := range argv {
		if strings.ContainsRune(argument, 0) {
			return fmt.Errorf("invalid TUI %s command", name)
		}
	}
	return nil
}

// paneChrome owns decoration and determines the content viewport. Pane content
// never needs to know whether a border, title, or no chrome is used.
type paneChrome interface {
	ContentRect(Rect) Rect
	Draw(*Surface, Rect, string, bool)
}

type borderChrome struct{}

func (borderChrome) ContentRect(rect Rect) Rect { return rect.Interior() }
func (borderChrome) Draw(surface *Surface, rect Rect, title string, focused bool) {
	drawPaneBorder(surface, rect, title, focused)
}

type noChrome struct{}

func (noChrome) ContentRect(rect Rect) Rect        { return rect }
func (noChrome) Draw(*Surface, Rect, string, bool) {}

// paneContent is one stateful TUI representation. Special panes can own their
// own state here without depending on PTY or libghostty.
type paneContent interface {
	Resize(int, int) error
	Draw(*Surface, Rect, core.Pane, bool, Style) (Cursor, error)
	HandleInput([]byte) (bool, error)
	Close()
}

// ptyPaneContent is implemented only by content that consumes a raw PTY byte
// stream. Responses contain terminal-query bytes that must be sent to the PTY.
type ptyPaneContent interface {
	paneContent
	WritePTY([]byte) ([]byte, error)
}

// paneRenderer describes a Pane kind and creates its per-Pane state. Adding a
// special Pane does not alter layout, chrome, focus, or frame composition.
type paneRenderer interface {
	DefaultChrome() core.PaneChrome
	UsesTerminal() bool
	Focusable(core.Pane) bool
	NewContent(*session, core.Pane) paneContent
}

type terminalPaneRenderer struct{}

func (terminalPaneRenderer) DefaultChrome() core.PaneChrome { return core.PaneChromeBorder }
func (terminalPaneRenderer) UsesTerminal() bool             { return true }
func (terminalPaneRenderer) Focusable(core.Pane) bool       { return true }
func (terminalPaneRenderer) NewContent(*session, core.Pane) paneContent {
	return &terminalPaneContent{}
}

type terminalPaneContent struct {
	terminal *libghostty.Terminal
	cols     int
	rows     int
	screen   libghostty.Screen
}

func (content *terminalPaneContent) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return nil
	}
	if content.terminal == nil {
		terminal, err := libghostty.NewTerminal(cols, rows)
		if err != nil {
			return err
		}
		content.terminal = terminal
	} else if content.cols != cols || content.rows != rows {
		if err := content.terminal.Resize(cols, rows); err != nil {
			return err
		}
	}
	content.cols, content.rows = cols, rows
	return nil
}

func (content *terminalPaneContent) WritePTY(data []byte) ([]byte, error) {
	if content.terminal == nil {
		return nil, fmt.Errorf("TUI terminal content is unavailable")
	}
	return content.terminal.WriteWithResponse(data)
}

func (content *terminalPaneContent) setClipboardHandler(handler libghostty.ClipboardHandler, maxBytes int) {
	if content.terminal != nil {
		_ = content.terminal.SetClipboardHandler(handler)
		_ = content.terminal.SetClipboardMaxBytes(maxBytes)
	}
}

func (content *terminalPaneContent) scroll(delta int) error {
	if content.terminal == nil {
		return fmt.Errorf("TUI terminal content is unavailable")
	}
	return content.terminal.Scroll(delta)
}

func (content *terminalPaneContent) scrollTop() error {
	if content.terminal == nil {
		return fmt.Errorf("TUI terminal content is unavailable")
	}
	return content.terminal.ScrollTop()
}

func (content *terminalPaneContent) scrollBottom() error {
	if content.terminal == nil {
		return fmt.Errorf("TUI terminal content is unavailable")
	}
	return content.terminal.ScrollBottom()
}

func (content *terminalPaneContent) beginSelection(x, y int) error {
	if content.terminal == nil {
		return fmt.Errorf("TUI terminal content is unavailable")
	}
	return content.terminal.BeginSelection(x, y)
}

func (content *terminalPaneContent) adjustSelection(adjustment libghostty.SelectionAdjust) error {
	if content.terminal == nil {
		return fmt.Errorf("TUI terminal content is unavailable")
	}
	return content.terminal.AdjustSelection(adjustment)
}

func (content *terminalPaneContent) clearSelection() error {
	if content.terminal == nil {
		return nil
	}
	return content.terminal.ClearSelection()
}

func (content *terminalPaneContent) selectionText() (string, error) {
	if content.terminal == nil {
		return "", fmt.Errorf("TUI terminal content is unavailable")
	}
	return content.terminal.SelectionText()
}

func (content *terminalPaneContent) paste(data []byte, allowUnsafe bool) ([]byte, error) {
	if content.terminal == nil {
		return nil, fmt.Errorf("TUI terminal content is unavailable")
	}
	return content.terminal.Paste(data, allowUnsafe)
}

func (content *terminalPaneContent) search(query string, next bool) (int, int, error) {
	if content.terminal == nil {
		return 0, 0, fmt.Errorf("TUI terminal content is unavailable")
	}
	return content.terminal.Search(query, next)
}

func (content *terminalPaneContent) Draw(surface *Surface, rect Rect, _ core.Pane, focused bool, _ Style) (Cursor, error) {
	if content.terminal == nil || rect.W <= 0 || rect.H <= 0 {
		return Cursor{}, nil
	}
	screen, err := content.terminal.TryScreen()
	if errors.Is(err, libghostty.ErrBusy) {
		screen = content.screen
		err = nil
	} else if err == nil {
		content.screen = screen
	}
	if err != nil {
		return Cursor{}, err
	}
	for y := 0; y < rect.H && y < screen.Rows; y++ {
		for x := 0; x < rect.W && x < screen.Cols; x++ {
			source := screen.At(x, y)
			style := Style{
				UnderlineStyle: source.Style.UnderlineStyle, UnderlineColor: colorFromGhostty(source.Style.UnderlineColor), HasUnderlineColor: source.Style.HasUnderlineColor,
				Foreground: colorFromGhostty(source.Style.Foreground), Background: colorFromGhostty(source.Style.Background),
				Bold: source.Style.Bold, Italic: source.Style.Italic, Underline: source.Style.Underline,
				Strikethrough: source.Style.Strikethrough, Faint: source.Style.Faint, Blink: source.Style.Blink,
			}
			target := (rect.Y+y)*surface.Width + rect.X + x
			if target >= 0 && target < len(surface.Cells) {
				if source.Width == 2 && x+1 >= rect.W {
					source.Text, source.Width = " ", 1
				}
				surface.Cells[target] = Cell{Text: source.Text, Width: source.Width, Style: style}
			}
		}
	}
	if focused && screen.Cursor.Visible && screen.Cursor.X < rect.W && screen.Cursor.Y < rect.H {
		return Cursor{X: rect.X + screen.Cursor.X, Y: rect.Y + screen.Cursor.Y, Visible: true, Shape: screen.Cursor.Shape}, nil
	}
	return Cursor{}, nil
}

func (*terminalPaneContent) HandleInput([]byte) (bool, error) { return false, nil }

func (content *terminalPaneContent) Close() {
	if content.terminal != nil {
		content.terminal.Close()
		content.terminal = nil
	}
}

type toolRendererKey struct{ provider, kind string }
type toolContentFactory func(*session, core.Pane) paneContent
type toolPaneRenderer struct {
	factories map[toolRendererKey]toolContentFactory
}

func (toolPaneRenderer) DefaultChrome() core.PaneChrome { return core.PaneChromeBorder }
func (toolPaneRenderer) UsesTerminal() bool             { return false }
func (toolPaneRenderer) Focusable(core.Pane) bool       { return true }
func (renderer toolPaneRenderer) NewContent(owner *session, pane core.Pane) paneContent {
	if pane.Tool == nil {
		return emptyPaneContent{}
	}
	factory := renderer.factories[toolRendererKey{provider: pane.Tool.Provider, kind: pane.Tool.Type}]
	if factory == nil {
		if owner != nil && pane.Tool.Provider != "ariadne" {
			return newExternalToolContent(owner, pane)
		}
		return unavailableToolContent{descriptor: *pane.Tool}
	}
	return factory(owner, pane)
}

var builtinToolTypes = []string{"command-palette", "stash-list", "help", "workspace-list", "resource-list", "agent-status", "diagnostics", "plugin-manager"}

func isBuiltinToolType(kind string) bool {
	for _, candidate := range builtinToolTypes {
		if candidate == kind {
			return true
		}
	}
	return false
}

func defaultToolFactories() map[toolRendererKey]toolContentFactory {
	result := make(map[toolRendererKey]toolContentFactory)
	for _, kind := range builtinToolTypes {
		key := toolRendererKey{provider: "ariadne", kind: kind}
		result[key] = func(owner *session, pane core.Pane) paneContent {
			return &builtinToolContent{owner: owner, descriptor: *pane.Tool, selected: 1}
		}
	}
	return result
}

type emptyPaneContent struct{}

func (emptyPaneContent) Resize(int, int) error { return nil }
func (emptyPaneContent) Draw(*Surface, Rect, core.Pane, bool, Style) (Cursor, error) {
	return Cursor{}, nil
}
func (emptyPaneContent) HandleInput([]byte) (bool, error) { return false, nil }
func (emptyPaneContent) Close()                           {}

func safeNewPaneContent(renderer paneRenderer, owner *session, pane core.Pane) (content paneContent) {
	defer func() {
		if recovered := recover(); recovered != nil {
			content = unavailableToolContent{descriptor: toolDescriptorOrFallback(pane)}
		}
	}()
	return renderer.NewContent(owner, pane)
}

func safeResizePane(content paneContent, cols, rows int) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("pane resize panic: %v", recovered)
		}
	}()
	return content.Resize(cols, rows)
}

func safeDrawPane(content paneContent, surface *Surface, rect Rect, pane core.Pane, focused bool, style Style) (cursor Cursor, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("pane draw panic: %v", recovered)
		}
	}()
	return content.Draw(surface, rect, pane, focused, style)
}

func safeInputPane(content paneContent, data []byte) (handled bool, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("pane input panic: %v", recovered)
		}
	}()
	return content.HandleInput(data)
}

func safeClosePaneContent(content paneContent) { defer func() { _ = recover() }(); content.Close() }
func safeWritePTY(content ptyPaneContent, data []byte) (response []byte, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("pane PTY panic: %v", recovered)
		}
	}()
	return content.WritePTY(data)
}

func toolDescriptorOrFallback(pane core.Pane) core.ToolDescriptor {
	if pane.Tool != nil {
		return *pane.Tool
	}
	return core.ToolDescriptor{Provider: "unknown", Type: string(pane.Kind), Instance: fmt.Sprint(pane.ID)}
}

type unavailableToolContent struct{ descriptor core.ToolDescriptor }

func (unavailableToolContent) Resize(int, int) error { return nil }
func (content unavailableToolContent) Draw(surface *Surface, rect Rect, _ core.Pane, _ bool, style Style) (Cursor, error) {
	surface.Text(rect.X, rect.Y, rect.W, "renderer unavailable", style)
	surface.Text(rect.X, rect.Y+1, rect.W, content.descriptor.Provider+"/"+content.descriptor.Type+"/"+content.descriptor.Instance, style)
	return Cursor{}, nil
}
func (unavailableToolContent) HandleInput([]byte) (bool, error) { return false, nil }
func (unavailableToolContent) Close()                           {}

type builtinToolContent struct {
	owner      *session
	descriptor core.ToolDescriptor
	selected   int
	scroll     int
	query      string
	cols, rows int
}

func (content *builtinToolContent) Resize(cols, rows int) error {
	content.cols, content.rows = cols, rows
	return nil
}
func (content *builtinToolContent) Close() {}

func (content *builtinToolContent) Draw(surface *Surface, rect Rect, _ core.Pane, focused bool, style Style) (Cursor, error) {
	lines := content.lines()
	content.clampSelection(len(lines))
	content.clampScroll(len(lines), rect.H)
	start := content.scroll
	if content.selectable() && content.selected >= rect.H {
		start = content.selected - rect.H + 1
	}
	for row := 0; row < rect.H && start+row < len(lines); row++ {
		index := start + row
		lineStyle := style
		if focused && index == content.selected && content.selectable() {
			lineStyle.Foreground, lineStyle.Background = lineStyle.Background, lineStyle.Foreground
		}
		surface.Text(rect.X, rect.Y+row, rect.W, lines[index], lineStyle)
	}
	return Cursor{}, nil
}

func (content *builtinToolContent) HandleInput(data []byte) (bool, error) {
	if content.owner == nil {
		return false, nil
	}
	if content.descriptor.Type == "command-palette" {
		if len(data) != 0 && data[0] == 0x1b {
			if len(data) == 1 {
				content.query = ""
			}
			return true, nil
		}
		for _, value := range data {
			switch value {
			case '\r', '\n':
				command := strings.TrimSpace(content.query)
				content.query = ""
				if command != "" {
					content.owner.executePrompt(command)
				}
			case 0x08, 0x7f:
				runes := []rune(content.query)
				if len(runes) > 0 {
					content.query = string(runes[:len(runes)-1])
				}
			default:
				if value >= 0x20 {
					content.query += string(value)
				}
			}
		}
		return true, nil
	}
	switch {
	case bytes.Equal(data, []byte("\x1b[A")):
		content.move(-1)
		return true, nil
	case bytes.Equal(data, []byte("\x1b[B")):
		content.move(1)
		return true, nil
	case bytes.Equal(data, []byte("\x1b[5~")):
		content.move(-max(1, content.rows-1))
		return true, nil
	case bytes.Equal(data, []byte("\x1b[6~")):
		content.move(max(1, content.rows-1))
		return true, nil
	}
	for _, value := range data {
		switch value {
		case 'j':
			content.move(1)
		case 'k':
			content.move(-1)
		case 'm':
			if content.descriptor.Type == "agent-status" {
				content.owner.ackAttentionAt(content.selected)
			}
		case 'i', 'u', 'd', 'g', 'v':
			if content.descriptor.Type == "plugin-manager" {
				actions := map[byte]string{'i': "install", 'u': "update", 'd': "uninstall", 'g': "grant", 'v': "revoke"}
				content.owner.pluginManagerAction(content.selected-1, actions[value])
			}
		case 'e':
			if content.descriptor.Type == "plugin-manager" {
				content.owner.pluginManagerAction(content.selected-1, "toggle")
			}
		case 'r':
			if content.descriptor.Type == "plugin-manager" {
				content.owner.pluginManagerAction(content.selected-1, "restart")
			}
			if content.descriptor.Type == "stash-list" {
				content.owner.restoreStashAt(content.selected)
			}
		case '\r', '\n':
			content.activate()
		}
	}
	content.clampSelection(len(content.lines()))
	return true, nil
}

func (content *builtinToolContent) clampSelection(lineCount int) {
	minimum := 0
	if content.selectable() && lineCount > 1 {
		minimum = 1
	}
	maximum := max(minimum, lineCount-1)
	if content.selected < minimum {
		content.selected = minimum
	}
	if content.selected > maximum {
		content.selected = maximum
	}
}

func (content *builtinToolContent) move(delta int) {
	lines := content.lines()
	if content.selectable() {
		content.selected += delta
		content.clampSelection(len(lines))
		return
	}
	if content.scrollable() {
		content.scroll += delta
		content.clampScroll(len(lines), content.rows)
	}
}

func (content *builtinToolContent) clampScroll(lineCount, height int) {
	maximum := max(0, lineCount-max(1, height))
	if content.scroll < 0 {
		content.scroll = 0
	}
	if content.scroll > maximum {
		content.scroll = maximum
	}
}

func (content *builtinToolContent) selectable() bool {
	switch content.descriptor.Type {
	case "stash-list", "workspace-list", "resource-list", "agent-status", "plugin-manager":
		return true
	default:
		return false
	}
}

func (content *builtinToolContent) scrollable() bool {
	return content.descriptor.Type == "help" || content.descriptor.Type == "diagnostics"
}

func (content *builtinToolContent) lines() []string {
	if content.owner == nil {
		return []string{"tool unavailable"}
	}
	switch content.descriptor.Type {
	case "resource-list":
		unit, err := client.ParseResourceUnit(content.descriptor.Instance)
		if err != nil {
			return []string{err.Error()}
		}
		lines := []string{}
		for _, row := range client.ListResources(content.owner.snapshot, unit).Rows() {
			lines = append(lines, strings.Join(row, "  "))
		}
		return lines
	case "command-palette":
		lines := []string{"Command palette", "> " + content.query}
		query := strings.ToLower(strings.TrimSpace(content.query))
		for _, command := range append(promptCommandUsages(), content.owner.pluginHelp()...) {
			if query == "" || strings.Contains(strings.ToLower(command), query) {
				lines = append(lines, command)
			}
		}
		return lines
	case "stash-list":
		lines := []string{"Stash — Enter preview, r restore"}
		for _, entry := range content.owner.snapshot.StashedPanes {
			count, _ := content.owner.paneAttention(entry.PaneID)
			lines = append(lines, fmt.Sprintf("pane %d !%d", entry.PaneID, count))
		}
		for _, entry := range content.owner.snapshot.StashedWindows {
			lines = append(lines, fmt.Sprintf("window %d !%d", entry.WindowID, content.owner.windowAttention(entry.WindowID)))
		}
		return lines
	case "help":
		return append(promptHelpLines(content.owner.keybindings), content.owner.pluginHelp()...)
	case "plugin-manager":
		return content.owner.pluginManagerLines()
	case "workspace-list":
		lines := []string{"Workspaces / Windows"}
		for _, workspace := range content.owner.snapshot.Workspaces {
			lines = append(lines, fmt.Sprintf("[%s] !%d", workspace.Name, content.owner.workspaceAttention(workspace.ID)))
			for _, id := range workspace.WindowIDs {
				if window, ok := content.owner.windowByID(id); ok {
					lines = append(lines, fmt.Sprintf("  %d %s !%d", id, window.Name, content.owner.windowAttention(id)))
				}
			}
		}
		return lines
	case "agent-status":
		attentions := sortedAttentions(content.owner.snapshot.Attentions, false)
		lines := []string{"Agent attention — Enter navigate, m acknowledge"}
		for _, attention := range attentions {
			marker := "!"
			if attention.AcknowledgedAt != nil {
				marker = " "
			}
			lines = append(lines, fmt.Sprintf("%s %-8s pane:%d %s", marker, attention.Class, attention.PaneID, attention.Message))
		}
		return lines
	case "diagnostics":
		status := content.owner.daemonStatus
		lines := []string{
			"Diagnostics",
			fmt.Sprintf("revision: %d", content.owner.snapshot.Revision),
			fmt.Sprintf("panes: %d", len(content.owner.snapshot.Panes)),
			fmt.Sprintf("attentions: %d", len(content.owner.snapshot.Attentions)),
			fmt.Sprintf("views: %d", len(content.owner.views)),
			fmt.Sprintf("connections: %d sessions: %d", status.Connections, status.Sessions),
			"shell: " + strings.Join(content.owner.shell, " "),
			"editor: " + strings.Join(content.owner.editor, " "),
		}
		for _, plugin := range status.Plugins {
			state := "enabled"
			if !plugin.Enabled {
				state = "disabled: " + plugin.Error
			}
			lines = append(lines, "plugin "+plugin.Name+": "+state)
		}
		return lines
	default:
		return []string{"renderer unavailable"}
	}
}

func (content *builtinToolContent) activate() {
	index := content.selected - 1
	switch content.descriptor.Type {
	case "plugin-manager":
		content.owner.pluginManagerAction(index, "details")
	case "resource-list":
		unit, err := client.ParseResourceUnit(content.descriptor.Instance)
		if err != nil {
			return
		}
		list := client.ListResources(content.owner.snapshot, unit)
		if index < 0 || index >= len(list.Entries) {
			return
		}
		entry := list.Entries[index]
		if entry.Pane != nil {
			if entry.Stashed {
				content.owner.previewPaneByID(entry.Pane.ID)
			} else {
				content.owner.focusPane(entry.Pane.ID)
			}
		}
		if entry.Window != nil && !entry.Stashed {
			content.owner.selectWindow(entry.Window.ID)
		}
		if entry.Workspace != nil && len(entry.Workspace.WindowIDs) != 0 {
			content.owner.selectWindow(entry.Workspace.WindowIDs[0])
		}
	case "stash-list":
		content.owner.previewStashAt(index)
	case "workspace-list":
		content.owner.activateWorkspaceLine(index)
	case "agent-status":
		content.owner.navigateAttentionAt(index)
	}
}

func sortedAttentions(values []core.Attention, unreadOnly bool) []core.Attention {
	result := make([]core.Attention, 0, len(values))
	for _, value := range values {
		if !unreadOnly || value.AcknowledgedAt == nil {
			result = append(result, value)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := severityRank(result[i].Severity), severityRank(result[j].Severity)
		if left != right {
			return left > right
		}
		if !result[i].UpdatedAt.Equal(result[j].UpdatedAt) {
			return result[i].UpdatedAt.After(result[j].UpdatedAt)
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func severityRank(value core.AttentionSeverity) int {
	switch value {
	case core.SeverityCritical:
		return 4
	case core.SeverityError:
		return 3
	case core.SeverityWarning:
		return 2
	default:
		return 1
	}
}

func colorFromGhostty(color libghostty.Color) Color {
	return Color{R: color.R, G: color.G, B: color.B}
}

type paneRendererRegistry map[core.PaneKind]paneRenderer

func defaultPaneRendererRegistry() paneRendererRegistry {
	return paneRendererRegistry{
		core.PaneTerminal: terminalPaneRenderer{},
		core.PaneTool:     toolPaneRenderer{factories: defaultToolFactories()},
	}
}

func (session *session) paneRenderer(pane core.Pane) (paneRenderer, error) {
	if session.renderers == nil {
		session.renderers = defaultPaneRendererRegistry()
	}
	return session.renderers.renderer(pane)
}

func (registry paneRendererRegistry) renderer(pane core.Pane) (paneRenderer, error) {
	renderer := registry[pane.Kind]
	if renderer == nil {
		return nil, fmt.Errorf("TUI renderer for Pane kind %q is unavailable", pane.Kind)
	}
	return renderer, nil
}

func chromeFor(pane core.Pane, renderer paneRenderer) paneChrome {
	return chromeForMode(pane, renderer, PaneFrameFull)
}

func chromeForMode(pane core.Pane, renderer paneRenderer, mode PaneFrameMode) paneChrome {
	if mode == "" {
		mode = PaneFrameFull
	}
	chrome := pane.Presentation.Chrome
	if chrome == core.PaneChromeAuto {
		if mode == PaneFrameFull {
			chrome = renderer.DefaultChrome()
		} else {
			chrome = core.PaneChromeNone
		}
	}
	if chrome == core.PaneChromeNone {
		return noChrome{}
	}
	return borderChrome{}
}
