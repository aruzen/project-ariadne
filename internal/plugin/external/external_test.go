package external

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/plugin"
	"github.com/aruzen/ariadne/internal/statefile"
)

func newTestManager(t *testing.T, root string, c Config) (*Manager, *core.Core, uint64) {
	t.Helper()
	engine, err := core.New(core.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	_, subscription, err := engine.Subscribe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for range subscription.Events() {
		}
	}()
	m, err := NewManager(context.Background(), engine, root, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close(context.Background()); _ = subscription.Close(); _ = engine.Close() })
	return m, engine, uint64(subscription.ID())
}

type orderedDeferredResult struct {
	peer *Peer
}

func (result orderedDeferredResult) rpcResult() any { return "started" }
func (result orderedDeferredResult) rpcAfterResponse(err error) {
	if err == nil {
		_ = result.peer.Notify("completed", "done")
	}
}

func testPackage(t *testing.T, id string) string {
	t.Helper()
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	name := "plugin"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := v1.Manifest{ID: id, Version: "1.0", APIVersion: 1, Runtime: "process", Entrypoints: map[string]v1.Entrypoint{runtime.GOOS + "/" + runtime.GOARCH: {Path: name, Args: []string{"-test.run=^TestProcessPlugin$"}}}, Capabilities: []v1.Capability{v1.CoreRead, v1.LabelWrite, v1.ToolStateWrite, v1.AttentionWrite, v1.CoreEvents, v1.PTYObserve, v1.FrontendInteract, v1.FrontendEditor}, Commands: []v1.Declaration{{Name: "echo"}, {Name: "hang"}, {Name: "crash"}, {Name: "malformed"}, {Name: "error"}, {Name: "interact"}}, Tools: []v1.Declaration{{Name: "demo"}}, Widgets: []v1.Declaration{{Name: "status"}}}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), asJSON(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// The installed package is a copy of this test binary, so tests exercise real
// stdio, process ownership, framing, and daemon persistence without a compiler.
func TestProcessPlugin(t *testing.T) {
	if os.Getenv("ARIADNE_PLUGIN_TEST_PROCESS") != "1" {
		return
	}
	reader := bufio.NewReaderSize(os.Stdin, 8192)
	backlog := []Message{}
	for {
		var m Message
		var err error
		if len(backlog) > 0 {
			m = backlog[0]
			backlog = backlog[1:]
		} else {
			m, err = ReadMessage(reader, 8<<20)
		}
		if err != nil {
			os.Exit(0)
		}
		if m.ID == nil || m.Method == "" {
			continue
		}
		var result any
		switch m.Method {
		case "initialize":
			result = v1.InitializeResult{APIVersion: 1}
		case "command":
			var c v1.Command
			_ = json.Unmarshal(m.Params, &c)
			switch c.Name {
			case "hang":
				time.Sleep(time.Hour)
			case "crash":
				os.Exit(9)
			case "malformed":
				_, _ = io.WriteString(os.Stdout, "Content-Length: 2\r\n\r\n{}")
				time.Sleep(time.Hour)
			case "interact":
				id := uint64(100000)
				_ = WriteMessage(os.Stdout, Message{JSONRPC: "2.0", ID: &id, Method: "frontend.interact", Params: asJSON(v1.APIRequest{Context: c.Context.Token, Params: asJSON(v1.Interaction{Kind: "prompt"})})})
				var response Message
				for {
					var err error
					response, err = ReadMessage(reader, 8<<20)
					if err != nil {
						os.Exit(2)
					}
					if response.Method != "" {
						if response.ID != nil {
							backlog = append(backlog, response)
						}
						continue
					}
					if response.ID != nil && *response.ID == id {
						break
					}
				}
				var text v1.InteractionResult
				_ = json.Unmarshal(response.Result, &text)
				c.Args = []string{text.Text}
			case "error":
				_ = WriteMessage(os.Stdout, Message{JSONRPC: "2.0", ID: m.ID, Error: &RPCError{Code: -32000, Message: "handler error"}})
				continue
			}
			result = v1.CommandResult{Text: strings.Join(c.Args, " ")}
		case "widget":
			result = v1.WidgetResult{Text: "ok"}
		case "view.render":
			var v v1.View
			_ = json.Unmarshal(m.Params, &v)
			cells := make([]v1.Cell, v.Width*v.Height)
			for i := range cells {
				cells[i] = v1.Cell{Text: " ", Width: 1}
			}
			if len(cells) >= 2 && v.Width >= 2 {
				cells[0] = v1.Cell{Text: "界", Width: 2}
				cells[1] = v1.Cell{Width: 0}
			}
			result = v1.Frame{ViewID: v.ID, Generation: v.Generation, Width: v.Width, Height: v.Height, Cells: cells}
		default:
			result = nil
		}
		_ = WriteMessage(os.Stdout, Message{JSONRPC: "2.0", ID: m.ID, Result: asJSON(result)})
	}
}
func manage(t *testing.T, m *Manager, frontend uint64, r v1.ManageRequest) v1.ManageResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := m.Manage(ctx, frontend, r)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestRegistryLifecycleAndRestoration(t *testing.T) {
	t.Setenv("ARIADNE_PLUGIN_TEST_PROCESS", "1")
	root := filepath.Join(t.TempDir(), "plugins")
	m, engine, frontend := newTestManager(t, root, DefaultConfig())
	dir := testPackage(t, "test-plugin")
	result := manage(t, m, frontend, v1.ManageRequest{Action: "install", Directory: dir})
	p := result.Plugins[0]
	if p.Enabled || p.Running || len(p.Grants) != 0 {
		t.Fatal("new install must be disabled and unapproved")
	}
	grant := v1.Grant{Capability: v1.CoreRead, Scope: v1.Scope{Kind: "workspace", IDs: []uint64{1}}}
	manage(t, m, frontend, v1.ManageRequest{Action: "grant", ID: "test-plugin", Grant: &grant})
	result = manage(t, m, frontend, v1.ManageRequest{Action: "enable", ID: "test-plugin"})
	if !result.Plugins[0].Running {
		t.Fatalf("startup failed: %+v", result)
	}
	dataDir := filepath.Join(root, "data", "test-plugin")
	if err := os.WriteFile(filepath.Join(dataDir, "private.txt"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	descriptor := core.ToolDescriptor{Provider: "test-plugin", Type: "demo", Instance: "shared"}
	value, err := engine.Execute(context.Background(), core.CreatePaneCommand{WindowID: 1, Pane: core.PaneSpec{Kind: core.PaneTool, Tool: &core.ToolInstance{Descriptor: descriptor, StateVersion: 1, Generation: 1, State: json.RawMessage(`{"saved":true}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	pane := value.(core.CreatePaneResult).Pane
	result = manage(t, m, frontend, v1.ManageRequest{Action: "run", ID: "test-plugin", Command: "echo", Args: []string{"one", "two"}})
	if result.Command.Text != "one two" {
		t.Fatal(result)
	}
	if err := m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	restored, err := NewManager(context.Background(), engine, root, DefaultConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close(context.Background())
	p = restored.List().Plugins[0]
	if !p.Enabled || !p.Running || len(p.Grants) != 1 {
		t.Fatalf("restoration: %+v", p)
	}
	result = manage(t, restored, frontend, v1.ManageRequest{Action: "update", ID: "test-plugin", Directory: dir})
	if result.Plugins[0].Enabled || result.Plugins[0].Running || len(result.Plugins[0].Grants) != 1 {
		t.Fatal("update must disable, preserving grants")
	}
	manage(t, restored, frontend, v1.ManageRequest{Action: "uninstall", ID: "test-plugin"})
	if len(restored.List().Plugins) != 0 {
		t.Fatal("uninstall must unregister")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "private.txt")); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := engine.Snapshot(context.Background())
	if len(snapshot.Panes) != 1 || snapshot.Panes[0].ID != pane.ID || string(snapshot.ToolInstances[0].State) != `{"saved":true}` {
		t.Fatal("uninstall lost Tool data")
	}
	manage(t, restored, frontend, v1.ManageRequest{Action: "install", Directory: dir})
	if len(restored.List().Plugins[0].Grants) != 0 {
		t.Fatal("reinstall must require fresh grants")
	}
	manage(t, restored, frontend, v1.ManageRequest{Action: "uninstall", ID: "test-plugin"})
	manage(t, restored, frontend, v1.ManageRequest{Action: "uninstall", ID: "test-plugin", Purge: true})
	if _, err := os.Stat(dataDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("purge retained data")
	}
	snapshot, _ = engine.Snapshot(context.Background())
	if len(snapshot.Panes) != 0 || len(snapshot.ToolInstances) != 0 {
		t.Fatal("purge retained Tool data")
	}
}
func TestRegistryCorruptionAndSaveFailure(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "registry.json")
	original := []byte(`{"version":999,"plugins":[]}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	m, _, frontend := newTestManager(t, root, DefaultConfig())
	if m.List().RegistryError == "" {
		t.Fatal("registry corruption hidden")
	}
	if _, err := m.Manage(context.Background(), frontend, v1.ManageRequest{Action: "install", Directory: testPackage(t, "test-plugin")}); err == nil {
		t.Fatal("corrupt registry mutated")
	}
	data, _ := os.ReadFile(path)
	if !bytes.Equal(data, original) {
		t.Fatal("corrupt registry not preserved")
	}
	root2 := t.TempDir()
	m2, _, frontend2 := newTestManager(t, root2, DefaultConfig())
	if err := os.Mkdir(filepath.Join(root2, "registry.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m2.Manage(context.Background(), frontend2, v1.ManageRequest{Action: "install", Directory: testPackage(t, "test-plugin")}); err == nil {
		t.Fatal("save failure treated as success")
	}
	if len(m2.List().Plugins) != 0 {
		t.Fatal("failed install published")
	}
	packages, _ := os.ReadDir(filepath.Join(root2, "packages"))
	if len(packages) != 0 {
		t.Fatal("failed install package leaked")
	}
}
func TestRuntimeFailureIsolationAndManualRestart(t *testing.T) {
	t.Setenv("ARIADNE_PLUGIN_TEST_PROCESS", "1")
	c := DefaultConfig()
	c.CommandMS = 100
	c.ShutdownMS = 100
	m, engine, frontend := newTestManager(t, t.TempDir(), c)
	for _, id := range []string{"failing-plugin", "healthy-plugin"} {
		manage(t, m, frontend, v1.ManageRequest{Action: "install", Directory: testPackage(t, id)})
		manage(t, m, frontend, v1.ManageRequest{Action: "enable", ID: id})
	}
	for _, command := range []string{"hang", "crash", "malformed"} {
		_, err := m.Manage(context.Background(), frontend, v1.ManageRequest{Action: "run", ID: "failing-plugin", Command: command})
		if err == nil {
			t.Fatal("failure succeeded")
		}
		deadline := time.Now().Add(time.Second)
		for m.List().Plugins[0].Running && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		p := m.List().Plugins[0]
		if !p.Enabled || p.Running || p.Error == "" {
			t.Fatalf("failure changed saved enable or was hidden: %+v", p)
		}
		if _, err := engine.Snapshot(context.Background()); err != nil {
			t.Fatal(err)
		}
		result := manage(t, m, frontend, v1.ManageRequest{Action: "run", ID: "healthy-plugin", Command: "echo", Args: []string{"alive"}})
		if result.Command.Text != "alive" {
			t.Fatal(result)
		}
		manage(t, m, frontend, v1.ManageRequest{Action: "restart", ID: "failing-plugin"})
	}
	for i := 0; i < 3; i++ {
		_, err := m.Manage(context.Background(), frontend, v1.ManageRequest{Action: "run", ID: "failing-plugin", Command: "error"})
		if err == nil {
			t.Fatal("handler error hidden")
		}
		if i < 2 && !m.List().Plugins[0].Running {
			t.Fatal("stopped before three consecutive errors")
		}
	}
	if m.List().Plugins[0].Running {
		t.Fatal("three errors did not stop plugin")
	}
}
func TestFrameGenerationsAndWideCells(t *testing.T) {
	t.Setenv("ARIADNE_PLUGIN_TEST_PROCESS", "1")
	m, engine, frontend := newTestManager(t, t.TempDir(), DefaultConfig())
	manage(t, m, frontend, v1.ManageRequest{Action: "install", Directory: testPackage(t, "test-plugin")})
	manage(t, m, frontend, v1.ManageRequest{Action: "enable", ID: "test-plugin"})
	tool := core.ToolInstance{Descriptor: core.ToolDescriptor{Provider: "test-plugin", Type: "demo", Instance: "shared"}, StateVersion: 1, Generation: 1, State: json.RawMessage(`{}`)}
	value, err := engine.Execute(context.Background(), core.CreatePaneCommand{WindowID: 1, Pane: core.PaneSpec{Kind: core.PaneTool, Tool: &tool}})
	if err != nil {
		t.Fatal(err)
	}
	pane := value.(core.CreatePaneResult).Pane
	v := v1.View{ID: "view", Generation: 1, PaneID: uint64(pane.ID), Width: 4, Height: 2}
	manage(t, m, frontend, v1.ManageRequest{Action: "view.open", ID: "test-plugin", View: &v})
	r := manage(t, m, frontend, v1.ManageRequest{Action: "render", ID: "test-plugin", View: &v})
	if r.Frame.Cells[0].Width != 2 || r.Frame.Cells[1].Width != 0 {
		t.Fatal("wide cells not preserved")
	}
	v.Generation = 2
	v.Width = 8
	manage(t, m, frontend, v1.ManageRequest{Action: "render", ID: "test-plugin", View: &v})
	v.Generation = 1
	if _, err := m.Manage(context.Background(), frontend, v1.ManageRequest{Action: "render", ID: "test-plugin", View: &v}); err == nil {
		t.Fatal("old generation accepted")
	}
	_, sub, err := engine.Subscribe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	v.Width = 2
	v.Generation = 1
	manage(t, m, uint64(sub.ID()), v1.ManageRequest{Action: "view.open", ID: "test-plugin", View: &v})
	manage(t, m, uint64(sub.ID()), v1.ManageRequest{Action: "render", ID: "test-plugin", View: &v})
	v.Generation = 2
	v.Width = 8
	manage(t, m, frontend, v1.ManageRequest{Action: "view.close", ID: "test-plugin", View: &v})
	if _, err := m.Manage(context.Background(), frontend, v1.ManageRequest{Action: "render", ID: "test-plugin", View: &v}); err == nil {
		t.Fatal("closed view resurrected")
	}
	m.Detach(frontend)
	// Structural/control/wide-cell validation is independent of a terminal.
	frame := v1.Frame{ViewID: v.ID, Generation: v.Generation, Width: v.Width, Height: v.Height, Cells: make([]v1.Cell, v.Width*v.Height)}
	for i := range frame.Cells {
		frame.Cells[i] = v1.Cell{Text: " ", Width: 1}
	}
	frame.Cells[0].Text = "\x1b"
	if validFrame(frame, v) == nil {
		t.Fatal("escape in surface accepted")
	}
	frame.Cells[0] = v1.Cell{Text: "界", Width: 1}
	if validFrame(frame, v) == nil {
		t.Fatal("invalid wide cell accepted")
	}
}
func TestFramingBoundsAndPeerResponseWhileAPIBusy(t *testing.T) {
	for _, data := range []string{"Content-Length: 99999999\r\n\r\n", "Content-Length: 2\n\n{}", "Content-Length: 2\r\nContent-Length: 2\r\n\r\n{}", strings.Repeat("x", 9000), "Content-Length: 2\r\n\r\n{}"} {
		if _, err := ReadMessage(bufio.NewReaderSize(strings.NewReader(data), 8192), 8<<20); err == nil {
			t.Fatalf("invalid framing accepted: %.50s", data)
		}
	}
	host, remote := net.Pipe()
	defer remote.Close()
	c := DefaultConfig()
	entered := make(chan struct{})
	release := make(chan struct{})
	p := NewPeer(context.Background(), host, host, c, func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
		close(entered)
		select {
		case <-release:
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, nil)
	p.Start()
	defer p.Close()
	go func() {
		id := uint64(100)
		_ = WriteMessage(remote, Message{JSONRPC: "2.0", ID: &id, Method: "host.busy", Params: asJSON(nil)})
		r := bufio.NewReader(remote)
		request, _ := ReadMessage(r, 8<<20)
		_ = WriteMessage(remote, Message{JSONRPC: "2.0", ID: request.ID, Result: asJSON("ok")})
		_, _ = ReadMessage(r, 8<<20)
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var result string
	if err := p.Call(ctx, "command", nil, &result); err != nil || result != "ok" {
		t.Fatalf("response reader blocked by API callback: %s %v", result, err)
	}
	close(release)
}
func TestTransientEditorNotPersisted(t *testing.T) {
	_, engine, _ := newTestManager(t, t.TempDir(), DefaultConfig())
	before, _ := engine.Snapshot(context.Background())
	value, err := engine.Execute(context.Background(), core.CreateTransientPaneCommand{WindowID: 1, Launch: core.LaunchSpec{Argv: []string{"editor", "file"}, CWD: t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	pane := value.(core.CreatePaneResult).Pane
	snapshot, _ := engine.Snapshot(context.Background())
	if !pane.Transient || len(snapshot.StashedPanes) != 1 || snapshot.Windows[0].Layout != nil {
		t.Fatal("temporary editor changed layout")
	}
	data, err := statefile.Encode(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := statefile.Decode(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Panes) != len(before.Panes) || len(restored.StashedPanes) != 0 {
		t.Fatal("temporary editor persisted")
	}
}

type dialogueOperations struct {
	entered chan struct{}
	release chan struct{}
}

func (o *dialogueOperations) PluginOperation(ctx context.Context, _ string, _ json.RawMessage, _ v1.Context) (any, error) {
	select {
	case o.entered <- struct{}{}:
	default:
	}
	select {
	case <-o.release:
		return v1.InteractionResult{Text: "answered"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func TestDialogueTimeIsExcludedAndInvocationCancellationReachesAPI(t *testing.T) {
	t.Setenv("ARIADNE_PLUGIN_TEST_PROCESS", "1")
	c := DefaultConfig()
	c.CommandMS = 80
	m, _, frontend := newTestManager(t, t.TempDir(), c)
	operations := &dialogueOperations{entered: make(chan struct{}, 1), release: make(chan struct{})}
	m.operations = operations
	manage(t, m, frontend, v1.ManageRequest{Action: "install", Directory: testPackage(t, "test-plugin")})
	g := v1.Grant{Capability: v1.FrontendInteract, Scope: v1.Scope{Kind: "all"}}
	manage(t, m, frontend, v1.ManageRequest{Action: "grant", ID: "test-plugin", Grant: &g})
	manage(t, m, frontend, v1.ManageRequest{Action: "enable", ID: "test-plugin"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		result, err := m.Manage(ctx, frontend, v1.ManageRequest{Action: "run", ID: "test-plugin", Command: "interact"})
		if err == nil && result.Command.Text != "answered" {
			err = errors.New("incorrect dialogue result")
		}
		done <- err
	}()
	select {
	case <-operations.entered:
	case <-ctx.Done():
		t.Fatal("host dialogue did not start")
	}
	time.Sleep(200 * time.Millisecond)
	if !m.List().Plugins[0].Running {
		t.Fatal("dialogue time counted against command deadline")
	}
	close(operations.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("dialogue did not finish")
	}
	operations.release = make(chan struct{})
	callCtx, callCancel := context.WithCancel(ctx)
	go func() {
		_, err := m.Manage(callCtx, frontend, v1.ManageRequest{Action: "run", ID: "test-plugin", Command: "interact"})
		done <- err
	}()
	<-operations.entered
	callCancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("invocation did not cancel")
	}
	// The API wait is tied to the invocation, so a later command can run normally.
	result := manage(t, m, frontend, v1.ManageRequest{Action: "run", ID: "test-plugin", Command: "echo", Args: []string{"after-cancel"}})
	if result.Command.Text != "after-cancel" {
		t.Fatal(result)
	}
}

func TestToolInputStartsBoundedInteractionAndDetachCancelsIt(t *testing.T) {
	c := DefaultConfig()
	c.MaxInteractions = 1
	m, _, frontend := newTestManager(t, t.TempDir(), c)
	operations := &dialogueOperations{entered: make(chan struct{}, 1), release: make(chan struct{})}
	m.operations = operations

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s := &session{
		manager:    m,
		id:         "interaction-plugin",
		generation: 1,
		ctx:        ctx,
		apiCtx:     ctx,
		cancel:     cancel,
		apiCancel:  cancel,
		manifest: v1.Manifest{
			ID:           "interaction-plugin",
			Capabilities: []v1.Capability{v1.FrontendInteract, v1.FrontendEditor},
		},
		grants: []v1.Grant{{Capability: v1.FrontendInteract, Scope: v1.Scope{Kind: "all"}}},
	}
	s.active.Store(true)
	m.mu.Lock()
	m.sessions[s.id] = s
	m.mu.Unlock()
	t.Cleanup(func() {
		m.mu.Lock()
		delete(m.sessions, s.id)
		m.mu.Unlock()
	})

	viewKey := fmt.Sprintf("%d/%s", frontend, "test-view")
	input, err := m.captureInvocation(ctx, s, frontend, 0, invocationInput, viewKey)
	if err != nil {
		t.Fatal(err)
	}
	result, err := invoke(s, "frontend.interact", input.Token, v1.Interaction{Kind: "prompt", Message: "value"})
	if err != nil {
		t.Fatal(err)
	}
	deferred, ok := result.(deferredInteractionStart)
	started := deferred.InteractionStarted
	if !ok || started.ID == "" {
		t.Fatalf("unexpected asynchronous result: %#v", result)
	}
	deferred.rpcAfterResponse(nil)
	select {
	case <-operations.entered:
	case <-time.After(time.Second):
		t.Fatal("frontend interaction did not start")
	}

	second, err := m.captureInvocation(ctx, s, frontend, 0, invocationCommand, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(s, "frontend.interact", second.Token, v1.Interaction{Kind: "prompt"}); !errors.Is(err, ErrOverflow) {
		t.Fatal("shared command/ToolPane interaction limit not enforced", err)
	}
	if _, err := invoke(s, "frontend.interact", second.Token, v1.Interaction{Kind: "editor"}); !errors.Is(err, ErrPermission) {
		t.Fatal("editor accepted without frontend.editor grant", err)
	}

	render, err := m.captureInvocation(ctx, s, frontend, 0, invocationRender, viewKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := invoke(s, "frontend.interact", render.Token, v1.Interaction{Kind: "prompt"}); !errors.Is(err, ErrPermission) {
		t.Fatal("render callback started an interaction", err)
	}

	m.mu.Lock()
	m.views[viewKey] = viewRecord{hostID: "host-view", frontend: frontend, plugin: s.id, generation: 1}
	m.mu.Unlock()
	s.active.Store(false) // Synthetic session has no RPC peer for view.close notification.
	if _, err := m.view(ctx, frontend, v1.ManageRequest{Action: "view.close", ID: s.id, View: &v1.View{ID: "test-view", Generation: 1}}); err != nil {
		t.Fatal(err)
	}
	s.active.Store(true)
	assertNoInteractions := func(message string) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for {
			m.mu.Lock()
			remaining := len(m.interactions)
			m.mu.Unlock()
			if remaining == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal(message)
			}
			time.Sleep(time.Millisecond)
		}
	}
	assertNoInteractions("view close did not cancel interaction")

	s.grants = append(s.grants, v1.Grant{Capability: v1.FrontendEditor, Scope: v1.Scope{Kind: "all"}})
	third, err := m.captureInvocation(ctx, s, frontend, 0, invocationInput, "another-view")
	if err != nil {
		t.Fatal(err)
	}
	value, err := invoke(s, "frontend.interact", third.Token, v1.Interaction{Kind: "editor"})
	if err != nil {
		t.Fatal(err)
	}
	value.(deferredInteractionStart).rpcAfterResponse(nil)
	select {
	case <-operations.entered:
	case <-time.After(time.Second):
		t.Fatal("second frontend interaction did not start")
	}
	m.Detach(frontend)
	assertNoInteractions("detach did not cancel interaction")
}

func TestToolInputPublishesInteractionResult(t *testing.T) {
	m, _, frontend := newTestManager(t, t.TempDir(), DefaultConfig())
	operations := &dialogueOperations{entered: make(chan struct{}, 1), release: make(chan struct{})}
	m.operations = operations
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	host, remote := net.Pipe()
	peer := NewPeer(ctx, host, host, m.config, nil, nil)
	peer.Start()
	t.Cleanup(func() {
		peer.Close()
		_ = remote.Close()
	})
	s := &session{
		manager:    m,
		id:         "result-plugin",
		generation: 1,
		ctx:        ctx,
		apiCtx:     ctx,
		cancel:     cancel,
		apiCancel:  cancel,
		peer:       peer,
		manifest:   v1.Manifest{ID: "result-plugin", Capabilities: []v1.Capability{v1.FrontendInteract}},
		grants:     []v1.Grant{{Capability: v1.FrontendInteract, Scope: v1.Scope{Kind: "all"}}},
	}
	s.active.Store(true)
	m.mu.Lock()
	m.sessions[s.id] = s
	m.mu.Unlock()
	t.Cleanup(func() {
		m.mu.Lock()
		delete(m.sessions, s.id)
		m.mu.Unlock()
	})

	captured, err := m.captureInvocation(ctx, s, frontend, 0, invocationInput, "result-view")
	if err != nil {
		t.Fatal(err)
	}
	value, err := invoke(s, "frontend.interact", captured.Token, v1.Interaction{Kind: "prompt"})
	if err != nil {
		t.Fatal(err)
	}
	deferred := value.(deferredInteractionStart)
	started := deferred.InteractionStarted
	deferred.rpcAfterResponse(nil)
	<-operations.entered
	close(operations.release)
	if err := remote.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	message, err := ReadMessage(bufio.NewReader(remote), m.config.MessageBytes)
	if err != nil {
		t.Fatal(err)
	}
	if message.Method != "interaction.result" || message.ID != nil {
		t.Fatalf("unexpected completion notification: %#v", message)
	}
	var completed v1.InteractionCompleted
	if err := json.Unmarshal(message.Params, &completed); err != nil {
		t.Fatal(err)
	}
	if completed.ID != started.ID || completed.Result == nil || completed.Result.Text != "answered" || completed.Error != "" {
		t.Fatalf("unexpected completion: %#v", completed)
	}
}

func TestControlAndPTYOverflowStopOnlyTarget(t *testing.T) {
	// Oversized queued control payloads are rejected before a write can block.
	host, remote := net.Pipe()
	defer remote.Close()
	c := DefaultConfig()
	c.ControlBytes = 256
	p := NewPeer(context.Background(), host, host, c, nil, nil)
	p.Start()
	defer p.Close()
	if err := p.Notify("large", strings.Repeat("x", 512)); !errors.Is(err, ErrOverflow) {
		t.Fatal("control overflow accepted", err)
	}
	t.Setenv("ARIADNE_PLUGIN_TEST_PROCESS", "1")
	c = DefaultConfig()
	c.PTYQueueBytes = 256
	m, engine, frontend := newTestManager(t, t.TempDir(), c)
	value, err := engine.Execute(context.Background(), core.CreatePaneCommand{WindowID: 1, Pane: core.PaneSpec{Kind: core.PaneTool}})
	if err != nil {
		t.Fatal(err)
	}
	pane := value.(core.CreatePaneResult).Pane
	manage(t, m, frontend, v1.ManageRequest{Action: "install", Directory: testPackage(t, "test-plugin")})
	g := v1.Grant{Capability: v1.PTYObserve, Scope: v1.Scope{Kind: "all"}}
	manage(t, m, frontend, v1.ManageRequest{Action: "grant", ID: "test-plugin", Grant: &g})
	manage(t, m, frontend, v1.ManageRequest{Action: "enable", ID: "test-plugin"})
	m.PublishTerminalEvent(plugin.TerminalEvent{Kind: plugin.TerminalOutput, PaneID: pane.ID, TerminalID: 42, Data: make([]byte, 257)})
	deadline := time.Now().Add(time.Second)
	for m.List().Plugins[0].Running && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if m.List().Plugins[0].Running || !m.List().Plugins[0].Enabled {
		t.Fatal("PTY overflow did not stop only runtime")
	}
	if _, err := engine.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestQueuedInputCannotCrossRuntimeRestart(t *testing.T) {
	t.Setenv("ARIADNE_PLUGIN_TEST_PROCESS", "1")
	m, engine, frontend := newTestManager(t, t.TempDir(), DefaultConfig())
	manage(t, m, frontend, v1.ManageRequest{Action: "install", Directory: testPackage(t, "test-plugin")})
	manage(t, m, frontend, v1.ManageRequest{Action: "enable", ID: "test-plugin"})
	tool := core.ToolInstance{Descriptor: core.ToolDescriptor{Provider: "test-plugin", Type: "demo", Instance: "input"}, StateVersion: 1, Generation: 1, State: asJSON(map[string]any{})}
	result, err := engine.Execute(context.Background(), core.CreatePaneCommand{WindowID: 1, Pane: core.PaneSpec{Kind: core.PaneTool, Tool: &tool}})
	if err != nil {
		t.Fatal(err)
	}
	view := v1.View{ID: "input-view", Generation: 1, PaneID: uint64(result.(core.CreatePaneResult).Pane.ID), Width: 4, Height: 1}
	manage(t, m, frontend, v1.ManageRequest{Action: "view.open", ID: "test-plugin", View: &view})
	frame := manage(t, m, frontend, v1.ManageRequest{Action: "render", ID: "test-plugin", View: &view}).Frame
	view.RuntimeGeneration = frame.RuntimeGeneration
	old := v1.Input{View: view, Data: []byte("before restart")}
	manage(t, m, frontend, v1.ManageRequest{Action: "input", ID: "test-plugin", Input: &old})
	manage(t, m, frontend, v1.ManageRequest{Action: "restart", ID: "test-plugin"})
	if _, err := m.Manage(context.Background(), frontend, v1.ManageRequest{Action: "input", ID: "test-plugin", Input: &old}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("queued input crossed restart", err)
	}
	frame = manage(t, m, frontend, v1.ManageRequest{Action: "render", ID: "test-plugin", View: &view}).Frame
	if frame.RuntimeGeneration == view.RuntimeGeneration {
		t.Fatal("runtime generation did not advance")
	}
	view.RuntimeGeneration = frame.RuntimeGeneration
	fresh := v1.Input{View: view, Data: []byte("after restart")}
	manage(t, m, frontend, v1.ManageRequest{Action: "input", ID: "test-plugin", Input: &fresh})
}

func TestDialogueDoesNotBlockOtherFrontendHostAPIs(t *testing.T) {
	host, remote := net.Pipe()
	defer remote.Close()
	entered := make(chan struct{})
	release := make(chan struct{})
	peer := NewPeer(context.Background(), host, host, DefaultConfig(), func(ctx context.Context, method string, _ json.RawMessage) (any, error) {
		if method == "frontend.interact" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return "available", nil
	}, nil)
	peer.Start()
	defer peer.Close()
	id := uint64(1)
	if err := WriteMessage(remote, Message{JSONRPC: "2.0", ID: &id, Method: "frontend.interact", Params: asJSON(nil)}); err != nil {
		t.Fatal(err)
	}
	<-entered
	id = 2
	if err := WriteMessage(remote, Message{JSONRPC: "2.0", ID: &id, Method: "core.snapshot", Params: asJSON(nil)}); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(remote)
	done := make(chan Message, 1)
	go func() { m, _ := ReadMessage(reader, 8<<20); done <- m }()
	select {
	case response := <-done:
		if response.ID == nil || *response.ID != 2 {
			t.Fatal("unexpected response", response)
		}
	case <-time.After(time.Second):
		t.Fatal("dialogue blocked another frontend API")
	}
	close(release)
	if _, err := ReadMessage(reader, 8<<20); err != nil {
		t.Fatal(err)
	}
}

func TestDeferredRPCWorkStartsAfterResponseIsQueued(t *testing.T) {
	host, remote := net.Pipe()
	defer remote.Close()
	var peer *Peer
	peer = NewPeer(context.Background(), host, host, DefaultConfig(), func(context.Context, string, json.RawMessage) (any, error) {
		return orderedDeferredResult{peer: peer}, nil
	}, nil)
	peer.Start()
	defer peer.Close()

	id := uint64(1)
	if err := WriteMessage(remote, Message{JSONRPC: "2.0", ID: &id, Method: "start", Params: asJSON(nil)}); err != nil {
		t.Fatal(err)
	}
	if err := remote.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(remote)
	response, err := ReadMessage(reader, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if response.ID == nil || *response.ID != id || string(response.Result) != `"started"` {
		t.Fatalf("completion preceded start response: %#v", response)
	}
	notification, err := ReadMessage(reader, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	if notification.ID != nil || notification.Method != "completed" {
		t.Fatalf("unexpected deferred notification: %#v", notification)
	}
}
