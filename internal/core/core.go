// Package core owns Ariadne's portable domain state and serializes all state
// transitions through one executor goroutine.
package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
)

const DefaultEventQueueCapacity = 256
const DefaultMaxAttentionEntries = 1024

type Config struct {
	EventQueueCapacity  int
	MaxAttentionEntries int
}

func DefaultConfig() Config {
	return Config{EventQueueCapacity: DefaultEventQueueCapacity, MaxAttentionEntries: DefaultMaxAttentionEntries}
}

type requestKind uint8

const (
	requestExecute requestKind = iota
	requestSnapshot
	requestSubscribe
	requestFrontendState
	requestUnsubscribe
)

type request struct {
	kind       requestKind
	command    Command
	check      func(Snapshot) error
	frontendID FrontendID
	closeErr   error
	response   chan response
}

type response struct {
	value        any
	snapshot     Snapshot
	subscription *Subscription
	frontend     FrontendState
	err          error
}

type frontend struct {
	subscription *Subscription
	state        FrontendState
}

type Core struct {
	config Config
	state  *state

	requests chan request
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

func New(config Config) (*Core, error) {
	return newCore(config, defaultState())
}

// NewFromSnapshot restores a validated state copy. Runtime frontend state is
// intentionally not part of Snapshot and always starts empty.
func NewFromSnapshot(config Config, snapshot Snapshot) (*Core, error) {
	state, err := stateFromSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	return newCore(config, state)
}

func newCore(config Config, state *state) (*Core, error) {
	if config.MaxAttentionEntries == 0 {
		config.MaxAttentionEntries = DefaultMaxAttentionEntries
	}
	if config.EventQueueCapacity <= 0 {
		return nil, fmt.Errorf("%w: EventQueueCapacity must be positive", ErrInvalidConfig)
	}
	if config.MaxAttentionEntries < 1 {
		return nil, fmt.Errorf("%w: MaxAttentionEntries must be positive", ErrInvalidConfig)
	}
	core := &Core{
		config:   config,
		state:    state,
		requests: make(chan request),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go core.run()
	return core, nil
}

// Execute applies one Command on the executor and returns its concrete result.
// Context cancellation prevents commands not yet accepted by the executor;
// once accepted, a command completes so callers receive a definitive result.
func (c *Core) Execute(ctx context.Context, command Command) (any, error) {
	return c.ExecuteChecked(ctx, command, nil)
}

// ExecuteChecked checks the current snapshot and applies the command in one
// executor turn. The check must be short, must not call Core, and must not retain
// or mutate the snapshot. It is an internal broker boundary, not a plugin API.
func (c *Core) ExecuteChecked(ctx context.Context, command Command, check func(Snapshot) error) (any, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: nil context", ErrInvalidArgument)
	}
	command, err := cloneCommand(command)
	if err != nil {
		return nil, err
	}
	response, err := c.submit(ctx, request{kind: requestExecute, command: command, check: check})
	if err != nil {
		return nil, err
	}
	return response.value, response.err
}

func (c *Core) Snapshot(ctx context.Context) (Snapshot, error) {
	if ctx == nil {
		return Snapshot{}, fmt.Errorf("%w: nil context", ErrInvalidArgument)
	}
	response, err := c.submit(ctx, request{kind: requestSnapshot})
	if err != nil {
		return Snapshot{}, err
	}
	return response.snapshot, response.err
}

// Subscribe atomically captures a Snapshot and registers the frontend for all
// subsequent Events. The context also controls the subscription lifetime.
func (c *Core) Subscribe(ctx context.Context) (Snapshot, *Subscription, error) {
	if ctx == nil {
		return Snapshot{}, nil, fmt.Errorf("%w: nil context", ErrInvalidArgument)
	}
	response, err := c.submit(ctx, request{kind: requestSubscribe})
	if err != nil {
		return Snapshot{}, nil, err
	}
	if response.err != nil {
		return Snapshot{}, nil, response.err
	}
	subscription := response.subscription
	go func() {
		select {
		case <-ctx.Done():
			_ = c.unsubscribe(context.Background(), subscription.id, ctx.Err())
		case <-subscription.Done():
		}
	}()
	return response.snapshot, subscription, nil
}

func (c *Core) FrontendState(ctx context.Context, id FrontendID) (FrontendState, error) {
	if ctx == nil {
		return FrontendState{}, fmt.Errorf("%w: nil context", ErrInvalidArgument)
	}
	response, err := c.submit(ctx, request{kind: requestFrontendState, frontendID: id})
	if err != nil {
		return FrontendState{}, err
	}
	return response.frontend, response.err
}

func (c *Core) unsubscribe(ctx context.Context, id FrontendID, closeErr error) error {
	response, err := c.submit(ctx, request{kind: requestUnsubscribe, frontendID: id, closeErr: closeErr})
	if errors.Is(err, ErrClosed) {
		return nil
	}
	if err != nil {
		return err
	}
	if errors.Is(response.err, ErrNotFound) {
		return nil
	}
	return response.err
}

func (c *Core) Close() error {
	c.stopOnce.Do(func() { close(c.stop) })
	<-c.done
	return nil
}

func (c *Core) submit(ctx context.Context, operation request) (response, error) {
	operation.response = make(chan response, 1)
	select {
	case <-ctx.Done():
		return response{}, ctx.Err()
	case <-c.done:
		return response{}, ErrClosed
	case c.requests <- operation:
	}
	select {
	case result := <-operation.response:
		return result, nil
	case <-c.done:
		select {
		case result := <-operation.response:
			return result, nil
		default:
			return response{}, ErrClosed
		}
	}
}

func (c *Core) run() {
	defer close(c.done)
	frontends := make(map[FrontendID]*frontend)
	var nextFrontendID FrontendID = 1
	for {
		select {
		case <-c.stop:
			for id, frontend := range frontends {
				frontend.subscription.finish(ErrClosed)
				delete(frontends, id)
			}
			return
		case operation := <-c.requests:
			result := c.handle(operation, frontends, &nextFrontendID)
			operation.response <- result
		}
	}
}

func (c *Core) handle(operation request, frontends map[FrontendID]*frontend, nextFrontendID *FrontendID) response {
	switch operation.kind {
	case requestExecute:
		if operation.check != nil {
			if err := operation.check(c.state.snapshot()); err != nil {
				return response{err: err}
			}
		}
		if changesPersistentState(operation.command) && c.state.revision == math.MaxUint64 {
			return response{err: fmt.Errorf("%w: revision exhausted", ErrInvalidState)}
		}
		value, event, err := c.execute(operation.command, frontends)
		if err != nil {
			return response{err: err}
		}
		if event != nil {
			c.state.revision++
			event.Revision = c.state.revision
			c.publish(*event, frontends)
		}
		return response{value: value}
	case requestSnapshot:
		return response{snapshot: c.state.snapshot()}
	case requestSubscribe:
		if *nextFrontendID == 0 {
			return response{err: fmt.Errorf("%w: frontend ID exhausted", ErrInvalidState)}
		}
		id := *nextFrontendID
		*nextFrontendID++
		subscription := &Subscription{
			id:     id,
			core:   c,
			events: make(chan Event, c.config.EventQueueCapacity),
			done:   make(chan struct{}),
		}
		frontends[id] = &frontend{subscription: subscription, state: c.initialFrontendState(id)}
		return response{snapshot: c.state.snapshot(), subscription: subscription}
	case requestFrontendState:
		frontend, exists := frontends[operation.frontendID]
		if !exists {
			return response{err: fmt.Errorf("%w: frontend %d", ErrNotFound, operation.frontendID)}
		}
		return response{frontend: frontend.state}
	case requestUnsubscribe:
		frontend, exists := frontends[operation.frontendID]
		if !exists {
			return response{err: fmt.Errorf("%w: frontend %d", ErrNotFound, operation.frontendID)}
		}
		delete(frontends, operation.frontendID)
		frontend.subscription.finish(operation.closeErr)
		return response{}
	default:
		return response{err: ErrInvalidCommand}
	}
}

func changesPersistentState(command Command) bool {
	switch command.(type) {
	case SetFocusCommand, SelectWindowCommand:
		return false
	default:
		return true
	}
}

func (c *Core) publish(event Event, frontends map[FrontendID]*frontend) {
	for id, frontend := range frontends {
		select {
		case frontend.subscription.events <- cloneEvent(event):
		default:
			delete(frontends, id)
			frontend.subscription.finish(ErrEventQueueOverflow)
		}
	}
}

func (c *Core) initialFrontendState(id FrontendID) FrontendState {
	state := FrontendState{ID: id}
	if len(c.state.workspaceOrder) == 0 {
		return state
	}
	state.WorkspaceID = c.state.workspaceOrder[0]
	for _, workspaceID := range c.state.workspaceOrder {
		workspace := c.state.workspaces[workspaceID]
		if len(workspace.WindowIDs) == 0 {
			continue
		}
		state.WorkspaceID = workspaceID
		state.WindowID = workspace.WindowIDs[0]
		window := c.state.windows[state.WindowID]
		if window.Layout != nil {
			state.PaneID = firstPane(*window.Layout)
		}
		return state
	}
	return state
}

func firstPane(node LayoutNode) PaneID {
	if node.Kind == LayoutPane {
		return node.PaneID
	}
	if len(node.Children) == 0 {
		return 0
	}
	return firstPane(node.Children[0])
}
