// Package tui implements the full-screen terminal frontend.
package tui

import (
	"github.com/rivo/uniseg"
	"strings"
	"unicode"
)

type Color struct {
	R uint8
	G uint8
	B uint8
}

type Style struct {
	Foreground        Color
	Background        Color
	Bold              bool
	Italic            bool
	Underline         bool
	UnderlineStyle    uint8
	UnderlineColor    Color
	HasUnderlineColor bool
	Strikethrough     bool
	Faint             bool
	Blink             bool
}

type Cell struct {
	Text  string
	Width uint8
	Style Style
}

type Surface struct {
	Width  int
	Height int
	Cells  []Cell
	Theme  *presentationTheme
}

func NewSurface(width, height int, style Style) *Surface {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	surface := &Surface{Width: width, Height: height, Cells: make([]Cell, width*height)}
	for index := range surface.Cells {
		surface.Cells[index] = Cell{Text: " ", Width: 1, Style: style}
	}
	return surface
}

func (surface *Surface) At(x, y int) Cell {
	if surface == nil || x < 0 || y < 0 || x >= surface.Width || y >= surface.Height {
		return Cell{}
	}
	return surface.Cells[y*surface.Width+x]
}

func (surface *Surface) Set(x, y int, cell Cell) {
	if surface == nil || x < 0 || y < 0 || x >= surface.Width || y >= surface.Height {
		return
	}
	if cell.Width == 0 {
		cell.Width = 1
	}
	if cell.Text == "" {
		cell.Text = " "
	}
	old := surface.At(x, y)
	if old.Width == 0 && x > 0 {
		surface.Cells[y*surface.Width+x-1] = Cell{Text: " ", Width: 1, Style: old.Style}
	}
	if old.Width == 2 && x+1 < surface.Width {
		surface.Cells[y*surface.Width+x+1] = Cell{Text: " ", Width: 1, Style: old.Style}
	}
	if cell.Width == 2 && x+1 >= surface.Width {
		cell.Text, cell.Width = " ", 1
	}
	if cell.Width == 2 && surface.At(x+1, y).Width == 2 && x+2 < surface.Width {
		surface.Cells[y*surface.Width+x+2] = Cell{Text: " ", Width: 1, Style: surface.At(x+1, y).Style}
	}
	surface.Cells[y*surface.Width+x] = cell
	if cell.Width == 2 && x+1 < surface.Width {
		surface.Cells[y*surface.Width+x+1] = Cell{Width: 0, Style: cell.Style}
	}
}

func (surface *Surface) Text(x, y, width int, value string, style Style) {
	if width <= 0 {
		return
	}
	position := 0
	graphemes := uniseg.NewGraphemes(cleanText(value))
	for graphemes.Next() {
		cells := graphemes.Width()
		if cells == 0 {
			continue
		}
		if position+cells > width {
			break
		}
		surface.Set(x+position, y, Cell{Text: graphemes.Str(), Width: uint8(cells), Style: style})
		position += cells
	}
	for position < width {
		surface.Set(x+position, y, Cell{Text: " ", Width: 1, Style: style})
		position++
	}
}

func fitText(value string, width int) string {
	if width <= 0 {
		return ""
	}
	value = cleanText(value)
	if textWidth(value) <= width {
		return value
	}
	if width == 1 {
		return "…"
	}
	var result strings.Builder
	graphemes := uniseg.NewGraphemes(value)
	used := 0
	for graphemes.Next() {
		if used+graphemes.Width() > width-1 {
			break
		}
		result.WriteString(graphemes.Str())
		used += graphemes.Width()
	}
	return result.String() + "…"
}

func textWidth(value string) int { return uniseg.StringWidth(value) }

func legacyTextWidth(value string) int {
	width := 0
	for _, r := range value {
		width += uniseg.StringWidth(string(r))
	}
	return width
}

func cleanText(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(value, "�"))
}
