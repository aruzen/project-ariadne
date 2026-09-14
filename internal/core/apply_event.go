package core

import "fmt"

// ApplyEvent projects one ordered Core event onto a frontend Snapshot.
// It rejects revision gaps so a frontend never renders silently stale state.
func ApplyEvent(snapshot Snapshot, event Event) (Snapshot, error) {
	if event.Revision != snapshot.Revision+1 {
		return Snapshot{}, fmt.Errorf("%w: event revision %d follows %d", ErrInvalidState, event.Revision, snapshot.Revision)
	}
	next := cloneSnapshot(snapshot)
	switch payload := event.Payload.(type) {
	case WorkspaceCreatedEvent:
		next.Workspaces = append(next.Workspaces, cloneWorkspace(payload.Workspace))
		if payload.Workspace.ID >= next.NextWorkspaceID {
			next.NextWorkspaceID = payload.Workspace.ID + 1
		}
	case WorkspaceEvent:
		upsertWorkspace(&next, payload.Workspace)
	case WorkspaceDeletedEvent:
		removeWorkspace(&next, payload.Workspace.ID)
		for _, label := range payload.RemovedLabels {
			removeSnapshotLabel(&next, label)
		}
	case WindowCreatedEvent:
		next.Windows = append(next.Windows, cloneWindow(payload.Window))
		if payload.Window.ID >= next.NextWindowID {
			next.NextWindowID = payload.Window.ID + 1
		}
	case WindowEvent:
		upsertWindow(&next, payload.Window)
	case WindowDeletedEvent:
		removeWindow(&next, payload.Window.ID)
		upsertWorkspace(&next, payload.Workspace)
		for _, label := range payload.RemovedLabels {
			removeSnapshotLabel(&next, label)
		}
	case PaneCreatedEvent:
		if payload.Tool != nil {
			upsertTool(&next, *payload.Tool)
		}
		next.Panes = append(next.Panes, clonePane(payload.Pane))
		if payload.Pane.ID >= next.NextPaneID {
			next.NextPaneID = payload.Pane.ID + 1
		}
		upsertWindow(&next, payload.Window)
		bumpNextSplitID(&next, payload.Window)
	case PaneMovedEvent:
		upsertPane(&next, payload.Pane)
		upsertWindow(&next, payload.SourceWindow)
		upsertWindow(&next, payload.DestinationWindow)
		bumpNextSplitID(&next, payload.SourceWindow)
		bumpNextSplitID(&next, payload.DestinationWindow)
	case PaneClosedEvent:
		for index := range next.Panes {
			if next.Panes[index].ID == payload.Pane.ID {
				next.Panes = append(next.Panes[:index], next.Panes[index+1:]...)
				break
			}
		}
		removeStashedPane(&next, payload.Pane.ID)
		if payload.Window.ID != 0 {
			upsertWindow(&next, payload.Window)
		}
		for _, label := range payload.RemovedLabels {
			removeSnapshotLabel(&next, label)
		}
		if payload.RemovedTool != nil {
			removeSnapshotTool(&next, payload.RemovedTool.Descriptor)
		}
		for _, attention := range payload.RemovedAttentions {
			removeSnapshotAttention(&next, attention.ID)
		}
	case PaneStashEvent:
		upsertPane(&next, payload.Pane)
		upsertWindow(&next, payload.Window)
		if event.Kind == EventPaneStashed {
			upsertStashedPane(&next, payload.Stashed)
		} else if event.Kind == EventPaneRestored {
			removeStashedPane(&next, payload.Pane.ID)
			bumpNextSplitID(&next, payload.Window)
		} else {
			return Snapshot{}, fmt.Errorf("%w: event %q has pane stash payload", ErrInvalidState, event.Kind)
		}
	case WindowStashEvent:
		upsertWindow(&next, payload.Window)
		upsertWorkspace(&next, payload.Workspace)
		if event.Kind == EventWindowStashed {
			upsertStashedWindow(&next, payload.Stashed)
		} else if event.Kind == EventWindowRestored {
			removeStashedWindow(&next, payload.Window.ID)
		} else {
			return Snapshot{}, fmt.Errorf("%w: event %q has window stash payload", ErrInvalidState, event.Kind)
		}
	case TerminalEvent:
		upsertPane(&next, payload.Pane)
	case LabelEvent:
		switch event.Kind {
		case EventLabelSet:
			upsertLabel(&next, payload.Label)
		case EventLabelRemoved:
			removeSnapshotLabel(&next, payload.Label)
		default:
			return Snapshot{}, fmt.Errorf("%w: event %q has label payload", ErrInvalidState, event.Kind)
		}
	case LabelsEvent:
		if event.Kind != EventLabelSourceCleared {
			return Snapshot{}, fmt.Errorf("%w: event %q has labels payload", ErrInvalidState, event.Kind)
		}
		for _, label := range payload.Labels {
			removeSnapshotLabel(&next, label)
		}
	case ToolEvent:
		upsertTool(&next, payload.Tool)
	case AttentionEvent:
		upsertAttention(&next, payload.Attention)
		for _, attention := range payload.Removed {
			removeSnapshotAttention(&next, attention.ID)
		}
	case AttentionsEvent:
		if event.Kind != EventAttentionSourceCleared {
			return Snapshot{}, fmt.Errorf("%w: event %q has attentions payload", ErrInvalidState, event.Kind)
		}
		for _, attention := range payload.Attentions {
			removeSnapshotAttention(&next, attention.ID)
		}
	default:
		return Snapshot{}, fmt.Errorf("%w: unsupported event payload %T", ErrInvalidState, event.Payload)
	}
	next.Revision = event.Revision
	if err := ValidateSnapshot(next); err != nil {
		return Snapshot{}, fmt.Errorf("apply event %q: %w", event.Kind, err)
	}
	return next, nil
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	result := snapshot
	result.Workspaces = make([]Workspace, len(snapshot.Workspaces))
	for index, workspace := range snapshot.Workspaces {
		result.Workspaces[index] = cloneWorkspace(workspace)
	}
	result.Windows = make([]Window, len(snapshot.Windows))
	for index, window := range snapshot.Windows {
		result.Windows[index] = cloneWindow(window)
	}
	result.Panes = make([]Pane, len(snapshot.Panes))
	for index, pane := range snapshot.Panes {
		result.Panes[index] = clonePane(pane)
	}
	result.StashedPanes = append([]StashedPane(nil), snapshot.StashedPanes...)
	result.StashedWindows = append([]StashedWindow(nil), snapshot.StashedWindows...)
	result.Labels = append([]Label(nil), snapshot.Labels...)
	result.ToolInstances = make([]ToolInstance, len(snapshot.ToolInstances))
	for index, tool := range snapshot.ToolInstances {
		result.ToolInstances[index] = cloneToolInstance(tool)
	}
	result.Attentions = make([]Attention, len(snapshot.Attentions))
	for index, attention := range snapshot.Attentions {
		result.Attentions[index] = cloneAttention(attention)
	}
	return result
}

func upsertTool(snapshot *Snapshot, tool ToolInstance) {
	key := keyForTool(tool.Descriptor)
	for index := range snapshot.ToolInstances {
		if keyForTool(snapshot.ToolInstances[index].Descriptor) == key {
			snapshot.ToolInstances[index] = cloneToolInstance(tool)
			return
		}
	}
	snapshot.ToolInstances = append(snapshot.ToolInstances, cloneToolInstance(tool))
}

func removeSnapshotTool(snapshot *Snapshot, descriptor ToolDescriptor) {
	key := keyForTool(descriptor)
	for index := range snapshot.ToolInstances {
		if keyForTool(snapshot.ToolInstances[index].Descriptor) == key {
			snapshot.ToolInstances = append(snapshot.ToolInstances[:index], snapshot.ToolInstances[index+1:]...)
			return
		}
	}
}

func upsertAttention(snapshot *Snapshot, attention Attention) {
	for index := range snapshot.Attentions {
		if snapshot.Attentions[index].ID == attention.ID {
			snapshot.Attentions[index] = cloneAttention(attention)
			return
		}
	}
	snapshot.Attentions = append(snapshot.Attentions, cloneAttention(attention))
}

func removeSnapshotAttention(snapshot *Snapshot, id uint64) {
	for index := range snapshot.Attentions {
		if snapshot.Attentions[index].ID == id {
			snapshot.Attentions = append(snapshot.Attentions[:index], snapshot.Attentions[index+1:]...)
			return
		}
	}
}

func upsertStashedPane(snapshot *Snapshot, stashed StashedPane) {
	for index := range snapshot.StashedPanes {
		if snapshot.StashedPanes[index].PaneID == stashed.PaneID {
			snapshot.StashedPanes[index] = stashed
			return
		}
	}
	snapshot.StashedPanes = append(snapshot.StashedPanes, stashed)
}

func removeStashedPane(snapshot *Snapshot, paneID PaneID) {
	for index := range snapshot.StashedPanes {
		if snapshot.StashedPanes[index].PaneID == paneID {
			snapshot.StashedPanes = append(snapshot.StashedPanes[:index], snapshot.StashedPanes[index+1:]...)
			return
		}
	}
}

func upsertStashedWindow(snapshot *Snapshot, stashed StashedWindow) {
	for index := range snapshot.StashedWindows {
		if snapshot.StashedWindows[index].WindowID == stashed.WindowID {
			snapshot.StashedWindows[index] = stashed
			return
		}
	}
	snapshot.StashedWindows = append(snapshot.StashedWindows, stashed)
}

func removeStashedWindow(snapshot *Snapshot, windowID WindowID) {
	for index := range snapshot.StashedWindows {
		if snapshot.StashedWindows[index].WindowID == windowID {
			snapshot.StashedWindows = append(snapshot.StashedWindows[:index], snapshot.StashedWindows[index+1:]...)
			return
		}
	}
}

func upsertWindow(snapshot *Snapshot, window Window) {
	for index := range snapshot.Windows {
		if snapshot.Windows[index].ID == window.ID {
			snapshot.Windows[index] = cloneWindow(window)
			return
		}
	}
	snapshot.Windows = append(snapshot.Windows, cloneWindow(window))
}

func upsertWorkspace(snapshot *Snapshot, workspace Workspace) {
	for index := range snapshot.Workspaces {
		if snapshot.Workspaces[index].ID == workspace.ID {
			snapshot.Workspaces[index] = cloneWorkspace(workspace)
			return
		}
	}
	snapshot.Workspaces = append(snapshot.Workspaces, cloneWorkspace(workspace))
}

func removeWorkspace(snapshot *Snapshot, workspaceID WorkspaceID) {
	for index := range snapshot.Workspaces {
		if snapshot.Workspaces[index].ID == workspaceID {
			snapshot.Workspaces = append(snapshot.Workspaces[:index], snapshot.Workspaces[index+1:]...)
			return
		}
	}
}

func removeWindow(snapshot *Snapshot, windowID WindowID) {
	for index := range snapshot.Windows {
		if snapshot.Windows[index].ID == windowID {
			snapshot.Windows = append(snapshot.Windows[:index], snapshot.Windows[index+1:]...)
			return
		}
	}
}

func upsertPane(snapshot *Snapshot, pane Pane) {
	for index := range snapshot.Panes {
		if snapshot.Panes[index].ID == pane.ID {
			snapshot.Panes[index] = clonePane(pane)
			return
		}
	}
	snapshot.Panes = append(snapshot.Panes, clonePane(pane))
}

func upsertLabel(snapshot *Snapshot, label Label) {
	for index := range snapshot.Labels {
		if keyForLabel(snapshot.Labels[index]) == keyForLabel(label) {
			snapshot.Labels[index] = label
			return
		}
	}
	snapshot.Labels = append(snapshot.Labels, label)
}

func removeSnapshotLabel(snapshot *Snapshot, label Label) {
	key := keyForLabel(label)
	for index := range snapshot.Labels {
		if keyForLabel(snapshot.Labels[index]) == key {
			snapshot.Labels = append(snapshot.Labels[:index], snapshot.Labels[index+1:]...)
			return
		}
	}
}

func bumpNextSplitID(snapshot *Snapshot, window Window) {
	var visit func(LayoutNode)
	visit = func(node LayoutNode) {
		if node.SplitID >= snapshot.NextSplitID {
			snapshot.NextSplitID = node.SplitID + 1
		}
		for _, child := range node.Children {
			visit(child)
		}
	}
	if window.Layout != nil {
		visit(*window.Layout)
	}
}
