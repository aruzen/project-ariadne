package external

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/aruzen/ariadne/internal/core"
)

func brokerSession(t *testing.T) (*session, *core.Core, uint64) {
	t.Helper()
	m, engine, frontend := newTestManager(t, t.TempDir(), DefaultConfig())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := &session{manager: m, id: "scope-plugin", generation: 1, ctx: ctx, apiCtx: ctx, cancel: cancel, apiCancel: cancel, manifest: v1.Manifest{ID: "scope-plugin", Capabilities: []v1.Capability{v1.CoreRead, v1.CoreEvents, v1.LayoutWrite, v1.LabelWrite, v1.AttentionWrite, v1.ToolStateWrite, v1.PTYInput, v1.ClipboardRead}, Tools: []v1.Declaration{{Name: "demo"}}}}
	s.active.Store(true)
	return s, engine, frontend
}
func invoke(s *session, method, token string, params any) (any, error) {
	return s.handleAPI(context.Background(), method, asJSON(v1.APIRequest{Context: token, Params: asJSON(params)}))
}
func TestWorkspaceScopesFollowActualMembershipAndStash(t *testing.T) {
	s, engine, _ := brokerSession(t)
	ctx := context.Background()
	value, err := engine.Execute(ctx, core.CreatePaneCommand{WindowID: 1, Pane: core.PaneSpec{Kind: core.PaneTool}})
	if err != nil {
		t.Fatal(err)
	}
	p := value.(core.CreatePaneResult).Pane
	value, err = engine.Execute(ctx, core.CreateWorkspaceCommand{Name: "second"})
	if err != nil {
		t.Fatal(err)
	}
	workspace := value.(core.CreateWorkspaceResult).Workspace
	value, err = engine.Execute(ctx, core.CreateWindowCommand{WorkspaceID: workspace.ID, Name: "second"})
	if err != nil {
		t.Fatal(err)
	}
	window := value.(core.CreateWindowResult).Window
	s.grants = []v1.Grant{{Capability: v1.CoreRead, Scope: v1.Scope{Kind: "workspace", IDs: []uint64{1}}}}
	snapshot, _ := engine.Snapshot(ctx)
	resource := v1.Resource{Kind: "pane", ID: uint64(p.ID)}
	if !s.allowed(snapshot, v1.CoreRead, resource, v1.Context{}) {
		t.Fatal("workspace pane denied")
	}
	_, err = engine.Execute(ctx, core.StashPaneCommand{PaneID: p.ID})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ = engine.Snapshot(ctx)
	if s.allowed(snapshot, v1.CoreRead, resource, v1.Context{}) {
		t.Fatal("stash origin treated as membership")
	}
	s.grants = append(s.grants, v1.Grant{Capability: v1.CoreRead, Scope: v1.Scope{Kind: "pane", IDs: []uint64{uint64(p.ID)}}})
	if !s.allowed(snapshot, v1.CoreRead, resource, v1.Context{}) {
		t.Fatal("explicit stashed Pane denied")
	}
	_, err = engine.Execute(ctx, core.RestorePaneCommand{PaneID: p.ID, DestinationWindowID: window.ID})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ = engine.Snapshot(ctx)
	s.grants = s.grants[:1]
	if s.allowed(snapshot, v1.CoreRead, resource, v1.Context{}) {
		t.Fatal("moved Pane retained old workspace permission")
	}
	s.grants = []v1.Grant{{Capability: v1.CoreRead, Scope: v1.Scope{Kind: "workspace", IDs: []uint64{uint64(workspace.ID)}}}}
	if !s.allowed(snapshot, v1.CoreRead, resource, v1.Context{}) {
		t.Fatal("new workspace membership denied")
	}
	_, err = engine.Execute(ctx, core.StashWindowCommand{WindowID: window.ID})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, _ = engine.Snapshot(ctx)
	if s.allowed(snapshot, v1.CoreRead, resource, v1.Context{}) || s.allowed(snapshot, v1.CoreRead, v1.Resource{Kind: "window", ID: uint64(window.ID)}, v1.Context{}) {
		t.Fatal("stashed Window retained workspace access")
	}
	s.grants = []v1.Grant{{Capability: v1.CoreRead, Scope: v1.Scope{Kind: "all"}}}
	if !s.allowed(snapshot, v1.CoreRead, resource, v1.Context{}) {
		t.Fatal("all scope denied stash")
	}
}
func TestContextCannotBeForgedAndKeepsOriginalFocusAndTerminal(t *testing.T) {
	s, engine, frontend := brokerSession(t)
	ctx := context.Background()
	value, err := engine.Execute(ctx, core.CreatePaneCommand{WindowID: 1, Pane: core.PaneSpec{Kind: core.PaneTerminal}})
	if err != nil {
		t.Fatal(err)
	}
	p := value.(core.CreatePaneResult).Pane
	value, err = engine.Execute(ctx, core.SplitPaneCommand{TargetPaneID: p.ID, Direction: core.SplitHorizontal, Pane: core.PaneSpec{Kind: core.PaneTool}})
	if err != nil {
		t.Fatal(err)
	}
	other := value.(core.CreatePaneResult).Pane
	s.grants = []v1.Grant{{Capability: v1.LabelWrite, Scope: v1.Scope{Kind: "context"}}, {Capability: v1.PTYInput, Scope: v1.Scope{Kind: "all"}}}
	captured, err := s.manager.capture(ctx, s, frontend, uint64(p.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(s, "label.set", "forged", map[string]any{"kind": "pane", "id": p.ID, "name": "state", "value": "bad"}); !errors.Is(err, ErrPermission) {
		t.Fatalf("forged token accepted: %v", err)
	}
	_, err = engine.Execute(ctx, core.SetFocusCommand{FrontendID: core.FrontendID(frontend), PaneID: other.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(s, "label.set", captured.Token, map[string]any{"kind": "pane", "id": p.ID, "name": "state", "value": "fixed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(s, "label.set", captured.Token, map[string]any{"kind": "pane", "id": other.ID, "name": "state", "value": "wrong"}); !errors.Is(err, ErrPermission) {
		t.Fatal("context followed focus")
	}
	// The invocation captured no live terminal; an arbitrary terminal ID is rejected.
	if _, err := invoke(s, "pty.input", captured.Token, map[string]any{"pane_id": p.ID, "terminal_id": 42, "data": []byte("x")}); !errors.Is(err, ErrPermission) {
		t.Fatal("old/arbitrary Terminal accepted")
	}
	if _, err := invoke(s, "label.set", captured.Token, map[string]any{"kind": "pane", "id": p.ID, "name": "state", "value": "forged-source", "source": "plugin:other"}); err == nil {
		t.Fatal("source spoofing accepted")
	}
	s.manager.releaseContext(captured.Token)
	if _, err := invoke(s, "label.set", captured.Token, map[string]any{"kind": "pane", "id": p.ID, "name": "state", "value": "late"}); !errors.Is(err, ErrPermission) {
		t.Fatal("expired invocation accepted")
	}
}
func TestSharedToolStateChecksEveryPaneAndProvider(t *testing.T) {
	s, engine, _ := brokerSession(t)
	ctx := context.Background()
	tool := core.ToolInstance{Descriptor: core.ToolDescriptor{Provider: s.id, Type: "demo", Instance: "shared"}, StateVersion: 1, Generation: 1, State: json.RawMessage(`{}`)}
	value, err := engine.Execute(ctx, core.CreatePaneCommand{WindowID: 1, Pane: core.PaneSpec{Kind: core.PaneTool, Tool: &tool}})
	if err != nil {
		t.Fatal(err)
	}
	one := value.(core.CreatePaneResult).Pane
	value, err = engine.Execute(ctx, core.SplitPaneCommand{TargetPaneID: one.ID, Direction: core.SplitHorizontal, Pane: core.PaneSpec{Kind: core.PaneTool, Tool: &tool}})
	if err != nil {
		t.Fatal(err)
	}
	two := value.(core.CreatePaneResult).Pane
	s.grants = []v1.Grant{{Capability: v1.ToolStateWrite, Scope: v1.Scope{Kind: "pane", IDs: []uint64{uint64(one.ID)}}}}
	params := map[string]any{"descriptor": tool.Descriptor, "expected_generation": 1, "state_version": 1, "state": map[string]bool{"updated": true}}
	if _, err := invoke(s, "tool.state.update", "", params); !errors.Is(err, ErrPermission) {
		t.Fatal("shared state checked only one Pane")
	}
	s.grants[0].Scope.IDs = append(s.grants[0].Scope.IDs, uint64(two.ID))
	if _, err := invoke(s, "tool.state.update", "", params); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(s, "tool.state.update", "", params); err == nil {
		t.Fatal("stale Tool generation accepted")
	}
	params["descriptor"] = core.ToolDescriptor{Provider: "ariadne", Type: "demo", Instance: "shared"}
	if _, err := invoke(s, "tool.state.update", "", params); !errors.Is(err, ErrPermission) {
		t.Fatal("provider spoofing accepted")
	}
}
func TestReadAndEventProjectionCannotLeakOtherPanes(t *testing.T) {
	s, engine, _ := brokerSession(t)
	ctx := context.Background()
	value, err := engine.Execute(ctx, core.CreatePaneCommand{WindowID: 1, Pane: core.PaneSpec{Kind: core.PaneTool, Title: "allowed"}})
	if err != nil {
		t.Fatal(err)
	}
	one := value.(core.CreatePaneResult).Pane
	value, err = engine.Execute(ctx, core.SplitPaneCommand{TargetPaneID: one.ID, Direction: core.SplitHorizontal, Pane: core.PaneSpec{Kind: core.PaneTool, Title: "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	two := value.(core.CreatePaneResult).Pane
	s.grants = []v1.Grant{{Capability: v1.CoreRead, Scope: v1.Scope{Kind: "pane", IDs: []uint64{uint64(one.ID)}}}, {Capability: v1.CoreEvents, Scope: v1.Scope{Kind: "pane", IDs: []uint64{uint64(one.ID)}}}}
	snapshot, _ := engine.Snapshot(ctx)
	projected := s.filterSnapshot(snapshot, v1.CoreRead, v1.Context{})
	if len(projected.Panes) != 1 || len(projected.Windows) != 0 {
		t.Fatal("scope leaked read data")
	}
	event := core.Event{Kind: core.EventPaneClosed, Payload: core.PaneClosedEvent{Pane: two}}
	if s.eventVisible(event, snapshot, snapshot) {
		t.Fatal("outside event delivered")
	}
	event.Payload = core.PaneClosedEvent{Pane: one}
	if !s.eventVisible(event, snapshot, snapshot) {
		t.Fatal("authorized event lost")
	}
	if err := ValidateGrant(v1.Grant{Capability: v1.ClipboardRead, Scope: v1.Scope{Kind: "pane", IDs: []uint64{1}}}); err == nil {
		t.Fatal("global clipboard resource scope accepted")
	}
}
func TestRevokeInvalidatesOldGeneration(t *testing.T) {
	t.Setenv("ARIADNE_PLUGIN_TEST_PROCESS", "1")
	m, _, frontend := newTestManager(t, t.TempDir(), DefaultConfig())
	manage(t, m, frontend, v1.ManageRequest{Action: "install", Directory: testPackage(t, "test-plugin")})
	g := v1.Grant{Capability: v1.CoreRead, Scope: v1.Scope{Kind: "all"}}
	manage(t, m, frontend, v1.ManageRequest{Action: "grant", ID: "test-plugin", Grant: &g})
	manage(t, m, frontend, v1.ManageRequest{Action: "enable", ID: "test-plugin"})
	old, err := m.get("test-plugin")
	if err != nil {
		t.Fatal(err)
	}
	c, err := m.capture(context.Background(), old, frontend, 0)
	if err != nil {
		t.Fatal(err)
	}
	manage(t, m, frontend, v1.ManageRequest{Action: "revoke", ID: "test-plugin", Grant: &g})
	current, err := m.get("test-plugin")
	if err != nil {
		t.Fatal(err)
	}
	if current.generation <= old.generation || old.active.Load() {
		t.Fatal("generation not invalidated")
	}
	if _, err := old.handleAPI(context.Background(), "core.snapshot", asJSON(v1.APIRequest{Context: c.Token})); err == nil {
		t.Fatal("old generation API accepted")
	}
	if _, err := invoke(current, "core.snapshot", "", map[string]any{}); !errors.Is(err, ErrPermission) {
		t.Fatal("revoke did not remove capability")
	}
}

func TestTerminalReplacementRejectsBothOldAndNewIDsFromOldContext(t *testing.T) {
	s, engine, frontend := brokerSession(t)
	ctx := context.Background()
	value, err := engine.Execute(ctx, core.CreatePaneCommand{WindowID: 1, Pane: core.PaneSpec{Kind: core.PaneTerminal, Terminal: &core.TerminalInstance{State: core.TerminalStarting, Launch: core.LaunchSpec{Argv: []string{"shell"}, CWD: t.TempDir()}}}})
	if err != nil {
		t.Fatal(err)
	}
	p := value.(core.CreatePaneResult).Pane
	if _, err := engine.Execute(ctx, core.ActivateTerminalCommand{PaneID: p.ID, TerminalID: 42}); err != nil {
		t.Fatal(err)
	}
	s.grants = []v1.Grant{{Capability: v1.PTYInput, Scope: v1.Scope{Kind: "all"}}}
	captured, err := s.manager.capture(ctx, s, frontend, uint64(p.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer s.manager.releaseContext(captured.Token)
	for _, command := range []core.Command{
		core.RecordTerminalExitCommand{TerminalID: 42, State: core.TerminalExited, Exit: core.TerminalExit{Kind: core.TerminalExitProcess}},
		core.PrepareTerminalRestartCommand{PaneID: p.ID},
		core.ActivateTerminalCommand{PaneID: p.ID, TerminalID: 43},
	} {
		if _, err := engine.Execute(ctx, command); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []uint64{42, 43} {
		if _, err := invoke(s, "pty.input", captured.Token, map[string]any{"pane_id": p.ID, "terminal_id": id, "data": []byte("stale")}); !errors.Is(err, ErrPermission) {
			t.Fatal("old context survived terminal replacement", id, err)
		}
	}
	current, err := s.manager.capture(ctx, s, frontend, uint64(p.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer s.manager.releaseContext(current.Token)
	if _, err := invoke(s, "pty.input", current.Token, map[string]any{"pane_id": p.ID, "terminal_id": uint64(43), "data": []byte("current")}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("current identity did not reach I/O broker", err)
	}
}

func TestContextSubscriptionsExpireAndDetachDoesNotRetainTokens(t *testing.T) {
	s, engine, frontend := brokerSession(t)
	s.manifest.Capabilities = append(s.manifest.Capabilities, v1.PTYObserve)
	s.grants = []v1.Grant{{Capability: v1.CoreEvents, Scope: v1.Scope{Kind: "context"}}, {Capability: v1.PTYObserve, Scope: v1.Scope{Kind: "context"}}}
	value, err := engine.Execute(context.Background(), core.CreatePaneCommand{WindowID: 1, Pane: core.PaneSpec{Kind: core.PaneTerminal}})
	if err != nil {
		t.Fatal(err)
	}
	p := value.(core.CreatePaneResult).Pane
	s.manager.mu.Lock()
	s.manager.sessions[s.id] = s
	s.manager.mu.Unlock()
	// This synthetic session has no supervisor; remove it before manager cleanup.
	defer func() { s.manager.mu.Lock(); delete(s.manager.sessions, s.id); s.manager.mu.Unlock() }()
	captured, err := s.manager.capture(context.Background(), s, frontend, uint64(p.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(s, "core.subscribe", captured.Token, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(s, "pty.subscribe", captured.Token, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if len(s.subscriptionContexts(v1.CoreEvents)) != 1 || len(s.subscriptionContexts(v1.PTYObserve)) != 1 {
		t.Fatal("context subscription missing")
	}
	s.manager.Detach(frontend)
	s.subMu.Lock()
	retained := len(s.subscriptions)
	s.subMu.Unlock()
	if retained != 0 {
		t.Fatal("detach retained expired subscription tokens")
	}
	if _, err := invoke(s, "core.subscribe", captured.Token, map[string]any{}); !errors.Is(err, ErrPermission) {
		t.Fatal("detached subscription revived", err)
	}
}
