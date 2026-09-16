package core

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strings"
)

func (c *Core) execute(command Command, frontends map[FrontendID]*frontend) (any, *Event, error) {
	switch value := command.(type) {
	case CreateWorkspaceCommand:
		return c.createWorkspace(value)
	case CreateWindowCommand:
		return c.createWindow(value)
	case RenameWorkspaceCommand:
		return c.renameWorkspace(value)
	case DeleteWorkspaceCommand:
		return c.deleteWorkspace(value, frontends)
	case RenameWindowCommand:
		return c.renameWindow(value)
	case DeleteWindowCommand:
		return c.deleteWindow(value, frontends)
	case CreatePaneCommand:
		return c.createPane(value)
	case SplitPaneCommand:
		return c.splitPane(value)
	case MovePaneCommand:
		return c.movePane(value, frontends)
	case ClosePaneCommand:
		return c.closePane(value, frontends)
	case ResizeSplitCommand:
		return c.resizeSplit(value)
	case StashPaneCommand:
		return c.stashPane(value, frontends)
	case RestorePaneCommand:
		return c.restorePane(value, frontends)
	case StashWindowCommand:
		return c.stashWindow(value, frontends)
	case RestoreWindowCommand:
		return c.restoreWindow(value, frontends)
	case SetFocusCommand:
		return c.setFocus(value, frontends)
	case SelectWindowCommand:
		return c.selectWindow(value, frontends)
	case RecordTerminalExitCommand:
		return c.recordTerminalExit(value)
	case ForgetTerminalSessionCommand:
		return c.forgetTerminalSession(value)
	case ActivateTerminalCommand:
		return c.activateTerminal(value)
	case FailTerminalStartCommand:
		return c.failTerminalStart(value)
	case PrepareTerminalRestartCommand:
		return c.prepareTerminalRestart(value)
	case PrepareTerminalRunCommand:
		return c.prepareTerminalRun(value)
	case BeginTerminalStopCommand:
		return c.beginTerminalStop(value)
	case SetLabelCommand:
		return c.setLabel(value)
	case RemoveLabelCommand:
		return c.removeLabel(value)
	case RemoveLabelsBySourceCommand:
		return c.removeLabelsBySource(value)
	case UpdateToolStateCommand:
		return c.updateToolState(value)
	case RaiseAttentionCommand:
		return c.raiseAttention(value)
	case AcknowledgeAttentionCommand:
		return c.acknowledgeAttention(value)
	case RemoveAttentionsBySourceCommand:
		return c.removeAttentionsBySource(value)
	default:
		return nil, nil, ErrInvalidCommand
	}
}

func (c *Core) updateToolState(command UpdateToolStateCommand) (any, *Event, error) {
	key := keyForTool(command.Descriptor)
	tool, exists := c.state.toolInstances[key]
	if !exists {
		return nil, nil, fmt.Errorf("%w: tool instance", ErrNotFound)
	}
	if command.ExpectedGeneration != tool.Generation {
		return nil, nil, fmt.Errorf("%w: tool generation is %d", ErrInvalidState, tool.Generation)
	}
	next := ToolInstance{Descriptor: command.Descriptor, StateVersion: command.StateVersion, Generation: tool.Generation + 1, State: append([]byte(nil), command.State...)}
	if next.Generation == 0 || !validToolInstance(next) {
		return nil, nil, fmt.Errorf("%w: invalid tool state", ErrInvalidArgument)
	}
	c.state.toolInstances[key] = next
	return ToolStateResult{Tool: cloneToolInstance(next)}, &Event{Kind: EventToolStateUpdated, Payload: ToolEvent{Tool: cloneToolInstance(next)}}, nil
}

func (c *Core) raiseAttention(command RaiseAttentionCommand) (any, *Event, error) {
	if command.OccurredAt.IsZero() {
		return nil, nil, fmt.Errorf("%w: attention time", ErrInvalidArgument)
	}
	probe := Attention{ID: 1, PaneID: command.PaneID, Source: command.Source, Key: command.Key, Class: command.Class,
		Severity: command.Severity, Message: command.Message, OccurredAt: command.OccurredAt, UpdatedAt: command.OccurredAt}
	if _, exists := c.state.panes[command.PaneID]; !exists || !validAttention(probe) {
		return nil, nil, fmt.Errorf("%w: invalid attention", ErrInvalidArgument)
	}
	key := keyForAttention(probe)
	if id, exists := c.state.attentionKeys[key]; exists {
		existing := c.state.attentions[id]
		if command.OccurredAt.Before(existing.UpdatedAt) {
			return nil, nil, fmt.Errorf("%w: attention time precedes current state", ErrInvalidArgument)
		}
		if existing.Class == command.Class && existing.Severity == command.Severity && existing.Message == command.Message && existing.AcknowledgedAt == nil {
			return AttentionResult{Attention: cloneAttention(existing), Changed: false}, nil, nil
		}
		existing.Class, existing.Severity, existing.Message = command.Class, command.Severity, command.Message
		existing.UpdatedAt, existing.AcknowledgedAt = command.OccurredAt, nil
		c.state.attentions[id] = existing
		return AttentionResult{Attention: cloneAttention(existing), Changed: true}, &Event{Kind: EventAttentionRaised, Payload: AttentionEvent{Attention: cloneAttention(existing)}}, nil
	}
	if c.state.nextAttentionID == 0 {
		return nil, nil, fmt.Errorf("%w: attention ID exhausted", ErrInvalidState)
	}
	probe.ID = c.state.nextAttentionID
	c.state.nextAttentionID++
	c.state.attentions[probe.ID] = probe
	c.state.attentionKeys[key] = probe.ID
	removed := c.state.evictAttentions(c.config.MaxAttentionEntries)
	return AttentionResult{Attention: cloneAttention(probe), Changed: true}, &Event{Kind: EventAttentionRaised, Payload: AttentionEvent{Attention: cloneAttention(probe), Removed: removed}}, nil
}

func (c *Core) acknowledgeAttention(command AcknowledgeAttentionCommand) (any, *Event, error) {
	attention, exists := c.state.attentions[command.ID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: attention %d", ErrNotFound, command.ID)
	}
	if command.At.IsZero() || command.At.Before(attention.UpdatedAt) {
		return nil, nil, fmt.Errorf("%w: acknowledgement time", ErrInvalidArgument)
	}
	if attention.AcknowledgedAt != nil {
		return AttentionResult{Attention: cloneAttention(attention), Changed: false}, nil, nil
	}
	value := command.At
	attention.AcknowledgedAt = &value
	c.state.attentions[attention.ID] = attention
	return AttentionResult{Attention: cloneAttention(attention), Changed: true}, &Event{Kind: EventAttentionAcknowledged, Payload: AttentionEvent{Attention: cloneAttention(attention)}}, nil
}

func (c *Core) removeAttentionsBySource(command RemoveAttentionsBySourceCommand) (any, *Event, error) {
	if strings.TrimSpace(command.Source) == "" || len(command.Source) > 128 || strings.ContainsRune(command.Source, 0) {
		return nil, nil, fmt.Errorf("%w: invalid attention source", ErrInvalidArgument)
	}
	removed := make([]Attention, 0)
	for id, attention := range c.state.attentions {
		if attention.Source == command.Source {
			removed = append(removed, cloneAttention(attention))
			delete(c.state.attentionKeys, keyForAttention(attention))
			delete(c.state.attentions, id)
		}
	}
	sort.Slice(removed, func(left, right int) bool { return removed[left].ID < removed[right].ID })
	result := RemoveAttentionsResult{Attentions: removed}
	if len(removed) == 0 {
		return result, nil, nil
	}
	return result, &Event{Kind: EventAttentionSourceCleared, Payload: AttentionsEvent{Attentions: removed}}, nil
}

func (s *state) evictAttentions(limit int) []Attention {
	var removed []Attention
	for len(s.attentions) > limit {
		var selected Attention
		for _, candidate := range s.attentions {
			if selected.ID == 0 || (selected.AcknowledgedAt == nil && candidate.AcknowledgedAt != nil) ||
				((selected.AcknowledgedAt == nil) == (candidate.AcknowledgedAt == nil) &&
					(candidate.UpdatedAt.Before(selected.UpdatedAt) || (candidate.UpdatedAt.Equal(selected.UpdatedAt) && candidate.ID < selected.ID))) {
				selected = candidate
			}
		}
		delete(s.attentions, selected.ID)
		delete(s.attentionKeys, keyForAttention(selected))
		removed = append(removed, cloneAttention(selected))
	}
	return removed
}

func (c *Core) setLabel(command SetLabelCommand) (any, *Event, error) {
	label := command.Label
	if !validLabel(label) || !c.state.labelTargetExists(label.TargetKind, label.TargetID) {
		return nil, nil, fmt.Errorf("%w: invalid label", ErrInvalidArgument)
	}
	key := keyForLabel(label)
	if existing, exists := c.state.labels[key]; exists && existing.Value == label.Value {
		return LabelResult{Label: existing, Changed: false}, nil, nil
	}
	c.state.labels[key] = label
	return LabelResult{Label: label, Changed: true}, &Event{Kind: EventLabelSet, Payload: LabelEvent{Label: label}}, nil
}

func (c *Core) removeLabel(command RemoveLabelCommand) (any, *Event, error) {
	probe := Label{TargetKind: command.TargetKind, TargetID: command.TargetID, Source: command.Source, Name: command.Name}
	if !validLabel(probe) {
		return nil, nil, fmt.Errorf("%w: invalid label identity", ErrInvalidArgument)
	}
	key := keyForLabel(probe)
	label, exists := c.state.labels[key]
	if !exists {
		return LabelResult{Label: probe, Changed: false}, nil, nil
	}
	delete(c.state.labels, key)
	return LabelResult{Label: label, Changed: true}, &Event{Kind: EventLabelRemoved, Payload: LabelEvent{Label: label}}, nil
}

func (c *Core) removeLabelsBySource(command RemoveLabelsBySourceCommand) (any, *Event, error) {
	if strings.TrimSpace(command.Source) == "" || len(command.Source) > 128 || strings.ContainsRune(command.Source, 0) {
		return nil, nil, fmt.Errorf("%w: invalid label source", ErrInvalidArgument)
	}
	removed := make([]Label, 0)
	for key, label := range c.state.labels {
		if label.Source == command.Source {
			removed = append(removed, label)
			delete(c.state.labels, key)
		}
	}
	sort.Slice(removed, func(left, right int) bool {
		if removed[left].TargetKind != removed[right].TargetKind {
			return removed[left].TargetKind < removed[right].TargetKind
		}
		if removed[left].TargetID != removed[right].TargetID {
			return removed[left].TargetID < removed[right].TargetID
		}
		return removed[left].Name < removed[right].Name
	})
	result := RemoveLabelsResult{Labels: removed}
	if len(removed) == 0 {
		return result, nil, nil
	}
	return result, &Event{Kind: EventLabelSourceCleared, Payload: LabelsEvent{Labels: append([]Label(nil), removed...)}}, nil
}

func (c *Core) activateTerminal(command ActivateTerminalCommand) (any, *Event, error) {
	if command.TerminalID == 0 {
		return nil, nil, fmt.Errorf("%w: terminal ID is zero", ErrInvalidArgument)
	}
	if _, _, exists := c.state.paneByTerminalID(command.TerminalID); exists {
		return nil, nil, fmt.Errorf("%w: terminal %d", ErrAlreadyExists, command.TerminalID)
	}
	pane, exists := c.state.panes[command.PaneID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: pane %d", ErrNotFound, command.PaneID)
	}
	if pane.Terminal == nil || pane.Terminal.State != TerminalStarting {
		return nil, nil, fmt.Errorf("%w: pane %d is not starting", ErrInvalidState, command.PaneID)
	}
	terminal := cloneTerminal(*pane.Terminal)
	terminal.ID = &command.TerminalID
	terminal.State = TerminalRunning
	pane.Terminal = &terminal
	c.state.panes[pane.ID] = pane
	result := TerminalResult{Pane: clonePane(pane)}
	event := Event{Kind: EventTerminalStarted, Payload: TerminalEvent{Pane: clonePane(pane)}}
	return result, &event, nil
}

func (c *Core) failTerminalStart(command FailTerminalStartCommand) (any, *Event, error) {
	pane, exists := c.state.panes[command.PaneID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: pane %d", ErrNotFound, command.PaneID)
	}
	if pane.Terminal == nil || pane.Terminal.State != TerminalStarting || strings.TrimSpace(command.Message) == "" {
		return nil, nil, fmt.Errorf("%w: invalid terminal start failure", ErrInvalidState)
	}
	terminal := cloneTerminal(*pane.Terminal)
	terminal.State = TerminalFailed
	terminal.Exit = &TerminalExit{Kind: TerminalExitPTYError, Message: command.Message}
	pane.Terminal = &terminal
	c.state.panes[pane.ID] = pane
	result := TerminalResult{Pane: clonePane(pane)}
	event := Event{Kind: EventTerminalStartFailed, Payload: TerminalEvent{Pane: clonePane(pane)}}
	return result, &event, nil
}

func (c *Core) prepareTerminalRestart(command PrepareTerminalRestartCommand) (any, *Event, error) {
	pane, exists := c.state.panes[command.PaneID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: pane %d", ErrNotFound, command.PaneID)
	}
	if pane.Terminal == nil {
		return nil, nil, fmt.Errorf("%w: pane %d has no terminal", ErrInvalidState, command.PaneID)
	}
	switch pane.Terminal.State {
	case TerminalPlaceholder, TerminalExited, TerminalFailed:
	default:
		return nil, nil, fmt.Errorf("%w: pane %d terminal cannot restart", ErrInvalidState, command.PaneID)
	}
	terminal := cloneTerminal(*pane.Terminal)
	terminal.ID = nil
	terminal.State = TerminalStarting
	terminal.Exit = nil
	terminal.HistoryAvailable = false
	pane.Terminal = &terminal
	c.state.panes[pane.ID] = pane
	result := TerminalResult{Pane: clonePane(pane)}
	event := Event{Kind: EventTerminalRestarting, Payload: TerminalEvent{Pane: clonePane(pane)}}
	return result, &event, nil
}

func (c *Core) prepareTerminalRun(command PrepareTerminalRunCommand) (any, *Event, error) {
	pane, exists := c.state.panes[command.PaneID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: pane %d", ErrNotFound, command.PaneID)
	}
	if pane.Kind != PaneTerminal {
		return nil, nil, fmt.Errorf("%w: pane %d is not a Terminal Pane", ErrInvalidState, pane.ID)
	}
	if pane.Terminal != nil {
		switch pane.Terminal.State {
		case TerminalPlaceholder, TerminalExited, TerminalFailed:
		default:
			return nil, nil, fmt.Errorf("%w: pane %d terminal cannot run", ErrInvalidState, pane.ID)
		}
	}
	terminal := TerminalInstance{State: TerminalStarting, Launch: cloneLaunch(command.Launch)}
	if !validTerminal(terminal) {
		return nil, nil, fmt.Errorf("%w: invalid terminal launch", ErrInvalidArgument)
	}
	pane.Terminal = &terminal
	c.state.panes[pane.ID] = pane
	result := TerminalResult{Pane: clonePane(pane)}
	event := Event{Kind: EventTerminalRunPrepared, Payload: TerminalEvent{Pane: clonePane(pane)}}
	return result, &event, nil
}

func (c *Core) beginTerminalStop(command BeginTerminalStopCommand) (any, *Event, error) {
	pane, exists := c.state.panes[command.PaneID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: pane %d", ErrNotFound, command.PaneID)
	}
	if pane.Terminal == nil || pane.Terminal.State != TerminalRunning || pane.Terminal.ID == nil {
		return nil, nil, fmt.Errorf("%w: pane %d terminal is not running", ErrInvalidState, command.PaneID)
	}
	terminal := cloneTerminal(*pane.Terminal)
	terminal.State = TerminalStopping
	pane.Terminal = &terminal
	c.state.panes[pane.ID] = pane
	result := TerminalResult{Pane: clonePane(pane)}
	event := Event{Kind: EventTerminalStopping, Payload: TerminalEvent{Pane: clonePane(pane)}}
	return result, &event, nil
}

func (c *Core) recordTerminalExit(command RecordTerminalExitCommand) (any, *Event, error) {
	paneID, pane, exists := c.state.paneByTerminalID(command.TerminalID)
	if !exists {
		return nil, nil, fmt.Errorf("%w: terminal %d", ErrNotFound, command.TerminalID)
	}
	if command.State != TerminalExited && command.State != TerminalFailed {
		return nil, nil, fmt.Errorf("%w: terminal exit state %q", ErrInvalidArgument, command.State)
	}
	if pane.Terminal.Exit != nil && *pane.Terminal.Exit == command.Exit && pane.Terminal.State == command.State && pane.Terminal.HistoryAvailable == command.HistoryAvailable {
		return TerminalResult{Pane: clonePane(pane)}, nil, nil
	}
	terminal := cloneTerminal(*pane.Terminal)
	terminal.State = command.State
	terminal.Exit = &TerminalExit{Kind: command.Exit.Kind, Code: command.Exit.Code, Signal: command.Exit.Signal, Message: command.Exit.Message}
	terminal.HistoryAvailable = command.HistoryAvailable
	if !validTerminal(terminal) {
		return nil, nil, fmt.Errorf("%w: invalid terminal exit", ErrInvalidArgument)
	}
	pane.Terminal = &terminal
	c.state.panes[paneID] = pane
	result := TerminalResult{Pane: clonePane(pane)}
	event := Event{Kind: EventTerminalExited, Payload: TerminalEvent{Pane: clonePane(pane)}}
	return result, &event, nil
}

func (c *Core) forgetTerminalSession(command ForgetTerminalSessionCommand) (any, *Event, error) {
	paneID, pane, exists := c.state.paneByTerminalID(command.TerminalID)
	if !exists {
		return nil, nil, fmt.Errorf("%w: terminal %d", ErrNotFound, command.TerminalID)
	}
	terminal := cloneTerminal(*pane.Terminal)
	if terminal.State != TerminalExited && terminal.State != TerminalFailed {
		return nil, nil, fmt.Errorf("%w: terminal %d is active", ErrInvalidState, command.TerminalID)
	}
	terminal.ID = nil
	terminal.HistoryAvailable = false
	pane.Terminal = &terminal
	c.state.panes[paneID] = pane
	result := TerminalResult{Pane: clonePane(pane)}
	event := Event{Kind: EventTerminalUnavailable, Payload: TerminalEvent{Pane: clonePane(pane)}}
	return result, &event, nil
}

func (s *state) paneByTerminalID(id TerminalID) (PaneID, Pane, bool) {
	if id == 0 {
		return 0, Pane{}, false
	}
	for paneID, pane := range s.panes {
		if pane.Terminal != nil && pane.Terminal.ID != nil && *pane.Terminal.ID == id {
			return paneID, pane, true
		}
	}
	return 0, Pane{}, false
}

func (c *Core) createWorkspace(command CreateWorkspaceCommand) (any, *Event, error) {
	if strings.TrimSpace(command.Name) == "" {
		return nil, nil, fmt.Errorf("%w: workspace name is empty", ErrInvalidArgument)
	}
	for _, workspace := range c.state.workspaces {
		if workspace.Name == command.Name {
			return nil, nil, fmt.Errorf("%w: workspace name %q", ErrAlreadyExists, command.Name)
		}
	}
	id, err := c.state.allocateWorkspaceID()
	if err != nil {
		return nil, nil, err
	}
	workspace := Workspace{ID: id, Name: command.Name, WindowIDs: []WindowID{}}
	c.state.workspaces[id] = workspace
	c.state.workspaceOrder = append(c.state.workspaceOrder, id)
	result := CreateWorkspaceResult{Workspace: cloneWorkspace(workspace)}
	event := Event{Kind: EventWorkspaceCreated, Payload: WorkspaceCreatedEvent{Workspace: cloneWorkspace(workspace)}}
	return result, &event, nil
}

func (c *Core) createWindow(command CreateWindowCommand) (any, *Event, error) {
	workspace, exists := c.state.workspaces[command.WorkspaceID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: workspace %d", ErrNotFound, command.WorkspaceID)
	}
	if strings.TrimSpace(command.Name) == "" {
		return nil, nil, fmt.Errorf("%w: window name is empty", ErrInvalidArgument)
	}
	for _, windowID := range workspace.WindowIDs {
		if c.state.windows[windowID].Name == command.Name {
			return nil, nil, fmt.Errorf("%w: window name %q in workspace %d", ErrAlreadyExists, command.Name, workspace.ID)
		}
	}
	id, err := c.state.allocateWindowID()
	if err != nil {
		return nil, nil, err
	}
	window := Window{ID: id, WorkspaceID: workspace.ID, Name: command.Name}
	c.state.windows[id] = window
	c.state.windowOrder = append(c.state.windowOrder, id)
	workspace.WindowIDs = append(workspace.WindowIDs, id)
	c.state.workspaces[workspace.ID] = workspace
	result := CreateWindowResult{Window: cloneWindow(window)}
	event := Event{Kind: EventWindowCreated, Payload: WindowCreatedEvent{Window: cloneWindow(window)}}
	return result, &event, nil
}

func (c *Core) renameWorkspace(command RenameWorkspaceCommand) (any, *Event, error) {
	workspace, exists := c.state.workspaces[command.WorkspaceID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: workspace %d", ErrNotFound, command.WorkspaceID)
	}
	if strings.TrimSpace(command.Name) == "" {
		return nil, nil, fmt.Errorf("%w: workspace name is empty", ErrInvalidArgument)
	}
	for id, other := range c.state.workspaces {
		if id != workspace.ID && other.Name == command.Name {
			return nil, nil, fmt.Errorf("%w: workspace name %q", ErrAlreadyExists, command.Name)
		}
	}
	workspace.Name = command.Name
	c.state.workspaces[workspace.ID] = workspace
	result := WorkspaceResult{Workspace: cloneWorkspace(workspace)}
	return result, &Event{Kind: EventWorkspaceRenamed, Payload: WorkspaceEvent{Workspace: cloneWorkspace(workspace)}}, nil
}

func (c *Core) deleteWorkspace(command DeleteWorkspaceCommand, frontends map[FrontendID]*frontend) (any, *Event, error) {
	workspace, exists := c.state.workspaces[command.WorkspaceID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: workspace %d", ErrNotFound, command.WorkspaceID)
	}
	if len(workspace.WindowIDs) != 0 || len(c.state.workspaces) == 1 {
		return nil, nil, fmt.Errorf("%w: workspace %d is not deletable", ErrInvalidState, workspace.ID)
	}
	delete(c.state.workspaces, workspace.ID)
	c.state.workspaceOrder = removeWorkspaceID(c.state.workspaceOrder, workspace.ID)
	removedLabels := c.state.removeTargetLabels(LabelWorkspace, uint64(workspace.ID))
	for _, frontend := range frontends {
		if frontend.state.WorkspaceID == workspace.ID {
			frontend.state = c.initialFrontendState(frontend.state.ID)
		}
	}
	result := DeleteWorkspaceResult{Workspace: cloneWorkspace(workspace), RemovedLabels: removedLabels}
	return result, &Event{Kind: EventWorkspaceDeleted, Payload: WorkspaceDeletedEvent{
		Workspace: cloneWorkspace(workspace), RemovedLabels: append([]Label(nil), removedLabels...),
	}}, nil
}

func (c *Core) renameWindow(command RenameWindowCommand) (any, *Event, error) {
	window, exists := c.state.windows[command.WindowID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: window %d", ErrNotFound, command.WindowID)
	}
	if strings.TrimSpace(command.Name) == "" {
		return nil, nil, fmt.Errorf("%w: window name is empty", ErrInvalidArgument)
	}
	workspace := c.state.workspaces[window.WorkspaceID]
	for _, id := range workspace.WindowIDs {
		if id != window.ID && c.state.windows[id].Name == command.Name {
			return nil, nil, fmt.Errorf("%w: window name %q", ErrAlreadyExists, command.Name)
		}
	}
	window.Name = command.Name
	c.state.windows[window.ID] = window
	result := WindowResult{Window: cloneWindow(window)}
	return result, &Event{Kind: EventWindowRenamed, Payload: WindowEvent{Window: cloneWindow(window)}}, nil
}

func (c *Core) deleteWindow(command DeleteWindowCommand, frontends map[FrontendID]*frontend) (any, *Event, error) {
	window, exists := c.state.windows[command.WindowID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: window %d", ErrNotFound, command.WindowID)
	}
	if _, stashed := c.state.stashedWindows[window.ID]; stashed {
		return nil, nil, fmt.Errorf("%w: window %d is stashed", ErrInvalidState, window.ID)
	}
	if window.Layout != nil {
		return nil, nil, fmt.Errorf("%w: window %d is not empty", ErrInvalidState, window.ID)
	}
	workspace := c.state.workspaces[window.WorkspaceID]
	workspace.WindowIDs = removeWindowID(workspace.WindowIDs, window.ID)
	c.state.workspaces[workspace.ID] = workspace
	delete(c.state.windows, window.ID)
	c.state.windowOrder = removeWindowID(c.state.windowOrder, window.ID)
	removedLabels := c.state.removeTargetLabels(LabelWindow, uint64(window.ID))
	for _, frontend := range frontends {
		if frontend.state.WindowID == window.ID {
			frontend.state = c.initialFrontendState(frontend.state.ID)
		}
	}
	result := DeleteWindowResult{Window: cloneWindow(window), Workspace: cloneWorkspace(workspace), RemovedLabels: removedLabels}
	return result, &Event{Kind: EventWindowDeleted, Payload: WindowDeletedEvent{
		Window: cloneWindow(window), Workspace: cloneWorkspace(workspace), RemovedLabels: append([]Label(nil), removedLabels...),
	}}, nil
}

func (c *Core) createPane(command CreatePaneCommand) (any, *Event, error) {
	window, exists := c.state.windows[command.WindowID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: window %d", ErrNotFound, command.WindowID)
	}
	if _, stashed := c.state.stashedWindows[window.ID]; stashed {
		return nil, nil, fmt.Errorf("%w: window %d is stashed", ErrInvalidState, window.ID)
	}
	if window.Layout != nil {
		return nil, nil, fmt.Errorf("%w: window %d is not empty", ErrInvalidState, window.ID)
	}
	if err := c.state.validatePaneSpec(command.Pane); err != nil {
		return nil, nil, err
	}
	id, err := c.state.allocatePaneID()
	if err != nil {
		return nil, nil, err
	}
	pane := paneFromSpec(id, window.ID, command.Pane)
	c.state.registerTool(command.Pane.Tool)
	layout := paneLeaf(id)
	window.Layout = &layout
	c.state.windows[window.ID] = window
	c.state.panes[id] = pane
	c.state.paneOrder = append(c.state.paneOrder, id)
	result := CreatePaneResult{Pane: clonePane(pane), Window: cloneWindow(window)}
	event := Event{Kind: EventPaneCreated, Payload: PaneCreatedEvent{Pane: clonePane(pane), Window: cloneWindow(window), Tool: cloneOptionalTool(command.Pane.Tool)}}
	return result, &event, nil
}

func (c *Core) splitPane(command SplitPaneCommand) (any, *Event, error) {
	target, exists := c.state.panes[command.TargetPaneID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: pane %d", ErrNotFound, command.TargetPaneID)
	}
	if _, stashed := c.state.stashedPanes[target.ID]; stashed {
		return nil, nil, fmt.Errorf("%w: pane %d is stashed", ErrInvalidState, target.ID)
	}
	if _, stashed := c.state.stashedWindows[target.WindowID]; stashed {
		return nil, nil, fmt.Errorf("%w: pane %d belongs to a stashed window", ErrInvalidState, target.ID)
	}
	if !validDirection(command.Direction) {
		return nil, nil, fmt.Errorf("%w: split direction %q", ErrInvalidArgument, command.Direction)
	}
	if err := c.state.validatePaneSpec(command.Pane); err != nil {
		return nil, nil, err
	}
	window := c.state.windows[target.WindowID]
	layout := cloneLayout(*window.Layout)
	splitID, err := c.state.splitIDForInsertion(layout, target.ID, command.Direction)
	if err != nil {
		return nil, nil, err
	}
	id, err := c.state.allocatePaneID()
	if err != nil {
		return nil, nil, err
	}
	if !insertSplit(&layout, target.ID, id, command.Direction, splitID) {
		return nil, nil, fmt.Errorf("%w: pane %d missing from layout", ErrInvalidState, target.ID)
	}
	pane := paneFromSpec(id, window.ID, command.Pane)
	c.state.registerTool(command.Pane.Tool)
	window.Layout = &layout
	c.state.windows[window.ID] = window
	c.state.panes[id] = pane
	c.state.paneOrder = append(c.state.paneOrder, id)
	result := CreatePaneResult{Pane: clonePane(pane), Window: cloneWindow(window)}
	event := Event{Kind: EventPaneCreated, Payload: PaneCreatedEvent{
		Pane: clonePane(pane), Window: cloneWindow(window), TargetPaneID: target.ID, Direction: command.Direction, Tool: cloneOptionalTool(command.Pane.Tool),
	}}
	return result, &event, nil
}

func (c *Core) movePane(command MovePaneCommand, frontends map[FrontendID]*frontend) (any, *Event, error) {
	pane, exists := c.state.panes[command.PaneID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: pane %d", ErrNotFound, command.PaneID)
	}
	if _, stashed := c.state.stashedPanes[pane.ID]; stashed {
		return nil, nil, fmt.Errorf("%w: pane %d is stashed", ErrInvalidState, pane.ID)
	}
	if _, stashed := c.state.stashedWindows[pane.WindowID]; stashed {
		return nil, nil, fmt.Errorf("%w: pane %d belongs to a stashed window", ErrInvalidState, pane.ID)
	}
	destination, exists := c.state.windows[command.DestinationID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: window %d", ErrNotFound, command.DestinationID)
	}
	if _, stashed := c.state.stashedWindows[destination.ID]; stashed {
		return nil, nil, fmt.Errorf("%w: destination window %d is stashed", ErrInvalidState, destination.ID)
	}
	if command.TargetPaneID == pane.ID {
		return nil, nil, fmt.Errorf("%w: pane cannot target itself", ErrInvalidArgument)
	}
	if destination.Layout == nil {
		if command.TargetPaneID != 0 {
			return nil, nil, fmt.Errorf("%w: empty destination has no target pane", ErrInvalidArgument)
		}
		if command.Direction != "" {
			return nil, nil, fmt.Errorf("%w: empty destination does not use a split direction", ErrInvalidArgument)
		}
	} else {
		if command.TargetPaneID == 0 || !validDirection(command.Direction) {
			return nil, nil, fmt.Errorf("%w: target pane and direction are required", ErrInvalidArgument)
		}
		target, exists := c.state.panes[command.TargetPaneID]
		if !exists || target.WindowID != destination.ID {
			return nil, nil, fmt.Errorf("%w: target pane %d in destination window", ErrNotFound, command.TargetPaneID)
		}
	}

	source := c.state.windows[pane.WindowID]
	if source.ID == destination.ID && destination.Layout != nil {
		layout, removed := removePaneFromLayout(source.Layout, pane.ID)
		if !removed || layout == nil {
			return nil, nil, fmt.Errorf("%w: cannot move the only pane within its window", ErrInvalidState)
		}
		splitID, err := c.state.splitIDForInsertion(*layout, command.TargetPaneID, command.Direction)
		if err != nil {
			return nil, nil, err
		}
		if !insertSplit(layout, command.TargetPaneID, pane.ID, command.Direction, splitID) {
			return nil, nil, fmt.Errorf("%w: target pane %d missing from layout", ErrInvalidState, command.TargetPaneID)
		}
		source.Layout = layout
		c.state.windows[source.ID] = source
		result := MovePaneResult{Pane: clonePane(pane), SourceWindow: cloneWindow(source), DestinationWindow: cloneWindow(source)}
		event := Event{Kind: EventPaneMoved, Payload: PaneMovedEvent{
			Pane: clonePane(pane), SourceWindow: cloneWindow(source), DestinationWindow: cloneWindow(source),
			TargetPaneID: command.TargetPaneID, Direction: command.Direction,
		}}
		return result, &event, nil
	}

	sourceLayout, removed := removePaneFromLayout(source.Layout, pane.ID)
	if !removed {
		return nil, nil, fmt.Errorf("%w: pane %d missing from layout", ErrInvalidState, pane.ID)
	}
	var destinationLayout *LayoutNode
	if destination.Layout == nil {
		leaf := paneLeaf(pane.ID)
		destinationLayout = &leaf
	} else {
		layout := cloneLayout(*destination.Layout)
		splitID, err := c.state.splitIDForInsertion(layout, command.TargetPaneID, command.Direction)
		if err != nil {
			return nil, nil, err
		}
		if !insertSplit(&layout, command.TargetPaneID, pane.ID, command.Direction, splitID) {
			return nil, nil, fmt.Errorf("%w: target pane %d missing from layout", ErrInvalidState, command.TargetPaneID)
		}
		destinationLayout = &layout
	}
	source.Layout = sourceLayout
	destination.Layout = destinationLayout
	pane.WindowID = destination.ID
	c.state.windows[source.ID] = source
	c.state.windows[destination.ID] = destination
	c.state.panes[pane.ID] = pane
	for _, frontend := range frontends {
		if frontend.state.PaneID == pane.ID {
			frontend.state.WorkspaceID = destination.WorkspaceID
			frontend.state.WindowID = destination.ID
		}
	}
	result := MovePaneResult{Pane: clonePane(pane), SourceWindow: cloneWindow(source), DestinationWindow: cloneWindow(destination)}
	event := Event{Kind: EventPaneMoved, Payload: PaneMovedEvent{
		Pane: clonePane(pane), SourceWindow: cloneWindow(source), DestinationWindow: cloneWindow(destination),
		TargetPaneID: command.TargetPaneID, Direction: command.Direction,
	}}
	return result, &event, nil
}

func (c *Core) closePane(command ClosePaneCommand, frontends map[FrontendID]*frontend) (any, *Event, error) {
	pane, exists := c.state.panes[command.PaneID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: pane %d", ErrNotFound, command.PaneID)
	}
	window := c.state.windows[pane.WindowID]
	if _, stashed := c.state.stashedPanes[pane.ID]; stashed {
		delete(c.state.stashedPanes, pane.ID)
	} else {
		layout, removed := removePaneFromLayout(window.Layout, pane.ID)
		if !removed {
			return nil, nil, fmt.Errorf("%w: pane %d missing from layout", ErrInvalidState, pane.ID)
		}
		window.Layout = layout
		c.state.windows[window.ID] = window
	}
	delete(c.state.panes, pane.ID)
	removedLabels := make([]Label, 0)
	removedAttentions := make([]Attention, 0)
	for key, label := range c.state.labels {
		if label.TargetKind == LabelPane && label.TargetID == uint64(pane.ID) {
			removedLabels = append(removedLabels, label)
			delete(c.state.labels, key)
		}
	}
	for id, attention := range c.state.attentions {
		if attention.PaneID == pane.ID {
			removedAttentions = append(removedAttentions, cloneAttention(attention))
			delete(c.state.attentionKeys, keyForAttention(attention))
			delete(c.state.attentions, id)
		}
	}
	sort.Slice(removedAttentions, func(left, right int) bool { return removedAttentions[left].ID < removedAttentions[right].ID })
	var removedTool *ToolInstance
	if pane.Tool != nil {
		used := false
		for _, other := range c.state.panes {
			if other.Tool != nil && keyForTool(*other.Tool) == keyForTool(*pane.Tool) {
				used = true
				break
			}
		}
		if !used {
			tool := c.state.toolInstances[keyForTool(*pane.Tool)]
			delete(c.state.toolInstances, keyForTool(*pane.Tool))
			copy := cloneToolInstance(tool)
			removedTool = &copy
		}
	}
	c.state.paneOrder = removePaneID(c.state.paneOrder, pane.ID)
	for _, frontend := range frontends {
		if frontend.state.PaneID == pane.ID {
			frontend.state.PaneID = 0
			if window.Layout != nil {
				frontend.state.PaneID = firstPane(*window.Layout)
			}
		}
	}
	result := ClosePaneResult{Pane: clonePane(pane), Window: cloneWindow(window)}
	event := Event{Kind: EventPaneClosed, Payload: PaneClosedEvent{Pane: clonePane(pane), Window: cloneWindow(window), RemovedLabels: removedLabels, RemovedTool: removedTool, RemovedAttentions: removedAttentions}}
	return result, &event, nil
}

func (c *Core) resizeSplit(command ResizeSplitCommand) (any, *Event, error) {
	if command.SplitID == 0 || len(command.Weights) < 2 {
		return nil, nil, fmt.Errorf("%w: invalid split resize", ErrInvalidArgument)
	}
	for _, weight := range command.Weights {
		if weight == 0 {
			return nil, nil, fmt.Errorf("%w: zero split weight", ErrInvalidArgument)
		}
	}
	for _, windowID := range c.state.windowOrder {
		window := c.state.windows[windowID]
		if window.Layout == nil {
			continue
		}
		layout := cloneLayout(*window.Layout)
		split := splitByID(&layout, command.SplitID)
		if split == nil {
			continue
		}
		if len(split.Children) != len(command.Weights) {
			return nil, nil, fmt.Errorf("%w: split weight count", ErrInvalidArgument)
		}
		split.Weights = append([]uint32(nil), command.Weights...)
		window.Layout = &layout
		c.state.windows[window.ID] = window
		result := ResizeSplitResult{Window: cloneWindow(window)}
		return result, &Event{Kind: EventSplitResized, Payload: WindowEvent{Window: cloneWindow(window)}}, nil
	}
	return nil, nil, fmt.Errorf("%w: split %d", ErrNotFound, command.SplitID)
}

func (c *Core) setFocus(command SetFocusCommand, frontends map[FrontendID]*frontend) (any, *Event, error) {
	frontend, exists := frontends[command.FrontendID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: frontend %d", ErrNotFound, command.FrontendID)
	}
	pane, exists := c.state.panes[command.PaneID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: pane %d", ErrNotFound, command.PaneID)
	}
	if _, stashed := c.state.stashedPanes[pane.ID]; stashed {
		return nil, nil, fmt.Errorf("%w: pane %d is stashed", ErrInvalidState, pane.ID)
	}
	if _, stashed := c.state.stashedWindows[pane.WindowID]; stashed {
		return nil, nil, fmt.Errorf("%w: pane %d belongs to a stashed window", ErrInvalidState, pane.ID)
	}
	window := c.state.windows[pane.WindowID]
	frontend.state.WorkspaceID = window.WorkspaceID
	frontend.state.WindowID = window.ID
	frontend.state.PaneID = pane.ID
	return SetFocusResult{Focus: frontend.state}, nil, nil
}

func (c *Core) selectWindow(command SelectWindowCommand, frontends map[FrontendID]*frontend) (any, *Event, error) {
	frontend, exists := frontends[command.FrontendID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: frontend %d", ErrNotFound, command.FrontendID)
	}
	window, exists := c.state.windows[command.WindowID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: window %d", ErrNotFound, command.WindowID)
	}
	if _, stashed := c.state.stashedWindows[window.ID]; stashed {
		return nil, nil, fmt.Errorf("%w: window %d is stashed", ErrInvalidState, window.ID)
	}
	frontend.state.WorkspaceID = window.WorkspaceID
	frontend.state.WindowID = window.ID
	frontend.state.PaneID = 0
	if window.Layout != nil {
		frontend.state.PaneID = firstPane(*window.Layout)
	}
	return SetFocusResult{Focus: frontend.state}, nil, nil
}

func paneFromSpec(id PaneID, windowID WindowID, spec PaneSpec) Pane {
	pane := Pane{ID: id, WindowID: windowID, Kind: spec.Kind, Title: spec.Title, Presentation: spec.Presentation}
	if spec.Terminal != nil {
		terminal := cloneTerminal(*spec.Terminal)
		pane.Terminal = &terminal
	}
	if spec.Tool != nil {
		descriptor := spec.Tool.Descriptor
		pane.Tool = &descriptor
	}
	return pane
}

func (s *state) validatePaneSpec(spec PaneSpec) error {
	var descriptor *ToolDescriptor
	if spec.Tool != nil {
		value := spec.Tool.Descriptor
		descriptor = &value
	}
	pane := Pane{Kind: spec.Kind, Presentation: spec.Presentation, Terminal: spec.Terminal, Tool: descriptor}
	if !validPane(pane) {
		return fmt.Errorf("%w: invalid pane specification", ErrInvalidArgument)
	}
	if spec.Terminal != nil && spec.Terminal.ID != nil {
		for _, existing := range s.panes {
			if existing.Terminal != nil && existing.Terminal.ID != nil && *existing.Terminal.ID == *spec.Terminal.ID {
				return fmt.Errorf("%w: terminal %d is already displayed", ErrAlreadyExists, *spec.Terminal.ID)
			}
		}
	}
	if spec.Tool != nil {
		if !validToolInstance(*spec.Tool) {
			return fmt.Errorf("%w: invalid tool instance", ErrInvalidArgument)
		}
		if existing, exists := s.toolInstances[keyForTool(spec.Tool.Descriptor)]; exists &&
			(existing.StateVersion != spec.Tool.StateVersion || !bytes.Equal(existing.State, spec.Tool.State)) {
			return fmt.Errorf("%w: tool instance state differs", ErrAlreadyExists)
		}
	}
	return nil
}

func (s *state) registerTool(tool *ToolInstance) {
	if tool == nil {
		return
	}
	key := keyForTool(tool.Descriptor)
	if _, exists := s.toolInstances[key]; !exists {
		s.toolInstances[key] = cloneToolInstance(*tool)
	}
}

func cloneOptionalTool(tool *ToolInstance) *ToolInstance {
	if tool == nil {
		return nil
	}
	copy := cloneToolInstance(*tool)
	return &copy
}

func (s *state) allocateWorkspaceID() (WorkspaceID, error) {
	if s.nextWorkspaceID == 0 || uint64(s.nextWorkspaceID) == math.MaxUint64 {
		return 0, fmt.Errorf("%w: workspace ID exhausted", ErrInvalidState)
	}
	id := s.nextWorkspaceID
	s.nextWorkspaceID++
	return id, nil
}

func (s *state) allocateWindowID() (WindowID, error) {
	if s.nextWindowID == 0 || uint64(s.nextWindowID) == math.MaxUint64 {
		return 0, fmt.Errorf("%w: window ID exhausted", ErrInvalidState)
	}
	id := s.nextWindowID
	s.nextWindowID++
	return id, nil
}

func (s *state) allocatePaneID() (PaneID, error) {
	if s.nextPaneID == 0 || uint64(s.nextPaneID) == math.MaxUint64 {
		return 0, fmt.Errorf("%w: pane ID exhausted", ErrInvalidState)
	}
	id := s.nextPaneID
	s.nextPaneID++
	return id, nil
}

func (s *state) allocateSplitID() (SplitID, error) {
	if s.nextSplitID == 0 || uint64(s.nextSplitID) == math.MaxUint64 {
		return 0, fmt.Errorf("%w: split ID exhausted", ErrInvalidState)
	}
	id := s.nextSplitID
	s.nextSplitID++
	return id, nil
}

func (s *state) splitIDForInsertion(layout LayoutNode, target PaneID, direction SplitDirection) (SplitID, error) {
	required, found := insertionRequiresSplit(layout, target, direction)
	if !found {
		return 0, fmt.Errorf("%w: pane %d missing from layout", ErrInvalidState, target)
	}
	if !required {
		return 0, nil
	}
	return s.allocateSplitID()
}

func insertionRequiresSplit(node LayoutNode, target PaneID, direction SplitDirection) (bool, bool) {
	if node.Kind == LayoutPane {
		return true, node.PaneID == target
	}
	for _, child := range node.Children {
		if child.Kind == LayoutPane && child.PaneID == target && node.Direction == direction {
			return false, true
		}
		if required, found := insertionRequiresSplit(child, target, direction); found {
			return required, true
		}
	}
	return false, false
}

func splitByID(node *LayoutNode, splitID SplitID) *LayoutNode {
	if node.Kind == LayoutSplit && node.SplitID == splitID {
		return node
	}
	for index := range node.Children {
		if split := splitByID(&node.Children[index], splitID); split != nil {
			return split
		}
	}
	return nil
}

func (s *state) removeTargetLabels(kind LabelTargetKind, id uint64) []Label {
	removed := make([]Label, 0)
	for key, label := range s.labels {
		if label.TargetKind == kind && label.TargetID == id {
			removed = append(removed, label)
			delete(s.labels, key)
		}
	}
	sort.Slice(removed, func(left, right int) bool {
		if removed[left].Source != removed[right].Source {
			return removed[left].Source < removed[right].Source
		}
		return removed[left].Name < removed[right].Name
	})
	return removed
}

func removeWorkspaceID(ids []WorkspaceID, removed WorkspaceID) []WorkspaceID {
	result := make([]WorkspaceID, 0, len(ids)-1)
	for _, id := range ids {
		if id != removed {
			result = append(result, id)
		}
	}
	return result
}

func removeWindowID(ids []WindowID, removed WindowID) []WindowID {
	result := make([]WindowID, 0, len(ids)-1)
	for _, id := range ids {
		if id != removed {
			result = append(result, id)
		}
	}
	return result
}

func removePaneID(ids []PaneID, removed PaneID) []PaneID {
	result := make([]PaneID, 0, len(ids)-1)
	for _, id := range ids {
		if id != removed {
			result = append(result, id)
		}
	}
	return result
}
