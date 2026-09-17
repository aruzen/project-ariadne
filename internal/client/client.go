// Package client provides the one-connection Ariadne command client used by
// CLI frontends. PTY attachment is layered onto the same Peer separately.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/protocol"
	"github.com/aruzen/streammux"
)

type Config struct {
	Stream streammux.Config
	Peer   streammux.PeerConfig
	// EventBuffer bounds frontend state lag. Overflow closes the connection.
	EventBuffer int
}

func DefaultConfig() Config {
	peer := streammux.DefaultPeerConfig()
	peer.InboundQueuePolicy = streammux.InboundQueueBackpressure
	return Config{Stream: streammux.DefaultConfig(), Peer: peer, EventBuffer: 256}
}

type Client struct {
	peer              *streammux.Peer
	cancel            context.CancelFunc
	serveDone         chan error
	events            chan core.Event
	closeOnce         sync.Once
	pluginMu          sync.Mutex
	pluginInteraction InteractionHandler
	pluginNext        atomic.Uint64
	pluginDialogues   map[string]*pluginDialogueCancel
}

func Open(parent context.Context, connection io.ReadWriteCloser, configuration Config) (*Client, error) {
	if parent == nil || connection == nil {
		return nil, errors.New("client: nil dependency")
	}
	if configuration.EventBuffer <= 0 {
		return nil, errors.New("client: EventBuffer must be positive")
	}
	ctx, cancel := context.WithCancel(parent)
	muxConnection, err := streammux.Open(ctx, connection, configuration.Stream)
	if err != nil {
		cancel()
		return nil, err
	}
	peer, err := streammux.NewPeer(muxConnection, configuration.Peer)
	if err != nil {
		cancel()
		_ = muxConnection.Close()
		return nil, err
	}
	client := &Client{pluginDialogues: map[string]*pluginDialogueCancel{}, peer: peer, cancel: cancel, serveDone: make(chan error, 1), events: make(chan core.Event, configuration.EventBuffer)}
	if err := peer.Register(protocol.MessageEvent, client.handleEvent); err != nil {
		cancel()
		_ = peer.Close()
		return nil, err
	}
	if err := peer.Register(protocol.MessagePluginInteraction, client.handlePluginInteraction); err != nil {
		cancel()
		_ = peer.Close()
		return nil, err
	}
	if err := peer.Register(protocol.MessagePluginInteractionCancel, client.handlePluginDialogueCancel); err != nil {
		cancel()
		_ = peer.Close()
		return nil, err
	}
	go func() { client.serveDone <- peer.Serve(ctx) }()
	return client, nil
}

func (client *Client) Sync(ctx context.Context) (protocol.SyncResult, error) {
	return Call[protocol.SyncResult](ctx, client, protocol.OperationSync, nil)
}

func Call[T any](ctx context.Context, client *Client, operation protocol.Operation, params any) (T, error) {
	var zero T
	if ctx == nil {
		return zero, errors.New("client: nil context")
	}
	if client == nil {
		return zero, errors.New("client: nil client")
	}
	var raw json.RawMessage
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return zero, fmt.Errorf("client: encode parameters: %w", err)
		}
		raw = encoded
	}
	payload, err := marshalRequest(protocol.Request{Version: protocol.Version, Operation: operation, Params: raw})
	if err != nil {
		return zero, err
	}
	frame, err := streammux.NewFrame(streammux.Header{
		Version: protocol.Version, MessageType: protocol.MessageCommand,
		Flags: streammux.FlagRequest, CorrelationID: 1, StreamID: 0,
	}, payload)
	if err != nil {
		return zero, err
	}
	responseFrame, err := client.peer.Call(ctx, frame)
	if err != nil {
		return zero, err
	}
	response, err := protocol.DecodeResponse(responseFrame.Payload)
	if err != nil {
		return zero, err
	}
	if err := json.Unmarshal(response.Result, &zero); err != nil {
		return zero, fmt.Errorf("client: decode result: %w", err)
	}
	return zero, nil
}

func (client *Client) Close() error {
	var closeErr error
	client.closeOnce.Do(func() {
		client.cancel()
		closeErr = client.peer.Close()
		serveErr := <-client.serveDone
		if errors.Is(serveErr, context.Canceled) || errors.Is(serveErr, io.EOF) {
			serveErr = nil
		}
		closeErr = errors.Join(closeErr, serveErr)
	})
	return closeErr
}

func (client *Client) Peer() *streammux.Peer     { return client.peer }
func (client *Client) Done() <-chan struct{}     { return client.peer.Done() }
func (client *Client) Err() error                { return client.peer.Err() }
func (client *Client) Events() <-chan core.Event { return client.events }

func (client *Client) handleEvent(ctx context.Context, _ *streammux.Peer, frame streammux.Frame) error {
	if frame.Header.Flags != streammux.FlagEvent || frame.Header.StreamID != 0 {
		return errors.New("client: invalid Core event frame")
	}
	event, err := protocol.DecodeEvent(frame.Payload)
	if err != nil {
		return fmt.Errorf("client: decode Core event: %w", err)
	}
	select {
	case client.events <- event:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return errors.New("client: Core event queue overflow")
	}
}

func marshalRequest(request protocol.Request) ([]byte, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("client: encode request: %w", err)
	}
	return payload, nil
}
