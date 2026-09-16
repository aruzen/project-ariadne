package tui

import (
	"fmt"
	"strconv"
	"strings"
)

type Cursor struct {
	X       int
	Y       int
	Visible bool
	Shape   uint8 // DECSCUSR: 0 default, 1/2 block, 3/4 underline, 5/6 bar
}

func EncodeFrame(surface *Surface, cursor Cursor) []byte {
	if surface == nil {
		return nil
	}
	var output strings.Builder
	output.Grow(surface.Width*surface.Height + 128)
	output.WriteString("\x1b[?25l\x1b[?7h\x1b[H")
	var previous Style
	haveStyle := false
	for y := 0; y < surface.Height; y++ {
		if y != 0 {
			output.WriteString("\x1b[")
			output.WriteString(strconv.Itoa(y + 1))
			output.WriteString(";1H")
		}
		for x := 0; x < surface.Width; x++ {
			cell := surface.At(x, y)
			if cell.Width == 0 {
				continue
			}
			if !haveStyle || cell.Style != previous {
				writeStyle(&output, cell.Style)
				previous = cell.Style
				haveStyle = true
			}
			writeCell(&output, surface, cell, x, y)
		}
	}
	writeCursor(&output, surface, cursor)
	return []byte(output.String())
}

func writeCursor(output *strings.Builder, surface *Surface, cursor Cursor) {
	output.WriteString("\x1b[0m\x1b[?7h")
	fmt.Fprintf(output, "\x1b[%d q", cursor.Shape)
	if cursor.Visible && cursor.X >= 0 && cursor.Y >= 0 && cursor.X < surface.Width && cursor.Y < surface.Height {
		fmt.Fprintf(output, "\x1b[%d;%dH\x1b[?25h", cursor.Y+1, cursor.X+1)
	} else {
		output.WriteString("\x1b[?25l")
	}
}

// EncodeDiff expands changed runs to include both halves of old/new wide cells.
func EncodeDiff(previous, surface *Surface, before, cursor Cursor) []byte {
	if surface == nil {
		return nil
	}
	if previous == nil || previous.Width != surface.Width || previous.Height != surface.Height {
		return EncodeFrame(surface, cursor)
	}
	var output strings.Builder
	haveChanges := false
	for y := 0; y < surface.Height; y++ {
		changed := make([]bool, surface.Width)
		for x := 0; x < surface.Width; x++ {
			if previous.At(x, y) == surface.At(x, y) {
				continue
			}
			changed[x] = true
			for _, frame := range []*Surface{previous, surface} {
				cell := frame.At(x, y)
				if cell.Width == 0 && x > 0 {
					changed[x-1] = true
				}
				if cell.Width == 2 && x+1 < surface.Width {
					changed[x+1] = true
				}
			}
		}
		// Close over every wide span touched by a changed run, including the
		// codepoint-width footprint of joined emoji on non-2027 terminals.
		for expanded := true; expanded; {
			expanded = false
			mark := func(x int) {
				if x >= 0 && x < surface.Width && !changed[x] {
					changed[x] = true
					expanded = true
				}
			}
			for x := 0; x < surface.Width; x++ {
				if !changed[x] {
					continue
				}
				for _, frame := range []*Surface{previous, surface} {
					cell := frame.At(x, y)
					if cell.Width == 0 {
						mark(x - 1)
					} else {
						span := max(int(cell.Width), legacyTextWidth(cell.Text))
						for n := 1; n < span; n++ {
							mark(x + n)
						}
					}
				}
			}
		}
		for x := 0; x < surface.Width; {
			if !changed[x] {
				x++
				continue
			}
			if !haveChanges {
				output.WriteString("\x1b[?25l\x1b[?7h")
				haveChanges = true
			}
			fmt.Fprintf(&output, "\x1b[%d;%dH", y+1, x+1)
			var previousStyle Style
			haveStyle := false
			for x < surface.Width && changed[x] {
				cell := surface.At(x, y)
				x++
				if cell.Width == 0 {
					continue
				}
				if !haveStyle || previousStyle != cell.Style {
					writeStyle(&output, cell.Style)
					previousStyle = cell.Style
					haveStyle = true
				}
				writeCell(&output, surface, cell, x-1, y)
			}
		}
	}
	if !haveChanges && before == cursor {
		return nil
	}
	writeCursor(&output, surface, cursor)
	diff := []byte(output.String())
	full := EncodeFrame(surface, cursor)
	if len(diff) >= len(full) {
		return full
	}
	return diff
}

func writeCell(output *strings.Builder, surface *Surface, cell Cell, x, y int) {
	legacy := legacyTextWidth(cell.Text) > int(cell.Width)
	// Keep autowrap for combining marks at the right edge, but prevent joined
	// emoji from wrapping into another row on legacy codepoint-width terminals.
	if legacy {
		output.WriteString("\x1b[?7l")
	}
	if cell.Text == "" {
		output.WriteByte(' ')
	} else {
		output.WriteString(cell.Text)
	}
	if legacy {
		output.WriteString("\x1b[?7h")
		fmt.Fprintf(output, "\x1b[%d;%dH", y+1, min(surface.Width, x+int(cell.Width)+1))
	}
}

func writeStyle(output *strings.Builder, style Style) {
	output.WriteString("\x1b[0")
	if style.Bold {
		output.WriteString(";1")
	}
	if style.Faint {
		output.WriteString(";2")
	}
	if style.Italic {
		output.WriteString(";3")
	}
	if style.UnderlineStyle != 0 {
		fmt.Fprintf(output, ";4:%d", style.UnderlineStyle)
	} else if style.Underline {
		output.WriteString(";4")
	}
	if style.Blink {
		output.WriteString(";5")
	}
	if style.Strikethrough {
		output.WriteString(";9")
	}
	if style.HasUnderlineColor {
		fmt.Fprintf(output, ";58:2::%d:%d:%d", style.UnderlineColor.R, style.UnderlineColor.G, style.UnderlineColor.B)
	}
	fmt.Fprintf(output, ";38;2;%d;%d;%d;48;2;%d;%d;%dm",
		style.Foreground.R, style.Foreground.G, style.Foreground.B,
		style.Background.R, style.Background.G, style.Background.B)
}
