package client

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/aruzen/ariadne/internal/protocol"
	"github.com/aruzen/streammux"
)

type FrontendControlHandler func(context.Context, protocol.FrontendControlRequest) error

func (client *Client) SetFrontendControlHandler(handler FrontendControlHandler) {
	client.frontendMu.Lock()
	client.frontendControl = handler
	client.frontendMu.Unlock()
}

func (client *Client) handleFrontendControl(ctx context.Context, _ *streammux.Peer, frame streammux.Frame) error {
	if frame.Header.Flags != streammux.FlagRequest || frame.Header.StreamID != 0 {
		return errors.New("client: invalid frontend control frame")
	}
	request, err := protocol.DecodeFrontendControlRequest(frame.Payload)
	if err != nil {
		return err
	}
	client.frontendMu.Lock()
	handler := client.frontendControl
	client.frontendMu.Unlock()
	go func() {
		response := protocol.FrontendControlResponse{Version: protocol.Version}
		if handler == nil {
			response.Error = &protocol.RemoteError{Code: protocol.CodeInvalidState, Message: "frontend control is unsupported"}
		} else if err := handler(ctx, request); err != nil {
			var remote *protocol.RemoteError
			if errors.As(err, &remote) {
				response.Error = remote
			} else {
				response.Error = &protocol.RemoteError{Code: protocol.CodeInvalidState, Message: err.Error()}
			}
		}
		payload, err := json.Marshal(response)
		if err != nil {
			return
		}
		result, err := streammux.NewFrame(streammux.Header{Version: protocol.Version, MessageType: protocol.MessageFrontendControl, Flags: streammux.FlagResponse, CorrelationID: frame.Header.CorrelationID}, payload)
		if err == nil {
			_ = client.peer.Respond(ctx, frame, result)
		}
	}()
	return nil
}
