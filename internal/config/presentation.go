package config

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
)

type PaneTitleMode string

const (
	PaneTitleAuto     PaneTitleMode = "auto"
	PaneTitlePane     PaneTitleMode = "pane"
	PaneTitleTerminal PaneTitleMode = "terminal"
)

func (mode PaneTitleMode) Valid() bool {
	return mode == PaneTitleAuto || mode == PaneTitlePane || mode == PaneTitleTerminal
}

// Keymaps are mode-local. Confirmation keys deliberately cannot be configured.
type Keymaps struct {
	Normal Keybindings `toml:"normal"`
	Copy   Keybindings `toml:"copy"`
	Prompt Keybindings `toml:"prompt"`
}

func DefaultKeymaps() Keymaps {
	return Keymaps{Normal: DefaultKeybindings(), Copy: Keybindings{
		"q": "cancel", "escape": "cancel", "h": "left", "j": "down", "k": "up", "l": "right",
		"left": "left", "down": "down", "up": "up", "right": "right",
		"ctrl-b": "page-up", "ctrl-f": "page-down", "page-up": "page-up", "page-down": "page-down",
		"ctrl-u": "half-up", "ctrl-d": "half-down",
		"g": "top", "G": "bottom", "space": "select", "enter": "copy", "/": "search", "n": "search-next", "N": "search-previous",
	}, Prompt: Keybindings{
		"enter": "accept", "escape": "cancel", "ctrl-c": "cancel", "backspace": "backspace", "ctrl-h": "backspace",
		"tab": "complete", "up": "history-previous", "ctrl-p": "history-previous", "down": "history-next", "ctrl-n": "history-next",
		"ctrl-u": "clear", "ctrl-w": "delete-word",
	}}
}

type ThemeStyle struct {
	Foreground string `toml:"foreground"`
	Background string `toml:"background"`
	Bold       bool   `toml:"bold"`
	Italic     bool   `toml:"italic"`
}

type LineGlyphs struct {
	Horizontal  string `toml:"horizontal"`
	Vertical    string `toml:"vertical"`
	TopLeft     string `toml:"top_left"`
	TopRight    string `toml:"top_right"`
	BottomLeft  string `toml:"bottom_left"`
	BottomRight string `toml:"bottom_right"`
	Cross       string `toml:"cross"`
	LeftJoin    string `toml:"left_join"`
	RightJoin   string `toml:"right_join"`
	TopJoin     string `toml:"top_join"`
	BottomJoin  string `toml:"bottom_join"`
}

type Theme struct {
	Base          ThemeStyle `toml:"base"`
	Border        ThemeStyle `toml:"border"`
	FocusedBorder ThemeStyle `toml:"focused_border"`
	Status        ThemeStyle `toml:"status"`
	Accent        ThemeStyle `toml:"accent"`
	Muted         ThemeStyle `toml:"muted"`
	Warning       ThemeStyle `toml:"warning"`
	Error         ThemeStyle `toml:"error"`
	Selection     ThemeStyle `toml:"selection"`
	Prompt        ThemeStyle `toml:"prompt"`
	Lines         LineGlyphs `toml:"lines"`
}

func DefaultTheme() Theme {
	base := ThemeStyle{Foreground: "#dcdcdc", Background: "#121418"}
	status := ThemeStyle{Foreground: "#e6e6e6", Background: "#262a30"}
	return Theme{Base: base, Border: ThemeStyle{Foreground: "#555a64", Background: base.Background},
		FocusedBorder: ThemeStyle{Foreground: "#6eb4ff", Background: base.Background, Bold: true}, Status: status,
		Accent: ThemeStyle{Foreground: "#14181c", Background: "#6eb4ff"}, Muted: ThemeStyle{Foreground: "#a0a5af", Background: status.Background},
		Warning: ThemeStyle{Foreground: "#ffd278", Background: status.Background}, Error: ThemeStyle{Foreground: "#ff6e6e", Background: status.Background},
		Selection: ThemeStyle{Foreground: base.Foreground, Background: "#506e96"}, Prompt: status,
		Lines: LineGlyphs{Horizontal: "─", Vertical: "│", TopLeft: "┌", TopRight: "┐", BottomLeft: "└", BottomRight: "┘", Cross: "┼", LeftJoin: "├", RightJoin: "┤", TopJoin: "┬", BottomJoin: "┴"}}
}

type StatusOptions struct {
	Left    []string                 `toml:"left"`
	Right   []string                 `toml:"right"`
	Widgets map[string]WidgetOptions `toml:"widgets"`
}

type WidgetOptions struct {
	Plugin     string   `toml:"plugin"`
	Command    []string `toml:"command"`
	Format     string   `toml:"format"`
	Style      string   `toml:"style"`
	CWD        string   `toml:"cwd"`
	IntervalMS int      `toml:"interval_ms"`
	TimeoutMS  int      `toml:"timeout_ms"`
	MaxBytes   int      `toml:"max_bytes"`
}

type CWDOptions struct {
	Terminal string            `toml:"terminal"`
	Tool     string            `toml:"tool"`
	Kinds    map[string]string `toml:"kinds"`
	Tools    map[string]string `toml:"tools"`
}

func DefaultStatusOptions() StatusOptions {
	return StatusOptions{Left: []string{"brand", "workspace", "pane"}, Right: []string{"attention", "state", "clock"}, Widgets: make(map[string]WidgetOptions)}
}

func ValidCWDSource(source string) bool {
	return source == "" || source == "pane" || source == "startup" || filepath.IsAbs(source)
}

func (configuration Config) validatePresentation() error {
	for name, bindings := range map[string]Keybindings{"normal": configuration.Keybindings.Normal, "copy": configuration.Keybindings.Copy, "prompt": configuration.Keybindings.Prompt} {
		if len(bindings) > MaxKeybindings {
			return fmt.Errorf("%w: keybindings.%s exceeds %d entries", ErrInvalid, name, MaxKeybindings)
		}
		for keys, commands := range bindings {
			if strings.TrimSpace(keys) == "" || len(keys) > MaxKeySequenceBytes || strings.ContainsRune(keys, 0) || len(commands) > MaxKeyCommandBytes || strings.ContainsRune(commands, 0) {
				return fmt.Errorf("%w: invalid keybindings.%s entry %q", ErrInvalid, name, keys)
			}
		}
	}
	t := configuration.TUI
	if !t.PaneTitle.Valid() {
		return fmt.Errorf("%w: tui.pane_title must be auto, pane, or terminal", ErrInvalid)
	}
	l := t.Theme.Lines
	for _, glyph := range []string{l.Horizontal, l.Vertical, l.TopLeft, l.TopRight, l.BottomLeft, l.BottomRight, l.Cross, l.LeftJoin, l.RightJoin, l.TopJoin, l.BottomJoin} {
		if uniseg.GraphemeClusterCount(glyph) != 1 || uniseg.StringWidth(glyph) != 1 {
			return fmt.Errorf("%w: theme.lines must use single-cell graphemes", ErrInvalid)
		}
		for _, r := range glyph {
			if unicode.IsControl(r) {
				return fmt.Errorf("%w: theme.lines cannot contain controls", ErrInvalid)
			}
		}
	}
	if t.MinPaneWidth < 1 || t.MinPaneHeight < 1 || t.MinPaneWidth > 65535 || t.MinPaneHeight > 65535 {
		return fmt.Errorf("%w: tui minimum pane size must be 1..65535", ErrInvalid)
	}
	for _, source := range append([]string{t.CWD.Terminal, t.CWD.Tool}, mapValues(t.CWD.Kinds, t.CWD.Tools)...) {
		if !ValidCWDSource(source) || strings.ContainsRune(source, 0) {
			return fmt.Errorf("%w: cwd must be pane, startup or an absolute path", ErrInvalid)
		}
	}
	for _, style := range []ThemeStyle{t.Theme.Base, t.Theme.Border, t.Theme.FocusedBorder, t.Theme.Status, t.Theme.Accent, t.Theme.Muted, t.Theme.Warning, t.Theme.Error, t.Theme.Selection, t.Theme.Prompt} {
		for _, color := range []string{style.Foreground, style.Background} {
			if len(color) != 7 || color[0] != '#' {
				return fmt.Errorf("%w: theme colors must be #RRGGBB", ErrInvalid)
			}
			if _, err := strconv.ParseUint(color[1:], 16, 24); err != nil {
				return fmt.Errorf("%w: invalid theme color %q", ErrInvalid, color)
			}
		}
	}
	knownStyles := map[string]bool{"": true, "base": true, "border": true, "focused_border": true, "status": true, "accent": true, "muted": true, "warning": true, "error": true, "selection": true, "prompt": true}
	builtins := map[string]bool{"brand": true, "workspace": true, "window": true, "pane": true, "attention": true, "state": true, "clock": true, "cwd": true}
	if len(t.Status.Widgets) > 64 || len(t.Status.Left)+len(t.Status.Right) > 64 {
		return fmt.Errorf("%w: too many status widgets", ErrInvalid)
	}
	for _, name := range append(append([]string(nil), t.Status.Left...), t.Status.Right...) {
		if _, ok := t.Status.Widgets[name]; !ok && !builtins[name] && !strings.Contains(name, "/") {
			return fmt.Errorf("%w: unknown status widget %q", ErrInvalid, name)
		}
	}
	for name, w := range t.Status.Widgets {
		if w.Plugin != "" {
			id, widget, ok := strings.Cut(w.Plugin, "/")
			if !ok || id == "" || widget == "" || strings.Contains(widget, "/") || len(w.Command) > 0 || builtins[name] {
				return fmt.Errorf("%w: invalid plugin widget %q", ErrInvalid, name)
			}
		}
		if name == "" || (!builtins[name] && len(w.Command) == 0 && w.Plugin == "") {
			return fmt.Errorf("%w: widget %q needs a command", ErrInvalid, name)
		}
		if !knownStyles[w.Style] || !ValidCWDSource(w.CWD) || strings.ContainsRune(w.CWD, 0) {
			return fmt.Errorf("%w: invalid widget %q style/cwd", ErrInvalid, name)
		}
		if w.IntervalMS < 0 || w.TimeoutMS < 0 || w.MaxBytes < 0 || w.IntervalMS > 86400000 || w.TimeoutMS > 60000 || w.MaxBytes > 1<<20 || len(w.Format) > 4096 {
			return fmt.Errorf("%w: invalid widget %q limits", ErrInvalid, name)
		}
		if err := validateCommand("tui.status.widgets."+name+".command", w.Command); err != nil {
			return err
		}
	}
	return nil
}

func mapValues(maps ...map[string]string) []string {
	var values []string
	for _, entries := range maps {
		for _, value := range entries {
			values = append(values, value)
		}
	}
	return values
}
