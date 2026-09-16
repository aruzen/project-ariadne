package tui

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"unicode/utf8"

	ariadneconfig "github.com/aruzen/ariadne/internal/config"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/protocol"
	"github.com/aruzen/ariadne/internal/vt/libghostty"
)

type clipboardRequest struct {
	paneID   core.PaneID
	request  libghostty.ClipboardRequest
	response chan libghostty.ClipboardResponse
}

func (session *session) clipboardHandler(ctx context.Context, paneID core.PaneID) libghostty.ClipboardHandler {
	return func(request libghostty.ClipboardRequest) libghostty.ClipboardResponse {
		if len(request.Text) > session.clipboardMax || !utf8.ValidString(request.Text) {
			return libghostty.ClipboardResponse{}
		}
		operation := clipboardRequest{paneID: paneID, request: request, response: make(chan libghostty.ClipboardResponse, 1)}
		select {
		case session.clipboardRequests <- operation:
		case <-ctx.Done():
			return libghostty.ClipboardResponse{}
		}
		select {
		case response := <-operation.response:
			return response
		case <-ctx.Done():
			return libghostty.ClipboardResponse{}
		}
	}
}

func (session *session) handleClipboardRequest(request clipboardRequest) {
	if session.inputMode != inputModeNormal {
		session.pendingClipboard = append(session.pendingClipboard, request)
		return
	}
	policy := session.clipboardRead
	if request.request.Operation == libghostty.ClipboardWrite {
		policy = session.clipboardWrite
	}
	if policy == ariadneconfig.ClipboardDeny {
		session.finishClipboardRequest(request, libghostty.ClipboardResponse{})
		return
	}
	perform := func() {
		response, err := session.performClipboardRequest(request)
		if err != nil {
			session.setMessage(err.Error())
		}
		session.finishClipboardRequest(request, response)
	}
	if policy == ariadneconfig.ClipboardAllow {
		perform()
		return
	}
	message := fmt.Sprintf("pane %d %s clipboard via %s (%d bytes)?",
		request.paneID, request.request.Operation, request.request.Protocol, len(request.request.Text))
	session.beginConfirmation(message, func(allowed bool) {
		if !allowed {
			session.finishClipboardRequest(request, libghostty.ClipboardResponse{})
			return
		}
		perform()
	})
}

func (session *session) performClipboardRequest(request clipboardRequest) (libghostty.ClipboardResponse, error) {
	switch request.request.Operation {
	case libghostty.ClipboardWrite:
		_, err := callTUI[protocol.ClipboardResult](session, protocol.OperationClipboardWrite, protocol.ClipboardWriteParams{
			PaneID: request.paneID, Protocol: request.request.Protocol, Text: request.request.Text, Approved: true,
		})
		if err == nil {
			session.queueOuterClipboard(request.request.Text)
		}
		return libghostty.ClipboardResponse{Allowed: err == nil}, err
	case libghostty.ClipboardRead:
		result, err := callTUI[protocol.ClipboardResult](session, protocol.OperationClipboardRead, protocol.ClipboardReadParams{
			PaneID: request.paneID, Protocol: request.request.Protocol, Approved: true,
		})
		if err != nil {
			return libghostty.ClipboardResponse{}, err
		}
		return libghostty.ClipboardResponse{Allowed: true, Text: result.Text}, nil
	default:
		return libghostty.ClipboardResponse{}, errors.New("unsupported clipboard operation")
	}
}

func (session *session) finishClipboardRequest(request clipboardRequest, response libghostty.ClipboardResponse) {
	request.response <- response
	session.resumeClipboardRequests()
}

func (session *session) resumeClipboardRequests() {
	if session.inputMode == inputModeNormal && len(session.pendingClipboard) != 0 {
		next := session.pendingClipboard[0]
		session.pendingClipboard = session.pendingClipboard[1:]
		session.handleClipboardRequest(next)
	}
}

func (session *session) enterCopyMode() {
	session.copyDecoder.pending = nil
	content := session.activeTerminalContent()
	if content == nil {
		session.setMessage("focused pane has no terminal view")
		return
	}
	session.copyMode = true
	session.copySelecting = false
	session.copyX = 0
	session.copyY = max(0, content.rows-1)
	session.copyStartX = session.copyX
	session.copyStartY = session.copyY
	session.copyNewOutput = false
	_ = content.clearSelection()
	session.dirty = true
}

func (session *session) leaveCopyMode() {
	if content := session.activeTerminalContent(); content != nil {
		_ = content.clearSelection()
		_ = content.scrollBottom()
	}
	session.copyMode = false
	session.copySelecting = false
	session.copyNewOutput = false
	session.dirty = true
}

func (session *session) handleCopyInput(data []byte) {
	content := session.activeTerminalContent()
	if content == nil {
		session.leaveCopyMode()
		return
	}
	for _, value := range data {
		switch value {
		case 0x1b, 'q':
			session.leaveCopyMode()
			return
		case 'h':
			session.moveCopyCursor(content, -1, 0, libghostty.SelectionLeft)
		case 'j':
			session.moveCopyCursor(content, 0, 1, libghostty.SelectionDown)
		case 'k':
			session.moveCopyCursor(content, 0, -1, libghostty.SelectionUp)
		case 'l':
			session.moveCopyCursor(content, 1, 0, libghostty.SelectionRight)
		case 0x15:
			if err := content.scroll(-max(1, content.rows/2)); err != nil {
				session.setMessage(err.Error())
			}
		case 0x04:
			if err := content.scroll(max(1, content.rows/2)); err != nil {
				session.setMessage(err.Error())
			}
		case 'g':
			if err := content.scrollTop(); err != nil {
				session.setMessage(err.Error())
			}
		case 'G':
			if err := content.scrollBottom(); err != nil {
				session.setMessage(err.Error())
			}
		case ' ':
			if err := content.beginSelection(session.copyX, session.copyY); err != nil {
				session.setMessage(err.Error())
			} else {
				session.copySelecting = true
				session.copyStartX = session.copyX
				session.copyStartY = session.copyY
			}
		case '\r', '\n':
			session.copySelection(content)
			return
		case '/':
			session.beginPromptWithCallback("/", "", session.searchCopyMode)
			return
		case 'n':
			session.repeatSearch(content, true)
		case 'N':
			session.repeatSearch(content, false)
		}
	}
	session.dirty = true
}

func (session *session) moveCopyCursor(content *terminalPaneContent, dx, dy int, adjustment libghostty.SelectionAdjust) {
	nextX, nextY := session.copyX+dx, session.copyY+dy
	if nextY < 0 {
		_ = content.scroll(-1)
		nextY = 0
	} else if nextY >= content.rows {
		_ = content.scroll(1)
		nextY = content.rows - 1
	}
	session.copyX = min(max(0, nextX), max(0, content.cols-1))
	session.copyY = min(max(0, nextY), max(0, content.rows-1))
	if session.copySelecting {
		if err := content.adjustSelection(adjustment); err != nil {
			session.setMessage(err.Error())
		}
	}
}

func (session *session) copySelection(content *terminalPaneContent) {
	text, err := content.selectionText()
	if err != nil {
		session.setMessage(err.Error())
		return
	}
	session.authorizeUserClipboard(libghostty.ClipboardWrite, "copy-mode", len(text), func() {
		_, writeErr := callTUI[protocol.ClipboardResult](session, protocol.OperationClipboardWrite, protocol.ClipboardWriteParams{
			PaneID: session.focus, Protocol: "copy-mode", Text: text, Approved: true,
		})
		if writeErr != nil {
			session.setMessage(writeErr.Error())
			return
		}
		session.queueOuterClipboard(text)
		session.leaveCopyMode()
		session.setMessage(fmt.Sprintf("copied %d bytes", len(text)))
	})
}

func (session *session) queueOuterClipboard(text string) {
	if os.Getenv("SSH_CONNECTION") == "" && os.Getenv("SSH_TTY") == "" {
		return
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	session.outerClipboard = append(session.outerClipboard,
		[]byte("\x1b]52;c;"+encoded+"\x07")...)
	session.dirty = true
}

func (session *session) requestPaste() {
	content := session.activeTerminalContent()
	if content == nil {
		session.setMessage("focused pane has no terminal view")
		return
	}
	session.authorizeUserClipboard(libghostty.ClipboardRead, "paste", 0, func() {
		result, err := callTUI[protocol.ClipboardResult](session, protocol.OperationClipboardRead, protocol.ClipboardReadParams{
			PaneID: session.focus, Protocol: "paste", Approved: true,
		})
		if err != nil {
			session.setMessage(err.Error())
			return
		}
		session.pasteText(content, []byte(result.Text), false)
	})
}

func (session *session) pasteText(content *terminalPaneContent, text []byte, allowUnsafe bool) {
	data, err := content.paste(text, allowUnsafe)
	if errors.Is(err, libghostty.ErrUnsafePaste) {
		session.beginConfirmation(fmt.Sprintf("paste potentially unsafe text (%d bytes)?", len(text)), func(allowed bool) {
			if allowed {
				session.pasteText(content, text, true)
			}
		})
		return
	}
	if err != nil {
		session.setMessage(err.Error())
		return
	}
	if view := session.views[session.focus]; view != nil && len(data) != 0 {
		session.sendPTYInput(view, data)
	}
}

func (session *session) authorizeUserClipboard(operation libghostty.ClipboardOperation, protocolName string, bytes int, perform func()) {
	policy := session.clipboardRead
	if operation == libghostty.ClipboardWrite {
		policy = session.clipboardWrite
	}
	if policy == ariadneconfig.ClipboardDeny {
		session.setMessage("clipboard access denied by policy")
		return
	}
	if policy == ariadneconfig.ClipboardAllow {
		perform()
		return
	}
	session.beginConfirmation(fmt.Sprintf("pane %d %s clipboard via %s (%d bytes)?", session.focus, operation, protocolName, bytes), func(allowed bool) {
		if allowed {
			perform()
		}
	})
}

func (session *session) searchCopyMode(query string) {
	if query == "" {
		return
	}
	session.searchQuery = query
	session.repeatSearch(session.activeTerminalContent(), true)
}

func (session *session) repeatSearch(content *terminalPaneContent, next bool) {
	if content == nil || session.searchQuery == "" {
		return
	}
	total, selected, err := content.search(session.searchQuery, next)
	if err != nil {
		session.setMessage(err.Error())
		return
	}
	session.setMessage(fmt.Sprintf("match %d/%d", selected+1, total))
}

func (session *session) activeTerminalContent() *terminalPaneContent {
	view := session.views[session.focus]
	if view == nil {
		return nil
	}
	content, _ := view.content.(*terminalPaneContent)
	return content
}
