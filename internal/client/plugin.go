package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/aruzen/ariadne/internal/protocol"
	"github.com/aruzen/streammux"
)

type pluginDialogueCancel struct{ cancel context.CancelFunc }
type InteractionHandler func(context.Context, v1.InteractionRequest) (v1.InteractionResult, error)

func (client *Client) SetPluginInteractionHandler(handler InteractionHandler) {
	client.pluginMu.Lock()
	client.pluginInteraction = handler
	client.pluginMu.Unlock()
}
func (client *Client) handlePluginInteraction(ctx context.Context, _ *streammux.Peer, request streammux.Frame) error {
	if request.Header.Flags != streammux.FlagRequest || request.Header.StreamID != 0 {
		return errors.New("client: invalid plugin interaction frame")
	}
	var params v1.InteractionRequest
	if err := json.Unmarshal(request.Payload, &params); err != nil {
		return err
	}
	client.pluginMu.Lock()
	if len(client.pluginDialogues) >= 16 {
		client.pluginMu.Unlock()
		return errors.New("client: plugin dialogue queue overflow")
	}
	dialogCtx, cancel := context.WithCancel(ctx)
	record := &pluginDialogueCancel{cancel: cancel}
	client.pluginDialogues[params.ID] = record
	handler := client.pluginInteraction
	client.pluginMu.Unlock()
	go func() {
		defer func() {
			cancel()
			client.pluginMu.Lock()
			if client.pluginDialogues[params.ID] == record {
				delete(client.pluginDialogues, params.ID)
			}
			client.pluginMu.Unlock()
		}()
		response := v1.InteractionResponse{}
		var err error
		if handler == nil {
			err = errors.New("plugin: frontend is headless; dialogue requires an interactive terminal")
		} else {
			response.Result, err = handler(dialogCtx, params)
		}
		if err != nil {
			response.Error = err.Error()
		}
		payload, err := json.Marshal(response)
		if err != nil {
			return
		}
		if len(payload)+64 > client.peer.MaxFrameBytes() {
			payload, _ = json.Marshal(v1.InteractionResponse{Error: "plugin: interaction result exceeds frontend transport limit"})
		}
		frame, err := streammux.NewFrame(streammux.Header{Version: protocol.Version, MessageType: protocol.MessagePluginInteraction, Flags: streammux.FlagResponse, CorrelationID: request.Header.CorrelationID}, payload)
		if err == nil {
			_ = client.peer.Respond(ctx, request, frame)
		}
	}()
	return nil
}
func (client *Client) handlePluginDialogueCancel(ctx context.Context, _ *streammux.Peer, frame streammux.Frame) error {
	if frame.Header.Flags != streammux.FlagEvent {
		return errors.New("client: invalid dialogue cancellation")
	}
	var request struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(frame.Payload, &request); err != nil {
		return err
	}
	client.pluginMu.Lock()
	cancel := client.pluginDialogues[request.ID]
	client.pluginMu.Unlock()
	if cancel != nil {
		cancel.cancel()
	}
	return nil
}
func (client *Client) Plugin(ctx context.Context, params v1.ManageRequest) (v1.ManageResult, error) {
	if params.RequestID == "" {
		params.RequestID = fmt.Sprintf("call-%d", client.pluginNext.Add(1))
	}
	result, err := Call[v1.ManageResult](ctx, client, protocol.OperationPlugin, params)
	if ctx.Err() != nil {
		cancelCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = Call[v1.ManageResult](cancelCtx, client, protocol.OperationPlugin, v1.ManageRequest{Action: "cancel", RequestID: params.RequestID})
	}
	return result, err
}
