// Package protocol implements Ariadne's JSON Command/Event protocol on a
// streammux Peer. PTY bytes and controls use streammux/pty separately.
package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/streammux"
	"github.com/aruzen/streammux/pty"
)

const (
	Version             = 1
	MessageCommand      = streammux.MessageType(0x1000)
	MessageEvent        = streammux.MessageType(0x1001)
	DefaultMaxJSONBytes = 1 << 20
	DefaultSendTimeout  = 2 * time.Second
)

type Operation string

const (
	OperationSync            Operation = "sync"
	OperationCreateWorkspace Operation = "create_workspace"
	OperationCreateWindow    Operation = "create_window"
	OperationCreatePane      Operation = "create_pane"
	OperationSplitPane       Operation = "split_pane"
	OperationMovePane        Operation = "move_pane"
	OperationSetFocus        Operation = "set_focus"
	OperationNewTerminal     Operation = "new_terminal"
	OperationListTerminals   Operation = "list_terminals"
	OperationRestartTerminal Operation = "restart_terminal"
	OperationKillTerminal    Operation = "kill_terminal"
	OperationDismissTerminal Operation = "dismiss_terminal"
	OperationDaemonStatus    Operation = "daemon_status"
	OperationDaemonStop      Operation = "daemon_stop"
)

type ErrorCode string

const (
	CodeInvalidArgument  ErrorCode = "invalid_argument"
	CodeNotFound         ErrorCode = "not_found"
	CodeAlreadyExists    ErrorCode = "already_exists"
	CodeAlreadyAttached  ErrorCode = "already_attached"
	CodeInvalidState     ErrorCode = "invalid_state"
	CodePermissionDenied ErrorCode = "permission_denied"
	CodeInternal         ErrorCode = "internal"
)

var (
	ErrInvalidPayload      = errors.New("protocol: invalid payload")
	ErrUnsupportedVersion  = errors.New("protocol: unsupported version")
	ErrNotSynchronized     = errors.New("protocol: connection is not synchronized")
	ErrAlreadySynchronized = errors.New("protocol: connection is already synchronized")
)

type Config struct {
	MaxJSONBytes int
	SendTimeout  time.Duration
	// CommandGuard brackets request execution. A daemon can use it to reject
	// new work and wait for in-flight commands before its shutdown Snapshot.
	CommandGuard func() (release func(), err error)
	Terminal     TerminalController
}

// TerminalController owns operations that cross the Core/PTY I/O boundary.
// Implementations must serialize lifecycle transitions where necessary.
type TerminalController interface {
	NewTerminal(context.Context, NewTerminalParams) (TerminalOperationResult, error)
	ListTerminals(context.Context) (ListTerminalsResult, error)
	RestartTerminal(context.Context, RestartTerminalParams) (TerminalOperationResult, error)
	KillTerminal(context.Context, PaneParams) (TerminalOperationResult, error)
	DismissTerminal(context.Context, PaneParams) (TerminalOperationResult, error)
	DaemonStatus(context.Context) (DaemonStatusResult, error)
	DaemonStop(context.Context, DaemonStopParams) (DaemonStatusResult, error)
}

type ResponseObserver interface {
	AfterResponse(Operation)
}

func DefaultConfig() Config {
	return Config{MaxJSONBytes: DefaultMaxJSONBytes, SendTimeout: DefaultSendTimeout}
}

func (configuration Config) normalized() (Config, error) {
	if configuration.MaxJSONBytes == 0 {
		configuration.MaxJSONBytes = DefaultMaxJSONBytes
	}
	if configuration.SendTimeout == 0 {
		configuration.SendTimeout = DefaultSendTimeout
	}
	if configuration.MaxJSONBytes < 1 || configuration.SendTimeout < 0 {
		return Config{}, errors.New("protocol: invalid configuration")
	}
	return configuration, nil
}

type Request struct {
	Version   uint16          `json:"version"`
	Operation Operation       `json:"operation"`
	Params    json.RawMessage `json:"params,omitempty"`
}

type RemoteError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

func (remote *RemoteError) Error() string {
	if remote == nil {
		return ""
	}
	return fmt.Sprintf("ariadne remote %s: %s", remote.Code, remote.Message)
}

func (remote *RemoteError) Is(target error) bool {
	other, ok := target.(*RemoteError)
	return ok && other.Code == remote.Code
}

type Response struct {
	Version uint16          `json:"version"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RemoteError    `json:"error,omitempty"`
}

type SyncResult struct {
	Snapshot core.Snapshot `json:"snapshot"`
}

type EventEnvelope struct {
	Version uint16     `json:"version"`
	Event   core.Event `json:"event"`
}

type CreateWorkspaceParams struct {
	Name string `json:"name"`
}

type CreateWindowParams struct {
	WorkspaceID core.WorkspaceID `json:"workspace_id"`
	Name        string           `json:"name"`
}

type CreatePaneParams struct {
	WindowID core.WindowID `json:"window_id"`
	Kind     core.PaneKind `json:"kind"`
	Title    string        `json:"title,omitempty"`
}

type SplitPaneParams struct {
	TargetPaneID core.PaneID         `json:"target_pane_id"`
	Direction    core.SplitDirection `json:"direction"`
	Kind         core.PaneKind       `json:"kind"`
	Title        string              `json:"title,omitempty"`
}

type MovePaneParams struct {
	PaneID        core.PaneID         `json:"pane_id"`
	DestinationID core.WindowID       `json:"destination_window_id"`
	TargetPaneID  core.PaneID         `json:"target_pane_id,omitempty"`
	Direction     core.SplitDirection `json:"direction,omitempty"`
}

type SetFocusParams struct {
	PaneID core.PaneID `json:"pane_id"`
}

type NewTerminalParams struct {
	WindowID     core.WindowID       `json:"window_id,omitempty"`
	TargetPaneID core.PaneID         `json:"target_pane_id,omitempty"`
	Direction    core.SplitDirection `json:"direction,omitempty"`
	Title        string              `json:"title,omitempty"`
	Argv         []string            `json:"argv"`
	CWD          string              `json:"cwd"`
	Env          []string            `json:"env"`
	InitialSize  pty.Size            `json:"initial_size"`
}

type RestartTerminalParams struct {
	PaneID      core.PaneID `json:"pane_id"`
	Env         []string    `json:"env"`
	InitialSize pty.Size    `json:"initial_size"`
}

type PaneParams struct {
	PaneID core.PaneID `json:"pane_id"`
}

type TerminalOperationResult struct {
	Pane core.Pane `json:"pane"`
}

type TerminalListEntry struct {
	Pane            core.Pane `json:"pane"`
	AttachmentCount int       `json:"attachment_count"`
}

type ListTerminalsResult struct {
	Revision uint64              `json:"revision"`
	Entries  []TerminalListEntry `json:"entries"`
}

type DaemonStatusResult struct {
	Stopping          bool `json:"stopping"`
	Connections       int  `json:"connections"`
	Sessions          int  `json:"sessions"`
	ActiveTerminals   int  `json:"active_terminals"`
	RetainedTerminals int  `json:"retained_terminals"`
}

type DaemonStopParams struct {
	Force bool `json:"force"`
}

type Protocol struct {
	peer   *streammux.Peer
	core   *core.Core
	config Config
	ctx    context.Context
	cancel context.CancelFunc

	mu           sync.Mutex
	subscription *core.Subscription
	closeOnce    sync.Once
}

func Register(peer *streammux.Peer, engine *core.Core, configuration Config) (*Protocol, error) {
	if peer == nil || engine == nil {
		return nil, errors.New("protocol: nil dependency")
	}
	configuration, err := configuration.normalized()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	protocol := &Protocol{peer: peer, core: engine, config: configuration, ctx: ctx, cancel: cancel}
	if err := peer.Register(MessageCommand, protocol.handleCommand); err != nil {
		cancel()
		return nil, err
	}
	go func() {
		select {
		case <-peer.Done():
			_ = protocol.Close()
		case <-ctx.Done():
		}
	}()
	return protocol, nil
}

func (protocol *Protocol) Close() error {
	protocol.closeOnce.Do(func() {
		protocol.cancel()
		protocol.mu.Lock()
		subscription := protocol.subscription
		protocol.subscription = nil
		protocol.mu.Unlock()
		if subscription != nil {
			_ = subscription.Close()
		}
	})
	return nil
}

func (protocol *Protocol) handleCommand(ctx context.Context, peer *streammux.Peer, frame streammux.Frame) error {
	if frame.Header.Flags != streammux.FlagRequest {
		return streammux.ErrInvalidFlags
	}
	if frame.Header.StreamID != 0 {
		return protocol.respondError(ctx, frame, CodeInvalidArgument, "Ariadne commands require StreamID zero")
	}
	if len(frame.Payload) == 0 || len(frame.Payload) > protocol.config.MaxJSONBytes {
		return protocol.respondError(ctx, frame, CodeInvalidArgument, "invalid command payload")
	}
	var request Request
	if err := decodeStrict(frame.Payload, &request); err != nil {
		return protocol.respondError(ctx, frame, CodeInvalidArgument, "invalid command payload")
	}
	if request.Version != Version {
		return protocol.respondError(ctx, frame, CodeInvalidArgument, "unsupported command version")
	}
	release := func() {}
	if protocol.config.CommandGuard != nil {
		var err error
		release, err = protocol.config.CommandGuard()
		if err != nil {
			return protocol.respondCoreError(ctx, frame, err)
		}
		if release == nil {
			return protocol.respondError(ctx, frame, CodeInternal, "internal error")
		}
	}
	defer func() {
		if release != nil {
			release()
		}
	}()
	if request.Operation == OperationSync {
		result, subscription, err := protocol.synchronize(request.Params)
		release()
		release = nil
		if err != nil {
			return protocol.respondCoreError(ctx, frame, err)
		}
		if err := protocol.respondResult(ctx, frame, result); err != nil {
			_ = subscription.Close()
			return err
		}
		go protocol.forwardEvents(subscription)
		return nil
	}
	if isTerminalOperation(request.Operation) {
		result, err := protocol.executeTerminal(ctx, request)
		release()
		release = nil
		if err != nil {
			return protocol.respondCoreError(ctx, frame, err)
		}
		responseErr := protocol.respondResult(ctx, frame, result)
		if observer, ok := protocol.config.Terminal.(ResponseObserver); ok {
			observer.AfterResponse(request.Operation)
		}
		return responseErr
	}
	frontendID, err := protocol.frontendID()
	if err != nil {
		release()
		release = nil
		return protocol.respondCoreError(ctx, frame, err)
	}
	command, err := commandFromRequest(request, frontendID)
	if err != nil {
		release()
		release = nil
		return protocol.respondCoreError(ctx, frame, err)
	}
	result, err := protocol.core.Execute(ctx, command)
	release()
	release = nil
	if err != nil {
		return protocol.respondCoreError(ctx, frame, err)
	}
	return protocol.respondResult(ctx, frame, result)
}

func isTerminalOperation(operation Operation) bool {
	switch operation {
	case OperationNewTerminal, OperationListTerminals, OperationRestartTerminal,
		OperationKillTerminal, OperationDismissTerminal, OperationDaemonStatus, OperationDaemonStop:
		return true
	default:
		return false
	}
}

func (protocol *Protocol) executeTerminal(ctx context.Context, request Request) (any, error) {
	if _, err := protocol.frontendID(); err != nil {
		return nil, err
	}
	if protocol.config.Terminal == nil {
		return nil, fmt.Errorf("%w: terminal operations are unavailable", core.ErrInvalidState)
	}
	switch request.Operation {
	case OperationNewTerminal:
		var params NewTerminalParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return protocol.config.Terminal.NewTerminal(ctx, params)
	case OperationListTerminals:
		if err := decodeNoParams(request.Params); err != nil {
			return nil, err
		}
		return protocol.config.Terminal.ListTerminals(ctx)
	case OperationRestartTerminal:
		var params RestartTerminalParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return protocol.config.Terminal.RestartTerminal(ctx, params)
	case OperationKillTerminal:
		var params PaneParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return protocol.config.Terminal.KillTerminal(ctx, params)
	case OperationDismissTerminal:
		var params PaneParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return protocol.config.Terminal.DismissTerminal(ctx, params)
	case OperationDaemonStatus:
		if err := decodeNoParams(request.Params); err != nil {
			return nil, err
		}
		return protocol.config.Terminal.DaemonStatus(ctx)
	case OperationDaemonStop:
		var params DaemonStopParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return protocol.config.Terminal.DaemonStop(ctx, params)
	default:
		return nil, fmt.Errorf("%w: unknown terminal operation", core.ErrInvalidArgument)
	}
}

func (protocol *Protocol) synchronize(params json.RawMessage) (SyncResult, *core.Subscription, error) {
	if len(params) != 0 && string(params) != "{}" {
		var empty struct{}
		if err := decodeParams(params, &empty); err != nil {
			return SyncResult{}, nil, fmt.Errorf("%w: sync does not accept parameters", core.ErrInvalidArgument)
		}
	}
	protocol.mu.Lock()
	if protocol.subscription != nil {
		protocol.mu.Unlock()
		return SyncResult{}, nil, ErrAlreadySynchronized
	}
	protocol.mu.Unlock()

	snapshot, subscription, err := protocol.core.Subscribe(protocol.ctx)
	if err != nil {
		return SyncResult{}, nil, err
	}
	protocol.mu.Lock()
	if protocol.subscription != nil {
		protocol.mu.Unlock()
		_ = subscription.Close()
		return SyncResult{}, nil, ErrAlreadySynchronized
	}
	protocol.subscription = subscription
	protocol.mu.Unlock()
	return SyncResult{Snapshot: snapshot}, subscription, nil
}

func (protocol *Protocol) frontendID() (core.FrontendID, error) {
	protocol.mu.Lock()
	defer protocol.mu.Unlock()
	if protocol.subscription == nil {
		return 0, ErrNotSynchronized
	}
	return protocol.subscription.ID(), nil
}

func commandFromRequest(request Request, frontendID core.FrontendID) (core.Command, error) {
	switch request.Operation {
	case OperationCreateWorkspace:
		var params CreateWorkspaceParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.CreateWorkspaceCommand{Name: params.Name}, nil
	case OperationCreateWindow:
		var params CreateWindowParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.CreateWindowCommand{WorkspaceID: params.WorkspaceID, Name: params.Name}, nil
	case OperationCreatePane:
		var params CreatePaneParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.CreatePaneCommand{WindowID: params.WindowID, Pane: core.PaneSpec{Kind: params.Kind, Title: params.Title}}, nil
	case OperationSplitPane:
		var params SplitPaneParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.SplitPaneCommand{TargetPaneID: params.TargetPaneID, Direction: params.Direction, Pane: core.PaneSpec{Kind: params.Kind, Title: params.Title}}, nil
	case OperationMovePane:
		var params MovePaneParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.MovePaneCommand{PaneID: params.PaneID, DestinationID: params.DestinationID, TargetPaneID: params.TargetPaneID, Direction: params.Direction}, nil
	case OperationSetFocus:
		var params SetFocusParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.SetFocusCommand{FrontendID: frontendID, PaneID: params.PaneID}, nil
	default:
		return nil, fmt.Errorf("%w: unknown operation", core.ErrInvalidArgument)
	}
}

func decodeParams(data json.RawMessage, destination any) error {
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("%w: missing params", core.ErrInvalidArgument)
	}
	if err := decodeStrict(data, destination); err != nil {
		return fmt.Errorf("%w: %v", core.ErrInvalidArgument, err)
	}
	return nil
}

func decodeNoParams(data json.RawMessage) error {
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) || bytes.Equal(bytes.TrimSpace(data), []byte("{}")) {
		return nil
	}
	var empty struct{}
	if err := decodeStrict(data, &empty); err != nil {
		return fmt.Errorf("%w: operation does not accept parameters", core.ErrInvalidArgument)
	}
	return nil
}

func decodeStrict(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPayload, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalidPayload
	}
	return nil
}

func (protocol *Protocol) respondResult(ctx context.Context, request streammux.Frame, result any) error {
	payload, err := json.Marshal(result)
	if err != nil {
		return protocol.respondError(ctx, request, CodeInternal, "internal error")
	}
	response, err := json.Marshal(Response{Version: Version, Result: payload})
	if err != nil {
		return err
	}
	frame, err := streammux.NewFrame(streammux.Header{
		Version: Version, MessageType: MessageCommand, Flags: streammux.FlagResponse,
		CorrelationID: request.Header.CorrelationID, StreamID: request.Header.StreamID,
	}, response)
	if err != nil {
		return err
	}
	return protocol.peer.Respond(ctx, request, frame)
}

func (protocol *Protocol) respondCoreError(ctx context.Context, request streammux.Frame, err error) error {
	code, message := classifyError(err)
	return protocol.respondError(ctx, request, code, message)
}

func (protocol *Protocol) respondError(ctx context.Context, request streammux.Frame, code ErrorCode, message string) error {
	payload, err := json.Marshal(Response{Version: Version, Error: &RemoteError{Code: code, Message: message}})
	if err != nil {
		return err
	}
	frame, err := streammux.NewFrame(streammux.Header{
		Version: Version, MessageType: MessageCommand, Flags: streammux.FlagResponse,
		CorrelationID: request.Header.CorrelationID, StreamID: request.Header.StreamID,
	}, payload)
	if err != nil {
		return err
	}
	return protocol.peer.Respond(ctx, request, frame)
}

func classifyError(err error) (ErrorCode, string) {
	switch {
	case errors.Is(err, core.ErrInvalidArgument), errors.Is(err, core.ErrInvalidCommand):
		return CodeInvalidArgument, "invalid argument"
	case errors.Is(err, core.ErrNotFound):
		return CodeNotFound, "not found"
	case errors.Is(err, pty.ErrSessionNotFound):
		return CodeNotFound, "not found"
	case errors.Is(err, core.ErrAlreadyExists):
		return CodeAlreadyExists, "already exists"
	case errors.Is(err, pty.ErrAttachmentLimit):
		return CodeAlreadyAttached, "already attached"
	case errors.Is(err, core.ErrInvalidState), errors.Is(err, ErrNotSynchronized), errors.Is(err, ErrAlreadySynchronized),
		errors.Is(err, pty.ErrSessionRunning), errors.Is(err, pty.ErrSessionLimit):
		return CodeInvalidState, "invalid state"
	default:
		return CodeInternal, "internal error"
	}
}

func (protocol *Protocol) forwardEvents(subscription *core.Subscription) {
	for {
		select {
		case event, ok := <-subscription.Events():
			if !ok {
				if subscription.Err() != nil && !errors.Is(subscription.Err(), context.Canceled) {
					_ = protocol.peer.Close()
				}
				return
			}
			payload, err := json.Marshal(EventEnvelope{Version: Version, Event: event})
			if err != nil {
				_ = protocol.peer.Close()
				return
			}
			frame, err := streammux.NewFrame(streammux.Header{
				Version: Version, MessageType: MessageEvent, Flags: streammux.FlagEvent,
			}, payload)
			if err != nil {
				_ = protocol.peer.Close()
				return
			}
			ctx, cancel := context.WithTimeout(protocol.ctx, protocol.config.SendTimeout)
			err = protocol.peer.Emit(ctx, frame)
			cancel()
			if err != nil {
				_ = protocol.peer.Close()
				return
			}
		case <-protocol.ctx.Done():
			return
		}
	}
}

// DecodeResponse validates a command response and returns a remote error as
// the Go error result.
func DecodeResponse(data []byte) (Response, error) {
	var response Response
	if err := decodeStrict(data, &response); err != nil {
		return Response{}, err
	}
	if response.Version != Version {
		return Response{}, ErrUnsupportedVersion
	}
	if response.Error != nil && len(response.Result) != 0 {
		return Response{}, ErrInvalidPayload
	}
	if response.Error != nil {
		return response, response.Error
	}
	if len(response.Result) == 0 {
		return Response{}, ErrInvalidPayload
	}
	return response, nil
}
