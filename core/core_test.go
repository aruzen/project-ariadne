package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/aruzen/streammux"
)

func newTestCore(t *testing.T, queueCapacity int) *Core {
	t.Helper()
	core, err := New(Config{EventQueueCapacity: queueCapacity})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := core.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return core
}

func execute[T any](t *testing.T, core *Core, command Command) T {
	t.Helper()
	value, err := core.Execute(context.Background(), command)
	if err != nil {
		t.Fatalf("Execute(%T): %v", command, err)
	}
	result, ok := value.(T)
	if !ok {
		t.Fatalf("Execute(%T) result is %T", command, value)
	}
	return result
}

func snapshot(t *testing.T, core *Core) Snapshot {
	t.Helper()
	result, err := core.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return result
}

func TestNewCreatesDefaultHierarchy(t *testing.T) {
	core := newTestCore(t, 8)
	state := snapshot(t, core)
	if state.Revision != 0 || state.NextWorkspaceID != 2 || state.NextWindowID != 2 || state.NextPaneID != 1 {
		t.Fatalf("unexpected initial counters: %+v", state)
	}
	if len(state.Workspaces) != 1 || state.Workspaces[0].ID != 1 || state.Workspaces[0].Name != "default" {
		t.Fatalf("unexpected default workspace: %+v", state.Workspaces)
	}
	if len(state.Windows) != 1 || state.Windows[0].ID != 1 || state.Windows[0].Name != "main" || state.Windows[0].Layout != nil {
		t.Fatalf("unexpected main window: %+v", state.Windows)
	}
	if len(state.Panes) != 0 {
		t.Fatalf("unexpected initial panes: %+v", state.Panes)
	}
}

func TestNamesAndTypedIDs(t *testing.T) {
	core := newTestCore(t, 8)
	workspace := execute[CreateWorkspaceResult](t, core, CreateWorkspaceCommand{Name: "other"}).Workspace
	window := execute[CreateWindowResult](t, core, CreateWindowCommand{WorkspaceID: workspace.ID, Name: "main"}).Window
	if workspace.ID != WorkspaceID(2) || window.ID != WindowID(2) {
		t.Fatalf("unexpected IDs: workspace=%d window=%d", workspace.ID, window.ID)
	}

	if _, err := core.Execute(context.Background(), CreateWorkspaceCommand{Name: "default"}); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate workspace error = %v", err)
	}
	if _, err := core.Execute(context.Background(), CreateWindowCommand{WorkspaceID: workspace.ID, Name: "main"}); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate window error = %v", err)
	}
	// The same Window name is valid in another Workspace.
	execute[CreateWindowResult](t, core, CreateWindowCommand{WorkspaceID: 1, Name: "secondary"})
	if got := snapshot(t, core).Revision; got != 3 {
		t.Fatalf("revision after rejected commands = %d, want 3", got)
	}
}

func TestSplitFlattensMatchingDirectionAndNestsOtherDirection(t *testing.T) {
	core := newTestCore(t, 16)
	one := execute[CreatePaneResult](t, core, CreatePaneCommand{WindowID: 1, Pane: PaneSpec{Kind: PaneTerminal, Title: "one"}})
	two := execute[CreatePaneResult](t, core, SplitPaneCommand{TargetPaneID: one.Pane.ID, Direction: SplitHorizontal, Pane: PaneSpec{Kind: PaneFixed, Title: "two"}})
	three := execute[CreatePaneResult](t, core, SplitPaneCommand{TargetPaneID: one.Pane.ID, Direction: SplitHorizontal, Pane: PaneSpec{Kind: PaneTerminal, Title: "three"}})

	root := three.Window.Layout
	if root == nil || root.Kind != LayoutSplit || root.Direction != SplitHorizontal {
		t.Fatalf("unexpected horizontal root: %+v", root)
	}
	wantOrder := []PaneID{one.Pane.ID, three.Pane.ID, two.Pane.ID}
	if len(root.Children) != len(wantOrder) || len(root.Weights) != len(wantOrder) {
		t.Fatalf("unexpected child/weight count: %+v", root)
	}
	for index, want := range wantOrder {
		if root.Children[index].PaneID != want || root.Weights[index] != 1 {
			t.Fatalf("child %d = %+v weight=%d, want pane=%d weight=1", index, root.Children[index], root.Weights[index], want)
		}
	}

	four := execute[CreatePaneResult](t, core, SplitPaneCommand{TargetPaneID: one.Pane.ID, Direction: SplitVertical, Pane: PaneSpec{Kind: PaneTerminal, Title: "four"}})
	root = four.Window.Layout
	if root.Children[0].Kind != LayoutSplit || root.Children[0].Direction != SplitVertical {
		t.Fatalf("opposite direction did not create nested split: %+v", root)
	}
	if root.Children[0].Children[0].PaneID != one.Pane.ID || root.Children[0].Children[1].PaneID != four.Pane.ID {
		t.Fatalf("unexpected nested order: %+v", root.Children[0])
	}

	closed := execute[ClosePaneResult](t, core, ClosePaneCommand{PaneID: one.Pane.ID})
	root = closed.Window.Layout
	if root.Children[0].PaneID != four.Pane.ID {
		t.Fatalf("single-child split was not collapsed: %+v", root)
	}
}

func TestClosingLastPaneKeepsWindowAndWorkspace(t *testing.T) {
	core := newTestCore(t, 8)
	pane := execute[CreatePaneResult](t, core, CreatePaneCommand{WindowID: 1, Pane: PaneSpec{Kind: PaneTerminal}}).Pane
	execute[ClosePaneResult](t, core, ClosePaneCommand{PaneID: pane.ID})
	replacement := execute[CreatePaneResult](t, core, CreatePaneCommand{WindowID: 1, Pane: PaneSpec{Kind: PaneTerminal}}).Pane
	state := snapshot(t, core)
	if len(state.Workspaces) != 1 || len(state.Windows) != 1 || len(state.Panes) != 1 || replacement.ID != pane.ID+1 {
		t.Fatalf("hierarchy not retained after last Pane closed: %+v", state)
	}
}

func TestMovePaneAndFrontendFocus(t *testing.T) {
	core := newTestCore(t, 16)
	one := execute[CreatePaneResult](t, core, CreatePaneCommand{WindowID: 1, Pane: PaneSpec{Kind: PaneTerminal}}).Pane
	two := execute[CreatePaneResult](t, core, SplitPaneCommand{TargetPaneID: one.ID, Direction: SplitHorizontal, Pane: PaneSpec{Kind: PaneTerminal}}).Pane
	destination := execute[CreateWindowResult](t, core, CreateWindowCommand{WorkspaceID: 1, Name: "destination"}).Window

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, subscription, err := core.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	execute[SetFocusResult](t, core, SetFocusCommand{FrontendID: subscription.ID(), PaneID: two.ID})
	move := execute[MovePaneResult](t, core, MovePaneCommand{PaneID: two.ID, DestinationID: destination.ID})
	if move.Pane.WindowID != destination.ID || move.SourceWindow.Layout == nil || move.DestinationWindow.Layout == nil {
		t.Fatalf("unexpected move result: %+v", move)
	}
	focus, err := core.FrontendState(context.Background(), subscription.ID())
	if err != nil {
		t.Fatalf("FrontendState: %v", err)
	}
	if focus.PaneID != two.ID || focus.WindowID != destination.ID || focus.WorkspaceID != 1 {
		t.Fatalf("focus did not follow moved Pane: %+v", focus)
	}
	if got := snapshot(t, core).Revision; got != 4 {
		t.Fatalf("focus changed persistent revision: got %d, want 4", got)
	}
}

func TestMovePaneIntoNonEmptyWindowAndWithinWindow(t *testing.T) {
	core := newTestCore(t, 16)
	one := execute[CreatePaneResult](t, core, CreatePaneCommand{WindowID: 1, Pane: PaneSpec{Kind: PaneTerminal}}).Pane
	two := execute[CreatePaneResult](t, core, SplitPaneCommand{TargetPaneID: one.ID, Direction: SplitHorizontal, Pane: PaneSpec{Kind: PaneTerminal}}).Pane
	destination := execute[CreateWindowResult](t, core, CreateWindowCommand{WorkspaceID: 1, Name: "destination"}).Window
	three := execute[CreatePaneResult](t, core, CreatePaneCommand{WindowID: destination.ID, Pane: PaneSpec{Kind: PaneTerminal}}).Pane

	moved := execute[MovePaneResult](t, core, MovePaneCommand{
		PaneID: two.ID, DestinationID: destination.ID, TargetPaneID: three.ID, Direction: SplitHorizontal,
	})
	if moved.SourceWindow.Layout == nil || moved.SourceWindow.Layout.PaneID != one.ID {
		t.Fatalf("source split did not collapse: %+v", moved.SourceWindow.Layout)
	}
	if moved.DestinationWindow.Layout == nil || len(moved.DestinationWindow.Layout.Children) != 2 {
		t.Fatalf("destination split missing: %+v", moved.DestinationWindow.Layout)
	}

	moved = execute[MovePaneResult](t, core, MovePaneCommand{
		PaneID: two.ID, DestinationID: destination.ID, TargetPaneID: three.ID, Direction: SplitVertical,
	})
	if moved.DestinationWindow.Layout == nil || moved.DestinationWindow.Layout.Direction != SplitVertical {
		t.Fatalf("same-window move did not rebuild layout: %+v", moved.DestinationWindow.Layout)
	}
	restored, err := NewFromSnapshot(Config{EventQueueCapacity: 8}, snapshot(t, core))
	if err != nil {
		t.Fatalf("moved hierarchy is invalid: %v", err)
	}
	if err := restored.Close(); err != nil {
		t.Fatalf("close restored Core: %v", err)
	}
}

func TestTerminalIDUsesStreammuxIdentityAndIsUnique(t *testing.T) {
	core := newTestCore(t, 8)
	terminalID := streammux.StreamID(42)
	one := execute[CreatePaneResult](t, core, CreatePaneCommand{
		WindowID: 1,
		Pane: PaneSpec{Kind: PaneTerminal, Terminal: &TerminalInstance{
			ID: &terminalID, State: TerminalRunning, Launch: LaunchSpec{Argv: []string{"/bin/sh"}, CWD: "/tmp"},
		}},
	})
	if one.Pane.Terminal == nil || one.Pane.Terminal.ID == nil || *one.Pane.Terminal.ID != terminalID {
		t.Fatalf("terminal identity lost: %+v", one.Pane)
	}
	_, err := core.Execute(context.Background(), SplitPaneCommand{
		TargetPaneID: one.Pane.ID,
		Direction:    SplitHorizontal,
		Pane: PaneSpec{Kind: PaneTerminal, Terminal: &TerminalInstance{
			ID: &terminalID, State: TerminalRunning, Launch: LaunchSpec{Argv: []string{"/bin/sh"}, CWD: "/tmp"},
		}},
	})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("duplicate TerminalID error = %v", err)
	}
}

func TestTerminalStateInvariants(t *testing.T) {
	core := newTestCore(t, 8)
	id := streammux.StreamID(7)
	launch := LaunchSpec{Argv: []string{"agent"}, CWD: "/work"}
	for _, test := range []struct {
		name     string
		terminal TerminalInstance
	}{
		{name: "running without ID", terminal: TerminalInstance{State: TerminalRunning, Launch: launch}},
		{name: "placeholder with ID", terminal: TerminalInstance{ID: &id, State: TerminalPlaceholder, Launch: launch}},
		{name: "failed without PTY error", terminal: TerminalInstance{State: TerminalFailed, Launch: launch, Exit: &TerminalExit{Kind: TerminalExitProcess, Code: 1}}},
		{name: "exited with PTY error", terminal: TerminalInstance{State: TerminalExited, Launch: launch, Exit: &TerminalExit{Kind: TerminalExitPTYError, Message: "failed"}}},
		{name: "history without runtime ID", terminal: TerminalInstance{State: TerminalPlaceholder, Launch: launch, HistoryAvailable: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := core.Execute(context.Background(), CreatePaneCommand{
				WindowID: 1, Pane: PaneSpec{Kind: PaneTerminal, Terminal: &test.terminal},
			})
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("Execute error = %v", err)
			}
		})
	}
	if got := snapshot(t, core).Revision; got != 0 {
		t.Fatalf("invalid Terminal commands changed Revision: %d", got)
	}
}

func TestSnapshotIsDeepCopyAndCanBeRestored(t *testing.T) {
	core := newTestCore(t, 8)
	one := execute[CreatePaneResult](t, core, CreatePaneCommand{WindowID: 1, Pane: PaneSpec{Kind: PaneTerminal}}).Pane
	execute[CreatePaneResult](t, core, SplitPaneCommand{TargetPaneID: one.ID, Direction: SplitHorizontal, Pane: PaneSpec{Kind: PaneFixed}})
	state := snapshot(t, core)
	state.Workspaces[0].Name = "mutated"
	state.Workspaces[0].WindowIDs[0] = 999
	state.Windows[0].Layout.Children[0].PaneID = 999
	if got := snapshot(t, core); got.Workspaces[0].Name != "default" || got.Windows[0].Layout.Children[0].PaneID != one.ID {
		t.Fatalf("snapshot mutation reached Core: %+v", got)
	}

	restoredState := snapshot(t, core)
	restored, err := NewFromSnapshot(Config{EventQueueCapacity: 8}, restoredState)
	if err != nil {
		t.Fatalf("NewFromSnapshot: %v", err)
	}
	defer restored.Close()
	got, err := restored.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("restored Snapshot: %v", err)
	}
	if got.Revision != restoredState.Revision || got.NextPaneID != restoredState.NextPaneID || len(got.Panes) != len(restoredState.Panes) {
		t.Fatalf("restored state differs: got=%+v want=%+v", got, restoredState)
	}
}

func TestSubscribeSnapshotAndEventsHaveNoRevisionGap(t *testing.T) {
	const commands = 100
	core := newTestCore(t, commands+1)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for index := 0; index < commands; index++ {
			_, err := core.Execute(context.Background(), CreateWorkspaceCommand{Name: fmt.Sprintf("workspace-%03d", index)})
			if err != nil {
				t.Errorf("CreateWorkspace %d: %v", index, err)
				return
			}
		}
	}()
	close(start)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	base, subscription, err := core.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	wg.Wait()
	final := snapshot(t, core)
	wantEvents := int(final.Revision - base.Revision)
	for index := 0; index < wantEvents; index++ {
		select {
		case event := <-subscription.Events():
			wantRevision := base.Revision + uint64(index) + 1
			if event.Revision != wantRevision {
				t.Fatalf("event revision = %d, want %d", event.Revision, wantRevision)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for event %d/%d", index+1, wantEvents)
		}
	}
}

func TestSlowFrontendIsDisconnectedOnQueueOverflow(t *testing.T) {
	core := newTestCore(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, subscription, err := core.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	execute[CreateWorkspaceResult](t, core, CreateWorkspaceCommand{Name: "one"})
	execute[CreateWorkspaceResult](t, core, CreateWorkspaceCommand{Name: "two"})
	select {
	case <-subscription.Done():
	case <-time.After(time.Second):
		t.Fatal("overflowed subscription was not closed")
	}
	if !errors.Is(subscription.Err(), ErrEventQueueOverflow) {
		t.Fatalf("subscription error = %v", subscription.Err())
	}
	if _, err := core.FrontendState(context.Background(), subscription.ID()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("overflowed frontend is still registered: %v", err)
	}
}

func TestSubscriptionContextRemovesFrontend(t *testing.T) {
	core := newTestCore(t, 8)
	ctx, cancel := context.WithCancel(context.Background())
	_, subscription, err := core.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	cancel()
	select {
	case <-subscription.Done():
	case <-time.After(time.Second):
		t.Fatal("canceled subscription was not closed")
	}
	if !errors.Is(subscription.Err(), context.Canceled) {
		t.Fatalf("subscription error = %v", subscription.Err())
	}
}

func TestEventPayloadsAreIndependentCopies(t *testing.T) {
	core := newTestCore(t, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, first, err := core.Subscribe(ctx)
	if err != nil {
		t.Fatalf("first Subscribe: %v", err)
	}
	_, second, err := core.Subscribe(ctx)
	if err != nil {
		t.Fatalf("second Subscribe: %v", err)
	}
	execute[CreateWindowResult](t, core, CreateWindowCommand{WorkspaceID: 1, Name: "events"})
	firstEvent := <-first.Events()
	secondEvent := <-second.Events()
	firstPayload := firstEvent.Payload.(WindowCreatedEvent)
	secondPayload := secondEvent.Payload.(WindowCreatedEvent)
	firstPayload.Window.Name = "mutated"
	if secondPayload.Window.Name != "events" {
		t.Fatalf("event payload shared between subscribers: %+v", secondPayload)
	}
}

func TestConcurrentCommandsAreSerialized(t *testing.T) {
	const workers = 64
	core := newTestCore(t, workers+1)
	var wg sync.WaitGroup
	errorsByWorker := make(chan error, workers)
	for index := 0; index < workers; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			_, err := core.Execute(context.Background(), CreateWorkspaceCommand{Name: fmt.Sprintf("concurrent-%02d", index)})
			errorsByWorker <- err
		}(index)
	}
	wg.Wait()
	close(errorsByWorker)
	for err := range errorsByWorker {
		if err != nil {
			t.Fatalf("concurrent Execute: %v", err)
		}
	}
	state := snapshot(t, core)
	if state.Revision != workers || len(state.Workspaces) != workers+1 || state.NextWorkspaceID != workers+2 {
		t.Fatalf("serialized state mismatch: revision=%d workspaces=%d next=%d", state.Revision, len(state.Workspaces), state.NextWorkspaceID)
	}
}

func TestInvalidRestoreIsRejected(t *testing.T) {
	state := Snapshot{
		NextWorkspaceID: 2,
		NextWindowID:    2,
		NextPaneID:      1,
		Workspaces:      []Workspace{{ID: 1, Name: "default", WindowIDs: []WindowID{1}}},
		Windows:         []Window{{ID: 1, WorkspaceID: 1, Name: "main", Layout: &LayoutNode{Kind: LayoutPane, PaneID: 9}}},
	}
	if _, err := NewFromSnapshot(Config{EventQueueCapacity: 8}, state); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("invalid restore error = %v", err)
	}
}

func TestRevisionExhaustionDoesNotMutateState(t *testing.T) {
	base := newTestCore(t, 8)
	state := snapshot(t, base)
	state.Revision = math.MaxUint64
	core, err := NewFromSnapshot(Config{EventQueueCapacity: 8}, state)
	if err != nil {
		t.Fatalf("NewFromSnapshot: %v", err)
	}
	defer core.Close()
	if _, err := core.Execute(context.Background(), CreateWorkspaceCommand{Name: "overflow"}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("revision exhaustion error = %v", err)
	}
	got, err := core.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(got.Workspaces) != len(state.Workspaces) || got.Revision != state.Revision {
		t.Fatalf("state changed after revision exhaustion: %+v", got)
	}
}

func TestCloseTerminatesSubscriptionsAndRejectsCommands(t *testing.T) {
	core, err := New(Config{EventQueueCapacity: 8})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, subscription, err := core.Subscribe(context.Background())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := core.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !errors.Is(subscription.Err(), ErrClosed) {
		t.Fatalf("subscription error = %v", subscription.Err())
	}
	if _, err := core.Execute(context.Background(), CreateWorkspaceCommand{Name: "late"}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Execute after Close error = %v", err)
	}
}
