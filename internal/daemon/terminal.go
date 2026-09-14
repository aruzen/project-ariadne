package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aruzen/ariadne/internal/core"
	ariadneprotocol "github.com/aruzen/ariadne/internal/protocol"
	"github.com/aruzen/streammux/pty"
)

const terminalStartFailure = "PTY process failed to start"
const terminalCleanupTimeout = 5 * time.Second
const systemLabelSource = "ariadne"
const terminalErrorLabel = "error"

func (server *Server) NewTerminal(ctx context.Context, params ariadneprotocol.NewTerminalParams) (ariadneprotocol.TerminalOperationResult, error) {
	server.terminalMu.Lock()
	defer server.terminalMu.Unlock()
	if err := validateLaunch(params.Argv, params.CWD, params.Env, params.InitialSize); err != nil {
		return ariadneprotocol.TerminalOperationResult{}, err
	}
	snapshot, err := server.core.Snapshot(ctx)
	if err != nil {
		return ariadneprotocol.TerminalOperationResult{}, err
	}
	command, err := newTerminalPaneCommand(snapshot, params)
	if err != nil {
		return ariadneprotocol.TerminalOperationResult{}, err
	}
	result, err := server.core.Execute(ctx, command)
	if err != nil {
		return ariadneprotocol.TerminalOperationResult{}, err
	}
	created, ok := result.(core.CreatePaneResult)
	if !ok {
		return ariadneprotocol.TerminalOperationResult{}, fmt.Errorf("%w: unexpected pane result", core.ErrInvalidState)
	}
	return server.startReservedTerminal(ctx, created.Pane, params.Env, params.InitialSize)
}

func (server *Server) RestartTerminal(ctx context.Context, params ariadneprotocol.RestartTerminalParams) (ariadneprotocol.TerminalOperationResult, error) {
	server.terminalMu.Lock()
	defer server.terminalMu.Unlock()
	if params.PaneID == 0 || errEnv(params.Env) != nil || params.InitialSize.Validate() != nil {
		return ariadneprotocol.TerminalOperationResult{}, fmt.Errorf("%w: invalid restart parameters", core.ErrInvalidArgument)
	}
	snapshot, err := server.core.Snapshot(ctx)
	if err != nil {
		return ariadneprotocol.TerminalOperationResult{}, err
	}
	pane, exists := paneByID(snapshot, params.PaneID)
	if !exists {
		return ariadneprotocol.TerminalOperationResult{}, fmt.Errorf("%w: pane %d", core.ErrNotFound, params.PaneID)
	}
	if pane.Terminal == nil {
		return ariadneprotocol.TerminalOperationResult{}, fmt.Errorf("%w: pane %d has no terminal", core.ErrInvalidState, pane.ID)
	}
	switch pane.Terminal.State {
	case core.TerminalPlaceholder, core.TerminalExited, core.TerminalFailed:
	default:
		return ariadneprotocol.TerminalOperationResult{}, fmt.Errorf("%w: pane %d cannot restart", core.ErrInvalidState, pane.ID)
	}
	if err := validateLaunch(pane.Terminal.Launch.Argv, pane.Terminal.Launch.CWD, params.Env, params.InitialSize); err != nil {
		return ariadneprotocol.TerminalOperationResult{}, err
	}
	if pane.Terminal.ID != nil {
		if err := server.manager.Remove(*pane.Terminal.ID); err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
			return ariadneprotocol.TerminalOperationResult{}, err
		}
		delete(server.terminalPanes, *pane.Terminal.ID)
	}
	if _, err := server.core.Execute(ctx, core.RemoveLabelCommand{
		TargetKind: core.LabelPane, TargetID: uint64(pane.ID), Source: systemLabelSource, Name: terminalErrorLabel,
	}); err != nil {
		return ariadneprotocol.TerminalOperationResult{}, err
	}
	result, err := server.core.Execute(ctx, core.PrepareTerminalRestartCommand{PaneID: pane.ID})
	if err != nil {
		return ariadneprotocol.TerminalOperationResult{}, err
	}
	reserved := result.(core.TerminalResult).Pane
	return server.startReservedTerminal(ctx, reserved, params.Env, params.InitialSize)
}

func (server *Server) RunTerminal(ctx context.Context, params ariadneprotocol.RunTerminalParams) (ariadneprotocol.TerminalOperationResult, error) {
	server.terminalMu.Lock()
	defer server.terminalMu.Unlock()
	if params.PaneID == 0 || len(params.Argv) == 0 || errEnv(params.Env) != nil || params.InitialSize.Validate() != nil {
		return ariadneprotocol.TerminalOperationResult{}, fmt.Errorf("%w: invalid run parameters", core.ErrInvalidArgument)
	}
	snapshot, err := server.core.Snapshot(ctx)
	if err != nil {
		return ariadneprotocol.TerminalOperationResult{}, err
	}
	pane, exists := paneByID(snapshot, params.PaneID)
	if !exists {
		return ariadneprotocol.TerminalOperationResult{}, fmt.Errorf("%w: pane %d", core.ErrNotFound, params.PaneID)
	}
	if pane.Kind != core.PaneTerminal {
		return ariadneprotocol.TerminalOperationResult{}, fmt.Errorf("%w: pane %d is not a Terminal Pane", core.ErrInvalidState, pane.ID)
	}
	if pane.Terminal != nil {
		switch pane.Terminal.State {
		case core.TerminalPlaceholder, core.TerminalExited, core.TerminalFailed:
		default:
			return ariadneprotocol.TerminalOperationResult{}, fmt.Errorf("%w: pane %d cannot run", core.ErrInvalidState, pane.ID)
		}
	}
	cwd := params.CWD
	if cwd == "" && pane.Terminal != nil {
		cwd = pane.Terminal.Launch.CWD
	}
	if cwd == "" {
		cwd = params.FallbackCWD
	}
	if err := validateLaunch(params.Argv, cwd, params.Env, params.InitialSize); err != nil {
		return ariadneprotocol.TerminalOperationResult{}, err
	}
	if pane.Terminal != nil && pane.Terminal.ID != nil {
		if err := server.manager.Remove(*pane.Terminal.ID); err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
			return ariadneprotocol.TerminalOperationResult{}, err
		}
		delete(server.terminalPanes, *pane.Terminal.ID)
	}
	if _, err := server.core.Execute(ctx, core.RemoveLabelCommand{
		TargetKind: core.LabelPane, TargetID: uint64(pane.ID), Source: systemLabelSource, Name: terminalErrorLabel,
	}); err != nil {
		return ariadneprotocol.TerminalOperationResult{}, err
	}
	result, err := server.core.Execute(ctx, core.PrepareTerminalRunCommand{
		PaneID: pane.ID, Launch: core.LaunchSpec{Argv: append([]string(nil), params.Argv...), CWD: cwd},
	})
	if err != nil {
		return ariadneprotocol.TerminalOperationResult{}, err
	}
	reserved := result.(core.TerminalResult).Pane
	return server.startReservedTerminal(ctx, reserved, params.Env, params.InitialSize)
}

func (server *Server) startReservedTerminal(ctx context.Context, pane core.Pane, environment []string, size pty.Size) (ariadneprotocol.TerminalOperationResult, error) {
	launch := pane.Terminal.Launch
	session, err := server.manager.Open(ctx, pty.ProcessSpec{
		Command: launch.Argv[0], Args: append([]string(nil), launch.Argv[1:]...),
		Env: append([]string(nil), environment...), Dir: launch.CWD, InitialSize: size,
	})
	if err != nil {
		failed, recordErr := server.core.Execute(context.WithoutCancel(ctx), core.FailTerminalStartCommand{
			PaneID: pane.ID, Message: terminalStartFailure,
		})
		if recordErr == nil {
			recordErr = server.setTerminalErrorLabel(context.WithoutCancel(ctx), failed.(core.TerminalResult).Pane)
		}
		return ariadneprotocol.TerminalOperationResult{}, errors.Join(err, recordErr)
	}
	result, err := server.core.Execute(ctx, core.ActivateTerminalCommand{PaneID: pane.ID, TerminalID: session.ID()})
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), terminalCleanupTimeout)
		defer cancel()
		_ = server.manager.Kill(cleanupCtx, session.ID())
		_ = server.manager.Remove(session.ID())
		failed, recordErr := server.core.Execute(cleanupCtx, core.FailTerminalStartCommand{PaneID: pane.ID, Message: terminalStartFailure})
		if recordErr == nil {
			recordErr = server.setTerminalErrorLabel(cleanupCtx, failed.(core.TerminalResult).Pane)
		}
		return ariadneprotocol.TerminalOperationResult{}, errors.Join(err, recordErr)
	}
	server.terminalPanes[session.ID()] = pane.ID
	return ariadneprotocol.TerminalOperationResult{Pane: result.(core.TerminalResult).Pane}, nil
}

func (server *Server) setTerminalErrorLabel(ctx context.Context, pane core.Pane) error {
	if pane.Terminal == nil || pane.Terminal.Exit == nil {
		return fmt.Errorf("%w: terminal error label without exit", core.ErrInvalidState)
	}
	value := pane.Terminal.Exit.Message
	if value == "" {
		switch pane.Terminal.Exit.Kind {
		case core.TerminalExitProcess:
			value = fmt.Sprintf("process exited with code %d", pane.Terminal.Exit.Code)
		case core.TerminalExitSignal:
			value = "process exited by signal " + pane.Terminal.Exit.Signal
		}
	}
	_, err := server.core.Execute(ctx, core.SetLabelCommand{Label: core.Label{
		TargetKind: core.LabelPane, TargetID: uint64(pane.ID), Source: systemLabelSource,
		Name: terminalErrorLabel, Value: value,
	}})
	return err
}

func (server *Server) restoreSystemLabels(ctx context.Context) error {
	snapshot, err := server.core.Snapshot(ctx)
	if err != nil {
		return err
	}
	for _, pane := range snapshot.Panes {
		if pane.Terminal == nil || pane.Terminal.Exit == nil || !terminalExitNeedsErrorLabel(*pane.Terminal.Exit) {
			continue
		}
		if err := server.setTerminalErrorLabel(ctx, pane); err != nil {
			return err
		}
	}
	return nil
}

func (server *Server) ListTerminals(ctx context.Context) (ariadneprotocol.ListTerminalsResult, error) {
	server.terminalMu.Lock()
	defer server.terminalMu.Unlock()
	snapshot, err := server.core.Snapshot(ctx)
	if err != nil {
		return ariadneprotocol.ListTerminalsResult{}, err
	}
	sessions := server.manager.List()
	attachments := make(map[core.TerminalID]int, len(sessions))
	for _, session := range sessions {
		attachments[session.ID] = session.AttachmentCount
	}
	entries := make([]ariadneprotocol.TerminalListEntry, 0, len(snapshot.Panes))
	for _, pane := range snapshot.Panes {
		if pane.Kind != core.PaneTerminal || pane.Terminal == nil {
			continue
		}
		entry := ariadneprotocol.TerminalListEntry{Pane: pane}
		if pane.Terminal.ID != nil {
			entry.AttachmentCount = attachments[*pane.Terminal.ID]
		}
		entries = append(entries, entry)
	}
	return ariadneprotocol.ListTerminalsResult{Revision: snapshot.Revision, Entries: entries}, nil
}

func (server *Server) KillTerminal(ctx context.Context, params ariadneprotocol.PaneParams) (ariadneprotocol.TerminalOperationResult, error) {
	server.terminalMu.Lock()
	defer server.terminalMu.Unlock()
	pane, err := server.runningPane(ctx, params.PaneID)
	if err != nil {
		return ariadneprotocol.TerminalOperationResult{}, err
	}
	terminalID := *pane.Terminal.ID
	if _, exists := server.manager.Get(terminalID); !exists {
		return ariadneprotocol.TerminalOperationResult{}, fmt.Errorf("%w: terminal %d", core.ErrInvalidState, terminalID)
	}
	if _, err := server.core.Execute(ctx, core.BeginTerminalStopCommand{PaneID: pane.ID}); err != nil {
		return ariadneprotocol.TerminalOperationResult{}, err
	}
	if err := server.manager.Kill(ctx, terminalID); err != nil {
		return ariadneprotocol.TerminalOperationResult{}, err
	}
	result, err := server.core.Execute(context.WithoutCancel(ctx), core.ClosePaneCommand{PaneID: pane.ID})
	if err != nil && !errors.Is(err, core.ErrNotFound) {
		return ariadneprotocol.TerminalOperationResult{}, err
	}
	if removeErr := server.manager.Remove(terminalID); removeErr != nil && !errors.Is(removeErr, pty.ErrSessionNotFound) {
		return ariadneprotocol.TerminalOperationResult{}, removeErr
	}
	delete(server.terminalPanes, terminalID)
	if result != nil {
		pane = result.(core.ClosePaneResult).Pane
	}
	return ariadneprotocol.TerminalOperationResult{Pane: pane}, nil
}

func (server *Server) DismissTerminal(ctx context.Context, params ariadneprotocol.PaneParams) (ariadneprotocol.TerminalOperationResult, error) {
	server.terminalMu.Lock()
	defer server.terminalMu.Unlock()
	snapshot, err := server.core.Snapshot(ctx)
	if err != nil {
		return ariadneprotocol.TerminalOperationResult{}, err
	}
	pane, exists := paneByID(snapshot, params.PaneID)
	if !exists {
		return ariadneprotocol.TerminalOperationResult{}, fmt.Errorf("%w: pane %d", core.ErrNotFound, params.PaneID)
	}
	if pane.Terminal == nil {
		return ariadneprotocol.TerminalOperationResult{}, fmt.Errorf("%w: pane %d has no terminal", core.ErrInvalidState, pane.ID)
	}
	switch pane.Terminal.State {
	case core.TerminalPlaceholder, core.TerminalExited, core.TerminalFailed:
	default:
		return ariadneprotocol.TerminalOperationResult{}, fmt.Errorf("%w: pane %d is not dismissible", core.ErrInvalidState, pane.ID)
	}
	if pane.Terminal.ID != nil {
		if err := server.manager.Remove(*pane.Terminal.ID); err != nil && !errors.Is(err, pty.ErrSessionNotFound) {
			return ariadneprotocol.TerminalOperationResult{}, err
		}
		delete(server.terminalPanes, *pane.Terminal.ID)
	}
	if _, err := server.core.Execute(ctx, core.ClosePaneCommand{PaneID: pane.ID}); err != nil {
		return ariadneprotocol.TerminalOperationResult{}, err
	}
	return ariadneprotocol.TerminalOperationResult{Pane: pane}, nil
}

func (server *Server) DaemonStatus(context.Context) (ariadneprotocol.DaemonStatusResult, error) {
	server.mu.Lock()
	stopping := server.stopping || server.closing
	connections := server.activeConnections
	server.mu.Unlock()
	stats := server.manager.Stats()
	result := ariadneprotocol.DaemonStatusResult{
		Stopping: stopping, Connections: connections, Sessions: stats.Sessions,
		ActiveTerminals: stats.ActiveSessions + stats.StartingSessions, RetainedTerminals: stats.RetainedExitedSessions,
	}
	if server.plugins != nil {
		for _, status := range server.plugins.Status() {
			value := ariadneprotocol.PluginStatus{Name: status.Name, Enabled: status.Enabled}
			if status.Error != nil {
				value.Error = status.Error.Error()
			}
			result.Plugins = append(result.Plugins, value)
		}
	}
	return result, nil
}

func (server *Server) DaemonStop(ctx context.Context, params ariadneprotocol.DaemonStopParams) (ariadneprotocol.DaemonStatusResult, error) {
	server.terminalMu.Lock()
	defer server.terminalMu.Unlock()
	status, err := server.DaemonStatus(ctx)
	if err != nil {
		return ariadneprotocol.DaemonStatusResult{}, err
	}
	if status.ActiveTerminals != 0 && !params.Force {
		return ariadneprotocol.DaemonStatusResult{}, fmt.Errorf("%w: %d active terminal(s)", core.ErrInvalidState, status.ActiveTerminals)
	}
	server.mu.Lock()
	server.stopping = true
	server.mu.Unlock()
	status.Stopping = true
	server.stopAfterResponse.Store(true)
	return status, nil
}

func (server *Server) AfterResponse(operation ariadneprotocol.Operation) {
	if operation == ariadneprotocol.OperationDaemonStop && server.stopAfterResponse.Swap(false) {
		server.requestStop(nil)
	}
}

func (server *Server) runningPane(ctx context.Context, paneID core.PaneID) (core.Pane, error) {
	if paneID == 0 {
		return core.Pane{}, fmt.Errorf("%w: pane ID is zero", core.ErrInvalidArgument)
	}
	snapshot, err := server.core.Snapshot(ctx)
	if err != nil {
		return core.Pane{}, err
	}
	pane, exists := paneByID(snapshot, paneID)
	if !exists {
		return core.Pane{}, fmt.Errorf("%w: pane %d", core.ErrNotFound, paneID)
	}
	if pane.Terminal == nil || pane.Terminal.State != core.TerminalRunning || pane.Terminal.ID == nil {
		return core.Pane{}, fmt.Errorf("%w: pane %d terminal is not running", core.ErrInvalidState, paneID)
	}
	return pane, nil
}

func newTerminalPaneCommand(snapshot core.Snapshot, params ariadneprotocol.NewTerminalParams) (core.Command, error) {
	windowID := params.WindowID
	if windowID == 0 {
		windowID = 1
	}
	var window *core.Window
	for index := range snapshot.Windows {
		if snapshot.Windows[index].ID == windowID {
			window = &snapshot.Windows[index]
			break
		}
	}
	if window == nil {
		return nil, fmt.Errorf("%w: window %d", core.ErrNotFound, windowID)
	}
	terminal := &core.TerminalInstance{
		State:  core.TerminalStarting,
		Launch: core.LaunchSpec{Argv: append([]string(nil), params.Argv...), CWD: params.CWD},
	}
	spec := core.PaneSpec{
		Kind: core.PaneTerminal, Title: params.Title, Presentation: params.Presentation, Terminal: terminal,
	}
	if window.Layout == nil {
		if params.TargetPaneID != 0 || params.Direction != "" {
			return nil, fmt.Errorf("%w: empty window does not accept split placement", core.ErrInvalidArgument)
		}
		return core.CreatePaneCommand{WindowID: windowID, Pane: spec}, nil
	}
	targetID := params.TargetPaneID
	if targetID == 0 {
		for _, pane := range snapshot.Panes {
			if pane.WindowID == windowID {
				targetID = pane.ID
			}
		}
	} else {
		target, exists := paneByID(snapshot, targetID)
		if !exists {
			return nil, fmt.Errorf("%w: target pane %d", core.ErrNotFound, targetID)
		}
		if target.WindowID != windowID {
			return nil, fmt.Errorf("%w: target pane is outside window %d", core.ErrInvalidArgument, windowID)
		}
	}
	if targetID == 0 {
		return nil, fmt.Errorf("%w: window %d layout has no Pane", core.ErrInvalidState, windowID)
	}
	direction := params.Direction
	if direction == "" {
		direction = core.SplitVertical
	}
	return core.SplitPaneCommand{TargetPaneID: targetID, Direction: direction, Pane: spec}, nil
}

func validateLaunch(argv []string, cwd string, environment []string, size pty.Size) error {
	if len(argv) == 0 || argv[0] == "" || cwd == "" || strings.ContainsRune(cwd, 0) || size.Validate() != nil {
		return fmt.Errorf("%w: invalid process specification", core.ErrInvalidArgument)
	}
	for _, argument := range argv {
		if strings.ContainsRune(argument, 0) {
			return fmt.Errorf("%w: command argument contains NUL", core.ErrInvalidArgument)
		}
	}
	if err := errEnv(environment); err != nil {
		return err
	}
	return nil
}

func errEnv(environment []string) error {
	for _, value := range environment {
		if strings.ContainsRune(value, 0) {
			return fmt.Errorf("%w: environment contains NUL", core.ErrInvalidArgument)
		}
	}
	return nil
}

func paneByID(snapshot core.Snapshot, id core.PaneID) (core.Pane, bool) {
	for _, pane := range snapshot.Panes {
		if pane.ID == id {
			return pane, true
		}
	}
	return core.Pane{}, false
}
