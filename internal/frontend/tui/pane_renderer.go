package tui

import (
	"fmt"

	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/vt/libghostty"
)

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
	NewContent(core.Pane) paneContent
}

type terminalPaneRenderer struct{}

func (terminalPaneRenderer) DefaultChrome() core.PaneChrome { return core.PaneChromeBorder }
func (terminalPaneRenderer) UsesTerminal() bool             { return true }
func (terminalPaneRenderer) Focusable(core.Pane) bool       { return true }
func (terminalPaneRenderer) NewContent(core.Pane) paneContent {
	return &terminalPaneContent{}
}

type terminalPaneContent struct {
	terminal *libghostty.Terminal
	cols     int
	rows     int
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

func (content *terminalPaneContent) Draw(surface *Surface, rect Rect, _ core.Pane, focused bool, _ Style) (Cursor, error) {
	if content.terminal == nil || rect.W <= 0 || rect.H <= 0 {
		return Cursor{}, nil
	}
	screen, err := content.terminal.Screen()
	if err != nil {
		return Cursor{}, err
	}
	for y := 0; y < rect.H && y < screen.Rows; y++ {
		for x := 0; x < rect.W && x < screen.Cols; x++ {
			source := screen.At(x, y)
			style := Style{
				Foreground: colorFromGhostty(source.Style.Foreground), Background: colorFromGhostty(source.Style.Background),
				Bold: source.Style.Bold, Italic: source.Style.Italic, Underline: source.Style.Underline,
				Strikethrough: source.Style.Strikethrough, Faint: source.Style.Faint, Blink: source.Style.Blink,
			}
			target := (rect.Y+y)*surface.Width + rect.X + x
			if target >= 0 && target < len(surface.Cells) {
				surface.Cells[target] = Cell{Text: source.Text, Width: source.Width, Style: style}
			}
		}
	}
	if focused && screen.Cursor.Visible && screen.Cursor.X < rect.W && screen.Cursor.Y < rect.H {
		return Cursor{X: rect.X + screen.Cursor.X, Y: rect.Y + screen.Cursor.Y, Visible: true}, nil
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

type fixedPaneRenderer struct{}

func (fixedPaneRenderer) DefaultChrome() core.PaneChrome { return core.PaneChromeBorder }
func (fixedPaneRenderer) UsesTerminal() bool             { return false }
func (fixedPaneRenderer) Focusable(core.Pane) bool       { return true }
func (fixedPaneRenderer) NewContent(core.Pane) paneContent {
	return emptyPaneContent{}
}

type emptyPaneContent struct{}

func (emptyPaneContent) Resize(int, int) error { return nil }
func (emptyPaneContent) Draw(*Surface, Rect, core.Pane, bool, Style) (Cursor, error) {
	return Cursor{}, nil
}
func (emptyPaneContent) HandleInput([]byte) (bool, error) { return false, nil }
func (emptyPaneContent) Close()                           {}

func colorFromGhostty(color libghostty.Color) Color {
	return Color{R: color.R, G: color.G, B: color.B}
}

type paneRendererRegistry map[core.PaneKind]paneRenderer

func defaultPaneRendererRegistry() paneRendererRegistry {
	return paneRendererRegistry{
		core.PaneTerminal: terminalPaneRenderer{},
		core.PaneFixed:    fixedPaneRenderer{},
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
	chrome := pane.Presentation.Chrome
	if chrome == core.PaneChromeAuto {
		chrome = renderer.DefaultChrome()
	}
	if chrome == core.PaneChromeNone {
		return noChrome{}
	}
	return borderChrome{}
}
