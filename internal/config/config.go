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

	"github.com/aruzen/ariadne/internal/plugin/external"
	"github.com/aruzen/streammux"
	"github.com/aruzen/streammux/pty"
	"github.com/pelletier/go-toml/v2"
)

const (
	DefaultDetachKey            = "ctrl-a d"
	DefaultMaxBytes             = 1 << 20
	DefaultTUIFrameMode         = TUIFrameFull
	DefaultSuccessfulExitPolicy = SuccessfulExitRetain
	DefaultClipboardPolicy      = ClipboardAsk
	DefaultClipboardMaxBytes    = 1 << 20
	DefaultClipboardTimeoutMS   = 2000
	MaxKeybindings              = 256
	MaxKeySequenceBytes         = 128
	MaxKeyCommandBytes          = 4096
)

type ClipboardPolicy string

const (
	ClipboardDeny  ClipboardPolicy = "deny"
	ClipboardAsk   ClipboardPolicy = "ask"
	ClipboardAllow ClipboardPolicy = "allow"
)

type SuccessfulExitPolicy string

const (
	SuccessfulExitRetain SuccessfulExitPolicy = "retain"
	SuccessfulExitClose  SuccessfulExitPolicy = "close"
)

type TUIFrameMode string

const (
	TUIFrameFull  TUIFrameMode = "full"
	TUIFrameSplit TUIFrameMode = "split"
	TUIFrameNone  TUIFrameMode = "none"
)

var (
	ErrInvalid      = errors.New("config: invalid configuration")
	ErrTooLarge     = errors.New("config: file too large")
	ErrInvalidInput = errors.New("config: invalid input")
)

// Config is loaded at process startup. Shell is retained for compatibility;
// Commands.Shell is the argv-capable replacement.
type Config struct {
	Shell       string           `toml:"shell"`
	Commands    DefaultCommands  `toml:"commands"`
	DetachKey   string           `toml:"detach_key"`
	Keybindings Keymaps          `toml:"keybindings"`
	TUI         TUIOptions       `toml:"tui"`
	GUI         GUIOptions       `toml:"gui"`
	Terminal    TerminalLimits   `toml:"terminal"`
	Clipboard   ClipboardOptions `toml:"clipboard"`
	Transport   TransportLimits  `toml:"transport"`
	Attention   AttentionLimits  `toml:"attention"`
	Plugins     external.Config  `toml:"plugins"`
}

// DefaultCommands are argv vectors and are executed without a shell. Empty
// vectors select the platform/environment fallback.
type DefaultCommands struct {
	Shell  []string `toml:"shell"`
	Editor []string `toml:"editor"`
}

func (configuration Config) ShellCommand(fallback []string) []string {
	if len(configuration.Commands.Shell) != 0 {
		return append([]string(nil), configuration.Commands.Shell...)
	}
	if configuration.Shell != "" {
		return []string{configuration.Shell}
	}
	return append([]string(nil), fallback...)
}

func (configuration Config) EditorCommand(fallback []string) []string {
	if len(configuration.Commands.Editor) != 0 {
		return append([]string(nil), configuration.Commands.Editor...)
	}
	return append([]string(nil), fallback...)
}

// Keybindings maps a portable key sequence to one or more command-prompt
// commands. An empty command explicitly disables a default binding.
type Keybindings map[string]string

func DefaultKeybindings() Keybindings {
	return Keybindings{
		"ctrl-a d":      "detach",
		"ctrl-a h":      "focus left",
		"ctrl-a j":      "focus down",
		"ctrl-a k":      "focus up",
		"ctrl-a l":      "focus right",
		"ctrl-a ctrl-h": "resize left",
		"ctrl-a ctrl-j": "resize down",
		"ctrl-a ctrl-k": "resize up",
		"ctrl-a ctrl-l": "resize right",
		"ctrl-a z":      "zoom toggle",
		"ctrl-a ctrl-a": "send-key ctrl-a",
		"ctrl-a %":      "split h",
		"ctrl-a \"":     "split v",
		"ctrl-a x":      "close-confirm",
		"ctrl-a r":      "restart",
		"ctrl-a c":      "new-window",
		"ctrl-a n":      "next-window",
		"ctrl-a p":      "previous-window",
		"ctrl-a )":      "next-workspace",
		"ctrl-a (":      "previous-workspace",
		"ctrl-a :":      "command-prompt",
		"ctrl-a ,":      "command-prompt rename-window",
		"ctrl-a $":      "command-prompt rename-workspace",
		"ctrl-a [":      "copy-mode",
		"ctrl-a ]":      "paste",
		"ctrl-a s":      "stash-pane",
		"ctrl-a S":      "stash-list",
		"ctrl-a a":      "attention next",
		"ctrl-a A":      "attention prev",
		"ctrl-a m":      "attention ack",
		"ctrl-a ?":      "help",
	}
}

type AttentionLimits struct {
	MaxEntries       int   `toml:"max_entries"`
	MarkerBytes      int   `toml:"marker_bytes"`
	PluginQueueBytes int64 `toml:"plugin_queue_bytes"`
}

type ClipboardOptions struct {
	Read             ClipboardPolicy `toml:"read"`
	Write            ClipboardPolicy `toml:"write"`
	MaxTextBytes     int             `toml:"max_text_bytes"`
	CommandTimeoutMS int             `toml:"command_timeout_ms"`
}

// TUIOptions controls local presentation and is not sent to the daemon.
type TUIOptions struct {
	PaneTitle     PaneTitleMode `toml:"pane_title"`
	PaneFrame     TUIFrameMode  `toml:"pane_frame"`
	Mouse         bool          `toml:"mouse"`
	MinPaneWidth  int           `toml:"min_pane_width"`
	MinPaneHeight int           `toml:"min_pane_height"`
	Theme         Theme         `toml:"theme"`
	Status        StatusOptions `toml:"status"`
	CWD           CWDOptions    `toml:"cwd"`
}

// GUIOptions controls Windows GUI presentation. It is sent to GUI frontends
// during synchronization; daemon and TUI behavior do not depend on it.
type GUIOptions struct {
	FontFamily        string   `toml:"font_family" json:"font_family"`
	FontSize          float64  `toml:"font_size" json:"font_size"`
	SoftwareRendering bool     `toml:"software_rendering" json:"software_rendering"`
	Background        string   `toml:"background" json:"background"`
	Foreground        string   `toml:"foreground" json:"foreground"`
	Selection         string   `toml:"selection" json:"selection"`
	Accent            string   `toml:"accent" json:"accent"`
	ColorTable        []string `toml:"color_table" json:"color_table"`
}

func DefaultGUIOptions() GUIOptions {
	return GUIOptions{
		FontFamily: "Cascadia Mono", FontSize: 14,
		Background: "#0c0f13", Foreground: "#e8eaed", Selection: "#264c6b", Accent: "#5191ff",
		ColorTable: []string{
			"#000000", "#cc2222", "#22aa22", "#22aaaa", "#2277cc", "#aa22aa", "#aaaa22", "#cccccc",
			"#666666", "#ff6666", "#66dd66", "#66dddd", "#66aaff", "#dd66dd", "#dddd66", "#ffffff",
		},
	}
}

// TerminalLimits bounds PTY output retained or queued by the daemon.
type TerminalLimits struct {
	SuccessfulExit       SuccessfulExitPolicy `toml:"successful_exit"`
	HistoryBytes         int                  `toml:"history_bytes"`
	MaxTotalHistoryBytes int64                `toml:"max_total_history_bytes"`
	ReadBufferBytes      int                  `toml:"read_buffer_bytes"`
	AttachmentQueueBytes int                  `toml:"attachment_queue_bytes"`
	ObserverQueueBytes   int                  `toml:"observer_queue_bytes"`
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
		DetachKey:   DefaultDetachKey,
		Keybindings: DefaultKeymaps(),
		TUI:         TUIOptions{PaneFrame: DefaultTUIFrameMode, PaneTitle: PaneTitleAuto, Mouse: true, MinPaneWidth: 2, MinPaneHeight: 1, Theme: DefaultTheme(), Status: DefaultStatusOptions(), CWD: CWDOptions{Terminal: "pane", Tool: "startup"}},
		GUI:         DefaultGUIOptions(),
		Terminal: TerminalLimits{
			SuccessfulExit: DefaultSuccessfulExitPolicy,
			HistoryBytes:   manager.HistoryBytes, MaxTotalHistoryBytes: manager.MaxTotalHistoryBytes,
			ReadBufferBytes: manager.ReadBufferBytes, AttachmentQueueBytes: manager.AttachmentQueueBytes,
			ObserverQueueBytes: manager.ObserverQueueBytes,
		},
		Clipboard: ClipboardOptions{
			Read: DefaultClipboardPolicy, Write: DefaultClipboardPolicy,
			MaxTextBytes: DefaultClipboardMaxBytes, CommandTimeoutMS: DefaultClipboardTimeoutMS,
		},
		Transport: TransportLimits{
			MaxFrameBytes: stream.MaxFrameBytes, WriteQueueFrames: stream.WriteQueue,
			OutboundQueueBytes: peer.OutboundQueueBytes, OutboundQueueFrames: peer.OutboundQueueFrames,
			PerStreamQueueBytes: peer.PerStreamQueueBytes, PerStreamQueueFrames: peer.PerStreamQueueFrames,
		},
		Plugins:   external.DefaultConfig(),
		Attention: AttentionLimits{MaxEntries: 1024, MarkerBytes: 8 << 10, PluginQueueBytes: 1 << 20},
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
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err == nil {
		if bindings, ok := raw["keybindings"].(map[string]any); ok {
			for name, value := range bindings {
				if _, flat := value.(string); flat {
					return Config{}, fmt.Errorf("%w: flat keybindings %q is no longer supported; move it to [keybindings.normal]", ErrInvalid, name)
				}
			}
		}
	}
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
	if _, err := configuration.Plugins.Normalize(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if configuration.DetachKey == "" || strings.TrimSpace(configuration.DetachKey) == "" {
		return fmt.Errorf("%w: detach_key is empty", ErrInvalid)
	}
	if strings.ContainsRune(configuration.DetachKey, 0) {
		return fmt.Errorf("%w: detach_key contains NUL", ErrInvalid)
	}
	if err := configuration.validatePresentation(); err != nil {
		return err
	}
	if strings.TrimSpace(configuration.GUI.FontFamily) == "" || strings.ContainsRune(configuration.GUI.FontFamily, 0) {
		return fmt.Errorf("%w: gui.font_family must not be empty or contain NUL", ErrInvalid)
	}
	if configuration.GUI.FontSize < 6 || configuration.GUI.FontSize > 96 || math.IsNaN(configuration.GUI.FontSize) || math.IsInf(configuration.GUI.FontSize, 0) {
		return fmt.Errorf("%w: gui.font_size must be between 6 and 96", ErrInvalid)
	}
	if len(configuration.GUI.ColorTable) != 16 {
		return fmt.Errorf("%w: gui.color_table must contain exactly 16 colors", ErrInvalid)
	}
	for name, value := range map[string]string{"background": configuration.GUI.Background, "foreground": configuration.GUI.Foreground, "selection": configuration.GUI.Selection, "accent": configuration.GUI.Accent} {
		if !validHexColor(value) {
			return fmt.Errorf("%w: gui.%s must be #rrggbb", ErrInvalid, name)
		}
	}
	for index, value := range configuration.GUI.ColorTable {
		if !validHexColor(value) {
			return fmt.Errorf("%w: gui.color_table[%d] must be #rrggbb", ErrInvalid, index)
		}
	}
	if configuration.Shell != "" {
		if strings.TrimSpace(configuration.Shell) == "" {
			return fmt.Errorf("%w: shell contains only whitespace", ErrInvalid)
		}
		if strings.ContainsRune(configuration.Shell, 0) {
			return fmt.Errorf("%w: shell contains NUL", ErrInvalid)
		}
	}
	if configuration.Shell != "" && len(configuration.Commands.Shell) != 0 {
		return fmt.Errorf("%w: shell and commands.shell cannot both be set", ErrInvalid)
	}
	if err := validateCommand("commands.shell", configuration.Commands.Shell); err != nil {
		return err
	}
	if err := validateCommand("commands.editor", configuration.Commands.Editor); err != nil {
		return err
	}
	switch configuration.TUI.PaneFrame {
	case TUIFrameFull, TUIFrameSplit, TUIFrameNone:
	default:
		return fmt.Errorf("%w: tui.pane_frame must be full, split, or none", ErrInvalid)
	}
	terminal := configuration.Terminal
	switch terminal.SuccessfulExit {
	case SuccessfulExitRetain, SuccessfulExitClose:
	default:
		return fmt.Errorf("%w: terminal.successful_exit must be retain or close", ErrInvalid)
	}
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
	clipboard := configuration.Clipboard
	if !validClipboardPolicy(clipboard.Read) || !validClipboardPolicy(clipboard.Write) {
		return fmt.Errorf("%w: clipboard policy must be deny, ask, or allow", ErrInvalid)
	}
	if clipboard.MaxTextBytes <= 0 || clipboard.CommandTimeoutMS <= 0 {
		return fmt.Errorf("%w: clipboard limits must be positive", ErrInvalid)
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
	if configuration.Attention.MaxEntries <= 0 || configuration.Attention.MarkerBytes <= 0 || configuration.Attention.PluginQueueBytes <= 0 {
		return fmt.Errorf("%w: attention limits must be positive", ErrInvalid)
	}
	return nil
}

func validHexColor(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, value := range value[1:] {
		if !((value >= '0' && value <= '9') || (value >= 'a' && value <= 'f') || (value >= 'A' && value <= 'F')) {
			return false
		}
	}
	return true
}

func validateCommand(name string, argv []string) error {
	if len(argv) > 128 {
		return fmt.Errorf("%w: %s has too many arguments", ErrInvalid, name)
	}
	for index, argument := range argv {
		if strings.ContainsRune(argument, 0) {
			return fmt.Errorf("%w: %s argument %d contains NUL", ErrInvalid, name, index)
		}
	}
	if len(argv) != 0 && strings.TrimSpace(argv[0]) == "" {
		return fmt.Errorf("%w: %s executable is empty", ErrInvalid, name)
	}
	return nil
}

func validClipboardPolicy(policy ClipboardPolicy) bool {
	switch policy {
	case ClipboardDeny, ClipboardAsk, ClipboardAllow:
		return true
	default:
		return false
	}
}
