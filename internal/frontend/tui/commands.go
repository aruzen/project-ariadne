package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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

func (session *session) handleAction(action inputAction) {
	switch action {
	case actionFocusLeft, actionFocusDown, actionFocusUp, actionFocusRight:
		if !session.zoom {
			session.moveFocus(action)
		}
	case actionResizeLeft, actionResizeDown, actionResizeUp, actionResizeRight:
		session.resizeFocusedPane(action)
	case actionZoom:
		if session.focus != 0 {
			session.zoom = !session.zoom
			session.relayout()
			session.syncViews()
			session.dirty = true
		}
	case actionSplitHorizontal:
		session.splitTerminal(core.SplitHorizontal)
	case actionSplitVertical:
		session.splitTerminal(core.SplitVertical)
	case actionClosePane:
		if session.focus != 0 {
			session.beginConfirmation(fmt.Sprintf("close pane %d?", session.focus), func(allowed bool) {
				if allowed {
					session.closeFocusedPane()
				}
			})
		}
	case actionRestart:
		session.restartFocused()
	case actionNewWindow:
		session.createWindow()
	case actionNextWindow:
		session.selectRelativeWindow(1)
	case actionPreviousWindow:
		session.selectRelativeWindow(-1)
	case actionNextWorkspace:
		session.selectRelativeWorkspace(1)
	case actionPreviousWorkspace:
		session.selectRelativeWorkspace(-1)
	case actionCommandPrompt:
		session.beginPrompt(":", "")
	case actionRenameWindow:
		session.beginPrompt(":", "rename-window ")
	case actionRenameWorkspace:
		session.beginPrompt(":", "rename-workspace ")
	case actionCopyMode:
		session.enterCopyMode()
	case actionPaste:
		session.requestPaste()
	case actionStashPane:
		session.stashFocusedPane()
	case actionListStash:
		session.showStash()
	}
}

func (session *session) beginPrompt(lead, initial string) {
	session.inputMode = inputModePrompt
	session.promptLead = lead
	session.prompt = initial
	session.promptCallback = session.executePrompt
	session.dirty = true
}

func (session *session) beginPromptWithCallback(lead, initial string, callback func(string)) {
	session.beginPrompt(lead, initial)
	session.promptCallback = callback
}

func (session *session) beginConfirmation(message string, callback func(bool)) {
	session.inputMode = inputModeConfirm
	session.confirm = message
	session.confirmCallback = callback
	session.dirty = true
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
	for _, value := range data {
		switch value {
		case '\r', '\n':
			command := session.prompt
			callback := session.promptCallback
			session.clearInputMode()
			if callback != nil {
				callback(command)
			}
			session.resumeClipboardRequests()
			return
		case 0x03, 0x1b:
			session.clearInputMode()
			session.resumeClipboardRequests()
			return
		case 0x08, 0x7f:
			runes := []rune(session.prompt)
			if len(runes) != 0 {
				session.prompt = string(runes[:len(runes)-1])
			}
		default:
			if value >= 0x20 {
				session.prompt += string([]byte{value})
			}
		}
	}
	session.dirty = true
}

func (session *session) clearInputMode() {
	session.inputMode = inputModeNormal
	session.prompt = ""
	session.promptLead = ""
	session.confirm = ""
	session.confirmCallback = nil
	session.promptCallback = nil
	session.dirty = true
}

func (session *session) executePrompt(command string) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return
	}
	switch fields[0] {
	case "split", "split-pane":
		if len(fields) != 2 || (fields[1] != "h" && fields[1] != "v") {
			session.setMessage("usage: split h|v")
			return
		}
		direction := core.SplitHorizontal
		if fields[1] == "v" {
			direction = core.SplitVertical
		}
		session.splitTerminal(direction)
	case "zoom":
		session.handleAction(actionZoom)
	case "close", "kill":
		session.closeFocusedPane()
	case "dismiss":
		session.dismissFocused()
	case "restart":
		session.restartFocused()
	case "run":
		arguments := fields[1:]
		if len(arguments) != 0 && arguments[0] == "--" {
			arguments = arguments[1:]
		}
		if len(arguments) == 0 {
			session.setMessage("usage: run [--] command [args...]")
			return
		}
		session.runFocused(arguments)
	case "new-window":
		name := ""
		if len(fields) > 1 {
			name = strings.Join(fields[1:], " ")
		}
		session.createNamedWindow(name)
	case "next-window":
		session.selectRelativeWindow(1)
	case "previous-window":
		session.selectRelativeWindow(-1)
	case "window":
		id, ok := parseID(fields)
		if !ok {
			session.setMessage("usage: window ID")
			return
		}
		session.selectWindow(core.WindowID(id))
	case "new-workspace":
		if len(fields) < 2 {
			session.setMessage("usage: new-workspace NAME")
			return
		}
		session.createWorkspace(strings.Join(fields[1:], " "))
	case "workspace":
		id, ok := parseID(fields)
		if !ok {
			session.setMessage("usage: workspace ID")
			return
		}
		session.selectWorkspace(core.WorkspaceID(id))
	case "rename-window":
		if len(fields) < 2 {
			session.setMessage("usage: rename-window NAME")
			return
		}
		_, err := callTUI[core.WindowResult](session, protocol.OperationRenameWindow, protocol.RenameWindowParams{
			WindowID: session.window, Name: strings.Join(fields[1:], " "),
		})
		session.reportCommand(err, "window renamed")
	case "rename-workspace":
		if len(fields) < 2 {
			session.setMessage("usage: rename-workspace NAME")
			return
		}
		_, err := callTUI[core.WorkspaceResult](session, protocol.OperationRenameWorkspace, protocol.RenameWorkspaceParams{
			WorkspaceID: session.workspace, Name: strings.Join(fields[1:], " "),
		})
		session.reportCommand(err, "workspace renamed")
	case "delete-window":
		_, err := callTUI[core.DeleteWindowResult](session, protocol.OperationDeleteWindow, protocol.DeleteWindowParams{WindowID: session.window})
		session.reportCommand(err, "window deleted")
	case "delete-workspace":
		_, err := callTUI[core.DeleteWorkspaceResult](session, protocol.OperationDeleteWorkspace, protocol.DeleteWorkspaceParams{WorkspaceID: session.workspace})
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
		session.showStash()
	case "restore-pane":
		session.restorePane(fields[1:])
	case "restore-window":
		session.restoreWindow(fields[1:])
	default:
		session.setMessage("unknown command: " + fields[0])
	}
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
	if session.window == 0 {
		session.setMessage("no active window")
		return
	}
	params := session.newTerminalParams(session.window)
	if session.focus != 0 {
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
		WindowID: windowID, Argv: []string{session.shell}, CWD: session.cwd,
		Env: append([]string(nil), session.env...), InitialSize: pty.Size{Cols: cols, Rows: rows},
	}
}

func (session *session) closeFocusedPane() {
	pane, exists := session.pane(session.focus)
	if !exists {
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
	if err == nil {
		session.zoom = false
	}
	session.reportCommand(err, fmt.Sprintf("pane %d closed", pane.ID))
}

func (session *session) dismissFocused() {
	if session.focus == 0 {
		return
	}
	_, err := callTUI[protocol.TerminalOperationResult](session, protocol.OperationDismissTerminal, protocol.PaneParams{PaneID: session.focus})
	session.reportCommand(err, "pane dismissed")
}

func (session *session) restartFocused() {
	if session.focus == 0 {
		return
	}
	result, err := callTUI[protocol.TerminalOperationResult](session, protocol.OperationRestartTerminal, protocol.RestartTerminalParams{
		PaneID: session.focus, Env: append([]string(nil), session.env...), InitialSize: session.focusedPTYSize(),
	})
	if err == nil {
		session.focus = result.Pane.ID
	}
	session.reportCommand(err, "pane restarted")
}

func (session *session) runFocused(argv []string) {
	if session.focus == 0 {
		return
	}
	_, err := callTUI[protocol.TerminalOperationResult](session, protocol.OperationRunTerminal, protocol.RunTerminalParams{
		PaneID: session.focus, Argv: append([]string(nil), argv...), FallbackCWD: session.cwd,
		Env: append([]string(nil), session.env...), InitialSize: session.focusedPTYSize(),
	})
	session.reportCommand(err, "command started")
}

func (session *session) focusedPTYSize() pty.Size {
	if placement, exists := placementFor(session.placements, session.focus); exists {
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
