package client

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/protocol"
	"github.com/aruzen/streammux"
)

func TestSyncCallAndRemoteError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	serverConnection, clientConnection := net.Pipe()
	engine, err := core.New(core.DefaultConfig())
	if err != nil {
		t.Fatalf("Core New: %v", err)
	}
	defer engine.Close()
	muxConnection, err := streammux.Open(ctx, serverConnection, streammux.DefaultConfig())
	if err != nil {
		t.Fatalf("streammux Open: %v", err)
	}
	serverPeer, err := streammux.NewPeer(muxConnection, streammux.DefaultPeerConfig())
	if err != nil {
		t.Fatalf("NewPeer: %v", err)
	}
	serverProtocol, err := protocol.Register(serverPeer, engine, protocol.DefaultConfig())
	if err != nil {
		t.Fatalf("protocol Register: %v", err)
	}
	defer serverProtocol.Close()
	go func() { _ = serverPeer.Serve(ctx) }()

	client, err := Open(ctx, clientConnection, DefaultConfig())
	if err != nil {
		t.Fatalf("client Open: %v", err)
	}
	defer client.Close()
	synchronized, err := client.Sync(ctx)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if synchronized.Snapshot.Revision != 0 {
		t.Fatalf("initial revision = %d", synchronized.Snapshot.Revision)
	}
	created, err := Call[core.CreateWorkspaceResult](ctx, client, protocol.OperationCreateWorkspace, protocol.CreateWorkspaceParams{Name: "work"})
	if err != nil || created.Workspace.Name != "work" {
		t.Fatalf("CreateWorkspace = %+v, %v", created, err)
	}
	select {
	case event := <-client.Events():
		if event.Kind != core.EventWorkspaceCreated || event.Revision != 1 {
			t.Fatalf("event = %+v", event)
		}
	case <-ctx.Done():
		t.Fatal("Core event was not delivered")
	}
	_, err = Call[core.CreateWorkspaceResult](ctx, client, protocol.OperationCreateWorkspace, protocol.CreateWorkspaceParams{Name: "work"})
	var remote *protocol.RemoteError
	if !errors.As(err, &remote) || remote.Code != protocol.CodeAlreadyExists {
		t.Fatalf("duplicate error = %v", err)
	}
}
