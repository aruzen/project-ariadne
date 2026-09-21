package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/aruzen/ariadne/internal/client"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/protocol"
	"github.com/aruzen/streammux/pty"
)

type tuiInputMode uint8

const (
	inputModeNormal tuiInputMode = iota
	inputModePrompt
	inputModeConfirm
)

func (session *session) beginPrompt(lead, initial string) {
	session.cancelMouseCapture()
	session.promptDecoder.pending = nil
	session.inputMode = inputModePrompt
	session.promptLead = lead
	session.prompt = initial
	session.promptHistoryIndex = len(session.promptHistory)
	session.promptDraft = initial
	session.promptCallback = session.executePrompt
	session.dirty = true
	if session.height <= 1 {
		session.relayout()
		session.syncViews()
	}
}

func (session *session) executeCommandSequence(commands []string) {
	for index, command := range commands {
		session.commandPending = false
		session.executePrompt(command)
		if session.commandPending {
			session.pendingCommands = append(session.pendingCommands[:0], commands[index+1:]...)
			return
		}
		if session.quitRequested || session.inputMode != inputModeNormal || session.copyMode {
			return
		}
	}
}

func (session *session) beginPromptWithCallback(lead, initial string, callback func(string)) {
	session.beginPrompt(lead, initial)
	session.promptCallback = callback
}

func (session *session) beginConfirmation(message string, callback func(bool)) {
	session.cancelMouseCapture()
	session.inputMode = inputModeConfirm
	session.confirm = message
	session.confirmCallback = callback
	session.dirty = true
	if session.height <= 1 {
		session.relayout()
		session.syncViews()
	}
}

func (session *session) handleModalInput(data []byte) {
	if session.inputMode == inputModeConfirm {
		for _, value := range data {
			switch value {
			case 'y', 'Y':
				callback := session.confirmCallback
				session.clearInputMode()
				if callback != nil {
					callback(true)
				}
				session.resumeClipboardRequests()
				return
			case 'n', 'N', 0x03, 0x1b, '\r', '\n':
				if (value == 0x03 || value == 0x1b) && session.activePluginDialogue != nil {
					session.cancelPluginDialogue()
					return
				}
				callback := session.confirmCallback
				session.clearInputMode()
				if callback != nil {
					callback(false)
				}
				session.resumeClipboardRequests()
				return
			}
		}
		return
	}
	for index := 0; index < len(data); index++ {
		value := data[index]
		if value == 0x1b && index+2 < len(data) && data[index+1] == '[' {
			switch data[index+2] {
			case 'A':
				session.movePromptHistory(-1)
				index += 2
				continue
			case 'B':
				session.movePromptHistory(1)
				index += 2
				continue
			}
		}
		switch value {
		case '\r', '\n':
			command := session.prompt
			callback := session.promptCallback
			session.recordPromptHistory(command)
			session.clearInputMode()
			if callback != nil {
				callback(command)
			}
			session.resumeClipboardRequests()
			return
		case 0x03, 0x1b:
			if session.activePluginDialogue != nil {
				session.cancelPluginDialogue()
				return
			}
			session.clearInputMode()
			session.resumeClipboardRequests()
			return
		case 0x08, 0x7f:
			session.prompt = deleteLastGrapheme(session.prompt)
		case 0x09:
			session.completePrompt()
		case 0x0e:
			session.movePromptHistory(1)
		case 0x10:
			session.movePromptHistory(-1)
		case 0x15:
			session.prompt = ""
			session.promptHistoryIndex = len(session.promptHistory)
			session.promptDraft = ""
		case 0x17:
			session.prompt = strings.TrimRightFunc(session.prompt, func(value rune) bool { return unicode.IsSpace(value) })
			session.prompt = strings.TrimRightFunc(session.prompt, func(value rune) bool { return !unicode.IsSpace(value) })
			session.prompt = strings.TrimRightFunc(session.prompt, func(value rune) bool { return unicode.IsSpace(value) })
		default:
			if value >= 0x20 {
				session.prompt += string([]byte{value})
			}
		}
	}
	session.dirty = true
}

func (session *session) recordPromptHistory(command string) {
	if session.promptLead != ":" {
		return
	}
	command = strings.TrimSpace(command)
	if command == "" || (len(session.promptHistory) != 0 && session.promptHistory[len(session.promptHistory)-1] == command) {
		return
	}
	session.promptHistory = append(session.promptHistory, command)
	if len(session.promptHistory) > 100 {
		session.promptHistory = append([]string(nil), session.promptHistory[len(session.promptHistory)-100:]...)
	}
}

func (session *session) movePromptHistory(delta int) {
	if session.promptLead != ":" || len(session.promptHistory) == 0 {
		return
	}
	if session.promptHistoryIndex < 0 || session.promptHistoryIndex > len(session.promptHistory) {
		session.promptHistoryIndex = len(session.promptHistory)
	}
	if session.promptHistoryIndex == len(session.promptHistory) {
		session.promptDraft = session.prompt
	}
	next := session.promptHistoryIndex + delta
	if next < 0 {
		next = 0
	}
	if next > len(session.promptHistory) {
		next = len(session.promptHistory)
	}
	session.promptHistoryIndex = next
	if next == len(session.promptHistory) {
		session.prompt = session.promptDraft
	} else {
		session.prompt = session.promptHistory[next]
	}
}

func (session *session) completePrompt() {
	if session.promptLead != ":" {
		return
	}
	prefix := session.prompt
	if strings.HasPrefix(prefix, "plugin run ") {
		matches := []string{}
		for _, p := range session.pluginStatus.Plugins {
			for _, c := range p.Manifest.Commands {
				candidate := "plugin run " + p.Manifest.ID + " " + c.Name
				if strings.HasPrefix(candidate, prefix) {
					matches = append(matches, candidate)
				}
			}
		}
		if len(matches) == 1 {
			session.prompt = matches[0] + " "
		}
		return
	}
	if prefix == "" || strings.IndexFunc(prefix, unicode.IsSpace) >= 0 {
		return
	}
	matches := make([]string, 0)
	for _, name := range promptCommandNames() {
		if strings.HasPrefix(name, prefix) {
			matches = append(matches, name)
		}
	}
	if len(matches) == 0 {
		return
	}
	completion := matches[0]
	for _, match := range matches[1:] {
		completion = commonPrefix(completion, match)
	}
	session.prompt = completion
	if len(matches) == 1 {
		session.prompt += " "
	}
}

func commonPrefix(left, right string) string {
	limit := min(len(left), len(right))
	index := 0
	for index < limit && left[index] == right[index] {
		index++
	}
	return left[:index]
}

func (session *session) clearInputMode() {
	session.inputMode = inputModeNormal
	session.prompt = ""
	session.promptLead = ""
	session.confirm = ""
	session.confirmCallback = nil
	session.promptCallback = nil
	session.dirty = true
	if session.height <= 1 {
		session.relayout()
		session.syncViews()
	}
}

func (session *session) executePrompt(commandLine string) {
	fields, err := parsePromptCommand(commandLine)
	if err != nil {
		session.setMessage("command parse error: " + err.Error())
		return
	}
	if len(fields) == 0 {
		return
	}
	switch fields[0] {
	case "plugin":
		session.pluginCommand(fields[1:])
	case "list":
		unit := client.ResourceWindow
		if len(fields) > 2 {
			session.setMessage("usage: list [pane|window|workspace]")
			return
		}
		if len(fields) == 2 {
			var err error
			unit, err = client.ParseResourceUnit(fields[1])
			if err != nil {
				session.setMessage(err.Error())
				return
			}
		}
		session.openResourceList(unit)
	case "delete":
		unit, id, err := client.ParseResourceTarget(fields[1:], map[client.ResourceUnit]uint64{client.ResourcePane: uint64(session.focus), client.ResourceWindow: uint64(session.window), client.ResourceWorkspace: uint64(session.workspace)})
		if err != nil {
			session.setMessage(err.Error())
			return
		}
		session.deleteResource(unit, id)
	case "help", "commands":
		if len(fields) == 1 {
			session.openBuiltinTool("help")
			return
		}
		if len(fields) != 2 {
			session.setMessage("usage: help [COMMAND]")
			return
		}
		if description, ok := promptCommandDescription(fields[1]); ok {
			session.setMessage(description)
		} else {
			session.setMessage("unknown command: " + fields[1])
		}
	case "command-palette", "palette":
		if len(fields) != 1 {
			session.setMessage("usage: command-palette")
			return
		}
		session.openBuiltinTool("command-palette")
	case "command-prompt", "prompt":
		initial := strings.Join(fields[1:], " ")
		if initial != "" {
			initial += " "
		}
		session.beginPrompt(":", initial)
	case "detach", "quit":
		if len(fields) != 1 {
			session.setMessage("usage: detach")
			return
		}
		session.quitRequested = true
		session.cancel()
	case "send-key":
		if len(fields) < 2 {
			session.setMessage("usage: send-key KEYS...")
			return
		}
		sequence, parseErr := parseKeySequence(strings.Join(fields[1:], " "))
		if parseErr != nil {
			session.setMessage(parseErr.Error())
			return
		}
		session.sendInput(sequence)
	case "edit", "editor":
		direction := core.SplitHorizontal
		arguments := fields[1:]
		if len(arguments) != 0 && (arguments[0] == "h" || arguments[0] == "v") {
			if arguments[0] == "v" {
				direction = core.SplitVertical
			}
			arguments = arguments[1:]
		}
		if len(arguments) != 0 && arguments[0] == "--" {
			arguments = arguments[1:]
		}
		if len(session.editor) == 0 {
			session.setMessage("no editor configured")
			return
		}
		argv := append([]string(nil), session.editor...)
		argv = append(argv, arguments...)
		session.splitTerminalCommand(direction, argv)
	case "split", "split-pane", "split-window":
		if len(fields) < 2 || (fields[1] != "h" && fields[1] != "v") {
			session.setMessage("usage: split h|v [-- command...]")
			return
		}
		direction := core.SplitHorizontal
		if fields[1] == "v" {
			direction = core.SplitVertical
		}
		arguments := fields[2:]
		if len(arguments) != 0 && arguments[0] == "--" {
			arguments = arguments[1:]
		}
		if len(fields) > 2 && len(arguments) == 0 {
			session.setMessage("usage: split h|v [-- command...]")
			return
		}
		session.splitTerminalCommand(direction, arguments)
	case "focus", "select-pane":
		if len(fields) != 2 {
			session.setMessage("usage: focus left|down|up|right|PANE")
			return
		}
		if action, ok := directionalAction(fields[1], false); ok {
			if !session.zoom {
				session.moveFocus(action)
			}
			return
		}
		id, parseErr := strconv.ParseUint(fields[1], 10, 64)
		if parseErr != nil || id == 0 {
			session.setMessage("usage: focus left|down|up|right|PANE")
			return
		}
		session.focusPane(core.PaneID(id))
	case "resize", "resize-pane":
		if len(fields) != 2 {
			session.setMessage("usage: resize left|down|up|right")
			return
		}
		action, ok := directionalAction(fields[1], true)
		if !ok {
			session.setMessage("usage: resize left|down|up|right")
			return
		}
		session.resizeFocusedPane(action)
	case "zoom":
		if len(fields) > 2 {
			session.setMessage("usage: zoom [on|off|toggle]")
			return
		}
		mode := "toggle"
		if len(fields) == 2 {
			mode = fields[1]
		}
		session.setZoom(mode)
	case "kill", "kill-pane":
		id, ok := optionalID(fields, uint64(session.focus))
		if !ok {
			session.setMessage("usage: kill [PANE]")
			return
		}
		_, err := callTUI[protocol.TerminalOperationResult](session, protocol.OperationStopTerminal, protocol.PaneParams{PaneID: core.PaneID(id)})
		session.reportCommand(err, "terminal stopped")
	case "close":
		id, ok := optionalID(fields, uint64(session.focus))
		if !ok {
			session.setMessage("usage: close [PANE]")
			return
		}
		session.closePane(core.PaneID(id))
	case "close-confirm":
		id, ok := optionalID(fields, uint64(session.focus))
		if !ok {
			session.setMessage("usage: close-confirm [PANE]")
			return
		}
		paneID := core.PaneID(id)
		session.beginConfirmation(fmt.Sprintf("close pane %d?", paneID), func(allowed bool) {
			if allowed {
				session.closePane(paneID)
			}
		})
	case "dismiss":
		id, ok := optionalID(fields, uint64(session.focus))
		if !ok {
			session.setMessage("usage: dismiss [PANE]")
			return
		}
		session.dismissPane(core.PaneID(id))
	case "restart":
		id, ok := optionalID(fields, uint64(session.focus))
		if !ok {
			session.setMessage("usage: restart [PANE]")
			return
		}
		session.restartPane(core.PaneID(id))
	case "run":
		arguments := fields[1:]
		paneID := session.focus
		if len(arguments) >= 2 && arguments[1] == "--" {
			id, parseErr := strconv.ParseUint(arguments[0], 10, 64)
			if parseErr != nil || id == 0 {
				session.setMessage("usage: run [PANE --] command [args...]")
				return
			}
			paneID = core.PaneID(id)
			arguments = arguments[2:]
		}
		if len(arguments) != 0 && arguments[0] == "--" {
			arguments = arguments[1:]
		}
		if len(arguments) == 0 {
			session.setMessage("usage: run [PANE --] command [args...]")
			return
		}
		session.runPane(paneID, arguments)
	case "new-window":
		name := ""
		if len(fields) > 1 {
			name = strings.Join(fields[1:], " ")
		}
		session.createNamedWindow(name)
	case "next-window":
		if !session.requireNoArguments(fields, "next-window") {
			return
		}
		session.selectRelativeWindow(1)
	case "previous-window":
		if !session.requireNoArguments(fields, "previous-window") {
			return
		}
		session.selectRelativeWindow(-1)
	case "window", "select-window":
		id, ok := parseID(fields)
		if !ok {
			session.setMessage("usage: window ID")
			return
		}
		session.selectWindow(core.WindowID(id))
	case "new-workspace":
		if len(fields) < 2 || strings.TrimSpace(strings.Join(fields[1:], " ")) == "" {
			session.setMessage("usage: new-workspace NAME")
			return
		}
		session.createWorkspace(strings.Join(fields[1:], " "))
	case "next-workspace":
		if !session.requireNoArguments(fields, "next-workspace") {
			return
		}
		session.selectRelativeWorkspace(1)
	case "previous-workspace", "prev-workspace":
		if !session.requireNoArguments(fields, "previous-workspace") {
			return
		}
		session.selectRelativeWorkspace(-1)
	case "workspace", "select-workspace":
		id, ok := parseID(fields)
		if !ok {
			session.setMessage("usage: workspace ID")
			return
		}
		session.selectWorkspace(core.WorkspaceID(id))
	case "rename-window":
		id, name, ok := namedTarget(fields, uint64(session.window))
		if !ok {
			session.setMessage("usage: rename-window [ID] NAME")
			return
		}
		_, err := callTUI[core.WindowResult](session, protocol.OperationRenameWindow, protocol.RenameWindowParams{
			WindowID: core.WindowID(id), Name: name,
		})
		session.reportCommand(err, "window renamed")
	case "rename-workspace":
		id, name, ok := namedTarget(fields, uint64(session.workspace))
		if !ok {
			session.setMessage("usage: rename-workspace [ID] NAME")
			return
		}
		_, err := callTUI[core.WorkspaceResult](session, protocol.OperationRenameWorkspace, protocol.RenameWorkspaceParams{
			WorkspaceID: core.WorkspaceID(id), Name: name,
		})
		session.reportCommand(err, "workspace renamed")
	case "delete-window":
		id, ok := optionalID(fields, uint64(session.window))
		if !ok {
			session.setMessage("usage: delete-window [ID]")
			return
		}
		_, err := callTUI[core.DeleteWindowResult](session, protocol.OperationDeleteWindow, protocol.DeleteWindowParams{WindowID: core.WindowID(id)})
		session.reportCommand(err, "window deleted")
	case "delete-workspace":
		id, ok := optionalID(fields, uint64(session.workspace))
		if !ok {
			session.setMessage("usage: delete-workspace [ID]")
			return
		}
		_, err := callTUI[core.DeleteWorkspaceResult](session, protocol.OperationDeleteWorkspace, protocol.DeleteWorkspaceParams{WorkspaceID: core.WorkspaceID(id)})
		session.reportCommand(err, "workspace deleted")
	case "move-pane":
		session.moveFocusedPane(fields[1:])
	case "stash-pane":
		paneID := session.focus
		if len(fields) == 2 {
			id, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil || id == 0 {
				session.setMessage("usage: stash-pane [PANE]")
				return
			}
			paneID = core.PaneID(id)
		} else if len(fields) != 1 {
			session.setMessage("usage: stash-pane [PANE]")
			return
		}
		session.stashPane(paneID)
	case "stash-window":
		windowID := session.window
		if len(fields) == 2 {
			id, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil || id == 0 {
				session.setMessage("usage: stash-window [WINDOW]")
				return
			}
			windowID = core.WindowID(id)
		} else if len(fields) != 1 {
			session.setMessage("usage: stash-window [WINDOW]")
			return
		}
		session.stashWindow(windowID)
	case "stash-list", "list-stash":
		if !session.requireNoArguments(fields, "stash-list") {
			return
		}
		session.openBuiltinTool("stash-list")
	case "stash-show":
		if !session.requireNoArguments(fields, "stash-show") {
			return
		}
		session.showStash()
	case "restore-pane":
		session.restorePane(fields[1:])
	case "restore-window":
		session.restoreWindow(fields[1:])
	case "tool":
		session.createTool(fields[1:])
	case "open-tool":
		if len(fields) != 2 {
			session.setMessage("usage: open-tool TYPE")
			return
		}
		session.openBuiltinTool(fields[1])
	case "workspaces", "windows":
		if !session.requireNoArguments(fields, fields[0]) {
			return
		}
		session.openBuiltinTool("workspace-list")
	case "agent-status":
		if !session.requireNoArguments(fields, "agent-status") {
			return
		}
		session.openBuiltinTool("agent-status")
	case "diagnostics", "status":
		if !session.requireNoArguments(fields, fields[0]) {
			return
		}
		session.openBuiltinTool("diagnostics")
	case "preview-pane":
		id, ok := parseID(fields)
		if !ok {
			session.setMessage("usage: preview-pane PANE")
			return
		}
		session.previewPaneByID(core.PaneID(id))
	case "preview-exit":
		if len(fields) != 1 {
			session.setMessage("usage: preview-exit")
			return
		}
		session.exitPreview()
	case "attention-next":
		if !session.requireNoArguments(fields, "attention-next") {
			return
		}
		session.navigateAttention(1)
	case "attention-prev", "attention-previous":
		if !session.requireNoArguments(fields, "attention-prev") {
			return
		}
		session.navigateAttention(-1)
	case "attention-ack":
		if !session.requireNoArguments(fields, "attention-ack") {
			return
		}
		session.ackCurrentAttention()
	case "attention":
		session.executeAttentionCommand(fields[1:])
	case "copy-mode":
		if len(fields) != 1 {
			session.setMessage("usage: copy-mode")
			return
		}
		session.enterCopyMode()
	case "paste":
		if len(fields) != 1 {
			session.setMessage("usage: paste")
			return
		}
		session.requestPaste()
	default:
		session.setMessage("unknown command: " + fields[0])
	}
}

func directionalAction(value string, resize bool) (inputAction, bool) {
	if resize {
		switch value {
		case "left", "h":
			return actionResizeLeft, true
		case "down", "j":
			return actionResizeDown, true
		case "up", "k":
			return actionResizeUp, true
		case "right", "l":
			return actionResizeRight, true
		}
	}
	switch value {
	case "left", "h":
		return actionFocusLeft, true
	case "down", "j":
		return actionFocusDown, true
	case "up", "k":
		return actionFocusUp, true
	case "right", "l":
		return actionFocusRight, true
	default:
		return actionNone, false
	}
}

func optionalID(fields []string, fallback uint64) (uint64, bool) {
	if len(fields) == 1 {
		return fallback, fallback != 0
	}
	if len(fields) != 2 {
		return 0, false
	}
	id, err := strconv.ParseUint(fields[1], 10, 64)
	return id, err == nil && id != 0
}

func namedTarget(fields []string, fallback uint64) (uint64, string, bool) {
	if len(fields) < 2 {
		return 0, "", false
	}
	id := fallback
	nameStart := 1
	if len(fields) >= 3 {
		if parsed, err := strconv.ParseUint(fields[1], 10, 64); err == nil && parsed != 0 {
			id = parsed
			nameStart = 2
		}
	}
	name := strings.Join(fields[nameStart:], " ")
	return id, name, id != 0 && strings.TrimSpace(name) != ""
}

func (session *session) requireNoArguments(fields []string, usage string) bool {
	if len(fields) == 1 {
		return true
	}
	session.setMessage("usage: " + usage)
	return false
}

func parseID(fields []string) (uint64, bool) {
	if len(fields) != 2 {
		return 0, false
	}
	id, err := strconv.ParseUint(fields[1], 10, 64)
	return id, err == nil && id != 0
}

func callTUI[T any](session *session, operation protocol.Operation, params any) (T, error) {
	ctx, cancel := context.WithTimeout(session.ctx, commandTimeout)
	defer cancel()
	return client.Call[T](ctx, session.client, operation, params)
}

func (session *session) splitTerminal(direction core.SplitDirection) {
	session.splitTerminalCommand(direction, nil)
}

func (session *session) splitTerminalCommand(direction core.SplitDirection, argv []string) {
	if session.window == 0 {
		session.setMessage("no active window")
		return
	}
	params := session.newTerminalParams(session.window)
	if len(argv) != 0 {
		params.Argv = append([]string(nil), argv...)
	}
	if session.focus != 0 {
		if !session.canSplit(direction, core.Pane{Kind: core.PaneTerminal, Presentation: params.Presentation}) {
			return
		}
		params.TargetPaneID = session.focus
		params.Direction = direction
	}
	result, err := callTUI[protocol.TerminalOperationResult](session, protocol.OperationNewTerminal, params)
	if err != nil {
		session.setMessage(err.Error())
		return
	}
	session.focus = result.Pane.ID
	_, err = callTUI[core.SetFocusResult](session, protocol.OperationSetFocus, protocol.SetFocusParams{PaneID: result.Pane.ID})
	session.reportCommand(err, fmt.Sprintf("pane %d created", result.Pane.ID))
}

func (session *session) newTerminalParams(windowID core.WindowID) protocol.NewTerminalParams {
	cols, rows := 80, 24
	if placement, exists := placementFor(session.placements, session.focus); exists {
		cols, rows = max(1, placement.Rect.W), max(1, placement.Rect.H)
	}
	return protocol.NewTerminalParams{
		WindowID: windowID, Argv: append([]string(nil), session.shell...), CWD: session.cwd,
		Env: append([]string(nil), session.env...), InitialSize: pty.Size{Cols: cols, Rows: rows},
	}
}

func (session *session) closeFocusedPane() {
	session.closePane(session.focus)
}

func (session *session) closePane(paneID core.PaneID) {
	pane, exists := session.pane(paneID)
	if !exists {
		session.setMessage(fmt.Sprintf("pane %d not found", paneID))
		return
	}
	var err error
	if pane.Kind == core.PaneTerminal && pane.Terminal != nil {
		switch pane.Terminal.State {
		case core.TerminalRunning:
			_, err = callTUI[protocol.TerminalOperationResult](session, protocol.OperationKillTerminal, protocol.PaneParams{PaneID: pane.ID})
		case core.TerminalPlaceholder, core.TerminalExited, core.TerminalFailed:
			_, err = callTUI[protocol.TerminalOperationResult](session, protocol.OperationDismissTerminal, protocol.PaneParams{PaneID: pane.ID})
		default:
			err = fmt.Errorf("pane %d is busy", pane.ID)
		}
	} else {
		_, err = callTUI[core.ClosePaneResult](session, protocol.OperationClosePane, protocol.PaneParams{PaneID: pane.ID})
	}
	if err == nil && session.focus == pane.ID {
		session.zoom = false
	}
	session.reportCommand(err, fmt.Sprintf("pane %d closed", pane.ID))
}

func (session *session) dismissFocused() {
	session.dismissPane(session.focus)
}

func (session *session) dismissPane(paneID core.PaneID) {
	if paneID == 0 {
		session.setMessage("no focused pane")
		return
	}
	_, err := callTUI[protocol.TerminalOperationResult](session, protocol.OperationDeletePane, protocol.PaneParams{PaneID: paneID})
	session.reportCommand(err, "pane deleted")
}

func (session *session) deleteResource(unit client.ResourceUnit, id uint64) {
	var err error
	switch unit {
	case client.ResourcePane:
		_, err = callTUI[protocol.TerminalOperationResult](session, protocol.OperationDeletePane, protocol.PaneParams{PaneID: core.PaneID(id)})
	case client.ResourceWindow:
		_, err = callTUI[core.DeleteWindowResult](session, protocol.OperationDeleteWindow, protocol.DeleteWindowParams{WindowID: core.WindowID(id)})
	case client.ResourceWorkspace:
		_, err = callTUI[core.DeleteWorkspaceResult](session, protocol.OperationDeleteWorkspace, protocol.DeleteWorkspaceParams{WorkspaceID: core.WorkspaceID(id)})
	}
	session.reportCommand(err, fmt.Sprintf("%s %d deleted", unit, id))
}

func (session *session) restartFocused() {
	session.restartPane(session.focus)
}

func (session *session) restartPane(paneID core.PaneID) {
	if paneID == 0 {
		session.setMessage("no focused pane")
		return
	}
	result, err := callTUI[protocol.TerminalOperationResult](session, protocol.OperationRestartTerminal, protocol.RestartTerminalParams{
		PaneID: paneID, Env: append([]string(nil), session.env...), InitialSize: session.panePTYSize(paneID),
	})
	session.reportCommand(err, "pane restarted")
	if err == nil && !session.isStashedPane(result.Pane.ID) {
		session.focusPane(result.Pane.ID)
	}
}

func (session *session) runFocused(argv []string) {
	session.runPane(session.focus, argv)
}

func (session *session) runPane(paneID core.PaneID, argv []string) {
	if paneID == 0 {
		session.setMessage("no focused pane")
		return
	}
	result, err := callTUI[protocol.TerminalOperationResult](session, protocol.OperationRunTerminal, protocol.RunTerminalParams{
		PaneID: paneID, Argv: append([]string(nil), argv...), FallbackCWD: session.cwd,
		Env: append([]string(nil), session.env...), InitialSize: session.panePTYSize(paneID),
	})
	session.reportCommand(err, "command started")
	if err == nil && !session.isStashedPane(result.Pane.ID) {
		session.focusPane(result.Pane.ID)
	}
}

func (session *session) focusedPTYSize() pty.Size {
	return session.panePTYSize(session.focus)
}

func (session *session) panePTYSize(paneID core.PaneID) pty.Size {
	if placement, exists := placementFor(session.placements, paneID); exists {
		return pty.Size{Cols: max(1, placement.Rect.W), Rows: max(1, placement.Rect.H)}
	}
	return pty.Size{Cols: 80, Rows: 24}
}

func (session *session) createWindow() {
	session.createNamedWindow("")
}

func (session *session) createNamedWindow(name string) {
	if session.workspace == 0 {
		session.setMessage("no active workspace")
		return
	}
	if name == "" {
		name = fmt.Sprintf("window-%d", session.snapshot.NextWindowID)
	}
	created, err := callTUI[core.CreateWindowResult](session, protocol.OperationCreateWindow, protocol.CreateWindowParams{
		WorkspaceID: session.workspace, Name: name,
	})
	if err != nil {
		session.setMessage(err.Error())
		return
	}
	session.selectWindow(created.Window.ID)
	session.splitTerminal(core.SplitHorizontal)
}

func (session *session) createWorkspace(name string) {
	created, err := callTUI[core.CreateWorkspaceResult](session, protocol.OperationCreateWorkspace, protocol.CreateWorkspaceParams{Name: name})
	if err != nil {
		session.setMessage(err.Error())
		return
	}
	window, err := callTUI[core.CreateWindowResult](session, protocol.OperationCreateWindow, protocol.CreateWindowParams{
		WorkspaceID: created.Workspace.ID, Name: "main",
	})
	if err != nil {
		session.setMessage(err.Error())
		return
	}
	session.selectWindow(window.Window.ID)
}

func (session *session) selectWindow(windowID core.WindowID) {
	if session.copyMode {
		session.leaveCopyMode()
	}
	selected, err := callTUI[core.SetFocusResult](session, protocol.OperationSelectWindow, protocol.SelectWindowParams{WindowID: windowID})
	if err != nil {
		session.setMessage(err.Error())
		return
	}
	session.workspace = selected.Focus.WorkspaceID
	session.window = selected.Focus.WindowID
	session.focus = selected.Focus.PaneID
	session.zoom = false
	session.relayout()
	session.syncViews()
	session.dirty = true
}

func (session *session) selectRelativeWindow(delta int) {
	workspace, exists := session.currentWorkspace()
	if !exists || len(workspace.WindowIDs) == 0 {
		return
	}
	index := indexWindow(workspace.WindowIDs, session.window)
	if index < 0 {
		index = 0
	} else {
		index = (index + delta + len(workspace.WindowIDs)) % len(workspace.WindowIDs)
	}
	session.selectWindow(workspace.WindowIDs[index])
}

func (session *session) selectRelativeWorkspace(delta int) {
	if len(session.snapshot.Workspaces) == 0 {
		return
	}
	index := 0
	for candidate := range session.snapshot.Workspaces {
		if session.snapshot.Workspaces[candidate].ID == session.workspace {
			index = candidate
			break
		}
	}
	for count := 0; count < len(session.snapshot.Workspaces); count++ {
		index = (index + delta + len(session.snapshot.Workspaces)) % len(session.snapshot.Workspaces)
		workspace := session.snapshot.Workspaces[index]
		if len(workspace.WindowIDs) != 0 {
			session.selectWindow(workspace.WindowIDs[0])
			return
		}
	}
}

func (session *session) selectWorkspace(workspaceID core.WorkspaceID) {
	for _, workspace := range session.snapshot.Workspaces {
		if workspace.ID != workspaceID {
			continue
		}
		if len(workspace.WindowIDs) == 0 {
			session.setMessage("workspace has no window")
			return
		}
		session.selectWindow(workspace.WindowIDs[0])
		return
	}
	session.setMessage("workspace not found")
}

func (session *session) moveFocusedPane(arguments []string) {
	if session.focus == 0 || len(arguments) < 1 || len(arguments) > 3 {
		session.setMessage("usage: move-pane WINDOW [TARGET h|v]")
		return
	}
	destination, err := strconv.ParseUint(arguments[0], 10, 64)
	if err != nil || destination == 0 {
		session.setMessage("invalid destination Window ID")
		return
	}
	params := protocol.MovePaneParams{PaneID: session.focus, DestinationID: core.WindowID(destination)}
	if len(arguments) == 3 {
		target, targetErr := strconv.ParseUint(arguments[1], 10, 64)
		if targetErr != nil || target == 0 || (arguments[2] != "h" && arguments[2] != "v") {
			session.setMessage("usage: move-pane WINDOW [TARGET h|v]")
			return
		}
		params.TargetPaneID = core.PaneID(target)
		params.Direction = core.SplitHorizontal
		if arguments[2] == "v" {
			params.Direction = core.SplitVertical
		}
	} else if len(arguments) != 1 {
		session.setMessage("usage: move-pane WINDOW [TARGET h|v]")
		return
	}
	_, err = callTUI[core.MovePaneResult](session, protocol.OperationMovePane, params)
	if err == nil {
		session.selectWindow(params.DestinationID)
	}
	session.reportCommand(err, "pane moved")
}

func (session *session) stashFocusedPane() {
	if session.focus == 0 {
		session.setMessage("no focused pane")
		return
	}
	session.stashPane(session.focus)
}

func (session *session) stashPane(paneID core.PaneID) {
	_, err := callTUI[core.StashPaneResult](session, protocol.OperationStashPane, protocol.PaneParams{PaneID: paneID})
	if err == nil && session.focus == paneID {
		session.zoom = false
	}
	session.reportCommand(err, fmt.Sprintf("pane %d stashed", paneID))
}

func (session *session) stashWindow(windowID core.WindowID) {
	_, err := callTUI[core.StashWindowResult](session, protocol.OperationStashWindow, protocol.DeleteWindowParams{WindowID: windowID})
	if err == nil && session.window == windowID {
		session.zoom = false
	}
	session.reportCommand(err, fmt.Sprintf("window %d stashed", windowID))
}

func (session *session) showStash() {
	result, err := callTUI[protocol.StashListResult](session, protocol.OperationListStash, nil)
	if err != nil {
		session.setMessage(err.Error())
		return
	}
	paneIDs := make([]string, len(result.Panes))
	for index, entry := range result.Panes {
		paneIDs[index] = fmt.Sprintf("%d:%s", entry.Pane.ID, stashPaneState(entry.Pane))
	}
	windowIDs := make([]string, len(result.Windows))
	for index, entry := range result.Windows {
		windowIDs[index] = fmt.Sprintf("%d:%dpanes", entry.Window.ID, len(entry.Panes))
	}
	session.setMessage(fmt.Sprintf("stash panes=[%s] windows=[%s]", strings.Join(paneIDs, ","), strings.Join(windowIDs, ",")))
}

func stashPaneState(pane core.Pane) string {
	if pane.Terminal == nil {
		return string(pane.Kind)
	}
	state := string(pane.Terminal.State)
	if pane.Terminal.HistoryAvailable {
		state += "+history"
	}
	return state
}

func (session *session) restorePane(arguments []string) {
	if len(arguments) != 1 && len(arguments) != 4 {
		session.setMessage("usage: restore-pane PANE [WINDOW TARGET h|v]")
		return
	}
	paneID, err := strconv.ParseUint(arguments[0], 10, 64)
	if err != nil || paneID == 0 {
		session.setMessage("invalid Pane ID")
		return
	}
	params := protocol.RestorePaneParams{PaneID: core.PaneID(paneID)}
	if len(arguments) == 4 {
		windowID, windowErr := strconv.ParseUint(arguments[1], 10, 64)
		targetID, targetErr := strconv.ParseUint(arguments[2], 10, 64)
		if windowErr != nil || windowID == 0 || targetErr != nil || targetID == 0 || (arguments[3] != "h" && arguments[3] != "v") {
			session.setMessage("usage: restore-pane PANE [WINDOW TARGET h|v]")
			return
		}
		params.DestinationWindowID = core.WindowID(windowID)
		params.TargetPaneID = core.PaneID(targetID)
		params.Direction = core.SplitHorizontal
		if arguments[3] == "v" {
			params.Direction = core.SplitVertical
		}
	}
	result, err := callTUI[core.RestorePaneResult](session, protocol.OperationRestorePane, params)
	if err == nil {
		session.selectWindow(result.Window.ID)
		session.pendingFocus = result.Pane.ID
		session.pendingWindow = result.Window.ID
	}
	session.reportCommand(err, fmt.Sprintf("pane %d restored", paneID))
}

func (session *session) restoreWindow(arguments []string) {
	if len(arguments) < 1 || len(arguments) > 2 {
		session.setMessage("usage: restore-window WINDOW [WORKSPACE]")
		return
	}
	windowID, err := strconv.ParseUint(arguments[0], 10, 64)
	if err != nil || windowID == 0 {
		session.setMessage("invalid Window ID")
		return
	}
	params := protocol.RestoreWindowParams{WindowID: core.WindowID(windowID)}
	if len(arguments) == 2 {
		workspaceID, workspaceErr := strconv.ParseUint(arguments[1], 10, 64)
		if workspaceErr != nil || workspaceID == 0 {
			session.setMessage("invalid Workspace ID")
			return
		}
		params.WorkspaceID = core.WorkspaceID(workspaceID)
	}
	result, err := callTUI[core.RestoreWindowResult](session, protocol.OperationRestoreWindow, params)
	if err == nil {
		session.selectWindow(result.Window.ID)
	}
	session.reportCommand(err, fmt.Sprintf("window %d restored", windowID))
}

func (session *session) reportCommand(err error, success string) {
	if err != nil {
		session.setMessage(err.Error())
		return
	}
	session.setMessage(success)
}

func indexWindow(ids []core.WindowID, id core.WindowID) int {
	for index, candidate := range ids {
		if candidate == id {
			return index
		}
	}
	return -1
}

func (session *session) currentWorkspace() (core.Workspace, bool) {
	for _, workspace := range session.snapshot.Workspaces {
		if workspace.ID == session.workspace {
			return workspace, true
		}
	}
	return core.Workspace{}, false
}
