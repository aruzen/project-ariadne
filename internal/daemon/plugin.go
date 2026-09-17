package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/aruzen/ariadne/internal/config"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/plugin/external"
	"github.com/aruzen/ariadne/internal/protocol"
	"github.com/aruzen/streammux"
	"github.com/aruzen/streammux/pty"
)

type pluginFrontend struct {
	peer   *streammux.Peer
	ctx    context.Context
	cancel context.CancelFunc
}
type pluginEditor struct {
	frontend uint64
	path     string
	terminal core.TerminalID
}
type pluginController struct {
	server   *Server
	peer     *streammux.Peer
	once     sync.Once
	frontend uint64
}

func (p *pluginController) Manage(ctx context.Context, frontend uint64, request v1.ManageRequest) (v1.ManageResult, error) {
	if err := ctx.Err(); err != nil {
		return v1.ManageResult{}, err
	}
	p.once.Do(func() {
		p.frontend = frontend
		linkCtx, cancel := context.WithCancel(p.server.ctx)
		p.server.mu.Lock()
		p.server.pluginFrontends[frontend] = pluginFrontend{p.peer, linkCtx, cancel}
		p.server.mu.Unlock()
	})
	if request.Action == "editor.open" || request.Action == "editor.finish" || request.Action == "editor.cancel" {
		return p.server.managePluginEditor(ctx, frontend, request)
	}
	result, err := p.server.externalPlugins.Manage(ctx, frontend, request)
	if errors.Is(err, external.ErrPermission) {
		return result, &protocol.RemoteError{Code: protocol.CodePermissionDenied, Message: err.Error()}
	}
	return result, err
}
func (p *pluginController) Detach(frontend uint64) {
	p.server.externalPlugins.Detach(frontend)
	p.server.mu.Lock()
	link, ok := p.server.pluginFrontends[frontend]
	delete(p.server.pluginFrontends, frontend)
	ids := []core.PaneID{}
	for id, e := range p.server.pluginEditors {
		if e.frontend == frontend {
			ids = append(ids, id)
		}
	}
	p.server.mu.Unlock()
	if ok {
		link.cancel()
	}
	for _, id := range ids {
		_, _ = p.server.managePluginEditor(context.Background(), frontend, v1.ManageRequest{Action: "editor.cancel", PaneID: uint64(id)})
	}
}
func decodePluginParams(data []byte, destination any) error {
	return external.DecodeParameters(data, destination)
}
func (server *Server) PluginOperation(ctx context.Context, method string, params json.RawMessage, c v1.Context) (any, error) {
	switch method {
	case "terminal.new":
		var p protocol.NewTerminalParams
		if err := decodePluginParams(params, &p); err != nil {
			return nil, err
		}
		result, err := server.NewTerminal(ctx, p)
		return map[string]any{"pane": external.PublicPane(result.Pane)}, err
	case "terminal.restart":
		var p protocol.RestartTerminalParams
		if err := decodePluginParams(params, &p); err != nil {
			return nil, err
		}
		result, err := server.RestartTerminal(ctx, p)
		return map[string]any{"pane": external.PublicPane(result.Pane)}, err
	case "terminal.run":
		var p protocol.RunTerminalParams
		if err := decodePluginParams(params, &p); err != nil {
			return nil, err
		}
		result, err := server.RunTerminal(ctx, p)
		return map[string]any{"pane": external.PublicPane(result.Pane)}, err
	case "terminal.stop", "terminal.delete":
		var p protocol.PaneParams
		if err := decodePluginParams(params, &p); err != nil {
			return nil, err
		}
		if method == "terminal.stop" {
			_, err := server.StopTerminal(ctx, p)
			return nil, err
		}
		_, err := server.DeletePane(ctx, p)
		return nil, err
	case "pty.input":
		var p struct {
			PaneID     uint64 `json:"pane_id"`
			TerminalID uint64 `json:"terminal_id"`
			Data       []byte `json:"data"`
		}
		if err := decodePluginParams(params, &p); err != nil {
			return nil, err
		}
		server.terminalMu.Lock()
		defer server.terminalMu.Unlock()
		snapshot, err := server.core.Snapshot(ctx)
		if err != nil {
			return nil, err
		}
		if err := external.CheckOperation(ctx, snapshot); err != nil {
			return nil, err
		}
		pane, ok := snapshot.PaneByTerminalID(core.TerminalID(p.TerminalID))
		if !ok || uint64(pane.ID) != p.PaneID || c.TerminalID != p.TerminalID {
			return nil, external.ErrPermission
		}
		session, ok := server.manager.Get(core.TerminalID(p.TerminalID))
		if !ok {
			return nil, external.ErrUnavailable
		}
		return nil, session.Input(ctx, p.Data)
	case "clipboard.read", "clipboard.write":
		policy := server.config.Clipboard.ReadPolicy
		if method == "clipboard.write" {
			policy = server.config.Clipboard.WritePolicy
		}
		if policy == config.ClipboardDeny {
			return nil, external.ErrPermission
		}
		if method == "clipboard.write" {
			var p struct {
				Text string `json:"text"`
			}
			if err := decodePluginParams(params, &p); err != nil {
				return nil, err
			}
			if len(p.Text) > server.config.Clipboard.MaxTextBytes || !utf8.ValidString(p.Text) {
				return nil, core.ErrInvalidArgument
			}
			server.clipboardMu.Lock()
			server.clipboardText = append(server.clipboardText[:0], p.Text...)
			server.clipboardMu.Unlock()
			_ = server.config.Clipboard.Backend.Write(ctx, []byte(p.Text))
			return nil, nil
		}
		if len(params) != 0 && string(params) != "{}" {
			return nil, core.ErrInvalidArgument
		}
		data, err := server.config.Clipboard.Backend.Read(ctx, server.config.Clipboard.MaxTextBytes)
		server.clipboardMu.Lock()
		defer server.clipboardMu.Unlock()
		if err == nil && len(data) <= server.config.Clipboard.MaxTextBytes && utf8.Valid(data) {
			server.clipboardText = append(server.clipboardText[:0], data...)
		}
		return v1.InteractionResult{Text: string(server.clipboardText)}, nil
	case "frontend.interact":
		var interaction v1.Interaction
		if err := decodePluginParams(params, &interaction); err != nil {
			return nil, err
		}
		server.mu.Lock()
		link, ok := server.pluginFrontends[c.FrontendID]
		server.mu.Unlock()
		if !ok {
			return nil, external.ErrUnavailable
		}
		requestCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(link.ctx, cancel)
		defer stop()
		interactionID := fmt.Sprintf("%s-%d", c.Token, server.pluginInteractionNext.Add(1))
		payload, err := json.Marshal(v1.InteractionRequest{ID: interactionID, Context: c, Interaction: interaction})
		if err != nil {
			return nil, err
		}
		if len(payload)+64 > link.peer.MaxFrameBytes() {
			return nil, external.ErrOverflow
		}
		frame, err := streammux.NewFrame(streammux.Header{Version: protocol.Version, MessageType: protocol.MessagePluginInteraction, Flags: streammux.FlagRequest, CorrelationID: 1}, payload)
		if err != nil {
			return nil, err
		}
		response, err := link.peer.Call(requestCtx, frame)
		if requestCtx.Err() != nil {
			data, _ := json.Marshal(map[string]string{"id": interactionID})
			cancelFrame, e := streammux.NewFrame(streammux.Header{Version: protocol.Version, MessageType: protocol.MessagePluginInteractionCancel, Flags: streammux.FlagEvent}, data)
			if e == nil {
				cancelCtx, cancel := context.WithTimeout(context.Background(), server.config.Clipboard.Timeout)
				_ = link.peer.Emit(cancelCtx, cancelFrame)
				cancel()
			}
		}
		if err != nil {
			return nil, err
		}
		var result v1.InteractionResponse
		if err := decodePluginParams(response.Payload, &result); err != nil {
			return nil, err
		}
		if result.Error != "" {
			return nil, errors.New(result.Error)
		}
		return result.Result, nil
	}
	return nil, errors.New("plugin: unsupported host operation")
}
func (server *Server) managePluginEditor(ctx context.Context, frontend uint64, request v1.ManageRequest) (v1.ManageResult, error) {
	if request.Action == "editor.open" {
		if request.Editor == nil || len(request.Editor.Argv) == 0 || len(request.Editor.Text) > core.MaxToolStateBytes {
			return v1.ManageResult{}, core.ErrInvalidArgument
		}
		p := request.Editor
		server.mu.Lock()
		_, hadLink := server.pluginFrontends[frontend]
		server.mu.Unlock()
		f, err := server.core.FrontendState(ctx, core.FrontendID(frontend))
		if err != nil {
			return v1.ManageResult{}, err
		}
		directory := filepath.Join(filepath.Dir(server.config.StatePath), "plugins", "editors")
		if err := os.MkdirAll(directory, 0700); err != nil {
			return v1.ManageResult{}, err
		}
		file, err := os.CreateTemp(directory, "edit-*")
		if err != nil {
			return v1.ManageResult{}, err
		}
		path := file.Name()
		if _, err = file.WriteString(p.Text); err != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return v1.ManageResult{}, err
		}
		if err = file.Close(); err != nil {
			_ = os.Remove(path)
			return v1.ManageResult{}, err
		}
		argv := append(append([]string(nil), p.Argv...), path)
		size := pty.Size{Cols: 80, Rows: 24}
		server.terminalMu.Lock()
		value, err := server.core.Execute(ctx, core.CreateTransientPaneCommand{WindowID: f.WindowID, Launch: core.LaunchSpec{Argv: argv, CWD: p.CWD}})
		var result protocol.TerminalOperationResult
		if err == nil {
			result, err = server.startReservedTerminal(ctx, value.(core.CreatePaneResult).Pane, p.Env, size)
		}
		server.terminalMu.Unlock()
		if err != nil {
			_ = os.Remove(path)
			if value != nil {
				_, _ = server.core.Execute(context.Background(), core.ClosePaneCommand{PaneID: value.(core.CreatePaneResult).Pane.ID})
			}
			return v1.ManageResult{}, err
		}
		id := result.Pane.ID
		terminal := *result.Pane.Terminal.ID
		server.mu.Lock()
		link, linked := server.pluginFrontends[frontend]
		cancelled := ctx.Err() != nil || (hadLink && (!linked || link.ctx.Err() != nil))
		if !cancelled {
			server.pluginEditors[id] = pluginEditor{frontend, path, terminal}
		}
		server.mu.Unlock()
		if cancelled {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), terminalCleanupTimeout)
			_, _ = server.StopTerminal(cleanupCtx, protocol.PaneParams{PaneID: id})
			_, _ = server.DeletePane(cleanupCtx, protocol.PaneParams{PaneID: id})
			cancel()
			_ = os.Remove(path)
			return v1.ManageResult{}, external.ErrUnavailable
		}
		return v1.ManageResult{Editor: &v1.EditorResult{PaneID: uint64(id), TerminalID: uint64(terminal)}}, nil
	}
	id := core.PaneID(request.PaneID)
	if request.Action == "editor.finish" {
		server.mu.Lock()
		e, ok := server.pluginEditors[id]
		server.mu.Unlock()
		if !ok || e.frontend != frontend {
			return v1.ManageResult{}, external.ErrPermission
		}
		session, ok := server.manager.Get(e.terminal)
		if !ok {
			return v1.ManageResult{}, external.ErrUnavailable
		}
		info := session.Info()
		if info.State != pty.SessionExited && info.State != pty.SessionFailed {
			return v1.ManageResult{}, core.ErrInvalidState
		}
	}
	server.mu.Lock()
	editor, ok := server.pluginEditors[id]
	if ok && editor.frontend == frontend {
		delete(server.pluginEditors, id)
	}
	server.mu.Unlock()
	if !ok || editor.frontend != frontend {
		return v1.ManageResult{}, external.ErrPermission
	}
	defer os.Remove(editor.path)
	var text string
	var resultErr error
	if request.Action == "editor.finish" {
		session, ok := server.manager.Get(editor.terminal)
		if !ok {
			resultErr = external.ErrUnavailable
		} else {
			status, err := session.Wait(ctx)
			if err != nil || status.Reason != pty.ExitReasonExited || status.Code != 0 {
				resultErr = errors.New("plugin: editor cancelled or exited unsuccessfully")
			} else {
				file, err := os.Open(editor.path)
				var data []byte
				if err == nil {
					data, err = io.ReadAll(io.LimitReader(file, core.MaxToolStateBytes+1))
					err = errors.Join(err, file.Close())
				}
				if err != nil {
					resultErr = err
				} else if len(data) > core.MaxToolStateBytes || !utf8.Valid(data) {
					resultErr = core.ErrInvalidArgument
				} else {
					text = string(data)
				}
			}
		}
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), terminalCleanupTimeout)
	defer cancel()
	server.terminalMu.Lock()
	if session, ok := server.manager.Get(editor.terminal); ok {
		_ = session.Kill(cleanupCtx)
		_, _ = session.Wait(cleanupCtx)
	}
	_, closeErr := server.core.Execute(cleanupCtx, core.ClosePaneCommand{PaneID: id})
	_ = server.manager.Remove(editor.terminal)
	delete(server.terminalPanes, editor.terminal)
	server.terminalMu.Unlock()
	if errors.Is(closeErr, core.ErrNotFound) {
		closeErr = nil
	}
	return v1.ManageResult{Editor: &v1.EditorResult{PaneID: uint64(id), Text: text}}, errors.Join(resultErr, closeErr)
}

func (server *Server) ReservedPluginIDs() []string {
	ids := []string{"ariadne"}
	for _, implementation := range server.config.Plugins {
		if implementation != nil {
			ids = append(ids, implementation.Name())
		}
	}
	return ids
}
