package tui

import (
	"context"

	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/protocol"
)

type frontendControl struct {
	ctx     context.Context
	request protocol.FrontendControlRequest
	done    chan error
}

func (s *session) enqueueFrontendControl(ctx context.Context, request protocol.FrontendControlRequest) error {
	control := frontendControl{ctx: ctx, request: request, done: make(chan error, 1)}
	select {
	case s.frontendControls <- control:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
	select {
	case err := <-control.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s *session) processFrontendControls() {
	for len(s.pendingControls) != 0 {
		control := s.pendingControls[0]
		if err := control.ctx.Err(); err != nil {
			s.pendingControls = s.pendingControls[1:]
			control.done <- err
			continue
		}
		if s.snapshot.Revision < control.request.MinimumRevision {
			return
		}
		s.pendingControls = s.pendingControls[1:]
		control.done <- s.applyFrontendControl(control.request)
	}
}

func (s *session) applyFrontendControl(request protocol.FrontendControlRequest) error {
	if request.Action != protocol.FrontendNavigate {
		return &protocol.RemoteError{Code: protocol.CodeInvalidArgument, Message: "unsupported frontend action"}
	}
	searchMode := s.copyMode && s.inputMode == inputModePrompt
	if (s.inputMode != inputModeNormal && !searchMode) || s.pluginEditor != nil || s.activePluginDialogue != nil || s.gesture != nil {
		return &protocol.RemoteError{Code: protocol.CodeInvalidState, Message: "frontend is busy"}
	}
	pane, exists := s.pane(request.PaneID)
	if !exists || pane.Transient {
		return &protocol.RemoteError{Code: protocol.CodeNotFound, Message: "pane not found"}
	}
	if s.isStashedPane(pane.ID) {
		s.leaveNavigationModes(searchMode)
		s.previewPaneByID(pane.ID)
		return nil
	}
	selected, err := callTUI[core.SetFocusResult](s, protocol.OperationSetFocus, protocol.SetFocusParams{PaneID: pane.ID})
	if err != nil {
		return err
	}
	s.leaveNavigationModes(searchMode)
	s.previewPane = 0
	s.previewPreviousFocus = 0
	s.workspace, s.window, s.focus = selected.Focus.WorkspaceID, selected.Focus.WindowID, selected.Focus.PaneID
	s.zoom = false
	s.relayout()
	s.syncViews()
	s.dirty = true
	return nil
}

func (s *session) leaveNavigationModes(searchMode bool) {
	if searchMode {
		s.clearInputMode()
	}
	if s.copyMode {
		s.leaveCopyMode()
	}
	s.zoom = false
}
