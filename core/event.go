package core

type EventKind string

const (
	EventWorkspaceCreated EventKind = "workspace_created"
	EventWindowCreated    EventKind = "window_created"
	EventPaneCreated      EventKind = "pane_created"
	EventPaneMoved        EventKind = "pane_moved"
	EventPaneClosed       EventKind = "pane_closed"
)

// Event exists only in memory. Revision is incremented once for every
// persistent domain mutation.
type Event struct {
	Revision uint64
	Kind     EventKind
	Payload  any
}

type WorkspaceCreatedEvent struct {
	Workspace Workspace
}

type WindowCreatedEvent struct {
	Window Window
}

type PaneCreatedEvent struct {
	Pane         Pane
	Window       Window
	TargetPaneID PaneID
	Direction    SplitDirection
}

type PaneMovedEvent struct {
	Pane              Pane
	SourceWindow      Window
	DestinationWindow Window
	TargetPaneID      PaneID
	Direction         SplitDirection
}

type PaneClosedEvent struct {
	Pane   Pane
	Window Window
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
		event.Payload = payload
	}
	return event
}
