package core

type EventKind string

const (
	EventWorkspaceCreated       EventKind = "workspace_created"
	EventWorkspaceRenamed       EventKind = "workspace_renamed"
	EventWorkspaceDeleted       EventKind = "workspace_deleted"
	EventWindowCreated          EventKind = "window_created"
	EventWindowRenamed          EventKind = "window_renamed"
	EventWindowDeleted          EventKind = "window_deleted"
	EventPaneCreated            EventKind = "pane_created"
	EventPaneMoved              EventKind = "pane_moved"
	EventPaneClosed             EventKind = "pane_closed"
	EventSplitResized           EventKind = "split_resized"
	EventPaneStashed            EventKind = "pane_stashed"
	EventPaneRestored           EventKind = "pane_restored"
	EventWindowStashed          EventKind = "window_stashed"
	EventWindowRestored         EventKind = "window_restored"
	EventTerminalExited         EventKind = "terminal_exited"
	EventTerminalUnavailable    EventKind = "terminal_unavailable"
	EventTerminalStarted        EventKind = "terminal_started"
	EventTerminalStartFailed    EventKind = "terminal_start_failed"
	EventTerminalRestarting     EventKind = "terminal_restarting"
	EventTerminalRunPrepared    EventKind = "terminal_run_prepared"
	EventTerminalStopping       EventKind = "terminal_stopping"
	EventLabelSet               EventKind = "label_set"
	EventLabelRemoved           EventKind = "label_removed"
	EventLabelSourceCleared     EventKind = "label_source_cleared"
	EventToolStateUpdated       EventKind = "tool_state_updated"
	EventAttentionRaised        EventKind = "attention_raised"
	EventAttentionAcknowledged  EventKind = "attention_acknowledged"
	EventAttentionSourceCleared EventKind = "attention_source_cleared"
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

type WorkspaceEvent struct {
	Workspace Workspace `json:"workspace"`
}

type WorkspaceDeletedEvent struct {
	Workspace     Workspace `json:"workspace"`
	RemovedLabels []Label   `json:"removed_labels,omitempty"`
}

type WindowCreatedEvent struct {
	Window Window `json:"window"`
}

type WindowEvent struct {
	Window Window `json:"window"`
}

type WindowDeletedEvent struct {
	Window        Window    `json:"window"`
	Workspace     Workspace `json:"workspace"`
	RemovedLabels []Label   `json:"removed_labels,omitempty"`
}

type PaneCreatedEvent struct {
	Pane         Pane           `json:"pane"`
	Window       Window         `json:"window"`
	TargetPaneID PaneID         `json:"target_pane_id,omitempty"`
	Direction    SplitDirection `json:"direction,omitempty"`
	Tool         *ToolInstance  `json:"tool,omitempty"`
}

type PaneMovedEvent struct {
	Pane              Pane           `json:"pane"`
	SourceWindow      Window         `json:"source_window"`
	DestinationWindow Window         `json:"destination_window"`
	TargetPaneID      PaneID         `json:"target_pane_id,omitempty"`
	Direction         SplitDirection `json:"direction,omitempty"`
}

type PaneClosedEvent struct {
	Pane              Pane          `json:"pane"`
	Window            Window        `json:"window"`
	RemovedLabels     []Label       `json:"removed_labels,omitempty"`
	RemovedTool       *ToolInstance `json:"removed_tool,omitempty"`
	RemovedAttentions []Attention   `json:"removed_attentions,omitempty"`
}

type PaneStashEvent struct {
	Pane    Pane        `json:"pane"`
	Window  Window      `json:"window"`
	Stashed StashedPane `json:"stashed"`
}

type WindowStashEvent struct {
	Window    Window        `json:"window"`
	Workspace Workspace     `json:"workspace"`
	Stashed   StashedWindow `json:"stashed"`
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

type ToolEvent struct {
	Tool ToolInstance `json:"tool"`
}
type AttentionEvent struct {
	Attention Attention   `json:"attention"`
	Removed   []Attention `json:"removed,omitempty"`
}
type AttentionsEvent struct {
	Attentions []Attention `json:"attentions"`
}

func cloneEvent(event Event) Event {
	switch payload := event.Payload.(type) {
	case WorkspaceCreatedEvent:
		payload.Workspace = cloneWorkspace(payload.Workspace)
		event.Payload = payload
	case WorkspaceEvent:
		payload.Workspace = cloneWorkspace(payload.Workspace)
		event.Payload = payload
	case WorkspaceDeletedEvent:
		payload.Workspace = cloneWorkspace(payload.Workspace)
		payload.RemovedLabels = append([]Label(nil), payload.RemovedLabels...)
		event.Payload = payload
	case WindowCreatedEvent:
		payload.Window = cloneWindow(payload.Window)
		event.Payload = payload
	case WindowEvent:
		payload.Window = cloneWindow(payload.Window)
		event.Payload = payload
	case WindowDeletedEvent:
		payload.Window = cloneWindow(payload.Window)
		payload.Workspace = cloneWorkspace(payload.Workspace)
		payload.RemovedLabels = append([]Label(nil), payload.RemovedLabels...)
		event.Payload = payload
	case PaneCreatedEvent:
		payload.Pane = clonePane(payload.Pane)
		payload.Window = cloneWindow(payload.Window)
		if payload.Tool != nil {
			tool := cloneToolInstance(*payload.Tool)
			payload.Tool = &tool
		}
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
		if payload.RemovedTool != nil {
			tool := cloneToolInstance(*payload.RemovedTool)
			payload.RemovedTool = &tool
		}
		payload.RemovedAttentions = cloneAttentions(payload.RemovedAttentions)
		event.Payload = payload
	case PaneStashEvent:
		payload.Pane = clonePane(payload.Pane)
		payload.Window = cloneWindow(payload.Window)
		event.Payload = payload
	case WindowStashEvent:
		payload.Window = cloneWindow(payload.Window)
		payload.Workspace = cloneWorkspace(payload.Workspace)
		event.Payload = payload
	case TerminalEvent:
		payload.Pane = clonePane(payload.Pane)
		event.Payload = payload
	case LabelsEvent:
		payload.Labels = append([]Label(nil), payload.Labels...)
		event.Payload = payload
	case ToolEvent:
		payload.Tool = cloneToolInstance(payload.Tool)
		event.Payload = payload
	case AttentionEvent:
		payload.Attention = cloneAttention(payload.Attention)
		payload.Removed = cloneAttentions(payload.Removed)
		event.Payload = payload
	case AttentionsEvent:
		payload.Attentions = cloneAttentions(payload.Attentions)
		event.Payload = payload
	}
	return event
}

func cloneAttentions(values []Attention) []Attention {
	result := make([]Attention, len(values))
	for index, attention := range values {
		result[index] = cloneAttention(attention)
	}
	return result
}
