package client

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/protocol"
	"github.com/aruzen/streammux"
)

func TestFrontendControlRequestWaitsForHandler(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	serverConnection, clientConnection := net.Pipe()
	connection, err := streammux.Open(ctx, serverConnection, streammux.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	peer, err := streammux.NewPeer(connection, streammux.DefaultPeerConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	go func() { _ = peer.Serve(ctx) }()
	frontend, err := Open(ctx, clientConnection, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer frontend.Close()
	handled := make(chan protocol.FrontendControlRequest, 1)
	frontend.SetFrontendControlHandler(func(_ context.Context, request protocol.FrontendControlRequest) error {
		handled <- request
		return nil
	})
	request := protocol.FrontendControlRequest{Version: protocol.Version, Action: protocol.FrontendNavigate, PaneID: core.PaneID(12), MinimumRevision: 9}
	payload, _ := json.Marshal(request)
	frame, err := streammux.NewFrame(streammux.Header{Version: protocol.Version, MessageType: protocol.MessageFrontendControl, Flags: streammux.FlagRequest, CorrelationID: 1}, payload)
	if err != nil {
		t.Fatal(err)
	}
	responseFrame, err := peer.Call(ctx, frame)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := protocol.DecodeFrontendControlResponse(responseFrame.Payload); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-handled:
		if got != request {
			t.Fatalf("request = %+v", got)
		}
	case <-ctx.Done():
		t.Fatal("handler was not called")
	}
}
