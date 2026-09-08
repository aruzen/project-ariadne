package core

type EventKind string

const (
	EventWorkspaceCreated    EventKind = "workspace_created"
	EventWindowCreated       EventKind = "window_created"
	EventPaneCreated         EventKind = "pane_created"
	EventPaneMoved           EventKind = "pane_moved"
	EventPaneClosed          EventKind = "pane_closed"
	EventTerminalExited      EventKind = "terminal_exited"
	EventTerminalUnavailable EventKind = "terminal_unavailable"
	EventTerminalStarted     EventKind = "terminal_started"
	EventTerminalStartFailed EventKind = "terminal_start_failed"
	EventTerminalRestarting  EventKind = "terminal_restarting"
	EventTerminalStopping    EventKind = "terminal_stopping"
	EventLabelSet            EventKind = "label_set"
	EventLabelRemoved        EventKind = "label_removed"
	EventLabelSourceCleared  EventKind = "label_source_cleared"
)

// Event exists only in memory. Revision is incremented once for every
// persistent domain mutation.
type Event struct {
	Revision uint64    `json:"revision"`
	Kind     EventKind `json:"kind"`
	Payload  any       `json:"payload"`
}

type WorkspaceCreatedEvent struct {
	Workspace Workspace `json:"workspace"`
}

type WindowCreatedEvent struct {
	Window Window `json:"window"`
}

type PaneCreatedEvent struct {
	Pane         Pane           `json:"pane"`
	Window       Window         `json:"window"`
	TargetPaneID PaneID         `json:"target_pane_id,omitempty"`
	Direction    SplitDirection `json:"direction,omitempty"`
}

type PaneMovedEvent struct {
	Pane              Pane           `json:"pane"`
	SourceWindow      Window         `json:"source_window"`
	DestinationWindow Window         `json:"destination_window"`
	TargetPaneID      PaneID         `json:"target_pane_id,omitempty"`
	Direction         SplitDirection `json:"direction,omitempty"`
}

type PaneClosedEvent struct {
	Pane          Pane    `json:"pane"`
	Window        Window  `json:"window"`
	RemovedLabels []Label `json:"removed_labels,omitempty"`
}

type TerminalEvent struct {
	Pane Pane `json:"pane"`
}

type LabelEvent struct {
	Label Label `json:"label"`
}

type LabelsEvent struct {
	Labels []Label `json:"labels"`
}

func cloneEvent(event Event) Event {
	switch payload := event.Payload.(type) {
	case WorkspaceCreatedEvent:
		payload.Workspace = cloneWorkspace(payload.Workspace)
		event.Payload = payload
	case WindowCreatedEvent:
		payload.Window = cloneWindow(payload.Window)
		event.Payload = payload
	case PaneCreatedEvent:
		payload.Pane = clonePane(payload.Pane)
		payload.Window = cloneWindow(payload.Window)
		event.Payload = payload
	case PaneMovedEvent:
		payload.Pane = clonePane(payload.Pane)
		payload.SourceWindow = cloneWindow(payload.SourceWindow)
		payload.DestinationWindow = cloneWindow(payload.DestinationWindow)
		event.Payload = payload
	case PaneClosedEvent:
		payload.Pane = clonePane(payload.Pane)
		payload.Window = cloneWindow(payload.Window)
		payload.RemovedLabels = append([]Label(nil), payload.RemovedLabels...)
		event.Payload = payload
	case TerminalEvent:
		payload.Pane = clonePane(payload.Pane)
		event.Payload = payload
	case LabelsEvent:
		payload.Labels = append([]Label(nil), payload.Labels...)
		event.Payload = payload
	}
	return event
}
