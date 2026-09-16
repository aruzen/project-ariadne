package tui

import (
	"errors"
	"fmt"

	"github.com/aruzen/ariadne/internal/vt/libghostty"
)

// Semantic inputs wait for VT encoding without blocking the event loop. Raw
// input stays behind them, so a following Enter cannot overtake a busy paste.
type paneInput struct {
	data  []byte
	paste bool
	mouse *paneMouseInput
}

type paneMouseInput struct {
	event      MouseEvent
	cols, rows int
	pressed    bool
}

func (input paneInput) size() int { return len(input.data) + 64 }

func (view *paneView) enqueueInput(input paneInput) error {
	if len(view.inputQueue) > 0 {
		last := &view.inputQueue[len(view.inputQueue)-1]
		if input.mouse != nil && last.mouse != nil && input.mouse.event.Action == 2 && last.mouse.event.Action == 2 && input.mouse.event.Button == last.mouse.event.Button {
			// Intermediate motion can be replaced, but never a press or release.
			last.mouse = input.mouse
			return nil
		}
		if input.mouse == nil && !input.paste && last.mouse == nil && !last.paste {
			if view.inputBytes+len(input.data) > 4<<20 {
				return errors.New("TUI pending input queue limit exceeded")
			}
			last.data = append(last.data, input.data...)
			view.inputBytes += len(input.data)
			return nil
		}
	}
	if view.inputBytes+input.size() > 4<<20 || len(view.inputQueue) >= 64 {
		return errors.New("TUI pending input queue limit exceeded")
	}
	input.data = append([]byte(nil), input.data...)
	view.inputQueue = append(view.inputQueue, input)
	view.inputBytes += input.size()
	return nil
}

func (view *paneView) drainInput(send func([]byte) error) error {
	for len(view.inputQueue) > 0 {
		input := view.inputQueue[0]
		data := input.data
		var err error
		if input.paste || input.mouse != nil {
			content, ok := view.content.(*terminalPaneContent)
			if !ok || content.terminal == nil {
				err = fmt.Errorf("TUI terminal content is unavailable")
			} else if input.paste {
				data, err = content.paste(data, true)
			} else {
				m := input.mouse
				e := m.event
				data, err = content.terminal.EncodeMouse(e.Action, e.Button, e.Mods, e.X, e.Y, m.cols, m.rows, m.pressed)
			}
		}
		if errors.Is(err, libghostty.ErrBusy) {
			return nil
		}
		view.inputQueue[0] = paneInput{}
		view.inputQueue = view.inputQueue[1:]
		view.inputBytes -= input.size()
		if err != nil {
			return err
		}
		if len(data) > 0 {
			if err := send(data); err != nil {
				return err
			}
		}
	}
	view.inputQueue = nil
	return nil
}

func (session *session) queuePTYInput(view *paneView, input paneInput) {
	if err := view.enqueueInput(input); err != nil {
		session.setMessage(err.Error())
		return
	}
	session.flushPTYInput(view)
}

func (session *session) flushPTYInput(view *paneView) {
	if view.attachment == nil || len(view.inputQueue) == 0 {
		return
	}
	if err := view.drainInput(func(data []byte) error {
		return session.writePTYInput(view, data)
	}); err != nil {
		view.inputQueue, view.inputBytes = nil, 0
		session.setMessage(err.Error())
	}
}
