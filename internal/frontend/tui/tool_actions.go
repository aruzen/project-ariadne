package tui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/protocol"
)

func (session *session) createTool(arguments []string) {
	if len(arguments) < 1 || len(arguments) > 3 {
		session.setMessage("usage: tool TYPE [INSTANCE] [h|v]")
		return
	}
	descriptor := core.ToolDescriptor{Provider: "ariadne", Type: arguments[0], Instance: "default"}
	if len(arguments) >= 2 {
		descriptor.Instance = arguments[1]
	}
	direction := core.SplitHorizontal
	if len(arguments) == 3 {
		if arguments[2] == "v" {
			direction = core.SplitVertical
		} else if arguments[2] != "h" {
			session.setMessage("usage: tool TYPE [INSTANCE] [h|v]")
			return
		}
	}
	tool := core.ToolInstance{Descriptor: descriptor, StateVersion: 1, Generation: 1, State: json.RawMessage(`{}`)}
	for _, existing := range session.snapshot.ToolInstances {
		if existing.Descriptor == descriptor {
			tool = existing
			break
		}
	}
	params := protocol.CreatePaneParams{WindowID: session.window, Kind: core.PaneTool, Title: descriptor.Type, Tool: &tool}
	var pane core.Pane
	var err error
	if session.focus == 0 {
		result, callErr := callTUI[core.CreatePaneResult](session, protocol.OperationCreatePane, params)
		pane, err = result.Pane, callErr
	} else {
		result, callErr := callTUI[core.CreatePaneResult](session, protocol.OperationSplitPane, protocol.SplitPaneParams{TargetPaneID: session.focus, Direction: direction, Kind: core.PaneTool, Title: descriptor.Type, Tool: &tool})
		pane, err = result.Pane, callErr
	}
	if err != nil {
		session.setMessage(err.Error())
		return
	}
	session.focus = pane.ID
	_, err = callTUI[core.SetFocusResult](session, protocol.OperationSetFocus, protocol.SetFocusParams{PaneID: pane.ID})
	session.reportCommand(err, fmt.Sprintf("tool pane %d created", pane.ID))
}

func (session *session) openBuiltinTool(kind string) {
	for _, pane := range session.snapshot.Panes {
		if pane.Tool != nil && pane.Tool.Provider == "ariadne" && pane.Tool.Type == kind && pane.Tool.Instance == "default" && !session.isStashedPane(pane.ID) {
			selected, err := callTUI[core.SetFocusResult](session, protocol.OperationSetFocus, protocol.SetFocusParams{PaneID: pane.ID})
			if err != nil {
				session.setMessage(err.Error())
				return
			}
			session.workspace, session.window, session.focus = selected.Focus.WorkspaceID, selected.Focus.WindowID, selected.Focus.PaneID
			session.zoom = false
			session.relayout()
			session.syncViews()
			session.dirty = true
			return
		}
	}
	session.createTool([]string{kind})
}

func (session *session) previewPaneByID(id core.PaneID) {
	if _, exists := session.pane(id); !exists || !session.isStashedPane(id) {
		session.setMessage("pane is not stashed")
		return
	}
	if session.previewPane == 0 {
		session.previewPreviousFocus = session.focus
	}
	session.previewPane, session.focus, session.zoom = id, id, false
	session.relayout()
	session.syncViews()
	session.dirty = true
}

func (session *session) exitPreview() {
	if session.previewPane == 0 {
		return
	}
	session.previewPane = 0
	session.focus = session.previewPreviousFocus
	session.previewPreviousFocus = 0
	session.relayout()
	session.syncViews()
	session.dirty = true
}

func (session *session) isStashedPane(id core.PaneID) bool {
	for _, stashed := range session.snapshot.StashedPanes {
		if stashed.PaneID == id {
			return true
		}
	}
	pane, exists := session.pane(id)
	if !exists {
		return false
	}
	for _, stashed := range session.snapshot.StashedWindows {
		if stashed.WindowID == pane.WindowID {
			return true
		}
	}
	return false
}

func (session *session) previewStashAt(index int) {
	if index < 0 {
		return
	}
	if index < len(session.snapshot.StashedPanes) {
		session.previewPaneByID(session.snapshot.StashedPanes[index].PaneID)
		return
	}
	index -= len(session.snapshot.StashedPanes)
	if index >= len(session.snapshot.StashedWindows) {
		return
	}
	window, ok := session.windowByID(session.snapshot.StashedWindows[index].WindowID)
	if !ok || window.Layout == nil {
		return
	}
	session.previewPaneByID(firstLayoutPane(*window.Layout))
}

func (session *session) restoreStashAt(selected int) {
	index := selected - 1
	if index < 0 {
		return
	}
	if index < len(session.snapshot.StashedPanes) {
		session.restorePane([]string{strconv.FormatUint(uint64(session.snapshot.StashedPanes[index].PaneID), 10)})
		return
	}
	index -= len(session.snapshot.StashedPanes)
	if index < len(session.snapshot.StashedWindows) {
		session.restoreWindow([]string{strconv.FormatUint(uint64(session.snapshot.StashedWindows[index].WindowID), 10)})
	}
}

func (session *session) navigateAttention(delta int) {
	values := sortedAttentions(session.snapshot.Attentions, true)
	if len(values) == 0 {
		session.setMessage("no unread attention")
		return
	}
	index := -1
	for i := range values {
		if values[i].ID == session.attentionCursor {
			index = i
			break
		}
	}
	if delta < 0 {
		if index < 0 {
			index = 0
		}
		index = (index - 1 + len(values)) % len(values)
	} else {
		index = (index + 1) % len(values)
	}
	session.goToAttention(values[index])
}

func (session *session) navigateAttentionAt(index int) {
	values := sortedAttentions(session.snapshot.Attentions, false)
	if index < 0 || index >= len(values) {
		return
	}
	session.goToAttention(values[index])
}

func (session *session) goToAttention(attention core.Attention) {
	session.attentionCursor = attention.ID
	if session.isStashedPane(attention.PaneID) {
		session.previewPaneByID(attention.PaneID)
		return
	}
	selected, err := callTUI[core.SetFocusResult](session, protocol.OperationSetFocus, protocol.SetFocusParams{PaneID: attention.PaneID})
	if err != nil {
		session.setMessage(err.Error())
		return
	}
	if session.previewPane != 0 {
		session.previewPane = 0
		session.previewPreviousFocus = 0
	}
	session.workspace, session.window, session.focus = selected.Focus.WorkspaceID, selected.Focus.WindowID, selected.Focus.PaneID
	session.zoom = false
	session.relayout()
	session.syncViews()
	session.dirty = true
}

func (session *session) ackCurrentAttention() {
	var selected *core.Attention
	for index := range session.snapshot.Attentions {
		value := &session.snapshot.Attentions[index]
		if value.ID == session.attentionCursor && value.AcknowledgedAt == nil {
			selected = value
			break
		}
	}
	if selected == nil {
		for index := range session.snapshot.Attentions {
			value := &session.snapshot.Attentions[index]
			if value.PaneID == session.focus && value.AcknowledgedAt == nil && (selected == nil || value.UpdatedAt.After(selected.UpdatedAt)) {
				selected = value
			}
		}
	}
	if selected == nil {
		session.setMessage("no unread attention")
		return
	}
	session.ackAttention(*selected)
}

func (session *session) ackAttentionAt(selected int) {
	values := sortedAttentions(session.snapshot.Attentions, false)
	index := selected - 1
	if index >= 0 && index < len(values) {
		session.ackAttention(values[index])
	}
}

func (session *session) ackAttention(attention core.Attention) {
	_, err := callTUI[core.AttentionResult](session, protocol.OperationAcknowledgeAttention, protocol.AcknowledgeAttentionParams{ID: attention.ID, At: time.Now()})
	if err == nil {
		session.attentionCursor = 0
	}
	session.reportCommand(err, "attention acknowledged")
}

func (session *session) activateWorkspaceLine(index int) {
	row := 0
	for _, workspace := range session.snapshot.Workspaces {
		if row == index {
			if len(workspace.WindowIDs) != 0 {
				session.selectWindow(workspace.WindowIDs[0])
			}
			return
		}
		row++
		for _, windowID := range workspace.WindowIDs {
			if row == index {
				session.selectWindow(windowID)
				return
			}
			row++
		}
	}
}

func (session *session) windowByID(id core.WindowID) (core.Window, bool) {
	for _, window := range session.snapshot.Windows {
		if window.ID == id {
			return window, true
		}
	}
	return core.Window{}, false
}

func firstLayoutPane(node core.LayoutNode) core.PaneID {
	if node.Kind == core.LayoutPane {
		return node.PaneID
	}
	for _, child := range node.Children {
		if id := firstLayoutPane(child); id != 0 {
			return id
		}
	}
	return 0
}

func (session *session) windowAttention(windowID core.WindowID) int {
	count := 0
	for _, attention := range session.snapshot.Attentions {
		if attention.AcknowledgedAt != nil {
			continue
		}
		if pane, exists := session.pane(attention.PaneID); exists && pane.WindowID == windowID {
			count++
		}
	}
	return count
}

func (session *session) workspaceAttention(workspaceID core.WorkspaceID) int {
	count := 0
	for _, window := range session.snapshot.Windows {
		if window.WorkspaceID == workspaceID {
			count += session.windowAttention(window.ID)
		}
	}
	return count
}

func (session *session) refreshDiagnostics() {
	found := false
	for _, pane := range session.snapshot.Panes {
		if pane.Tool != nil && pane.Tool.Provider == "ariadne" && pane.Tool.Type == "diagnostics" {
			found = true
			break
		}
	}
	if !found {
		return
	}
	status, err := callTUI[protocol.DaemonStatusResult](session, protocol.OperationDaemonStatus, nil)
	if err == nil {
		session.daemonStatus = status
	}
}
