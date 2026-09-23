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

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/streammux"
	"github.com/aruzen/streammux/pty"
)

const (
	Version                        = 1
	MessageCommand                 = streammux.MessageType(0x1000)
	MessageEvent                   = streammux.MessageType(0x1001)
	MessagePluginInteraction       = streammux.MessageType(0x1002)
	MessagePluginInteractionCancel = streammux.MessageType(0x1003)
	MessageFrontendControl         = streammux.MessageType(0x1004)
	DefaultMaxJSONBytes            = 1 << 20
	DefaultSendTimeout             = 2 * time.Second
)

type Operation string

const (
	OperationSync                 Operation = "sync"
	OperationReloadFrontendConfig Operation = "reload_frontend_config"
	OperationPlugin               Operation = "plugin"
	OperationCreateWorkspace      Operation = "create_workspace"
	OperationRenameWorkspace      Operation = "rename_workspace"
	OperationDeleteWorkspace      Operation = "delete_workspace"
	OperationCreateWindow         Operation = "create_window"
	OperationRenameWindow         Operation = "rename_window"
	OperationDeleteWindow         Operation = "delete_window"
	OperationCreatePane           Operation = "create_pane"
	OperationSplitPane            Operation = "split_pane"
	OperationMovePane             Operation = "move_pane"
	OperationClosePane            Operation = "close_pane"
	OperationResizeSplit          Operation = "resize_split"
	OperationStashPane            Operation = "stash_pane"
	OperationRestorePane          Operation = "restore_pane"
	OperationStashWindow          Operation = "stash_window"
	OperationRestoreWindow        Operation = "restore_window"
	OperationListStash            Operation = "list_stash"
	OperationSetFocus             Operation = "set_focus"
	OperationSelectWindow         Operation = "select_window"
	OperationNewTerminal          Operation = "new_terminal"
	OperationListTerminals        Operation = "list_terminals"
	OperationRestartTerminal      Operation = "restart_terminal"
	OperationRunTerminal          Operation = "run_terminal"
	OperationKillTerminal         Operation = "kill_terminal"
	OperationStopTerminal         Operation = "stop_terminal"
	OperationDeletePane           Operation = "delete_pane"
	OperationDismissTerminal      Operation = "dismiss_terminal"
	OperationDaemonStatus         Operation = "daemon_status"
	OperationDaemonStop           Operation = "daemon_stop"
	OperationClipboardRead        Operation = "clipboard_read"
	OperationClipboardWrite       Operation = "clipboard_write"
	OperationUpdateToolState      Operation = "update_tool_state"
	OperationAcknowledgeAttention Operation = "acknowledge_attention"
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
	ErrPermissionDenied    = errors.New("protocol: permission denied")
)

type Config struct {
	MaxJSONBytes int
	SendTimeout  time.Duration
	// CommandGuard brackets request execution. A daemon can use it to reject
	// new work and wait for in-flight commands before its shutdown Snapshot.
	CommandGuard func() (release func(), err error)
	Terminal     TerminalController
	Plugins      PluginController
	GUI          GUIOptions
	// GUIProvider returns the latest frontend presentation settings. When nil,
	// GUI is used for compatibility with embedders that provide static options.
	GUIProvider func() GUIOptions
	// ReloadFrontendConfig reloads only frontend-safe settings from the daemon's
	// configured file. The caller cannot select an arbitrary path.
	ReloadFrontendConfig func(context.Context) (GUIOptions, error)
}

type GUIOptions struct {
	ConfigPath         string            `json:"config_path,omitempty"`
	FontFamily         string            `json:"font_family"`
	FontSize           float64           `json:"font_size"`
	SoftwareRendering  bool              `json:"software_rendering"`
	Background         string            `json:"background"`
	Foreground         string            `json:"foreground"`
	Selection          string            `json:"selection"`
	Accent             string            `json:"accent"`
	ColorTable         []string          `json:"color_table"`
	Shell              []string          `json:"shell"`
	Editor             []string          `json:"editor"`
	Keybindings        map[string]string `json:"keybindings"`
	DefaultKeybindings map[string]string `json:"default_keybindings"`
}

// PluginController is the shared management and extension bridge for every frontend.
type PluginController interface {
	Attach(uint64) error
	Manage(context.Context, uint64, v1.ManageRequest) (v1.ManageResult, error)
	Detach(uint64)
}

type FrontendAction string

const FrontendNavigate FrontendAction = "navigate"

type FrontendControlRequest struct {
	Version         uint16         `json:"version"`
	Action          FrontendAction `json:"action"`
	PaneID          core.PaneID    `json:"pane_id"`
	MinimumRevision uint64         `json:"minimum_revision"`
}

type FrontendControlResponse struct {
	Version uint16       `json:"version"`
	Error   *RemoteError `json:"error,omitempty"`
}

// TerminalController owns operations that cross the Core/PTY I/O boundary.
// Implementations must serialize lifecycle transitions where necessary.
type TerminalController interface {
	NewTerminal(context.Context, NewTerminalParams) (TerminalOperationResult, error)
	ListTerminals(context.Context) (ListTerminalsResult, error)
	RestartTerminal(context.Context, RestartTerminalParams) (TerminalOperationResult, error)
	RunTerminal(context.Context, RunTerminalParams) (TerminalOperationResult, error)
	KillTerminal(context.Context, PaneParams) (TerminalOperationResult, error)
	DismissTerminal(context.Context, PaneParams) (TerminalOperationResult, error)
	DaemonStatus(context.Context) (DaemonStatusResult, error)
	DaemonStop(context.Context, DaemonStopParams) (DaemonStatusResult, error)
	ClipboardRead(context.Context, ClipboardReadParams) (ClipboardResult, error)
	ClipboardWrite(context.Context, ClipboardWriteParams) (ClipboardResult, error)
}

type ResponseObserver interface {
	AfterResponse(Operation)
}

// ResourceController owns deletion and stopping without bypassing PTY cleanup.
type ResourceController interface {
	StopTerminal(context.Context, PaneParams) (TerminalOperationResult, error)
	DeletePane(context.Context, PaneParams) (TerminalOperationResult, error)
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
	GUI      GUIOptions    `json:"gui"`
}

type EventEnvelope struct {
	Version uint16     `json:"version"`
	Event   core.Event `json:"event"`
}

// DecodeEvent decodes an event while preserving its concrete payload type.
func DecodeEvent(data []byte) (core.Event, error) {
	var envelope struct {
		Version uint16 `json:"version"`
		Event   struct {
			Revision uint64          `json:"revision"`
			Kind     core.EventKind  `json:"kind"`
			Payload  json.RawMessage `json:"payload"`
		} `json:"event"`
	}
	if err := decodeStrict(data, &envelope); err != nil {
		return core.Event{}, err
	}
	if envelope.Version != Version {
		return core.Event{}, ErrUnsupportedVersion
	}
	var payload any
	switch envelope.Event.Kind {
	case core.EventWorkspaceCreated:
		payload = &core.WorkspaceCreatedEvent{}
	case core.EventWorkspaceRenamed:
		payload = &core.WorkspaceEvent{}
	case core.EventWorkspaceDeleted:
		payload = &core.WorkspaceDeletedEvent{}
	case core.EventWindowCreated:
		payload = &core.WindowCreatedEvent{}
	case core.EventWindowRenamed, core.EventSplitResized:
		payload = &core.WindowEvent{}
	case core.EventWindowDeleted:
		payload = &core.WindowDeletedEvent{}
	case core.EventPaneCreated:
		payload = &core.PaneCreatedEvent{}
	case core.EventPaneMoved:
		payload = &core.PaneMovedEvent{}
	case core.EventPaneClosed:
		payload = &core.PaneClosedEvent{}
	case core.EventPaneStashed, core.EventPaneRestored:
		payload = &core.PaneStashEvent{}
	case core.EventWindowStashed, core.EventWindowRestored:
		payload = &core.WindowStashEvent{}
	case core.EventTerminalExited, core.EventTerminalUnavailable, core.EventTerminalStarted,
		core.EventTerminalStartFailed, core.EventTerminalRestarting, core.EventTerminalRunPrepared, core.EventTerminalStopping:
		payload = &core.TerminalEvent{}
	case core.EventLabelSet, core.EventLabelRemoved:
		payload = &core.LabelEvent{}
	case core.EventLabelSourceCleared:
		payload = &core.LabelsEvent{}
	case core.EventToolStateUpdated:
		payload = &core.ToolEvent{}
	case core.EventAttentionRaised, core.EventAttentionAcknowledged:
		payload = &core.AttentionEvent{}
	case core.EventAttentionSourceCleared:
		payload = &core.AttentionsEvent{}
	default:
		return core.Event{}, fmt.Errorf("%w: unknown event kind %q", ErrInvalidPayload, envelope.Event.Kind)
	}
	if err := decodeStrict(envelope.Event.Payload, payload); err != nil {
		return core.Event{}, err
	}
	switch value := payload.(type) {
	case *core.WorkspaceCreatedEvent:
		payload = *value
	case *core.WorkspaceEvent:
		payload = *value
	case *core.WorkspaceDeletedEvent:
		payload = *value
	case *core.WindowCreatedEvent:
		payload = *value
	case *core.WindowEvent:
		payload = *value
	case *core.WindowDeletedEvent:
		payload = *value
	case *core.PaneCreatedEvent:
		payload = *value
	case *core.PaneMovedEvent:
		payload = *value
	case *core.PaneClosedEvent:
		payload = *value
	case *core.PaneStashEvent:
		payload = *value
	case *core.WindowStashEvent:
		payload = *value
	case *core.TerminalEvent:
		payload = *value
	case *core.LabelEvent:
		payload = *value
	case *core.LabelsEvent:
		payload = *value
	case *core.ToolEvent:
		payload = *value
	case *core.AttentionEvent:
		payload = *value
	case *core.AttentionsEvent:
		payload = *value
	}
	return core.Event{Revision: envelope.Event.Revision, Kind: envelope.Event.Kind, Payload: payload}, nil
}

type CreateWorkspaceParams struct {
	Name string `json:"name"`
}

type CreateWindowParams struct {
	WorkspaceID core.WorkspaceID `json:"workspace_id"`
	Name        string           `json:"name"`
}

type RenameWorkspaceParams struct {
	WorkspaceID core.WorkspaceID `json:"workspace_id"`
	Name        string           `json:"name"`
}

type DeleteWorkspaceParams struct {
	WorkspaceID core.WorkspaceID `json:"workspace_id"`
}

type RenameWindowParams struct {
	WindowID core.WindowID `json:"window_id"`
	Name     string        `json:"name"`
}

type DeleteWindowParams struct {
	WindowID core.WindowID `json:"window_id"`
}

type CreatePaneParams struct {
	WindowID     core.WindowID         `json:"window_id"`
	Kind         core.PaneKind         `json:"kind"`
	Title        string                `json:"title,omitempty"`
	Presentation core.PanePresentation `json:"presentation,omitempty"`
	Tool         *core.ToolInstance    `json:"tool,omitempty"`
}

type SplitPaneParams struct {
	TargetPaneID core.PaneID           `json:"target_pane_id"`
	Direction    core.SplitDirection   `json:"direction"`
	Kind         core.PaneKind         `json:"kind"`
	Title        string                `json:"title,omitempty"`
	Presentation core.PanePresentation `json:"presentation,omitempty"`
	Tool         *core.ToolInstance    `json:"tool,omitempty"`
}

type UpdateToolStateParams struct {
	Descriptor         core.ToolDescriptor `json:"descriptor"`
	ExpectedGeneration uint64              `json:"expected_generation"`
	StateVersion       uint32              `json:"state_version"`
	State              json.RawMessage     `json:"state"`
}

type AcknowledgeAttentionParams struct {
	ID uint64    `json:"id"`
	At time.Time `json:"at"`
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

type ResizeSplitParams struct {
	SplitID core.SplitID `json:"split_id"`
	Weights []uint32     `json:"weights"`
}

type SelectWindowParams struct {
	WindowID core.WindowID `json:"window_id"`
}

type RestorePaneParams struct {
	PaneID              core.PaneID         `json:"pane_id"`
	DestinationWindowID core.WindowID       `json:"destination_window_id,omitempty"`
	TargetPaneID        core.PaneID         `json:"target_pane_id,omitempty"`
	Direction           core.SplitDirection `json:"direction,omitempty"`
}

type RestoreWindowParams struct {
	WindowID    core.WindowID    `json:"window_id"`
	WorkspaceID core.WorkspaceID `json:"workspace_id,omitempty"`
}

type StashListResult struct {
	Panes   []StashedPaneEntry   `json:"panes"`
	Windows []StashedWindowEntry `json:"windows"`
}

type StashedPaneEntry struct {
	Stashed core.StashedPane `json:"stashed"`
	Pane    core.Pane        `json:"pane"`
}

type StashedWindowEntry struct {
	Stashed core.StashedWindow `json:"stashed"`
	Window  core.Window        `json:"window"`
	Panes   []core.Pane        `json:"panes"`
}

type NewTerminalParams struct {
	WindowID     core.WindowID         `json:"window_id,omitempty"`
	TargetPaneID core.PaneID           `json:"target_pane_id,omitempty"`
	Direction    core.SplitDirection   `json:"direction,omitempty"`
	Title        string                `json:"title,omitempty"`
	Presentation core.PanePresentation `json:"presentation,omitempty"`
	Argv         []string              `json:"argv"`
	CWD          string                `json:"cwd"`
	Env          []string              `json:"env"`
	InitialSize  pty.Size              `json:"initial_size"`
}

type RestartTerminalParams struct {
	PaneID      core.PaneID `json:"pane_id"`
	Env         []string    `json:"env"`
	InitialSize pty.Size    `json:"initial_size"`
}

type RunTerminalParams struct {
	PaneID      core.PaneID `json:"pane_id"`
	Argv        []string    `json:"argv"`
	CWD         string      `json:"cwd,omitempty"`
	FallbackCWD string      `json:"fallback_cwd"`
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
	Stopping          bool           `json:"stopping"`
	Connections       int            `json:"connections"`
	Sessions          int            `json:"sessions"`
	ActiveTerminals   int            `json:"active_terminals"`
	RetainedTerminals int            `json:"retained_terminals"`
	Plugins           []PluginStatus `json:"plugins,omitempty"`
}

type PluginStatus struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Error   string `json:"error,omitempty"`
}

type DaemonStopParams struct {
	Force bool `json:"force"`
}

type ClipboardReadParams struct {
	PaneID   core.PaneID `json:"pane_id"`
	Protocol string      `json:"protocol"`
	Approved bool        `json:"approved"`
}

type ClipboardWriteParams struct {
	PaneID   core.PaneID `json:"pane_id"`
	Protocol string      `json:"protocol"`
	Text     string      `json:"text"`
	Approved bool        `json:"approved"`
}

type ClipboardResult struct {
	Text string `json:"text,omitempty"`
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
	pluginCalls  map[string]context.CancelFunc
	pluginBytes  int
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
	protocol := &Protocol{peer: peer, core: engine, config: configuration, ctx: ctx, cancel: cancel, pluginCalls: map[string]context.CancelFunc{}}
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
			// Remove the Core frontend first so a racing plugin invocation cannot
			// capture a new Context after its lifecycle notification.
			_ = subscription.Close()
			if protocol.config.Plugins != nil {
				protocol.config.Plugins.Detach(uint64(subscription.ID()))
			}
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
	if request.Operation == OperationPlugin {
		frontend, err := protocol.frontendID()
		if err != nil {
			return protocol.respondCoreError(ctx, frame, err)
		}
		var params v1.ManageRequest
		if err := decodeParams(request.Params, &params); err != nil {
			return protocol.respondCoreError(ctx, frame, err)
		}
		if protocol.config.Plugins == nil {
			return protocol.respondError(ctx, frame, CodeInvalidState, "plugin manager is unavailable")
		}
		requestID := params.RequestID
		if requestID == "" {
			requestID = fmt.Sprintf("rpc-%d", frame.Header.CorrelationID)
		}
		if len(requestID) > 128 {
			return protocol.respondError(ctx, frame, CodeInvalidArgument, "invalid plugin request ID")
		}
		if params.Action == "cancel" {
			protocol.mu.Lock()
			cancel := protocol.pluginCalls[requestID]
			protocol.mu.Unlock()
			if cancel != nil {
				cancel()
			}
			return protocol.respondResult(ctx, frame, v1.ManageResult{})
		}
		protocol.mu.Lock()
		_, duplicate := protocol.pluginCalls[requestID]
		if duplicate || len(protocol.pluginCalls) >= 64 || protocol.pluginBytes+len(frame.Payload) > 16<<20 {
			protocol.mu.Unlock()
			return protocol.respondError(ctx, frame, CodeInvalidState, "plugin frontend request queue overflow")
		}
		callCtx, cancel := context.WithCancel(protocol.ctx)
		protocol.pluginCalls[requestID] = cancel
		protocol.pluginBytes += len(frame.Payload)
		protocol.mu.Unlock()
		commandRelease := release
		release = nil
		// streammux orders handlers on StreamID 0. Long plugin calls must leave that
		// handler so Core events, editor operations, and cancellation can continue.
		go func() {
			defer func() {
				cancel()
				commandRelease()
				protocol.mu.Lock()
				delete(protocol.pluginCalls, requestID)
				protocol.pluginBytes -= len(frame.Payload)
				protocol.mu.Unlock()
			}()
			result, err := protocol.config.Plugins.Manage(callCtx, uint64(frontend), params)
			if err != nil {
				code, _ := classifyError(err)
				var remote *RemoteError
				if errors.As(err, &remote) {
					code = remote.Code
				}
				_ = protocol.respondError(callCtx, frame, code, err.Error())
				return
			}
			encoded, err := json.Marshal(result)
			if err != nil || len(encoded)+64 > protocol.peer.MaxFrameBytes() {
				_ = protocol.respondError(callCtx, frame, CodeInvalidArgument, "plugin response exceeds frontend transport limit")
				return
			}
			_ = protocol.respondResult(callCtx, frame, result)
		}()
		return nil
	}
	if request.Operation == OperationReloadFrontendConfig {
		if _, err := protocol.frontendID(); err != nil {
			return protocol.respondCoreError(ctx, frame, err)
		}
		if err := decodeNoParams(request.Params); err != nil {
			return protocol.respondCoreError(ctx, frame, err)
		}
		if protocol.config.ReloadFrontendConfig == nil {
			return protocol.respondError(ctx, frame, CodeInvalidState, "frontend configuration reload is unavailable")
		}
		result, err := protocol.config.ReloadFrontendConfig(ctx)
		release()
		release = nil
		if err != nil {
			return protocol.respondCoreError(ctx, frame, err)
		}
		return protocol.respondResult(ctx, frame, result)
	}
	if request.Operation == OperationListStash {
		if _, err := protocol.frontendID(); err != nil {
			return protocol.respondCoreError(ctx, frame, err)
		}
		if err := decodeNoParams(request.Params); err != nil {
			return protocol.respondCoreError(ctx, frame, err)
		}
		snapshot, err := protocol.core.Snapshot(ctx)
		if err != nil {
			return protocol.respondCoreError(ctx, frame, err)
		}
		release()
		release = nil
		return protocol.respondResult(ctx, frame, stashList(snapshot))
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
	// close_pane is a legacy core operation. A live daemon must release a retained
	// terminal too, rather than orphaning its manager session by removing only core state.
	if request.Operation == OperationClosePane {
		if _, ok := protocol.config.Terminal.(ResourceController); ok {
			request.Operation = OperationDeletePane
			value, err := protocol.executeTerminal(ctx, request)
			release()
			release = nil
			if err != nil {
				return protocol.respondCoreError(ctx, frame, err)
			}
			result := value.(TerminalOperationResult)
			return protocol.respondResult(ctx, frame, core.ClosePaneResult{Pane: result.Pane})
		}
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

func stashList(snapshot core.Snapshot) StashListResult {
	panes := make(map[core.PaneID]core.Pane, len(snapshot.Panes))
	for _, pane := range snapshot.Panes {
		panes[pane.ID] = pane
	}
	windows := make(map[core.WindowID]core.Window, len(snapshot.Windows))
	for _, window := range snapshot.Windows {
		windows[window.ID] = window
	}
	result := StashListResult{
		Panes:   make([]StashedPaneEntry, 0, len(snapshot.StashedPanes)),
		Windows: make([]StashedWindowEntry, 0, len(snapshot.StashedWindows)),
	}
	for _, stashed := range snapshot.StashedPanes {
		result.Panes = append(result.Panes, StashedPaneEntry{Stashed: stashed, Pane: panes[stashed.PaneID]})
	}
	for _, stashed := range snapshot.StashedWindows {
		entry := StashedWindowEntry{Stashed: stashed, Window: windows[stashed.WindowID]}
		for _, pane := range snapshot.Panes {
			if pane.WindowID == stashed.WindowID {
				entry.Panes = append(entry.Panes, pane)
			}
		}
		result.Windows = append(result.Windows, entry)
	}
	return result
}

func isTerminalOperation(operation Operation) bool {
	switch operation {
	case OperationNewTerminal, OperationListTerminals, OperationRestartTerminal, OperationRunTerminal, OperationStopTerminal, OperationDeletePane,
		OperationKillTerminal, OperationDismissTerminal, OperationDaemonStatus, OperationDaemonStop,
		OperationClipboardRead, OperationClipboardWrite:
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
	case OperationStopTerminal, OperationDeletePane:
		var params PaneParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		controller, ok := protocol.config.Terminal.(ResourceController)
		if !ok {
			return nil, fmt.Errorf("%w: resource operations are unavailable", core.ErrInvalidState)
		}
		if request.Operation == OperationStopTerminal {
			return controller.StopTerminal(ctx, params)
		}
		return controller.DeletePane(ctx, params)
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
	case OperationRunTerminal:
		var params RunTerminalParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return protocol.config.Terminal.RunTerminal(ctx, params)
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
	case OperationClipboardRead:
		var params ClipboardReadParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return protocol.config.Terminal.ClipboardRead(ctx, params)
	case OperationClipboardWrite:
		var params ClipboardWriteParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return protocol.config.Terminal.ClipboardWrite(ctx, params)
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
	if protocol.config.Plugins != nil {
		if err := protocol.config.Plugins.Attach(uint64(subscription.ID())); err != nil {
			protocol.mu.Lock()
			protocol.subscription = nil
			protocol.mu.Unlock()
			_ = subscription.Close()
			return SyncResult{}, nil, err
		}
	}
	options := protocol.config.GUI
	if protocol.config.GUIProvider != nil {
		options = protocol.config.GUIProvider()
	}
	return SyncResult{Snapshot: snapshot, GUI: options}, subscription, nil
}

func DecodeFrontendControlRequest(data []byte) (FrontendControlRequest, error) {
	var request FrontendControlRequest
	if err := decodeStrict(data, &request); err != nil {
		return FrontendControlRequest{}, err
	}
	if request.Version != Version || request.Action != FrontendNavigate || request.PaneID == 0 {
		return FrontendControlRequest{}, ErrInvalidPayload
	}
	return request, nil
}

func DecodeFrontendControlResponse(data []byte) (FrontendControlResponse, error) {
	var response FrontendControlResponse
	if err := decodeStrict(data, &response); err != nil {
		return FrontendControlResponse{}, err
	}
	if response.Version != Version {
		return FrontendControlResponse{}, ErrUnsupportedVersion
	}
	if response.Error != nil {
		return response, response.Error
	}
	return response, nil
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
	case OperationRenameWorkspace:
		var params RenameWorkspaceParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.RenameWorkspaceCommand{WorkspaceID: params.WorkspaceID, Name: params.Name}, nil
	case OperationDeleteWorkspace:
		var params DeleteWorkspaceParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.DeleteWorkspaceCommand{WorkspaceID: params.WorkspaceID}, nil
	case OperationCreateWindow:
		var params CreateWindowParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.CreateWindowCommand{WorkspaceID: params.WorkspaceID, Name: params.Name}, nil
	case OperationRenameWindow:
		var params RenameWindowParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.RenameWindowCommand{WindowID: params.WindowID, Name: params.Name}, nil
	case OperationDeleteWindow:
		var params DeleteWindowParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.DeleteWindowCommand{WindowID: params.WindowID}, nil
	case OperationCreatePane:
		var params CreatePaneParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.CreatePaneCommand{WindowID: params.WindowID, Pane: core.PaneSpec{Kind: params.Kind, Title: params.Title, Presentation: params.Presentation, Tool: params.Tool}}, nil
	case OperationSplitPane:
		var params SplitPaneParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.SplitPaneCommand{TargetPaneID: params.TargetPaneID, Direction: params.Direction, Pane: core.PaneSpec{Kind: params.Kind, Title: params.Title, Presentation: params.Presentation, Tool: params.Tool}}, nil
	case OperationUpdateToolState:
		var params UpdateToolStateParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.UpdateToolStateCommand{Descriptor: params.Descriptor, ExpectedGeneration: params.ExpectedGeneration, StateVersion: params.StateVersion, State: params.State}, nil
	case OperationAcknowledgeAttention:
		var params AcknowledgeAttentionParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.AcknowledgeAttentionCommand{ID: params.ID, At: params.At}, nil
	case OperationMovePane:
		var params MovePaneParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.MovePaneCommand{PaneID: params.PaneID, DestinationID: params.DestinationID, TargetPaneID: params.TargetPaneID, Direction: params.Direction}, nil
	case OperationClosePane:
		var params PaneParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.ClosePaneCommand{PaneID: params.PaneID}, nil
	case OperationResizeSplit:
		var params ResizeSplitParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.ResizeSplitCommand{SplitID: params.SplitID, Weights: params.Weights}, nil
	case OperationStashPane:
		var params PaneParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.StashPaneCommand{PaneID: params.PaneID}, nil
	case OperationRestorePane:
		var params RestorePaneParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.RestorePaneCommand{
			FrontendID: frontendID, PaneID: params.PaneID, DestinationWindowID: params.DestinationWindowID,
			TargetPaneID: params.TargetPaneID, Direction: params.Direction,
		}, nil
	case OperationStashWindow:
		var params DeleteWindowParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.StashWindowCommand{WindowID: params.WindowID}, nil
	case OperationRestoreWindow:
		var params RestoreWindowParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.RestoreWindowCommand{FrontendID: frontendID, WindowID: params.WindowID, WorkspaceID: params.WorkspaceID}, nil
	case OperationSetFocus:
		var params SetFocusParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.SetFocusCommand{FrontendID: frontendID, PaneID: params.PaneID}, nil
	case OperationSelectWindow:
		var params SelectWindowParams
		if err := decodeParams(request.Params, &params); err != nil {
			return nil, err
		}
		return core.SelectWindowCommand{FrontendID: frontendID, WindowID: params.WindowID}, nil
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
	case errors.Is(err, ErrPermissionDenied):
		return CodePermissionDenied, "permission denied"
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
