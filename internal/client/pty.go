package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/aruzen/streammux"
	"github.com/aruzen/streammux/pty"
)

const ptyProtocolVersion = 1

type PTYEventKind uint8

const (
	PTYReplayBegin PTYEventKind = iota + 1
	PTYOutput
	PTYReplayEnd
	PTYExit
	PTYError
)

type PTYEvent struct {
	Kind   PTYEventKind
	Data   []byte
	Replay *pty.ReplayMetadata
	Exit   *pty.ExitStatus
	Error  *pty.ProtocolErrorEvent
}

type PTYAttachment struct {
	owner  *PTYClient
	id     streammux.StreamID
	events chan PTYEvent
	once   sync.Once
}

func (attachment *PTYAttachment) ID() streammux.StreamID  { return attachment.id }
func (attachment *PTYAttachment) Events() <-chan PTYEvent { return attachment.events }

func (attachment *PTYAttachment) Detach(ctx context.Context) error {
	var detachErr error
	attachment.once.Do(func() {
		detachErr = attachment.owner.detach(ctx, attachment.id)
		attachment.owner.remove(attachment.id, attachment)
	})
	return detachErr
}

type PTYClient struct {
	client *Client
	types  pty.MessageTypes
	mu     sync.Mutex
	active map[streammux.StreamID]*PTYAttachment
}

func RegisterPTY(client *Client, types pty.MessageTypes) (*PTYClient, error) {
	if client == nil {
		return nil, errors.New("client: nil PTY client")
	}
	if err := validatePTYMessageTypes(types); err != nil {
		return nil, err
	}
	protocol := &PTYClient{client: client, types: types, active: make(map[streammux.StreamID]*PTYAttachment)}
	handlers := map[streammux.MessageType]streammux.Handler{
		types.ReplayBegin: protocol.handleReplayBegin,
		types.Output:      protocol.handleOutput,
		types.ReplayEnd:   protocol.handleReplayEnd,
		types.Exit:        protocol.handleExit,
		types.Error:       protocol.handleError,
	}
	if err := client.peer.RegisterHandlers(handlers); err != nil {
		return nil, err
	}
	return protocol, nil
}

func (protocol *PTYClient) Attach(ctx context.Context, id streammux.StreamID, replay pty.ReplayMode) (*PTYAttachment, pty.AttachResult, error) {
	if ctx == nil || id == 0 {
		return nil, pty.AttachResult{}, errors.New("client: invalid PTY attach")
	}
	attachment := &PTYAttachment{owner: protocol, id: id, events: make(chan PTYEvent, 64)}
	protocol.mu.Lock()
	if _, exists := protocol.active[id]; exists {
		protocol.mu.Unlock()
		return nil, pty.AttachResult{}, pty.ErrAlreadyAttached
	}
	protocol.active[id] = attachment
	protocol.mu.Unlock()
	payload, err := pty.EncodeControl(pty.AttachRequest{Version: ptyProtocolVersion, Replay: replay})
	if err != nil {
		protocol.remove(id, attachment)
		return nil, pty.AttachResult{}, err
	}
	response, err := protocol.call(ctx, protocol.types.Attach, id, payload)
	if err != nil {
		protocol.remove(id, attachment)
		return nil, pty.AttachResult{}, err
	}
	if response.Attach == nil {
		protocol.remove(id, attachment)
		return nil, pty.AttachResult{}, pty.ErrInvalidPayload
	}
	return attachment, *response.Attach, nil
}

func (protocol *PTYClient) Input(ctx context.Context, id streammux.StreamID, data []byte) error {
	frame, err := streammux.NewFrame(streammux.Header{
		Version: ptyProtocolVersion, MessageType: protocol.types.Input, Flags: streammux.FlagEvent, StreamID: id,
	}, data)
	if err != nil {
		return err
	}
	return protocol.client.peer.Send(ctx, frame, streammux.TrafficInteractive)
}

func (protocol *PTYClient) Resize(ctx context.Context, id streammux.StreamID, size pty.Size) error {
	payload, err := pty.EncodeSize(size)
	if err != nil {
		return err
	}
	_, err = protocol.call(ctx, protocol.types.Resize, id, payload)
	return err
}

func (protocol *PTYClient) detach(ctx context.Context, id streammux.StreamID) error {
	_, err := protocol.call(ctx, protocol.types.Detach, id, nil)
	return err
}

func (protocol *PTYClient) call(ctx context.Context, messageType streammux.MessageType, id streammux.StreamID, payload []byte) (pty.ProtocolResponse, error) {
	frame, err := streammux.NewFrame(streammux.Header{
		Version: ptyProtocolVersion, MessageType: messageType, Flags: streammux.FlagRequest,
		CorrelationID: 1, StreamID: id,
	}, payload)
	if err != nil {
		return pty.ProtocolResponse{}, err
	}
	responseFrame, err := protocol.client.peer.Call(ctx, frame)
	if err != nil {
		return pty.ProtocolResponse{}, err
	}
	response, err := pty.DecodeProtocolResponse(responseFrame.Payload)
	if err != nil {
		return pty.ProtocolResponse{}, err
	}
	if response.Version != ptyProtocolVersion {
		return pty.ProtocolResponse{}, pty.ErrUnsupportedProtocolVersion
	}
	return response, nil
}

func (protocol *PTYClient) handleOutput(ctx context.Context, _ *streammux.Peer, frame streammux.Frame) error {
	if err := validatePTYEventFrame(frame); err != nil {
		return err
	}
	return protocol.deliver(ctx, frame.Header.StreamID, PTYEvent{Kind: PTYOutput, Data: append([]byte(nil), frame.Payload...)})
}

func (protocol *PTYClient) handleReplayBegin(ctx context.Context, _ *streammux.Peer, frame streammux.Frame) error {
	if err := validatePTYEventFrame(frame); err != nil {
		return err
	}
	var metadata pty.ReplayMetadata
	if err := json.Unmarshal(frame.Payload, &metadata); err != nil || metadata.Version != ptyProtocolVersion {
		return pty.ErrInvalidPayload
	}
	return protocol.deliver(ctx, frame.Header.StreamID, PTYEvent{Kind: PTYReplayBegin, Replay: &metadata})
}

func (protocol *PTYClient) handleReplayEnd(ctx context.Context, _ *streammux.Peer, frame streammux.Frame) error {
	if err := validatePTYEventFrame(frame); err != nil {
		return err
	}
	if len(frame.Payload) != 0 {
		return pty.ErrInvalidPayload
	}
	return protocol.deliver(ctx, frame.Header.StreamID, PTYEvent{Kind: PTYReplayEnd})
}

func (protocol *PTYClient) handleExit(ctx context.Context, _ *streammux.Peer, frame streammux.Frame) error {
	if err := validatePTYEventFrame(frame); err != nil {
		return err
	}
	var envelope struct {
		Version uint16         `json:"version"`
		Exit    pty.ExitStatus `json:"exit"`
	}
	if err := json.Unmarshal(frame.Payload, &envelope); err != nil || envelope.Version != ptyProtocolVersion {
		return pty.ErrInvalidPayload
	}
	return protocol.deliver(ctx, frame.Header.StreamID, PTYEvent{Kind: PTYExit, Exit: &envelope.Exit})
}

func (protocol *PTYClient) handleError(ctx context.Context, _ *streammux.Peer, frame streammux.Frame) error {
	if err := validatePTYEventFrame(frame); err != nil {
		return err
	}
	var remote pty.ProtocolErrorEvent
	if err := json.Unmarshal(frame.Payload, &remote); err != nil || remote.Version != ptyProtocolVersion {
		return pty.ErrInvalidPayload
	}
	return protocol.deliver(ctx, frame.Header.StreamID, PTYEvent{Kind: PTYError, Error: &remote})
}

func validatePTYEventFrame(frame streammux.Frame) error {
	if frame.Header.Flags != streammux.FlagEvent || frame.Header.StreamID == 0 {
		return pty.ErrUnexpectedFrame
	}
	return nil
}

func validatePTYMessageTypes(types pty.MessageTypes) error {
	values := []streammux.MessageType{
		types.Open, types.List, types.Inspect, types.Attach, types.Detach, types.Input,
		types.Output, types.Resize, types.Kill, types.Exit, types.ReplayBegin, types.ReplayEnd, types.Error,
	}
	seen := make(map[streammux.MessageType]struct{}, len(values))
	for _, value := range values {
		if value == 0 {
			return pty.ErrInvalidMessageTypes
		}
		if _, exists := seen[value]; exists {
			return pty.ErrInvalidMessageTypes
		}
		seen[value] = struct{}{}
	}
	return nil
}

func (protocol *PTYClient) deliver(ctx context.Context, id streammux.StreamID, event PTYEvent) error {
	protocol.mu.Lock()
	attachment := protocol.active[id]
	protocol.mu.Unlock()
	if attachment == nil {
		return nil
	}
	select {
	case attachment.events <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-protocol.client.Done():
		if err := protocol.client.Err(); err != nil {
			return fmt.Errorf("client: PTY connection closed: %w", err)
		}
		return errors.New("client: PTY connection closed")
	}
}

func (protocol *PTYClient) remove(id streammux.StreamID, attachment *PTYAttachment) {
	protocol.mu.Lock()
	if protocol.active[id] == attachment {
		delete(protocol.active, id)
	}
	protocol.mu.Unlock()
}
