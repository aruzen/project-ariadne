// Package tui implements the full-screen terminal frontend.
package tui

import "strings"

type Color struct {
	R uint8
	G uint8
	B uint8
}

type Style struct {
	Foreground    Color
	Background    Color
	Bold          bool
	Italic        bool
	Underline     bool
	Strikethrough bool
	Faint         bool
	Blink         bool
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
	for _, character := range value {
		if position >= width {
			break
		}
		surface.Set(x+position, y, Cell{Text: string(character), Width: 1, Style: style})
		position++
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
	runes := []rune(strings.ReplaceAll(value, "\x00", ""))
	if len(runes) <= width {
		return string(runes)
	}
	if width == 1 {
		return "…"
	}
	return string(runes[:width-1]) + "…"
}
