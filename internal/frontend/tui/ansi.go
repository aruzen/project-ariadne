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
}

func EncodeFrame(surface *Surface, cursor Cursor) []byte {
	if surface == nil {
		return nil
	}
	var output strings.Builder
	output.Grow(surface.Width*surface.Height + 128)
	output.WriteString("\x1b[H")
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
			if cell.Text == "" {
				output.WriteByte(' ')
			} else {
				output.WriteString(cell.Text)
			}
		}
	}
	output.WriteString("\x1b[0m")
	if cursor.Visible && cursor.X >= 0 && cursor.Y >= 0 && cursor.X < surface.Width && cursor.Y < surface.Height {
		fmt.Fprintf(&output, "\x1b[%d;%dH\x1b[?25h", cursor.Y+1, cursor.X+1)
	} else {
		output.WriteString("\x1b[?25l")
	}
	return []byte(output.String())
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
	if style.Underline {
		output.WriteString(";4")
	}
	if style.Blink {
		output.WriteString(";5")
	}
	if style.Strikethrough {
		output.WriteString(";9")
	}
	fmt.Fprintf(output, ";38;2;%d;%d;%d;48;2;%d;%d;%dm",
		style.Foreground.R, style.Foreground.G, style.Foreground.B,
		style.Background.R, style.Background.G, style.Background.B)
}
