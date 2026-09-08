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
	case WindowCreatedEvent:
		next.Windows = append(next.Windows, cloneWindow(payload.Window))
		if payload.Window.ID >= next.NextWindowID {
			next.NextWindowID = payload.Window.ID + 1
		}
		for index := range next.Workspaces {
			if next.Workspaces[index].ID == payload.Window.WorkspaceID {
				next.Workspaces[index].WindowIDs = append(next.Workspaces[index].WindowIDs, payload.Window.ID)
				break
			}
		}
	case PaneCreatedEvent:
		next.Panes = append(next.Panes, clonePane(payload.Pane))
		if payload.Pane.ID >= next.NextPaneID {
			next.NextPaneID = payload.Pane.ID + 1
		}
		upsertWindow(&next, payload.Window)
	case PaneMovedEvent:
		upsertPane(&next, payload.Pane)
		upsertWindow(&next, payload.SourceWindow)
		upsertWindow(&next, payload.DestinationWindow)
	case PaneClosedEvent:
		for index := range next.Panes {
			if next.Panes[index].ID == payload.Pane.ID {
				next.Panes = append(next.Panes[:index], next.Panes[index+1:]...)
				break
			}
		}
		upsertWindow(&next, payload.Window)
		for _, label := range payload.RemovedLabels {
			removeSnapshotLabel(&next, label)
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
	result.Labels = append([]Label(nil), snapshot.Labels...)
	return result
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
