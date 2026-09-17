package tui

import (
	"bytes"
	"time"

	"github.com/aruzen/ariadne/internal/core"
)

// Outer terminal framing stays ahead of configurable keys. Paste cannot run
// commands, and fragmented mouse reports cannot leak into a shell or prompt.
type inputFramer struct {
	pending  []byte
	last     time.Time
	pasting  bool
	paste    []byte
	tail     []byte
	overflow bool
	pane     core.PaneID
	mode     tuiInputMode
	copy     bool
}

var pasteStart = []byte("\x1b[200~")
var pasteEnd = []byte("\x1b[201~")
var mouseStart = []byte("\x1b[<")

func (session *session) processInput(data []byte) {
	f := &session.inputFrames
	var ordinary []byte
	flush := func() {
		if len(ordinary) > 0 {
			session.processKeys(ordinary)
			ordinary = nil
		}
	}
	for _, b := range data {
		if f.pasting {
			f.tail = append(f.tail, b)
			if bytes.Equal(f.tail, pasteEnd) {
				if !f.overflow && session.focus == f.pane && session.inputMode == f.mode && session.copyMode == f.copy {
					session.acceptPaste(f.paste)
				} else if f.overflow {
					session.setMessage("paste exceeds text limit")
				}
				f.pasting = false
				f.paste = nil
				f.tail = nil
				f.overflow = false
				continue
			}
			for len(f.tail) > 0 && !bytes.HasPrefix(pasteEnd, f.tail) {
				limit := session.clipboardMax
				if limit == 0 {
					limit = 1 << 20
				}
				if len(f.paste) < limit {
					f.paste = append(f.paste, f.tail[0])
				} else {
					f.overflow = true
				}
				f.tail = f.tail[1:]
			}
			continue
		}
		f.pending = append(f.pending, b)
		f.last = time.Now()
		if bytes.Equal(f.pending, pasteStart) {
			flush()
			f.pending = nil
			f.pasting = true
			f.pane = session.focus
			f.mode = session.inputMode
			f.copy = session.copyMode
			continue
		}
		if bytes.HasPrefix(f.pending, mouseStart) {
			if b == 'm' || b == 'M' {
				flush()
				if e, ok := parseMouse(f.pending); ok {
					session.handleMouse(e)
				}
				f.pending = nil
			} else if len(f.pending) > 64 || (len(f.pending) > 3 && b != ';' && (b < '0' || b > '9')) {
				f.pending = nil
			}
			continue
		}
		for len(f.pending) > 0 && !bytes.HasPrefix(pasteStart, f.pending) && !bytes.HasPrefix(mouseStart, f.pending) {
			ordinary = append(ordinary, f.pending[0])
			f.pending = f.pending[1:]
		}
	}
	flush()
}

func (session *session) flushInputFrames() {
	f := &session.inputFrames
	if f.pasting || len(f.pending) == 0 || time.Since(f.last) < 50*time.Millisecond {
		return
	}
	data := f.pending
	f.pending = nil
	if !bytes.HasPrefix(data, mouseStart) {
		session.processKeys(data)
	}
}

func (session *session) acceptPaste(data []byte) {
	if session.inputMode == inputModeConfirm || session.copyMode {
		return
	}
	if session.inputMode == inputModePrompt {
		session.prompt += cleanText(string(data))
		session.dirty = true
		return
	}
	if content := session.activeTerminalContent(); content != nil {
		if view := session.views[session.focus]; view != nil && view.attachment != nil {
			session.queuePTYInput(view, paneInput{data: data, paste: true})
		}
		return
	}
	if view := session.views[session.focus]; view != nil {
		if content, ok := view.content.(*externalToolContent); ok {
			_, err := content.HandlePaste(data)
			if err != nil {
				session.setMessage(err.Error())
			}
			return
		}
	}
	// Tool input is text-only; newline/control bytes must not activate actions.
	session.sendInput([]byte(cleanText(string(data))))
}
