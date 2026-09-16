package tui

import (
	"fmt"
	"strconv"
	"unicode"

	"github.com/aruzen/ariadne/internal/config"
	"github.com/rivo/uniseg"
)

type presentationTheme struct {
	styles map[string]Style
	glyphs map[string]string
}

func resolveTheme(theme config.Theme) (presentationTheme, error) {
	result := presentationTheme{styles: make(map[string]Style), glyphs: make(map[string]string)}
	for name, source := range map[string]config.ThemeStyle{"base": theme.Base, "border": theme.Border, "focused_border": theme.FocusedBorder, "status": theme.Status, "accent": theme.Accent, "muted": theme.Muted, "warning": theme.Warning, "error": theme.Error, "selection": theme.Selection, "prompt": theme.Prompt} {
		parse := func(value string) (Color, error) {
			if len(value) != 7 || value[0] != '#' {
				return Color{}, fmt.Errorf("theme.%s: expected #RRGGBB", name)
			}
			rgb, err := strconv.ParseUint(value[1:], 16, 24)
			return Color{R: uint8(rgb >> 16), G: uint8(rgb >> 8), B: uint8(rgb)}, err
		}
		fg, err := parse(source.Foreground)
		if err != nil {
			return result, err
		}
		bg, err := parse(source.Background)
		if err != nil {
			return result, err
		}
		result.styles[name] = Style{Foreground: fg, Background: bg, Bold: source.Bold, Italic: source.Italic}
	}
	l := theme.Lines
	for original, replacement := range map[string]string{"─": l.Horizontal, "│": l.Vertical, "┌": l.TopLeft, "┐": l.TopRight, "└": l.BottomLeft, "┘": l.BottomRight, "┼": l.Cross, "├": l.LeftJoin, "┤": l.RightJoin, "┬": l.TopJoin, "┴": l.BottomJoin} {
		g := uniseg.NewGraphemes(replacement)
		if !g.Next() || g.Width() != 1 || g.Next() {
			return result, fmt.Errorf("theme.lines: %q must be one single-cell grapheme", replacement)
		}
		for _, r := range replacement {
			if unicode.IsControl(r) {
				return result, fmt.Errorf("theme.lines: control character")
			}
		}
		result.glyphs[original] = replacement
	}
	return result, nil
}

func (surface *Surface) themeStyle(name string) Style {
	if surface.Theme != nil {
		return surface.Theme.styles[name]
	}
	defaults, _ := resolveTheme(config.DefaultTheme())
	return defaults.styles[name]
}

func (surface *Surface) frameStyle(focused bool) Style {
	if focused {
		return surface.themeStyle("focused_border")
	}
	return surface.themeStyle("border")
}

func (surface *Surface) lineGlyph(original string) string {
	if surface.Theme != nil {
		if glyph, ok := surface.Theme.glyphs[original]; ok {
			return glyph
		}
	}
	return original
}
