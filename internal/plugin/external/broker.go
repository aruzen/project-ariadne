package external

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/plugin"
)

func (s *session) handleAPI(parent context.Context, method string, data json.RawMessage) (result any, apiErr error) {
	defer func() {
		if errors.Is(apiErr, context.DeadlineExceeded) {
			go s.peer.Fail(apiErr)
		}
	}()
	if method == "log" {
		var p struct {
			Level   string `json:"level"`
			Message string `json:"message"`
		}
		if err := strict(data, &p); err != nil {
			return nil, err
		}
		if len(p.Message) > 4096 || len(p.Level) > 32 {
			return nil, ErrOverflow
		}
		if s.log != nil {
			_, _ = s.log.Write([]byte(p.Level + ": " + p.Message + "\n"))
		}
		return nil, nil
	}
	if method == "cancel" {
		return nil, nil
	}
	parent = s.apiCtx
	var request v1.APIRequest
	if err := strict(data, &request); err != nil {
		return nil, fmt.Errorf("plugin: invalid API envelope: %w", err)
	}
	s.gate.RLock()
	defer s.gate.RUnlock()
	if !s.active.Load() {
		return nil, ErrUnavailable
	}
	invocationCtx, invocationErr := s.manager.invocationContext(s, request.Context)
	if invocationErr != nil {
		return nil, invocationErr
	}
	parent, parentCancel := context.WithCancel(parent)
	defer parentCancel()
	stopInvocation := context.AfterFunc(invocationCtx, parentCancel)
	defer stopInvocation()
	c, err := s.manager.contextFor(s, request.Context)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, milliseconds(s.manager.config.APIMS))
	defer cancel()
	if c.Token != "" {
		if _, err := s.manager.engine.FrontendState(ctx, core.FrontendID(c.FrontendID)); err != nil {
			return nil, ErrUnavailable
		}
	}
	snapshot, err := s.manager.engine.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	type permission struct {
		cap      v1.Capability
		resource v1.Resource
	}
	var permissions []permission
	check := func(cap v1.Capability, resources ...v1.Resource) error {
		for _, r := range resources {
			if !s.allowed(snapshot, cap, r, c) {
				return ErrPermission
			}
			permissions = append(permissions, permission{cap, r})
		}
		return nil
	}
	invokeIO := func(ioCtx context.Context, method string, params json.RawMessage, c v1.Context) (any, error) {
		ioCtx = withOperationCheck(ioCtx, func(current core.Snapshot) error {
			if !s.active.Load() || parent.Err() != nil {
				return ErrUnavailable
			}
			for _, p := range permissions {
				if !s.allowed(current, p.cap, p.resource, c) {
					return ErrPermission
				}
			}
			return nil
		})
		return s.io(ioCtx, method, params, c)
	}
	pane := func(id uint64) v1.Resource { return v1.Resource{Kind: "pane", ID: id} }
	execute := func(command core.Command) (any, error) {
		result, err := s.manager.engine.ExecuteChecked(ctx, command, func(current core.Snapshot) error {
			if !s.active.Load() || parent.Err() != nil {
				return ErrUnavailable
			}
			for _, p := range permissions {
				if !s.allowed(current, p.cap, p.resource, c) {
					return ErrPermission
				}
			}
			// Recheck dynamic sets: a concurrent frontend may have moved a Pane,
			// created another shared Tool view, or changed the destination layout.
			switch command := command.(type) {
			case core.UpdateToolStateCommand:
				for _, p := range current.Panes {
					if p.Tool != nil && *p.Tool == command.Descriptor && !s.allowed(current, v1.ToolStateWrite, pane(uint64(p.ID)), c) {
						return ErrPermission
					}
				}
			case core.MovePaneCommand:
				for _, p := range current.Panes {
					if p.ID == command.PaneID && !s.allowed(current, v1.LayoutWrite, v1.Resource{Kind: "window", ID: uint64(p.WindowID)}, c) {
						return ErrPermission
					}
				}
			case core.ResizeSplitCommand:
				for _, p := range current.Panes {
					for _, grant := range permissions {
						if grant.resource.Kind == "window" && grant.resource.ID == uint64(p.WindowID) && !s.allowed(current, v1.LayoutWrite, pane(uint64(p.ID)), c) {
							return ErrPermission
						}
					}
				}
			case core.StashWindowCommand:
				for _, p := range current.Panes {
					if p.WindowID == command.WindowID && !s.allowed(current, v1.LayoutWrite, pane(uint64(p.ID)), c) {
						return ErrPermission
					}
				}
			case core.RestoreWindowCommand:
				for _, p := range current.Panes {
					if p.WindowID == command.WindowID && !s.allowed(current, v1.LayoutWrite, pane(uint64(p.ID)), c) {
						return ErrPermission
					}
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		switch value := result.(type) {
		case core.CreateWorkspaceResult:
			return map[string]uint64{"workspace_id": uint64(value.Workspace.ID)}, nil
		case core.CreateWindowResult:
			return map[string]uint64{"window_id": uint64(value.Window.ID)}, nil
		case core.CreatePaneResult:
			return map[string]uint64{"pane_id": uint64(value.Pane.ID)}, nil
		case core.ToolStateResult:
			return map[string]any{"tool": publicTool(value.Tool)}, nil
		}
		return nil, nil
	}
	switch method {
	case "core.subscribe", "pty.subscribe":
		if c.Token == "" {
			return nil, ErrPermission
		}
		cap := v1.CoreEvents
		if method == "pty.subscribe" {
			cap = v1.PTYObserve
		}
		if !s.hasCapability(cap) {
			return nil, ErrPermission
		}
		if cap == v1.PTYObserve {
			if err := check(cap, pane(c.PaneID)); err != nil {
				return nil, err
			}
		} else {
			visible := s.allowed(snapshot, cap, pane(c.PaneID), c) || s.allowed(snapshot, cap, v1.Resource{Kind: "window", ID: c.WindowID}, c) || s.allowed(snapshot, cap, v1.Resource{Kind: "workspace", ID: c.WorkspaceID}, c)
			if !visible {
				return nil, ErrPermission
			}
		}
		if !s.subscribe(c.Token, cap) {
			return nil, ErrUnavailable
		}
		s.updateObservation(snapshot)
		if cap == v1.CoreEvents {
			return s.filterSnapshot(snapshot, cap, c), nil
		}
		return nil, nil
	case "core.snapshot":
		if !s.hasCapability(v1.CoreRead) {
			return nil, ErrPermission
		}
		return s.filterSnapshot(snapshot, v1.CoreRead, c), nil
	case "label.set", "label.remove":
		var p struct {
			Kind  string `json:"kind"`
			ID    uint64 `json:"id"`
			Name  string `json:"name"`
			Value string `json:"value,omitempty"`
		}
		if err := strict(request.Params, &p); err != nil {
			return nil, err
		}
		if err := check(v1.LabelWrite, v1.Resource{Kind: p.Kind, ID: p.ID}); err != nil {
			return nil, err
		}
		if method == "label.set" {
			return execute(core.SetLabelCommand{Label: core.Label{TargetKind: core.LabelTargetKind(p.Kind), TargetID: p.ID, Source: plugin.LabelSource(s.id), Name: p.Name, Value: p.Value}})
		}
		return execute(core.RemoveLabelCommand{TargetKind: core.LabelTargetKind(p.Kind), TargetID: p.ID, Source: plugin.LabelSource(s.id), Name: p.Name})
	case "attention.raise":
		var p struct {
			PaneID   uint64 `json:"pane_id"`
			Key      string `json:"key"`
			Class    string `json:"class"`
			Severity string `json:"severity"`
			Message  string `json:"message"`
		}
		if err := strict(request.Params, &p); err != nil {
			return nil, err
		}
		if err := check(v1.AttentionWrite, pane(p.PaneID)); err != nil {
			return nil, err
		}
		return execute(core.RaiseAttentionCommand{PaneID: core.PaneID(p.PaneID), Source: plugin.LabelSource(s.id), Key: p.Key, Class: core.AttentionClass(p.Class), Severity: core.AttentionSeverity(p.Severity), Message: p.Message, OccurredAt: time.Now()})
	case "tool.state.update":
		var p struct {
			Descriptor         core.ToolDescriptor `json:"descriptor"`
			ExpectedGeneration uint64              `json:"expected_generation"`
			StateVersion       uint32              `json:"state_version"`
			State              json.RawMessage     `json:"state"`
		}
		if err := strict(request.Params, &p); err != nil {
			return nil, err
		}
		if p.Descriptor.Provider != s.id || !declares(s.manifest.Tools, p.Descriptor.Type) {
			return nil, ErrPermission
		}
		found := false
		for _, target := range snapshot.Panes {
			if target.Tool != nil && *target.Tool == p.Descriptor {
				found = true
				if err := check(v1.ToolStateWrite, pane(uint64(target.ID))); err != nil {
					return nil, err
				}
			}
		}
		if !found {
			return nil, ErrPermission
		}
		return execute(core.UpdateToolStateCommand{Descriptor: p.Descriptor, ExpectedGeneration: p.ExpectedGeneration, StateVersion: p.StateVersion, State: p.State})

	case "layout.workspace.create":
		var p struct {
			Name string `json:"name"`
		}
		if err := strict(request.Params, &p); err != nil {
			return nil, err
		}
		if !s.allCapability(v1.LayoutWrite) {
			return nil, ErrPermission
		}
		return execute(core.CreateWorkspaceCommand{Name: p.Name})
	case "layout.workspace.rename", "layout.workspace.delete", "layout.window.create":
		var p struct {
			WorkspaceID uint64 `json:"workspace_id"`
			Name        string `json:"name,omitempty"`
		}
		if err := strict(request.Params, &p); err != nil {
			return nil, err
		}
		if err := check(v1.LayoutWrite, v1.Resource{Kind: "workspace", ID: p.WorkspaceID}); err != nil {
			return nil, err
		}
		switch method {
		case "layout.workspace.rename":
			return execute(core.RenameWorkspaceCommand{WorkspaceID: core.WorkspaceID(p.WorkspaceID), Name: p.Name})
		case "layout.workspace.delete":
			return execute(core.DeleteWorkspaceCommand{WorkspaceID: core.WorkspaceID(p.WorkspaceID)})
		default:
			return execute(core.CreateWindowCommand{WorkspaceID: core.WorkspaceID(p.WorkspaceID), Name: p.Name})
		}
	case "layout.window.rename", "layout.window.delete", "layout.window.stash":
		var p struct {
			WindowID uint64 `json:"window_id"`
			Name     string `json:"name,omitempty"`
		}
		if err := strict(request.Params, &p); err != nil {
			return nil, err
		}
		if err := check(v1.LayoutWrite, v1.Resource{Kind: "window", ID: p.WindowID}); err != nil {
			return nil, err
		}
		if method == "layout.window.stash" {
			for _, target := range snapshot.Panes {
				if uint64(target.WindowID) == p.WindowID {
					if err := check(v1.LayoutWrite, pane(uint64(target.ID))); err != nil {
						return nil, err
					}
				}
			}
			return execute(core.StashWindowCommand{WindowID: core.WindowID(p.WindowID)})
		}
		if method == "layout.window.rename" {
			return execute(core.RenameWindowCommand{WindowID: core.WindowID(p.WindowID), Name: p.Name})
		}
		return execute(core.DeleteWindowCommand{WindowID: core.WindowID(p.WindowID)})
	case "layout.window.restore":
		var p struct {
			WindowID    uint64 `json:"window_id"`
			WorkspaceID uint64 `json:"workspace_id"`
		}
		if err := strict(request.Params, &p); err != nil {
			return nil, err
		}
		if err := check(v1.LayoutWrite, v1.Resource{Kind: "window", ID: p.WindowID}, v1.Resource{Kind: "workspace", ID: p.WorkspaceID}); err != nil {
			return nil, err
		}
		for _, target := range snapshot.Panes {
			if uint64(target.WindowID) == p.WindowID {
				if err := check(v1.LayoutWrite, pane(uint64(target.ID))); err != nil {
					return nil, err
				}
			}
		}
		return execute(core.RestoreWindowCommand{FrontendID: core.FrontendID(c.FrontendID), WindowID: core.WindowID(p.WindowID), WorkspaceID: core.WorkspaceID(p.WorkspaceID)})
	case "layout.pane.restore":
		var p struct {
			PaneID       uint64              `json:"pane_id"`
			WindowID     uint64              `json:"window_id"`
			TargetPaneID uint64              `json:"target_pane_id,omitempty"`
			Direction    core.SplitDirection `json:"direction,omitempty"`
		}
		if err := strict(request.Params, &p); err != nil {
			return nil, err
		}
		if err := check(v1.LayoutWrite, pane(p.PaneID), v1.Resource{Kind: "window", ID: p.WindowID}); err != nil {
			return nil, err
		}
		if p.TargetPaneID != 0 {
			if err := check(v1.LayoutWrite, pane(p.TargetPaneID)); err != nil {
				return nil, err
			}
		}
		return execute(core.RestorePaneCommand{FrontendID: core.FrontendID(c.FrontendID), PaneID: core.PaneID(p.PaneID), DestinationWindowID: core.WindowID(p.WindowID), TargetPaneID: core.PaneID(p.TargetPaneID), Direction: p.Direction})
	case "layout.resize":
		var p struct {
			WindowID uint64   `json:"window_id"`
			SplitID  uint64   `json:"split_id"`
			Weights  []uint32 `json:"weights"`
		}
		if err := strict(request.Params, &p); err != nil {
			return nil, err
		}
		if err := check(v1.LayoutWrite, v1.Resource{Kind: "window", ID: p.WindowID}); err != nil {
			return nil, err
		}
		found := false
		for _, w := range snapshot.Windows {
			if uint64(w.ID) != p.WindowID || w.Layout == nil {
				continue
			}
			var visit func(core.LayoutNode) error
			visit = func(n core.LayoutNode) error {
				if n.Kind == core.LayoutPane {
					return check(v1.LayoutWrite, pane(uint64(n.PaneID)))
				}
				if uint64(n.SplitID) == p.SplitID {
					found = true
				}
				for _, child := range n.Children {
					if err := visit(child); err != nil {
						return err
					}
				}
				return nil
			}
			if err := visit(*w.Layout); err != nil {
				return nil, err
			}
		}
		if !found {
			return nil, ErrPermission
		}
		return execute(core.ResizeSplitCommand{SplitID: core.SplitID(p.SplitID), Weights: p.Weights})
	case "layout.create-tool", "layout.split-tool":
		var p struct {
			WindowID     uint64              `json:"window_id,omitempty"`
			TargetPaneID uint64              `json:"target_pane_id,omitempty"`
			Direction    core.SplitDirection `json:"direction,omitempty"`
			Type         string              `json:"type"`
			Instance     string              `json:"instance"`
			Title        string              `json:"title,omitempty"`
		}
		if err := strict(request.Params, &p); err != nil {
			return nil, err
		}
		if !declares(s.manifest.Tools, p.Type) {
			return nil, ErrPermission
		}
		spec := core.PaneSpec{Kind: core.PaneTool, Title: p.Title, Tool: &core.ToolInstance{Descriptor: core.ToolDescriptor{Provider: s.id, Type: p.Type, Instance: p.Instance}, StateVersion: 1, Generation: 1, State: json.RawMessage(`{}`)}}
		if method == "layout.create-tool" {
			if err := check(v1.LayoutWrite, v1.Resource{Kind: "window", ID: p.WindowID}); err != nil {
				return nil, err
			}
			return execute(core.CreatePaneCommand{WindowID: core.WindowID(p.WindowID), Pane: spec})
		}
		if err := check(v1.LayoutWrite, pane(p.TargetPaneID)); err != nil {
			return nil, err
		}
		return execute(core.SplitPaneCommand{TargetPaneID: core.PaneID(p.TargetPaneID), Direction: p.Direction, Pane: spec})
	case "layout.move-pane":
		var p struct {
			PaneID       uint64              `json:"pane_id"`
			WindowID     uint64              `json:"window_id"`
			TargetPaneID uint64              `json:"target_pane_id,omitempty"`
			Direction    core.SplitDirection `json:"direction,omitempty"`
		}
		if err := strict(request.Params, &p); err != nil {
			return nil, err
		}
		if err := check(v1.LayoutWrite, pane(p.PaneID), v1.Resource{Kind: "window", ID: p.WindowID}); err != nil {
			return nil, err
		}
		if p.TargetPaneID != 0 {
			if err := check(v1.LayoutWrite, pane(p.TargetPaneID)); err != nil {
				return nil, err
			}
		}
		for _, source := range snapshot.Panes {
			if uint64(source.ID) == p.PaneID {
				if err := check(v1.LayoutWrite, v1.Resource{Kind: "window", ID: uint64(source.WindowID)}); err != nil {
					return nil, err
				}
			}
		}
		return execute(core.MovePaneCommand{PaneID: core.PaneID(p.PaneID), DestinationID: core.WindowID(p.WindowID), TargetPaneID: core.PaneID(p.TargetPaneID), Direction: p.Direction})
	case "layout.stash-pane", "layout.close-tool":
		var p struct {
			PaneID uint64 `json:"pane_id"`
		}
		if err := strict(request.Params, &p); err != nil {
			return nil, err
		}
		if err := check(v1.LayoutWrite, pane(p.PaneID)); err != nil {
			return nil, err
		}
		if method == "layout.stash-pane" {
			return execute(core.StashPaneCommand{PaneID: core.PaneID(p.PaneID)})
		}
		for _, target := range snapshot.Panes {
			if uint64(target.ID) == p.PaneID && target.Kind != core.PaneTool {
				return nil, ErrPermission
			}
		}
		return execute(core.ClosePaneCommand{PaneID: core.PaneID(p.PaneID)})
	case "clipboard.read", "clipboard.write":
		cap := v1.ClipboardRead
		if method == "clipboard.write" {
			cap = v1.ClipboardWrite
		}
		if !s.hasCapability(cap) {
			return nil, ErrPermission
		}
		return invokeIO(ctx, method, request.Params, c)
	case "frontend.interact":
		var p v1.Interaction
		if err := strict(request.Params, &p); err != nil {
			return nil, err
		}
		if p.Kind != "prompt" && p.Kind != "confirm" && p.Kind != "editor" {
			return nil, errors.New("plugin: unknown interaction")
		}
		capability := v1.FrontendInteract
		if p.Kind == "editor" {
			capability = v1.FrontendEditor
		}
		if !s.hasCapability(capability) || c.Token == "" {
			return nil, ErrPermission
		}
		origin, view := s.manager.invocationSource(s, c.Token)
		switch origin {
		case invocationInput:
			return s.manager.startInteraction(s, c, view, request.Params)
		case invocationRender, invocationWidget, invocationOther:
			return nil, ErrPermission
		case invocationCommand:
		}
		// Host dialogue time is excluded from the command deadline.
		interactionID, interactionCtx, err := s.manager.beginInteraction(s, c, "", parent)
		if err != nil {
			return nil, err
		}
		defer s.manager.finishInteraction(s, interactionID)
		counter := s.manager.interactionCounter(c.Token)
		if counter == nil {
			return nil, ErrPermission
		}
		counter.Add(1)
		defer counter.Add(-1)
		return invokeIO(interactionCtx, method, request.Params, c)
	case "pty.input":
		var p struct {
			PaneID     uint64 `json:"pane_id"`
			TerminalID uint64 `json:"terminal_id"`
			Data       []byte `json:"data"`
		}
		if err := strict(request.Params, &p); err != nil {
			return nil, err
		}
		if err := check(v1.PTYInput, pane(p.PaneID)); err != nil {
			return nil, err
		}
		if c.Token == "" || p.PaneID != c.PaneID || p.TerminalID != c.TerminalID || p.TerminalID == 0 {
			return nil, ErrPermission
		}
		for _, target := range snapshot.Panes {
			if uint64(target.ID) == p.PaneID {
				if target.Terminal == nil || target.Terminal.ID == nil || uint64(*target.Terminal.ID) != p.TerminalID {
					return nil, ErrPermission
				}
			}
		}
		return invokeIO(ctx, method, request.Params, c)
	case "terminal.new", "terminal.restart", "terminal.run", "terminal.stop", "terminal.delete":
		// Inspect only routing fields here. The daemon strictly decodes the complete
		// operation-specific DTO before running any lifecycle change.
		var routing struct {
			PaneID       uint64 `json:"pane_id"`
			WindowID     uint64 `json:"window_id"`
			TargetPaneID uint64 `json:"target_pane_id"`
		}
		if err := json.Unmarshal(request.Params, &routing); err != nil {
			return nil, err
		}
		if method == "terminal.new" {
			if routing.WindowID == 0 {
				return nil, ErrPermission
			}
			if err := check(v1.TerminalLifecycle, v1.Resource{Kind: "window", ID: routing.WindowID}); err != nil {
				return nil, err
			}
			if routing.TargetPaneID != 0 {
				if err := check(v1.TerminalLifecycle, pane(routing.TargetPaneID)); err != nil {
					return nil, err
				}
			} else {
				if err := check(v1.TerminalLifecycle, v1.Resource{Kind: "window", ID: routing.WindowID}); err != nil {
					return nil, err
				}
			}
		} else {
			if err := check(v1.TerminalLifecycle, pane(routing.PaneID)); err != nil {
				return nil, err
			}
		}
		return invokeIO(ctx, method, request.Params, c)
	default:
		return nil, errors.New("plugin: method not found")
	}
}
func (s *session) io(ctx context.Context, method string, params json.RawMessage, c v1.Context) (any, error) {
	if s.manager.operations == nil {
		return nil, ErrUnavailable
	}
	return s.manager.operations.PluginOperation(ctx, method, params, c)
}
