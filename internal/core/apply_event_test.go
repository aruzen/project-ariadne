package core

import (
	"errors"
	"testing"
	"time"
)

func TestApplyEventProjectsPaneAndTerminalState(t *testing.T) {
	snapshot := DefaultSnapshot()
	pane := Pane{ID: 1, WindowID: 1, Kind: PaneTerminal, Terminal: &TerminalInstance{
		State: TerminalStarting, Launch: LaunchSpec{Argv: []string{"sh"}, CWD: "/tmp"},
	}}
	window := snapshot.Windows[0]
	window.Layout = &LayoutNode{Kind: LayoutPane, PaneID: pane.ID}
	created, err := ApplyEvent(snapshot, Event{Revision: 1, Kind: EventPaneCreated, Payload: PaneCreatedEvent{Pane: pane, Window: window}})
	if err != nil || len(created.Panes) != 1 || created.Windows[0].Layout.PaneID != pane.ID {
		t.Fatalf("created = %+v, %v", created, err)
	}
	terminalID := TerminalID(7)
	pane.Terminal = &TerminalInstance{ID: &terminalID, State: TerminalRunning, Launch: LaunchSpec{Argv: []string{"sh"}, CWD: "/tmp"}, HistoryAvailable: true}
	started, err := ApplyEvent(created, Event{Revision: 2, Kind: EventTerminalStarted, Payload: TerminalEvent{Pane: pane}})
	if err != nil || started.Panes[0].Terminal.ID == nil || *started.Panes[0].Terminal.ID != terminalID {
		t.Fatalf("started = %+v, %v", started, err)
	}
	closedWindow := window
	closedWindow.Layout = nil
	closed, err := ApplyEvent(started, Event{Revision: 3, Kind: EventPaneClosed, Payload: PaneClosedEvent{Pane: pane, Window: closedWindow}})
	if err != nil || len(closed.Panes) != 0 || closed.Windows[0].Layout != nil {
		t.Fatalf("closed = %+v, %v", closed, err)
	}
}

func TestApplyEventProjectsToolAndAttentionState(t *testing.T) {
	snapshot := DefaultSnapshot()
	descriptor := ToolDescriptor{Provider: "ariadne", Type: "help", Instance: "default"}
	tool := ToolInstance{Descriptor: descriptor, StateVersion: 1, Generation: 1, State: []byte(`{}`)}
	pane := Pane{ID: 1, WindowID: 1, Kind: PaneTool, Tool: &descriptor}
	window := snapshot.Windows[0]
	window.Layout = &LayoutNode{Kind: LayoutPane, PaneID: pane.ID}
	created, err := ApplyEvent(snapshot, Event{Revision: 1, Kind: EventPaneCreated, Payload: PaneCreatedEvent{Pane: pane, Window: window, Tool: &tool}})
	if err != nil || len(created.ToolInstances) != 1 {
		t.Fatalf("created = %+v, %v", created, err)
	}
	tool.Generation = 2
	tool.State = []byte(`{"page":2}`)
	updated, err := ApplyEvent(created, Event{Revision: 2, Kind: EventToolStateUpdated, Payload: ToolEvent{Tool: tool}})
	if err != nil || len(updated.ToolInstances) != 1 || updated.ToolInstances[0].Generation != 2 {
		t.Fatalf("updated = %+v, %v", updated, err)
	}
	now := time.Unix(300, 0).UTC()
	attention := Attention{ID: 1, PaneID: pane.ID, Source: "plugin:test", Key: "job", Class: AttentionWaiting, Severity: SeverityInfo, OccurredAt: now, UpdatedAt: now}
	raised, err := ApplyEvent(updated, Event{Revision: 3, Kind: EventAttentionRaised, Payload: AttentionEvent{Attention: attention}})
	if err != nil || len(raised.Attentions) != 1 {
		t.Fatalf("raised = %+v, %v", raised, err)
	}
	acknowledgedAt := now.Add(time.Second)
	attention.AcknowledgedAt = &acknowledgedAt
	acknowledged, err := ApplyEvent(raised, Event{Revision: 4, Kind: EventAttentionAcknowledged, Payload: AttentionEvent{Attention: attention}})
	if err != nil || acknowledged.Attentions[0].AcknowledgedAt == nil {
		t.Fatalf("acknowledged = %+v, %v", acknowledged, err)
	}
	closedWindow := window
	closedWindow.Layout = nil
	closed, err := ApplyEvent(acknowledged, Event{Revision: 5, Kind: EventPaneClosed, Payload: PaneClosedEvent{
		Pane: pane, Window: closedWindow, RemovedTool: &tool, RemovedAttentions: []Attention{attention},
	}})
	if err != nil || len(closed.ToolInstances) != 0 || len(closed.Attentions) != 0 {
		t.Fatalf("closed = %+v, %v", closed, err)
	}
}

func TestApplyEventRejectsRevisionGap(t *testing.T) {
	_, err := ApplyEvent(DefaultSnapshot(), Event{Revision: 2, Kind: EventWorkspaceCreated, Payload: WorkspaceCreatedEvent{Workspace: Workspace{ID: 2, Name: "other"}}})
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("ApplyEvent error = %v", err)
	}
}
