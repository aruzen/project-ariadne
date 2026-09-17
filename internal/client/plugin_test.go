package client

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/protocol"
	"github.com/aruzen/streammux"
)

type testPluginController struct {
	entered   chan struct{}
	cancelled chan struct{}
}

func (p *testPluginController) Manage(ctx context.Context, _ uint64, r v1.ManageRequest) (v1.ManageResult, error) {
	if r.Action == "run" {
		close(p.entered)
		<-ctx.Done()
		close(p.cancelled)
		return v1.ManageResult{}, ctx.Err()
	}
	return v1.ManageResult{}, nil
}
func (*testPluginController) Detach(uint64) {}
func TestPluginCommandDoesNotBlockCoreAndCanCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	serverConnection, clientConnection := net.Pipe()
	engine, err := core.New(core.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	conn, err := streammux.Open(ctx, serverConnection, streammux.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	peer, err := streammux.NewPeer(conn, streammux.DefaultPeerConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	controller := &testPluginController{entered: make(chan struct{}), cancelled: make(chan struct{})}
	c := protocol.DefaultConfig()
	c.Plugins = controller
	serverProtocol, err := protocol.Register(peer, engine, c)
	if err != nil {
		t.Fatal(err)
	}
	defer serverProtocol.Close()
	go func() { _ = peer.Serve(ctx) }()
	frontend, err := Open(ctx, clientConnection, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer frontend.Close()
	if _, err := frontend.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	commandCtx, commandCancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, err := frontend.Plugin(commandCtx, v1.ManageRequest{Action: "run", ID: "example", Command: "wait"})
		done <- err
	}()
	select {
	case <-controller.entered:
	case <-ctx.Done():
		t.Fatal("command did not start")
	}
	if _, err := Call[core.CreateWorkspaceResult](ctx, frontend, protocol.OperationCreateWorkspace, protocol.CreateWorkspaceParams{Name: "during-command"}); err != nil {
		t.Fatal("Core request blocked by plugin command", err)
	}
	select {
	case <-frontend.Events():
	case <-ctx.Done():
		t.Fatal("Core event blocked")
	}
	commandCancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("client cancellation blocked")
	}
	select {
	case <-controller.cancelled:
	case <-ctx.Done():
		t.Fatal("host invocation was not cancelled")
	}
}
func TestPluginDialogueDoesNotBlockEventsAndHeadlessIsExplicit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	serverConnection, clientConnection := net.Pipe()
	engine, err := core.New(core.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	conn, err := streammux.Open(ctx, serverConnection, streammux.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	peer, err := streammux.NewPeer(conn, streammux.DefaultPeerConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	serverProtocol, err := protocol.Register(peer, engine, protocol.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer serverProtocol.Close()
	go func() { _ = peer.Serve(ctx) }()
	frontend, err := Open(ctx, clientConnection, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer frontend.Close()
	if _, err := frontend.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	dialogue := func() (v1.InteractionResponse, error) {
		payload, _ := json.Marshal(v1.InteractionRequest{ID: "dialogue", Interaction: v1.Interaction{Kind: "prompt"}})
		frame, _ := streammux.NewFrame(streammux.Header{Version: 1, MessageType: protocol.MessagePluginInteraction, Flags: streammux.FlagRequest, CorrelationID: 1}, payload)
		response, err := peer.Call(ctx, frame)
		var result v1.InteractionResponse
		if err == nil {
			err = json.Unmarshal(response.Payload, &result)
		}
		return result, err
	}
	result, err := dialogue()
	if err != nil || result.Error == "" {
		t.Fatal("headless request did not return an explicit error", result, err)
	}
	entered := make(chan struct{})
	frontend.SetPluginInteractionHandler(func(ctx context.Context, _ v1.InteractionRequest) (v1.InteractionResult, error) {
		close(entered)
		<-ctx.Done()
		return v1.InteractionResult{}, ctx.Err()
	})
	done := make(chan error, 1)
	go func() { _, err := dialogue(); done <- err }()
	<-entered
	if _, err := Call[core.CreateWorkspaceResult](ctx, frontend, protocol.OperationCreateWorkspace, protocol.CreateWorkspaceParams{Name: "during-dialogue"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-frontend.Events():
	case <-ctx.Done():
		t.Fatal("dialogue blocked Core event delivery")
	}
	data, _ := json.Marshal(map[string]string{"id": "dialogue"})
	frame, _ := streammux.NewFrame(streammux.Header{Version: 1, MessageType: protocol.MessagePluginInteractionCancel, Flags: streammux.FlagEvent}, data)
	if err := peer.Emit(ctx, frame); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("dialogue cancellation blocked")
	}
	frontend.SetPluginInteractionHandler(func(context.Context, v1.InteractionRequest) (v1.InteractionResult, error) {
		return v1.InteractionResult{Text: strings.Repeat("x", 2*peer.MaxFrameBytes())}, nil
	})
	if result, err := dialogue(); err != nil || result.Error == "" {
		t.Fatal("oversize dialogue result", err, result.Error)
	}
	if _, err := Call[core.CreateWorkspaceResult](ctx, frontend, protocol.OperationCreateWorkspace, protocol.CreateWorkspaceParams{Name: "after-oversize"}); err != nil {
		t.Fatal("oversize dialogue disconnected frontend", err)
	}
}
