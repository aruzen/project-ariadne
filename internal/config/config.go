// Package config loads Ariadne's strict, startup-only TOML configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"strings"

	"github.com/aruzen/streammux"
	"github.com/aruzen/streammux/pty"
	"github.com/pelletier/go-toml/v2"
)

const (
	DefaultDetachKey = "ctrl-a d"
	DefaultMaxBytes  = 1 << 20
)

var (
	ErrInvalid      = errors.New("config: invalid configuration")
	ErrTooLarge     = errors.New("config: file too large")
	ErrInvalidInput = errors.New("config: invalid input")
)

// Config is loaded at process startup. An empty Shell selects the runtime
// $SHELL and /bin/sh fallback chain.
type Config struct {
	Shell     string          `toml:"shell"`
	DetachKey string          `toml:"detach_key"`
	Terminal  TerminalLimits  `toml:"terminal"`
	Transport TransportLimits `toml:"transport"`
}

// TerminalLimits bounds PTY output retained or queued by the daemon.
type TerminalLimits struct {
	HistoryBytes         int   `toml:"history_bytes"`
	MaxTotalHistoryBytes int64 `toml:"max_total_history_bytes"`
	ReadBufferBytes      int   `toml:"read_buffer_bytes"`
	AttachmentQueueBytes int   `toml:"attachment_queue_bytes"`
	ObserverQueueBytes   int   `toml:"observer_queue_bytes"`
}

// TransportLimits bounds streammux frames and in-memory queues. Queue byte
// limits count frame headers as well as payload bytes.
type TransportLimits struct {
	MaxFrameBytes        int `toml:"max_frame_bytes"`
	WriteQueueFrames     int `toml:"write_queue_frames"`
	OutboundQueueBytes   int `toml:"outbound_queue_bytes"`
	OutboundQueueFrames  int `toml:"outbound_queue_frames"`
	PerStreamQueueBytes  int `toml:"per_stream_queue_bytes"`
	PerStreamQueueFrames int `toml:"per_stream_queue_frames"`
}

func Default() Config {
	manager := pty.DefaultManagerConfig()
	stream := streammux.DefaultConfig()
	peer := streammux.DefaultPeerConfig()
	return Config{
		DetachKey: DefaultDetachKey,
		Terminal: TerminalLimits{
			HistoryBytes: manager.HistoryBytes, MaxTotalHistoryBytes: manager.MaxTotalHistoryBytes,
			ReadBufferBytes: manager.ReadBufferBytes, AttachmentQueueBytes: manager.AttachmentQueueBytes,
			ObserverQueueBytes: manager.ObserverQueueBytes,
		},
		Transport: TransportLimits{
			MaxFrameBytes: stream.MaxFrameBytes, WriteQueueFrames: stream.WriteQueue,
			OutboundQueueBytes: peer.OutboundQueueBytes, OutboundQueueFrames: peer.OutboundQueueFrames,
			PerStreamQueueBytes: peer.PerStreamQueueBytes, PerStreamQueueFrames: peer.PerStreamQueueFrames,
		},
	}
}

// ApplyStream applies user-configurable transport limits without changing
// protocol versions or validators.
func (configuration Config) ApplyStream(target *streammux.Config) {
	target.MaxFrameBytes = configuration.Transport.MaxFrameBytes
	target.WriteQueue = configuration.Transport.WriteQueueFrames
}

// ApplyPeer applies user-configurable queue limits without changing traffic
// classification, scheduling weights, or backpressure policy.
func (configuration Config) ApplyPeer(target *streammux.PeerConfig) {
	target.OutboundQueueBytes = configuration.Transport.OutboundQueueBytes
	target.OutboundQueueFrames = configuration.Transport.OutboundQueueFrames
	target.PerStreamQueueBytes = configuration.Transport.PerStreamQueueBytes
	target.PerStreamQueueFrames = configuration.Transport.PerStreamQueueFrames
}

// ApplyManager applies user-configurable PTY buffering limits without changing
// process/session lifecycle policy.
func (configuration Config) ApplyManager(target *pty.ManagerConfig) {
	target.HistoryBytes = configuration.Terminal.HistoryBytes
	target.MaxTotalHistoryBytes = configuration.Terminal.MaxTotalHistoryBytes
	target.ReadBufferBytes = configuration.Terminal.ReadBufferBytes
	target.AttachmentQueueBytes = configuration.Terminal.AttachmentQueueBytes
	target.ObserverQueueBytes = configuration.Terminal.ObserverQueueBytes
}

// Parse rejects unknown keys and TOML type mismatches.
func Parse(data []byte) (Config, error) {
	if len(data) > DefaultMaxBytes {
		return Config{}, fmt.Errorf("%w: %d bytes", ErrTooLarge, len(data))
	}
	configuration := Default()
	decoder := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err := decoder.Decode(&configuration); err != nil {
		return Config{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := configuration.validate(); err != nil {
		return Config{}, err
	}
	return configuration, nil
}

// Load returns defaults when path does not exist.
func Load(path string) (Config, error) {
	if path == "" {
		return Config{}, fmt.Errorf("%w: empty path", ErrInvalidInput)
	}
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("config: open: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, DefaultMaxBytes+1))
	if err != nil {
		return Config{}, fmt.Errorf("config: read: %w", err)
	}
	if len(data) > DefaultMaxBytes {
		return Config{}, fmt.Errorf("%w: exceeds %d bytes", ErrTooLarge, DefaultMaxBytes)
	}
	return Parse(data)
}

func (configuration Config) validate() error {
	if configuration.DetachKey == "" || strings.TrimSpace(configuration.DetachKey) == "" {
		return fmt.Errorf("%w: detach_key is empty", ErrInvalid)
	}
	if strings.ContainsRune(configuration.DetachKey, 0) {
		return fmt.Errorf("%w: detach_key contains NUL", ErrInvalid)
	}
	if configuration.Shell != "" {
		if strings.TrimSpace(configuration.Shell) == "" {
			return fmt.Errorf("%w: shell contains only whitespace", ErrInvalid)
		}
		if strings.ContainsRune(configuration.Shell, 0) {
			return fmt.Errorf("%w: shell contains NUL", ErrInvalid)
		}
	}
	terminal := configuration.Terminal
	if terminal.HistoryBytes < 0 || terminal.MaxTotalHistoryBytes < 0 {
		return fmt.Errorf("%w: terminal history limits must not be negative", ErrInvalid)
	}
	if (terminal.HistoryBytes == 0) != (terminal.MaxTotalHistoryBytes == 0) {
		return fmt.Errorf("%w: terminal history limits must both be zero or both be positive", ErrInvalid)
	}
	if terminal.HistoryBytes > 0 && int64(terminal.HistoryBytes) > terminal.MaxTotalHistoryBytes {
		return fmt.Errorf("%w: terminal.history_bytes exceeds terminal.max_total_history_bytes", ErrInvalid)
	}
	if terminal.ReadBufferBytes <= 0 || terminal.AttachmentQueueBytes <= 0 || terminal.ObserverQueueBytes <= 0 {
		return fmt.Errorf("%w: terminal buffer limits must be positive", ErrInvalid)
	}
	transport := configuration.Transport
	if transport.MaxFrameBytes <= 0 || uint64(transport.MaxFrameBytes) > math.MaxUint32 {
		return fmt.Errorf("%w: transport.max_frame_bytes must be between 1 and %d", ErrInvalid, uint64(math.MaxUint32))
	}
	if transport.WriteQueueFrames < 0 {
		return fmt.Errorf("%w: transport.write_queue_frames must not be negative", ErrInvalid)
	}
	if transport.OutboundQueueBytes <= 0 || transport.OutboundQueueFrames <= 0 ||
		transport.PerStreamQueueBytes <= 0 || transport.PerStreamQueueFrames <= 0 {
		return fmt.Errorf("%w: transport peer queue limits must be positive", ErrInvalid)
	}
	if transport.PerStreamQueueBytes > transport.OutboundQueueBytes {
		return fmt.Errorf("%w: transport.per_stream_queue_bytes exceeds transport.outbound_queue_bytes", ErrInvalid)
	}
	if transport.PerStreamQueueFrames > transport.OutboundQueueFrames {
		return fmt.Errorf("%w: transport.per_stream_queue_frames exceeds transport.outbound_queue_frames", ErrInvalid)
	}
	return nil
}
