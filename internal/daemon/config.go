package daemon

import (
	"errors"
	"fmt"

	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/plugin"
	ariadneprotocol "github.com/aruzen/ariadne/internal/protocol"
	"github.com/aruzen/ariadne/internal/statefile"
	"github.com/aruzen/streammux"
	"github.com/aruzen/streammux/pty"
)

const DefaultMaxConnections = 64

var (
	ErrInvalidConfig      = errors.New("daemon: invalid configuration")
	ErrClosed             = errors.New("daemon: closed")
	ErrAlreadyServing     = errors.New("daemon: already serving")
	ErrPTYOperationDenied = errors.New("daemon: PTY operation must use an Ariadne command")
	ErrTerminalNotOwned   = errors.New("daemon: terminal is not owned by Ariadne")
)

type Config struct {
	StatePath       string
	MaxConnections  int
	Core            core.Config
	Manager         pty.ManagerConfig
	Stream          streammux.Config
	Peer            streammux.PeerConfig
	AriadneProtocol ariadneprotocol.Config
	PTYProtocol     pty.ProtocolConfig
	State           statefile.Options
	Plugins         []plugin.Plugin
	Plugin          plugin.Config
}

func DefaultConfig(statePath string) Config {
	manager := pty.DefaultManagerConfig()
	manager.SessionLifecycle = pty.SessionLifecycleDescriptor{
		ExitPolicy: pty.ExitedSessionRetain, MaxRetainedSessions: 64,
	}
	manager.MaxAttachmentsPerSession = 1
	peer := streammux.DefaultPeerConfig()
	peer.Classify = classifyFrame
	ptyProtocol := pty.DefaultProtocolConfig()
	ptyProtocol.Types = DefaultPTYMessageTypes()
	return Config{
		StatePath: statePath, MaxConnections: DefaultMaxConnections,
		Core: core.DefaultConfig(), Manager: manager,
		Stream: streammux.DefaultConfig(), Peer: peer,
		AriadneProtocol: ariadneprotocol.DefaultConfig(), PTYProtocol: ptyProtocol,
		State: statefile.DefaultOptions(), Plugin: plugin.DefaultConfig(),
	}
}

func (configuration Config) withDefaults() (Config, error) {
	if configuration.StatePath == "" {
		return Config{}, fmt.Errorf("%w: empty state path", ErrInvalidConfig)
	}
	if configuration.MaxConnections == 0 {
		configuration.MaxConnections = DefaultMaxConnections
	}
	if configuration.MaxConnections < 1 {
		return Config{}, fmt.Errorf("%w: MaxConnections must be positive", ErrInvalidConfig)
	}
	if configuration.Core.EventQueueCapacity == 0 {
		configuration.Core = core.DefaultConfig()
	}
	if configuration.Manager.MaxSessions == 0 {
		defaults := DefaultConfig(configuration.StatePath)
		configuration.Manager = defaults.Manager
	}
	if configuration.PTYProtocol.Types == (pty.MessageTypes{}) {
		configuration.PTYProtocol.Types = DefaultPTYMessageTypes()
	}
	if configuration.Peer.Classify == nil {
		configuration.Peer.Classify = classifyFrame
	}
	return configuration, nil
}

func DefaultPTYMessageTypes() pty.MessageTypes {
	return pty.MessageTypes{
		Open: 0x1100, List: 0x1101, Inspect: 0x1102,
		Attach: 0x1103, Detach: 0x1104, Input: 0x1105, Output: 0x1106,
		Resize: 0x1107, Kill: 0x1108, Exit: 0x1109,
		ReplayBegin: 0x110a, ReplayEnd: 0x110b, Error: 0x110c,
	}
}

func classifyFrame(frame streammux.Frame) streammux.TrafficClass {
	if frame.Header.Flags != streammux.FlagEvent {
		return streammux.TrafficControl
	}
	types := DefaultPTYMessageTypes()
	if frame.Header.MessageType == types.Output {
		return streammux.TrafficInteractive
	}
	return streammux.TrafficControl
}
