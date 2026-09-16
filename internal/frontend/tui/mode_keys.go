package tui

import (
	"time"

	"github.com/aruzen/ariadne/internal/config"
	"github.com/rivo/uniseg"
)

var copyActions = map[string]byte{"cancel": 'q', "left": 'h', "down": 'j', "up": 'k', "right": 'l', "half-up": 0x15, "half-down": 0x04, "top": 'g', "bottom": 'G', "select": ' ', "copy": '\r', "search": '/', "search-next": 'n', "search-previous": 'N', "page-up": 0x15, "page-down": 0x04}
var promptActions = map[string]byte{"accept": '\r', "cancel": 0x03, "backspace": 0x7f, "complete": '\t', "history-next": 0x0e, "history-previous": 0x10, "clear": 0x15, "delete-word": 0x17}

func modeDecoder(bindings config.Keybindings, copy bool) (inputDecoder, error) {
	actions := promptActions
	if copy {
		actions = copyActions
	}
	return newBindingDecoder(bindings, func(name string) bool { _, ok := actions[name]; return ok }, true)
}

func (session *session) modeInput(data []byte, copy bool) {
	decoder := &session.promptDecoder
	if copy {
		decoder = &session.copyDecoder
	}
	if decoder.bindings == nil {
		if copy {
			session.handleCopyInput(data)
		} else {
			session.handleModalInput(data)
		}
		return
	}
	for _, token := range decoder.Feed(data) {
		if !copy && len(token.data) > 0 {
			for _, b := range token.data {
				if b >= 0x20 && b != 0x7f {
					session.prompt += string([]byte{b})
					session.dirty = true
				}
			}
		}
		for _, command := range token.commands {
			if copy {
				if command == "page-up" || command == "page-down" {
					if content := session.activeTerminalContent(); content != nil {
						delta := content.rows
						if command == "page-up" {
							delta = -delta
						}
						_ = content.scroll(delta)
						session.dirty = true
					}
				} else {
					session.handleCopyInput([]byte{copyActions[command]})
				}
				if !session.copyMode || session.inputMode != inputModeNormal {
					break
				}
			} else {
				session.handleModalInput([]byte{promptActions[command]})
				if session.inputMode != inputModePrompt {
					break
				}
			}
		}
	}
	if len(decoder.pending) > 0 {
		session.modeKeyTime = time.Now()
	} else {
		session.modeKeyTime = time.Time{}
	}
}

func (session *session) flushModeEscape() {
	if session.modeKeyTime.IsZero() || time.Since(session.modeKeyTime) < 50*time.Millisecond {
		return
	}
	decoder := &session.promptDecoder
	if session.copyMode && session.inputMode == inputModeNormal {
		decoder = &session.copyDecoder
	}
	if string(decoder.pending) == "\x1b" {
		decoder.pending = nil
		for _, action := range decoder.bindings["\x1b"] {
			if session.inputMode == inputModePrompt {
				session.handleModalInput([]byte{promptActions[action]})
			} else if session.copyMode {
				session.handleCopyInput([]byte{copyActions[action]})
			}
		}
	}
	session.modeKeyTime = time.Time{}
}

func deleteLastGrapheme(value string) string {
	g := uniseg.NewGraphemes(value)
	last := 0
	for g.Next() {
		start, _ := g.Positions()
		last = start
	}
	return value[:last]
}
