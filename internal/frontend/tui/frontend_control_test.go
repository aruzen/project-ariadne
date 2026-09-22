package tui

import (
	"context"
	"testing"

	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/protocol"
)

func frontendControlSession() session {
	return session{
		ctx: context.Background(), width: 80, height: 24, workspace: 1, window: 1, focus: 1,
		views: map[core.PaneID]*paneView{}, copyMode: true, zoom: true,
		snapshot: core.Snapshot{Revision: 5,
			Workspaces:   []core.Workspace{{ID: 1, WindowIDs: []core.WindowID{1}}, {ID: 2}},
			Windows:      []core.Window{{ID: 1, WorkspaceID: 1, Layout: &core.LayoutNode{Kind: core.LayoutPane, PaneID: 1}}, {ID: 2, WorkspaceID: 2}},
			Panes:        []core.Pane{{ID: 1, WindowID: 1, Kind: core.PaneTool}, {ID: 2, WindowID: 2, Kind: core.PaneTool}, {ID: 3, WindowID: 2, Kind: core.PaneTool}},
			StashedPanes: []core.StashedPane{{PaneID: 2}, {PaneID: 3}},
		},
	}
}

func TestFrontendControlSwitchesStashedPreviewWithoutLosingOrigin(t *testing.T) {
	s := frontendControlSession()
	if err := s.applyFrontendControl(protocol.FrontendControlRequest{Action: protocol.FrontendNavigate, PaneID: 2}); err != nil {
		t.Fatal(err)
	}
	if s.previewPane != 2 || s.previewPreviousFocus != 1 || s.copyMode || s.zoom {
		t.Fatalf("first preview state = pane:%d previous:%d copy:%v zoom:%v", s.previewPane, s.previewPreviousFocus, s.copyMode, s.zoom)
	}
	if err := s.applyFrontendControl(protocol.FrontendControlRequest{Action: protocol.FrontendNavigate, PaneID: 3}); err != nil {
		t.Fatal(err)
	}
	if s.previewPane != 3 || s.previewPreviousFocus != 1 {
		t.Fatalf("switched preview state = pane:%d previous:%d", s.previewPane, s.previewPreviousFocus)
	}
}

func TestFrontendControlRejectsModalWithoutChangingState(t *testing.T) {
	s := frontendControlSession()
	s.inputMode = inputModeConfirm
	err := s.applyFrontendControl(protocol.FrontendControlRequest{Action: protocol.FrontendNavigate, PaneID: 2})
	if err == nil || s.previewPane != 0 || !s.copyMode || !s.zoom {
		t.Fatalf("modal navigation = %v, state=%+v", err, s)
	}
}

func TestFrontendControlWaitsForMinimumRevision(t *testing.T) {
	s := frontendControlSession()
	done := make(chan error, 1)
	s.pendingControls = []frontendControl{{ctx: context.Background(), request: protocol.FrontendControlRequest{Action: protocol.FrontendNavigate, PaneID: 2, MinimumRevision: 6}, done: done}}
	s.processFrontendControls()
	select {
	case <-done:
		t.Fatal("control applied before required revision")
	default:
	}
	s.snapshot.Revision = 6
	s.processFrontendControls()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
