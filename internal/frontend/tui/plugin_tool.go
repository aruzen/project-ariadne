package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/plugin/external"
)

type externalToolContent struct {
	owner      *session
	pane       core.PaneID
	provider   string
	id         string
	cols, rows int
	generation uint64
	running    bool
	closed     bool
	next       time.Time
	frame      *v1.Frame
	err        string
	inputs     chan v1.Input
	inputBytes atomic.Int64
	overflow   bool
	cancel     context.CancelFunc
}

func newExternalToolContent(s *session, p core.Pane) paneContent {
	limits, _ := s.pluginLimits.Normalize()
	ctx, cancel := context.WithCancel(s.ctx)
	c := &externalToolContent{owner: s, pane: p.ID, provider: p.Tool.Provider, id: fmt.Sprintf("%d-%d", p.ID, time.Now().UnixNano()), generation: 1, inputs: make(chan v1.Input, limits.ControlQueue), cancel: cancel}
	go func() {
		for {
			select {
			case input := <-c.inputs:
				data, _ := json.Marshal(input)
				c.inputBytes.Add(-int64(len(data) + 128))
				result, err := s.client.Plugin(ctx, v1.ManageRequest{Action: "input", ID: c.provider, Input: &input})
				s.sendPluginResult(pluginResult{result: result, err: err, content: c})
			case <-ctx.Done():
				return
			}
		}
	}()
	return c
}
func (c *externalToolContent) view() v1.View {
	var runtime uint64
	if c.frame != nil {
		runtime = c.frame.RuntimeGeneration
	}
	return v1.View{ID: c.id, Generation: c.generation, RuntimeGeneration: runtime, PaneID: uint64(c.pane), Width: c.cols, Height: c.rows}
}
func (c *externalToolContent) Resize(cols, rows int) error {
	if c.cols != cols || c.rows != rows {
		c.cols, c.rows = cols, rows
		c.generation++
		c.frame = nil
		c.next = time.Time{}
	}
	return nil
}
func (c *externalToolContent) refresh(now time.Time) {
	if c.closed || c.running || c.cols < 1 || c.rows < 1 || now.Before(c.next) {
		return
	}
	c.running = true
	c.next = now.Add(100 * time.Millisecond)
	view := c.view()
	generation := c.generation
	go func() {
		result, err := c.owner.client.Plugin(c.owner.ctx, v1.ManageRequest{Action: "render", ID: c.provider, View: &view})
		c.owner.sendPluginResult(pluginResult{result: result, err: err, content: c, generation: generation, render: true})
	}()
}
func (c *externalToolContent) Draw(surface *Surface, rect Rect, _ core.Pane, focused bool, style Style) (Cursor, error) {
	frame := c.frame
	if frame == nil {
		surface.Text(rect.X, rect.Y, rect.W, "Unavailable: "+c.provider, style)
		if c.err != "" {
			surface.Text(rect.X, rect.Y+1, rect.W, c.err, style)
		}
		return Cursor{}, nil
	}
	for y := 0; y < rect.H && y < frame.Height; y++ {
		for x := 0; x < rect.W && x < frame.Width; x++ {
			source := frame.Cells[y*frame.Width+x]
			style := Style{Foreground: Color(source.Style.Foreground), Background: Color(source.Style.Background), Bold: source.Style.Bold, Italic: source.Style.Italic, Underline: source.Style.Underline, Strikethrough: source.Style.Strikethrough, Faint: source.Style.Faint, Blink: source.Style.Blink, UnderlineStyle: source.Style.UnderlineStyle, UnderlineColor: Color(source.Style.UnderlineColor), HasUnderlineColor: source.Style.HasUnderlineColor}
			if source.Width == 2 && x+1 >= rect.W {
				source.Text = " "
				source.Width = 1
			}
			target := (rect.Y+y)*surface.Width + rect.X + x
			if target >= 0 && target < len(surface.Cells) {
				surface.Cells[target] = Cell{Text: source.Text, Width: source.Width, Style: style}
			}
		}
	}
	cursor := frame.Cursor
	if focused && cursor.Visible {
		return Cursor{X: rect.X + cursor.X, Y: rect.Y + cursor.Y, Visible: true, Shape: cursor.Shape}, nil
	}
	return Cursor{}, nil
}
func (c *externalToolContent) enqueue(input v1.Input) (bool, error) {
	input.View = c.view()
	data, _ := json.Marshal(input)
	size := int64(len(data) + 128)
	limit := external.DefaultConfig().ControlBytes
	if c.owner != nil && c.owner.pluginLimits.ControlBytes > 0 {
		limit = c.owner.pluginLimits.ControlBytes
	}
	overflow := c.inputBytes.Add(size) > int64(limit)
	if !overflow {
		select {
		case c.inputs <- input:
			return true, nil
		default:
		}
	}
	c.inputBytes.Add(-size)
	if !c.overflow {
		c.overflow = true
		if c.owner != nil && c.owner.client != nil {
			c.cancel()
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_, _ = c.owner.client.Plugin(ctx, v1.ManageRequest{Action: "fault", ID: c.provider})
			}()
		}
	}
	return true, fmt.Errorf("plugin: Tool input queue overflow")
}
func (c *externalToolContent) HandleInput(data []byte) (bool, error) {
	return c.enqueue(v1.Input{Data: append([]byte(nil), data...)})
}
func (c *externalToolContent) HandlePaste(data []byte) (bool, error) {
	return c.enqueue(v1.Input{Data: append([]byte(nil), data...), Paste: true})
}
func (c *externalToolContent) HandleMouse(e MouseEvent) (bool, error) {
	return c.enqueue(v1.Input{Mouse: &v1.Mouse{X: e.X, Y: e.Y, Button: e.Button, Action: e.Action, Wheel: e.Wheel, Shift: e.Mods&4 != 0, Alt: e.Mods&8 != 0, Ctrl: e.Mods&16 != 0}})
}
func (c *externalToolContent) Close() {
	if c.closed {
		return
	}
	c.closed = true
	c.cancel()
	view := c.view()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = c.owner.client.Plugin(ctx, v1.ManageRequest{Action: "view.close", ID: c.provider, View: &view})
	}()
}
