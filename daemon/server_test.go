package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aruzen/ariadne/core"
	ariadneprotocol "github.com/aruzen/ariadne/protocol"
	"github.com/aruzen/ariadne/statefile"
	"github.com/aruzen/streammux"
	"github.com/aruzen/streammux/pty"
)

type testManagedProcess struct {
	reader *io.PipeReader
	writer *io.PipeWriter
	done   chan struct{}
	once   sync.Once
	mu     sync.Mutex
	status pty.ExitStatus
}

func newTestManagedProcess() *testManagedProcess {
	reader, writer := io.Pipe()
	return &testManagedProcess{reader: reader, writer: writer, done: make(chan struct{})}
}

func (process *testManagedProcess) Output() io.Reader     { return process.reader }
func (process *testManagedProcess) Input() io.Writer      { return io.Discard }
func (process *testManagedProcess) Resize(pty.Size) error { return nil }
func (process *testManagedProcess) Terminate() error {
	process.complete(pty.ExitStatus{Reason: pty.ExitReasonKilled})
	return nil
}
func (process *testManagedProcess) Kill() error {
	process.complete(pty.ExitStatus{Reason: pty.ExitReasonKilled})
	return nil
}
func (process *testManagedProcess) WaitStatus() (pty.ExitStatus, error) {
	<-process.done
	process.mu.Lock()
	defer process.mu.Unlock()
	return process.status, nil
}
func (process *testManagedProcess) Close() error {
	_ = process.reader.Close()
	_ = process.writer.Close()
	return nil
}
func (process *testManagedProcess) complete(status pty.ExitStatus) {
	process.once.Do(func() {
		process.mu.Lock()
		process.status = status
		process.mu.Unlock()
		_ = process.writer.Close()
		close(process.done)
	})
}

type testFactory struct {
	mu        sync.Mutex
	processes []*testManagedProcess
}

func (factory *testFactory) StartManaged(context.Context, pty.ProcessSpec) (pty.ManagedProcess, error) {
	factory.mu.Lock()
	defer factory.mu.Unlock()
	if len(factory.processes) == 0 {
		return nil, errors.New("no test process")
	}
	process := factory.processes[0]
	factory.processes = factory.processes[1:]
	return process, nil
}

func openTestServer(t *testing.T, factory pty.ManagedFactory) (*Server, string) {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "state.json")
	configuration := DefaultConfig(statePath)
	configuration.Manager.GracefulKillTimeout = 10 * time.Millisecond
	configuration.State.Debounce = 5 * time.Millisecond
	server, _, err := Open(context.Background(), factory, configuration)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = server.Close(context.Background()) })
	return server, statePath
}

func executeCore[T any](t *testing.T, server *Server, command core.Command) T {
	t.Helper()
	value, err := server.core.Execute(context.Background(), command)
	if err != nil {
		t.Fatalf("Core Execute(%T): %v", command, err)
	}
	result, ok := value.(T)
	if !ok {
		t.Fatalf("Core Execute(%T) result = %T", command, value)
	}
	return result
}

func serverSnapshot(t *testing.T, server *Server) core.Snapshot {
	t.Helper()
	snapshot, err := server.core.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Core Snapshot: %v", err)
	}
	return snapshot
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true")
		}
		time.Sleep(time.Millisecond)
	}
}

func addRunningPane(t *testing.T, server *Server, terminalID streammux.StreamID) core.Pane {
	t.Helper()
	return executeCore[core.CreatePaneResult](t, server, core.CreatePaneCommand{
		WindowID: 1,
		Pane: core.PaneSpec{Kind: core.PaneTerminal, Terminal: &core.TerminalInstance{
			ID: &terminalID, State: core.TerminalRunning,
			Launch: core.LaunchSpec{Argv: []string{"test-command"}, CWD: "/tmp"},
		}},
	}).Pane
}

func TestDefaultConfigUsesBoundedAriadnePTYPolicy(t *testing.T) {
	configuration := DefaultConfig("state.json")
	if configuration.Manager.SessionLifecycle.ExitPolicy != pty.ExitedSessionRetain ||
		configuration.Manager.SessionLifecycle.MaxRetainedSessions != 64 ||
		configuration.Manager.MaxAttachmentsPerSession != 1 {
		t.Fatalf("unexpected Manager policy: %+v", configuration.Manager)
	}
	types := configuration.PTYProtocol.Types
	if types.Open < 0x1100 || types.Error > 0x11ff || types.Attach == types.Input {
		t.Fatalf("PTY MessageTypes outside reserved range: %+v", types)
	}
}

func TestPTYAuthorizationRequiresAriadneOwnership(t *testing.T) {
	server, _ := openTestServer(t, &testFactory{})
	addRunningPane(t, server, 7)
	if err := server.authorizePTY(context.Background(), pty.AuthorizationRequest{Operation: pty.OperationAttach, SessionID: 7}); err != nil {
		t.Fatalf("owned Attach denied: %v", err)
	}
	if err := server.authorizePTY(context.Background(), pty.AuthorizationRequest{Operation: pty.OperationAttach, SessionID: 8}); !errors.Is(err, ErrTerminalNotOwned) {
		t.Fatalf("unowned Attach error = %v", err)
	}
	if err := server.authorizePTY(context.Background(), pty.AuthorizationRequest{Operation: pty.OperationOpen}); !errors.Is(err, ErrPTYOperationDenied) {
		t.Fatalf("generic Open error = %v", err)
	}
}

func TestManagerAbnormalExitIsRetainedThenRemovalClearsRuntimeID(t *testing.T) {
	process := newTestManagedProcess()
	server, _ := openTestServer(t, &testFactory{processes: []*testManagedProcess{process}})
	addRunningPane(t, server, 1)
	session, err := server.manager.Open(context.Background(), pty.ProcessSpec{
		Command: "test-command", Dir: "/tmp", InitialSize: pty.Size{Cols: 80, Rows: 24},
	})
	if err != nil {
		t.Fatalf("Manager Open: %v", err)
	}
	if session.ID() != 1 {
		t.Fatalf("Session ID = %d, want 1", session.ID())
	}
	process.complete(pty.ExitStatus{Reason: pty.ExitReasonExited, Code: 7})
	waitFor(t, func() bool {
		pane, exists := serverSnapshot(t, server).PaneByTerminalID(1)
		return exists && pane.Terminal.State == core.TerminalExited && pane.Terminal.Exit != nil && pane.Terminal.Exit.Code == 7
	})
	waitFor(t, func() bool {
		info, exists := server.manager.Get(1)
		return exists && info.Info().State == pty.SessionExited && server.manager.Stats().RetainedExitedSessions == 1
	})
	if err := server.manager.Remove(1); err != nil {
		t.Fatalf("Manager Remove: %v", err)
	}
	waitFor(t, func() bool {
		snapshot := serverSnapshot(t, server)
		return len(snapshot.Panes) == 1 && snapshot.Panes[0].Terminal.ID == nil && snapshot.Panes[0].Terminal.Exit.Code == 7
	})
}

func TestManagerSuccessfulExitRemovesPaneAndSession(t *testing.T) {
	process := newTestManagedProcess()
	server, _ := openTestServer(t, &testFactory{processes: []*testManagedProcess{process}})
	addRunningPane(t, server, 1)
	_, err := server.manager.Open(context.Background(), pty.ProcessSpec{
		Command: "test-command", Dir: "/tmp", InitialSize: pty.Size{Cols: 80, Rows: 24},
	})
	if err != nil {
		t.Fatalf("Manager Open: %v", err)
	}
	process.complete(pty.ExitStatus{Reason: pty.ExitReasonExited, Code: 0})
	waitFor(t, func() bool {
		return len(serverSnapshot(t, server).Panes) == 0 && len(server.manager.List()) == 0
	})
}

func TestTerminalOperationsNewListRestartKillAndDismiss(t *testing.T) {
	first := newTestManagedProcess()
	second := newTestManagedProcess()
	third := newTestManagedProcess()
	server, _ := openTestServer(t, &testFactory{processes: []*testManagedProcess{first, second, third}})
	params := ariadneprotocol.NewTerminalParams{
		Argv: []string{"test-command", "arg"}, CWD: "/tmp", Env: []string{"TEST=value"},
		InitialSize: pty.Size{Cols: 100, Rows: 30},
	}
	created, err := server.NewTerminal(context.Background(), params)
	if err != nil {
		t.Fatalf("NewTerminal: %v", err)
	}
	if created.Pane.Terminal == nil || created.Pane.Terminal.State != core.TerminalRunning || created.Pane.Terminal.ID == nil {
		t.Fatalf("created Pane = %+v", created.Pane)
	}
	firstID := *created.Pane.Terminal.ID
	listed, err := server.ListTerminals(context.Background())
	if err != nil || len(listed.Entries) != 1 || listed.Entries[0].Pane.ID != created.Pane.ID {
		t.Fatalf("ListTerminals = %+v, %v", listed, err)
	}
	first.complete(pty.ExitStatus{Reason: pty.ExitReasonExited, Code: 7})
	waitFor(t, func() bool {
		pane, exists := serverSnapshot(t, server).PaneByTerminalID(firstID)
		return exists && pane.Terminal.State == core.TerminalExited
	})
	restarted, err := server.RestartTerminal(context.Background(), ariadneprotocol.RestartTerminalParams{
		PaneID: created.Pane.ID, Env: []string{"TEST=new"}, InitialSize: pty.Size{Cols: 80, Rows: 24},
	})
	if err != nil {
		t.Fatalf("RestartTerminal: %v", err)
	}
	if restarted.Pane.Terminal.ID == nil || *restarted.Pane.Terminal.ID == firstID || restarted.Pane.Terminal.State != core.TerminalRunning {
		t.Fatalf("restarted Pane = %+v", restarted.Pane)
	}
	if _, err := server.KillTerminal(context.Background(), ariadneprotocol.PaneParams{PaneID: created.Pane.ID}); err != nil {
		t.Fatalf("KillTerminal: %v", err)
	}
	if len(serverSnapshot(t, server).Panes) != 0 || len(server.manager.List()) != 0 {
		t.Fatal("kill did not remove Pane and Session")
	}

	dismissed, err := server.NewTerminal(context.Background(), params)
	if err != nil {
		t.Fatalf("second NewTerminal: %v", err)
	}
	third.complete(pty.ExitStatus{Reason: pty.ExitReasonExited, Code: 9})
	waitFor(t, func() bool {
		pane, exists := serverSnapshot(t, server).PaneByTerminalID(*dismissed.Pane.Terminal.ID)
		return exists && pane.Terminal.State == core.TerminalExited
	})
	if _, err := server.DismissTerminal(context.Background(), ariadneprotocol.PaneParams{PaneID: dismissed.Pane.ID}); err != nil {
		t.Fatalf("DismissTerminal: %v", err)
	}
	if len(serverSnapshot(t, server).Panes) != 0 || len(server.manager.List()) != 0 {
		t.Fatal("dismiss did not remove Pane and retained Session")
	}
}

func TestDaemonStopRefusesActiveTerminalWithoutForce(t *testing.T) {
	process := newTestManagedProcess()
	server, _ := openTestServer(t, &testFactory{processes: []*testManagedProcess{process}})
	created, err := server.NewTerminal(context.Background(), ariadneprotocol.NewTerminalParams{
		Argv: []string{"test-command"}, CWD: "/tmp", InitialSize: pty.Size{Cols: 80, Rows: 24},
	})
	if err != nil {
		t.Fatalf("NewTerminal: %v", err)
	}
	if _, err := server.DaemonStop(context.Background(), ariadneprotocol.DaemonStopParams{}); !errors.Is(err, core.ErrInvalidState) {
		t.Fatalf("DaemonStop without force error = %v", err)
	}
	server.mu.Lock()
	stopping := server.stopping
	server.mu.Unlock()
	if stopping {
		t.Fatal("refused stop changed daemon state")
	}
	if _, err := server.KillTerminal(context.Background(), ariadneprotocol.PaneParams{PaneID: created.Pane.ID}); err != nil {
		t.Fatalf("KillTerminal: %v", err)
	}
	status, err := server.DaemonStop(context.Background(), ariadneprotocol.DaemonStopParams{})
	if err != nil || !status.Stopping {
		t.Fatalf("DaemonStop = %+v, %v", status, err)
	}
	server.AfterResponse(ariadneprotocol.OperationDaemonStop)
	server.mu.Lock()
	stopping = server.stopping
	server.mu.Unlock()
	if !stopping {
		t.Fatal("accepted stop did not change daemon state")
	}
}

func TestNewTerminalStartFailureLeavesRestartablePane(t *testing.T) {
	server, _ := openTestServer(t, &testFactory{})
	_, err := server.NewTerminal(context.Background(), ariadneprotocol.NewTerminalParams{
		Argv: []string{"missing-command"}, CWD: "/tmp", Env: []string{"SECRET=not-persisted"},
		InitialSize: pty.Size{Cols: 80, Rows: 24},
	})
	if err == nil {
		t.Fatal("NewTerminal unexpectedly succeeded")
	}
	snapshot := serverSnapshot(t, server)
	if len(snapshot.Panes) != 1 || snapshot.Panes[0].Terminal == nil {
		t.Fatalf("failed Pane missing: %+v", snapshot.Panes)
	}
	terminal := snapshot.Panes[0].Terminal
	if terminal.State != core.TerminalFailed || terminal.ID != nil || terminal.Exit == nil ||
		terminal.Exit.Kind != core.TerminalExitPTYError || terminal.Exit.Message != terminalStartFailure {
		t.Fatalf("failed Terminal = %+v", terminal)
	}
}

func TestNewTerminalRejectsTargetOutsideDestinationWindow(t *testing.T) {
	server, _ := openTestServer(t, &testFactory{})
	executeCore[core.CreateWindowResult](t, server, core.CreateWindowCommand{WorkspaceID: 1, Name: "other"})
	first := executeCore[core.CreatePaneResult](t, server, core.CreatePaneCommand{
		WindowID: 1, Pane: core.PaneSpec{Kind: core.PaneFixed},
	})
	executeCore[core.CreatePaneResult](t, server, core.CreatePaneCommand{
		WindowID: 2, Pane: core.PaneSpec{Kind: core.PaneFixed},
	})
	if _, err := server.NewTerminal(context.Background(), ariadneprotocol.NewTerminalParams{
		WindowID: 2, TargetPaneID: first.Pane.ID, Direction: core.SplitVertical,
		Argv: []string{"test-command"}, CWD: "/tmp", InitialSize: pty.Size{Cols: 80, Rows: 24},
	}); !errors.Is(err, core.ErrInvalidArgument) {
		t.Fatalf("outside target error = %v", err)
	}
}

func TestTerminalStartFailureRemainsRestartableWithoutEnvironment(t *testing.T) {
	server, _ := openTestServer(t, &testFactory{})
	_, err := server.NewTerminal(context.Background(), ariadneprotocol.NewTerminalParams{
		Argv: []string{"secret-command"}, CWD: "/tmp", Env: []string{"SECRET=value"},
		InitialSize: pty.Size{Cols: 80, Rows: 24},
	})
	if err == nil {
		t.Fatal("NewTerminal unexpectedly succeeded")
	}
	snapshot := serverSnapshot(t, server)
	if len(snapshot.Panes) != 1 || snapshot.Panes[0].Terminal.State != core.TerminalFailed ||
		snapshot.Panes[0].Terminal.Exit == nil || snapshot.Panes[0].Terminal.Exit.Message != terminalStartFailure {
		t.Fatalf("failed Pane = %+v", snapshot.Panes)
	}
	data, marshalErr := json.Marshal(snapshot)
	if marshalErr != nil {
		t.Fatalf("Marshal Snapshot: %v", marshalErr)
	}
	if bytes.Contains(data, []byte("SECRET=value")) {
		t.Fatal("environment leaked into Core snapshot")
	}
}

func TestClosePersistsRunningTerminalAsPlaceholder(t *testing.T) {
	server, statePath := openTestServer(t, &testFactory{})
	addRunningPane(t, server, 99)
	if err := server.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	loaded, err := statefile.Load(statePath, statefile.DefaultOptions())
	if err != nil {
		t.Fatalf("Load persisted state: %v", err)
	}
	if len(loaded.Snapshot.Panes) != 1 || loaded.Snapshot.Panes[0].Terminal == nil || loaded.Snapshot.Panes[0].Terminal.State != core.TerminalPlaceholder {
		t.Fatalf("running Terminal not persisted as placeholder: %+v", loaded.Snapshot)
	}
}

func TestCloseWaitsForInFlightCommandAndRejectsNewCommands(t *testing.T) {
	server, _ := openTestServer(t, &testFactory{})
	release, err := server.guardCommand()
	if err != nil {
		t.Fatalf("guardCommand: %v", err)
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- server.Close(context.Background()) }()
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned before command release: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	release()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not finish after command release")
	}
	if _, err := server.guardCommand(); !errors.Is(err, core.ErrInvalidState) {
		t.Fatalf("guard after Close error = %v", err)
	}
}

type pipeListener struct {
	connection net.Conn
	once       sync.Once
	closed     chan struct{}
}

func newPipeListener(connection net.Conn) *pipeListener {
	return &pipeListener{connection: connection, closed: make(chan struct{})}
}

func (listener *pipeListener) Accept() (net.Conn, error) {
	var connection net.Conn
	listener.once.Do(func() { connection = listener.connection })
	if connection != nil {
		return connection, nil
	}
	<-listener.closed
	return nil, net.ErrClosed
}
func (listener *pipeListener) Close() error {
	select {
	case <-listener.closed:
	default:
		close(listener.closed)
	}
	return nil
}
func (listener *pipeListener) Addr() net.Addr { return pipeAddress("pipe") }

type pipeAddress string

func (address pipeAddress) Network() string { return string(address) }
func (address pipeAddress) String() string  { return string(address) }

func TestServerRegistersAriadneAndRestrictedPTYProtocols(t *testing.T) {
	server, _ := openTestServer(t, &testFactory{})
	clientStream, serverStream := net.Pipe()
	listener := newPipeListener(serverStream)
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	clientConn, err := streammux.Open(context.Background(), clientStream, streammux.DefaultConfig())
	if err != nil {
		t.Fatalf("client Conn: %v", err)
	}
	clientPeer, err := streammux.NewPeer(clientConn, streammux.DefaultPeerConfig())
	if err != nil {
		t.Fatalf("client Peer: %v", err)
	}
	clientServe := make(chan error, 1)
	go func() { clientServe <- clientPeer.Serve(context.Background()) }()

	syncPayload, err := json.Marshal(ariadneprotocol.Request{Version: ariadneprotocol.Version, Operation: ariadneprotocol.OperationSync})
	if err != nil {
		t.Fatalf("marshal sync: %v", err)
	}
	syncFrame, err := streammux.NewFrame(streammux.Header{
		Version: 1, MessageType: ariadneprotocol.MessageCommand, Flags: streammux.FlagRequest, CorrelationID: 1,
	}, syncPayload)
	if err != nil {
		t.Fatalf("new sync frame: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	responseFrame, err := clientPeer.Call(ctx, syncFrame)
	if err != nil {
		t.Fatalf("sync Call: %v", err)
	}
	if _, err := ariadneprotocol.DecodeResponse(responseFrame.Payload); err != nil {
		t.Fatalf("sync response: %v", err)
	}

	openPayload, err := pty.EncodeControl(pty.OpenRequest{
		Version: 1, Command: "forbidden", InitialSize: pty.Size{Cols: 80, Rows: 24},
	})
	if err != nil {
		t.Fatalf("encode PTY Open: %v", err)
	}
	openFrame, err := streammux.NewFrame(streammux.Header{
		Version: 1, MessageType: DefaultPTYMessageTypes().Open, Flags: streammux.FlagRequest, CorrelationID: 1,
	}, openPayload)
	if err != nil {
		t.Fatalf("new PTY Open frame: %v", err)
	}
	responseFrame, err = clientPeer.Call(ctx, openFrame)
	if err != nil {
		t.Fatalf("PTY Open Call: %v", err)
	}
	_, err = pty.DecodeProtocolResponse(responseFrame.Payload)
	if !errors.Is(err, &pty.RemoteError{Code: pty.CodePermissionDenied}) {
		t.Fatalf("PTY Open response error = %v", err)
	}

	if err := clientPeer.Close(); err != nil {
		t.Fatalf("client Close: %v", err)
	}
	if err := server.Close(context.Background()); err != nil {
		t.Fatalf("server Close: %v", err)
	}
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not return")
	}
}
