package core

import (
	"errors"
	"testing"
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

func TestApplyEventRejectsRevisionGap(t *testing.T) {
	_, err := ApplyEvent(DefaultSnapshot(), Event{Revision: 2, Kind: EventWorkspaceCreated, Payload: WorkspaceCreatedEvent{Workspace: Workspace{ID: 2, Name: "other"}}})
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("ApplyEvent error = %v", err)
	}
}
