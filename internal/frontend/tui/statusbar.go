package tui

import (
	"fmt"
	"time"

	"github.com/aruzen/ariadne/internal/core"
)

type StatusContext struct {
	Workspace string
	Window    string
	PaneID    core.PaneID
	PaneTitle string
	State     core.TerminalState
	Message   string
	Now       time.Time
}

type Segment struct {
	Text  string
	Style Style
}

// StatusWidget is intentionally independent of PTY and libghostty state.
type StatusWidget interface {
	Segments(StatusContext) []Segment
}

type StatusWidgetFunc func(StatusContext) []Segment

func (function StatusWidgetFunc) Segments(context StatusContext) []Segment {
	return function(context)
}

type StatusBar struct {
	Left       []StatusWidget
	Right      []StatusWidget
	Background Style
}

func DefaultStatusBar() StatusBar {
	background := Style{Foreground: Color{R: 230, G: 230, B: 230}, Background: Color{R: 38, G: 42, B: 48}}
	accent := background
	accent.Foreground = Color{R: 20, G: 24, B: 28}
	accent.Background = Color{R: 110, G: 180, B: 255}
	muted := background
	muted.Foreground = Color{R: 160, G: 165, B: 175}
	return StatusBar{
		Background: background,
		Left: []StatusWidget{
			StatusWidgetFunc(func(context StatusContext) []Segment {
				return []Segment{{Text: " ariadne ", Style: accent}}
			}),
			StatusWidgetFunc(func(context StatusContext) []Segment {
				return []Segment{{Text: fmt.Sprintf(" %s/%s ", context.Workspace, context.Window), Style: background}}
			}),
			StatusWidgetFunc(func(context StatusContext) []Segment {
				value := fmt.Sprintf(" pane:%d", context.PaneID)
				if context.PaneTitle != "" {
					value += " " + context.PaneTitle
				}
				return []Segment{{Text: value + " ", Style: muted}}
			}),
		},
		Right: []StatusWidget{
			StatusWidgetFunc(func(context StatusContext) []Segment {
				value := context.Message
				if value == "" {
					value = string(context.State)
				}
				if value == "" {
					value = "ready"
				}
				return []Segment{{Text: " " + value + " ", Style: muted}}
			}),
			StatusWidgetFunc(func(context StatusContext) []Segment {
				return []Segment{{Text: context.Now.Format(" 15:04 "), Style: background}}
			}),
		},
	}
}

func (bar StatusBar) Draw(surface *Surface, y int, context StatusContext) {
	if surface == nil || y < 0 || y >= surface.Height {
		return
	}
	surface.Text(0, y, surface.Width, "", bar.Background)
	left := flattenWidgets(bar.Left, context)
	right := flattenWidgets(bar.Right, context)
	rightWidth := segmentsWidth(right)
	leftLimit := surface.Width - rightWidth
	position := 0
	for _, segment := range left {
		if position >= leftLimit {
			break
		}
		value := fitText(segment.Text, leftLimit-position)
		surface.Text(position, y, len([]rune(value)), value, segment.Style)
		position += len([]rune(value))
	}
	position = surface.Width - rightWidth
	if position < 0 {
		position = 0
	}
	for _, segment := range right {
		if position >= surface.Width {
			break
		}
		value := fitText(segment.Text, surface.Width-position)
		surface.Text(position, y, len([]rune(value)), value, segment.Style)
		position += len([]rune(value))
	}
}

func flattenWidgets(widgets []StatusWidget, context StatusContext) []Segment {
	var result []Segment
	for _, widget := range widgets {
		if widget != nil {
			result = append(result, widget.Segments(context)...)
		}
	}
	return result
}

func segmentsWidth(segments []Segment) int {
	total := 0
	for _, segment := range segments {
		total += len([]rune(segment.Text))
	}
	return total
}
