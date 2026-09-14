package core

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/aruzen/streammux"
)

func TestStashPanePreservesTerminalAndRestoresPlacement(t *testing.T) {
	engine := newTestCore(t, 32)
	terminalID := streammux.StreamID(77)
	one := execute[CreatePaneResult](t, engine, CreatePaneCommand{
		WindowID: 1,
		Pane: PaneSpec{Kind: PaneTerminal, Terminal: &TerminalInstance{
			ID: &terminalID, State: TerminalRunning, Launch: LaunchSpec{Argv: []string{"sh"}, CWD: "/tmp"},
		}},
	}).Pane
	two := execute[CreatePaneResult](t, engine, SplitPaneCommand{
		TargetPaneID: one.ID, Direction: SplitHorizontal, Pane: PaneSpec{Kind: PaneTool},
	})
	splitID := two.Window.Layout.SplitID
	execute[ResizeSplitResult](t, engine, ResizeSplitCommand{SplitID: splitID, Weights: []uint32{3, 7}})

	base, subscription, err := engine.Subscribe(context.Background())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	stashed := execute[StashPaneResult](t, engine, StashPaneCommand{PaneID: one.ID})
	if stashed.Window.Layout == nil || stashed.Window.Layout.PaneID != two.Pane.ID {
		t.Fatalf("remaining layout = %+v", stashed.Window.Layout)
	}
	if stashed.Stashed.TargetPaneID != two.Pane.ID || !stashed.Stashed.Before || stashed.Stashed.Weight != 3 || stashed.Stashed.TargetWeight != 7 {
		t.Fatalf("stash hint = %+v", stashed.Stashed)
	}
	state := snapshot(t, engine)
	if len(state.StashedPanes) != 1 || len(state.Panes) != 2 {
		t.Fatalf("stashed state = %+v", state)
	}
	kept, ok := state.PaneByTerminalID(terminalID)
	if !ok || kept.ID != one.ID || kept.Terminal.State != TerminalRunning {
		t.Fatalf("running terminal was not retained: %+v, %v", kept, ok)
	}

	restored := execute[RestorePaneResult](t, engine, RestorePaneCommand{FrontendID: subscription.ID(), PaneID: one.ID})
	root := restored.Window.Layout
	if root == nil || root.SplitID != splitID || len(root.Children) != 2 || root.Children[0].PaneID != one.ID || root.Children[1].PaneID != two.Pane.ID {
		t.Fatalf("restored layout = %+v", root)
	}
	if root.Weights[0] != 3 || root.Weights[1] != 7 {
		t.Fatalf("restored weights = %v", root.Weights)
	}

	for range 2 {
		event := <-subscription.Events()
		base, err = ApplyEvent(base, event)
		if err != nil {
			t.Fatalf("ApplyEvent(%s): %v", event.Kind, err)
		}
	}
	if len(base.StashedPanes) != 0 || !layoutContainsPane(base.Windows[0].Layout, one.ID) {
		t.Fatalf("projected restore state = %+v", base)
	}
}

func TestRestorePaneFallsBackWhenOriginWasDeleted(t *testing.T) {
	engine := newTestCore(t, 16)
	pane := execute[CreatePaneResult](t, engine, CreatePaneCommand{WindowID: 1, Pane: PaneSpec{Kind: PaneTool}}).Pane
	destination := execute[CreateWindowResult](t, engine, CreateWindowCommand{WorkspaceID: 1, Name: "destination"}).Window
	execute[StashPaneResult](t, engine, StashPaneCommand{PaneID: pane.ID})
	execute[DeleteWindowResult](t, engine, DeleteWindowCommand{WindowID: 1})
	_, subscription, err := engine.Subscribe(context.Background())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	execute[SetFocusResult](t, engine, SelectWindowCommand{FrontendID: subscription.ID(), WindowID: destination.ID})
	restored := execute[RestorePaneResult](t, engine, RestorePaneCommand{FrontendID: subscription.ID(), PaneID: pane.ID})
	if restored.Window.ID != destination.ID || restored.Pane.WindowID != destination.ID || restored.Window.Layout.PaneID != pane.ID {
		t.Fatalf("fallback restore = %+v", restored)
	}
}

func TestStashWindowRestoresOriginalIndexAndFrontend(t *testing.T) {
	engine := newTestCore(t, 16)
	second := execute[CreateWindowResult](t, engine, CreateWindowCommand{WorkspaceID: 1, Name: "second"}).Window
	third := execute[CreateWindowResult](t, engine, CreateWindowCommand{WorkspaceID: 1, Name: "third"}).Window
	_, subscription, err := engine.Subscribe(context.Background())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	execute[SetFocusResult](t, engine, SelectWindowCommand{FrontendID: subscription.ID(), WindowID: second.ID})
	stashed := execute[StashWindowResult](t, engine, StashWindowCommand{WindowID: second.ID})
	if len(stashed.Workspace.WindowIDs) != 2 || stashed.Workspace.WindowIDs[0] != 1 || stashed.Workspace.WindowIDs[1] != third.ID {
		t.Fatalf("workspace after stash = %+v", stashed.Workspace)
	}
	focus, err := engine.FrontendState(context.Background(), subscription.ID())
	if err != nil || focus.WindowID == second.ID {
		t.Fatalf("frontend remained on stashed window: %+v, %v", focus, err)
	}
	restored := execute[RestoreWindowResult](t, engine, RestoreWindowCommand{FrontendID: subscription.ID(), WindowID: second.ID})
	if got := restored.Workspace.WindowIDs; len(got) != 3 || got[0] != 1 || got[1] != second.ID || got[2] != third.ID {
		t.Fatalf("restored window order = %v", got)
	}
}

func TestPaneCanCloseInsideStashedWindow(t *testing.T) {
	engine := newTestCore(t, 16)
	pane := execute[CreatePaneResult](t, engine, CreatePaneCommand{WindowID: 1, Pane: PaneSpec{Kind: PaneTool}}).Pane
	execute[StashWindowResult](t, engine, StashWindowCommand{WindowID: 1})
	closed := execute[ClosePaneResult](t, engine, ClosePaneCommand{PaneID: pane.ID})
	if closed.Window.Layout != nil {
		t.Fatalf("stashed window layout after close = %+v", closed.Window.Layout)
	}
	state := snapshot(t, engine)
	if len(state.Panes) != 0 || len(state.StashedWindows) != 1 {
		t.Fatalf("state after close in stashed window = %+v", state)
	}
	execute[RestoreWindowResult](t, engine, RestoreWindowCommand{WindowID: 1})
}

func TestConcurrentRestorePaneHasOneWinner(t *testing.T) {
	engine := newTestCore(t, 16)
	pane := execute[CreatePaneResult](t, engine, CreatePaneCommand{WindowID: 1, Pane: PaneSpec{Kind: PaneTool}}).Pane
	execute[StashPaneResult](t, engine, StashPaneCommand{PaneID: pane.ID})
	_, subscription, err := engine.Subscribe(context.Background())
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	var wait sync.WaitGroup
	errorsByWorker := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := engine.Execute(context.Background(), RestorePaneCommand{FrontendID: subscription.ID(), PaneID: pane.ID})
			errorsByWorker <- err
		}()
	}
	wait.Wait()
	close(errorsByWorker)
	succeeded, rejected := 0, 0
	for err := range errorsByWorker {
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrInvalidState) {
			rejected++
		} else {
			t.Fatalf("unexpected restore error: %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("restore outcomes: succeeded=%d rejected=%d", succeeded, rejected)
	}
}

func TestTerminalExitAndStashCommute(t *testing.T) {
	engine := newTestCore(t, 16)
	id := streammux.StreamID(88)
	pane := execute[CreatePaneResult](t, engine, CreatePaneCommand{
		WindowID: 1,
		Pane: PaneSpec{Kind: PaneTerminal, Terminal: &TerminalInstance{
			ID: &id, State: TerminalRunning, Launch: LaunchSpec{Argv: []string{"sh"}, CWD: "/tmp"},
		}},
	}).Pane
	start := make(chan struct{})
	errorsByOperation := make(chan error, 2)
	go func() {
		<-start
		_, err := engine.Execute(context.Background(), StashPaneCommand{PaneID: pane.ID})
		errorsByOperation <- err
	}()
	go func() {
		<-start
		_, err := engine.Execute(context.Background(), RecordTerminalExitCommand{
			TerminalID: id, State: TerminalExited, Exit: TerminalExit{Kind: TerminalExitProcess}, HistoryAvailable: true,
		})
		errorsByOperation <- err
	}()
	close(start)
	for range 2 {
		if err := <-errorsByOperation; err != nil {
			t.Fatalf("concurrent operation: %v", err)
		}
	}
	state := snapshot(t, engine)
	if len(state.StashedPanes) != 1 || state.Panes[0].Terminal.State != TerminalExited || !state.Panes[0].Terminal.HistoryAvailable {
		t.Fatalf("final state = %+v", state)
	}
}
