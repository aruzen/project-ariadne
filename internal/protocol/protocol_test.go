package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/streammux"
)

type peerPair struct {
	client         *streammux.Peer
	server         *streammux.Peer
	serverProtocol *Protocol
	events         chan EventEnvelope
	serveErrors    chan error
}

func newPeerPair(t *testing.T, engine *core.Core) *peerPair {
	t.Helper()
	clientStream, serverStream := net.Pipe()
	clientConn, err := streammux.Open(context.Background(), clientStream, streammux.DefaultConfig())
	if err != nil {
		t.Fatalf("open client Conn: %v", err)
	}
	serverConn, err := streammux.Open(context.Background(), serverStream, streammux.DefaultConfig())
	if err != nil {
		t.Fatalf("open server Conn: %v", err)
	}
	client, err := streammux.NewPeer(clientConn, streammux.DefaultPeerConfig())
	if err != nil {
		t.Fatalf("new client Peer: %v", err)
	}
	server, err := streammux.NewPeer(serverConn, streammux.DefaultPeerConfig())
	if err != nil {
		t.Fatalf("new server Peer: %v", err)
	}
	serverProtocol, err := Register(server, engine, DefaultConfig())
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	pair := &peerPair{
		client: client, server: server, serverProtocol: serverProtocol,
		events: make(chan EventEnvelope, 16), serveErrors: make(chan error, 2),
	}
	if err := client.Register(MessageEvent, func(_ context.Context, _ *streammux.Peer, frame streammux.Frame) error {
		var envelope EventEnvelope
		if err := decodeStrict(frame.Payload, &envelope); err != nil {
			return err
		}
		pair.events <- envelope
		return nil
	}); err != nil {
		t.Fatalf("register client event handler: %v", err)
	}
	go func() { pair.serveErrors <- client.Serve(context.Background()) }()
	go func() { pair.serveErrors <- server.Serve(context.Background()) }()
	t.Cleanup(func() {
		_ = pair.serverProtocol.Close()
		_ = pair.client.Close()
		_ = pair.server.Close()
	})
	return pair
}

func call(t *testing.T, peer *streammux.Peer, operation Operation, params any) (Response, error) {
	t.Helper()
	var raw json.RawMessage
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal params: %v", err)
		}
		raw = data
	}
	payload, err := json.Marshal(Request{Version: Version, Operation: operation, Params: raw})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	requestFrame, err := streammux.NewFrame(streammux.Header{
		Version: Version, MessageType: MessageCommand, Flags: streammux.FlagRequest, CorrelationID: 1,
	}, payload)
	if err != nil {
		t.Fatalf("NewFrame: %v", err)
	}
	frame, err := peer.Call(ctx, requestFrame)
	if err != nil {
		return Response{}, err
	}
	return DecodeResponse(frame.Payload)
}

func newProtocolCore(t *testing.T) *core.Core {
	t.Helper()
	engine, err := core.New(core.Config{EventQueueCapacity: 16})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine
}

func TestSyncThenCommandReturnsResponseAndEvent(t *testing.T) {
	engine := newProtocolCore(t)
	pair := newPeerPair(t, engine)
	response, err := call(t, pair.client, OperationSync, nil)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	var synchronized SyncResult
	if err := json.Unmarshal(response.Result, &synchronized); err != nil {
		t.Fatalf("decode sync result: %v", err)
	}
	if synchronized.Snapshot.Revision != 0 || len(synchronized.Snapshot.Workspaces) != 1 {
		t.Fatalf("unexpected sync snapshot: %+v", synchronized.Snapshot)
	}

	response, err = call(t, pair.client, OperationCreateWorkspace, map[string]any{"name": "new"})
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	var created core.CreateWorkspaceResult
	if err := json.Unmarshal(response.Result, &created); err != nil {
		t.Fatalf("decode create result: %v", err)
	}
	if created.Workspace.Name != "new" || created.Workspace.ID != 2 {
		t.Fatalf("unexpected created workspace: %+v", created)
	}
	select {
	case envelope := <-pair.events:
		if envelope.Version != Version || envelope.Event.Revision != 1 || envelope.Event.Kind != core.EventWorkspaceCreated {
			t.Fatalf("unexpected event: %+v", envelope)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Core Event")
	}
}

func TestCommandRequiresSync(t *testing.T) {
	pair := newPeerPair(t, newProtocolCore(t))
	_, err := call(t, pair.client, OperationCreateWorkspace, map[string]any{"name": "new"})
	if !errors.Is(err, &RemoteError{Code: CodeInvalidState}) {
		t.Fatalf("command before sync error = %v", err)
	}
}

func TestDuplicateSyncAndStrictParamsAreRejected(t *testing.T) {
	pair := newPeerPair(t, newProtocolCore(t))
	if _, err := call(t, pair.client, OperationSync, nil); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if _, err := call(t, pair.client, OperationSync, nil); !errors.Is(err, &RemoteError{Code: CodeInvalidState}) {
		t.Fatalf("duplicate sync error = %v", err)
	}
	_, err := call(t, pair.client, OperationCreateWorkspace, map[string]any{"name": "new", "unknown": true})
	if !errors.Is(err, &RemoteError{Code: CodeInvalidArgument}) {
		t.Fatalf("unknown param error = %v", err)
	}
}

func TestUnknownOperationAndCoreErrorsUseStableCodes(t *testing.T) {
	pair := newPeerPair(t, newProtocolCore(t))
	if _, err := call(t, pair.client, OperationSync, nil); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if _, err := call(t, pair.client, Operation("unknown"), map[string]any{}); !errors.Is(err, &RemoteError{Code: CodeInvalidArgument}) {
		t.Fatalf("unknown operation error = %v", err)
	}
	if _, err := call(t, pair.client, OperationCreateWindow, map[string]any{"workspace_id": 999, "name": "missing"}); !errors.Is(err, &RemoteError{Code: CodeNotFound}) {
		t.Fatalf("not found error = %v", err)
	}
	if _, err := call(t, pair.client, OperationCreateWorkspace, map[string]any{"name": "default"}); !errors.Is(err, &RemoteError{Code: CodeAlreadyExists}) {
		t.Fatalf("already exists error = %v", err)
	}
}

func TestCreatePaneCommandPreservesPresentation(t *testing.T) {
	params, err := json.Marshal(CreatePaneParams{
		WindowID: 1, Kind: core.PaneFixed,
		Presentation: core.PanePresentation{Chrome: core.PaneChromeNone},
	})
	if err != nil {
		t.Fatal(err)
	}
	command, err := commandFromRequest(Request{
		Version: Version, Operation: OperationCreatePane, Params: params,
	}, 1)
	if err != nil {
		t.Fatalf("commandFromRequest: %v", err)
	}
	created, ok := command.(core.CreatePaneCommand)
	if !ok || created.Pane.Presentation.Chrome != core.PaneChromeNone {
		t.Fatalf("command = %#v", command)
	}
}

func TestMalformedCommandDoesNotClosePeer(t *testing.T) {
	pair := newPeerPair(t, newProtocolCore(t))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	payload := []byte(`{"version":1,"operation":"sync","unknown":true}`)
	requestFrame, err := streammux.NewFrame(streammux.Header{
		Version: Version, MessageType: MessageCommand, Flags: streammux.FlagRequest, CorrelationID: 1,
	}, payload)
	if err != nil {
		t.Fatalf("NewFrame: %v", err)
	}
	frame, err := pair.client.Call(ctx, requestFrame)
	if err != nil {
		t.Fatalf("malformed Call: %v", err)
	}
	if _, err := DecodeResponse(frame.Payload); !errors.Is(err, &RemoteError{Code: CodeInvalidArgument}) {
		t.Fatalf("malformed response error = %v", err)
	}
	if _, err := call(t, pair.client, OperationSync, nil); err != nil {
		t.Fatalf("peer unusable after malformed command: %v", err)
	}
}

func TestDecodeResponseValidation(t *testing.T) {
	if _, err := DecodeResponse([]byte(`{"version":2,"result":{}}`)); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("version error = %v", err)
	}
	if _, err := DecodeResponse([]byte(`{"version":1}`)); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("missing result error = %v", err)
	}
	if _, err := DecodeResponse([]byte(`{"version":1,"result":{},"unknown":1}`)); !errors.Is(err, ErrInvalidPayload) {
		t.Fatalf("unknown field error = %v", err)
	}
}
