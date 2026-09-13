package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aruzen/streammux"
	"github.com/aruzen/streammux/pty"
)

func TestParseDefaultsAndOverrides(t *testing.T) {
	configuration, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse defaults: %v", err)
	}
	if configuration != Default() {
		t.Fatalf("defaults = %+v, want %+v", configuration, Default())
	}
	configuration, err = Parse([]byte("shell = \"/bin/zsh\"\ndetach_key = \"ctrl-b x\"\n"))
	if err != nil {
		t.Fatalf("Parse overrides: %v", err)
	}
	if configuration.Shell != "/bin/zsh" || configuration.DetachKey != "ctrl-b x" {
		t.Fatalf("overrides = %+v", configuration)
	}
}

func TestParseIsStrict(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
	}{
		{name: "unknown key", data: "unknown = true\n"},
		{name: "unknown table", data: "[unknown]\nvalue = 1\n"},
		{name: "wrong type", data: "detach_key = 1\n"},
		{name: "duplicate", data: "shell = \"a\"\nshell = \"b\"\n"},
		{name: "blank detach", data: "detach_key = \"   \"\n"},
		{name: "blank shell", data: "shell = \"   \"\n"},
		{name: "unknown TUI frame", data: "[tui]\npane_frame = \"unknown\"\n"},
		{name: "negative history", data: "[terminal]\nhistory_bytes = -1\n"},
		{name: "partial history disable", data: "[terminal]\nhistory_bytes = 0\n"},
		{name: "history exceeds total", data: "[terminal]\nhistory_bytes = 1024\nmax_total_history_bytes = 512\n"},
		{name: "zero attachment queue", data: "[terminal]\nattachment_queue_bytes = 0\n"},
		{name: "negative write queue", data: "[transport]\nwrite_queue_frames = -1\n"},
		{name: "stream bytes exceed total", data: "[transport]\nper_stream_queue_bytes = 32\noutbound_queue_bytes = 16\n"},
		{name: "stream frames exceed total", data: "[transport]\nper_stream_queue_frames = 32\noutbound_queue_frames = 16\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Parse([]byte(test.data)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Parse error = %v", err)
			}
		})
	}
}

func TestParseTUIFrameMode(t *testing.T) {
	configuration, err := Parse([]byte("[tui]\npane_frame = \"split\"\n"))
	if err != nil {
		t.Fatalf("Parse TUI frame: %v", err)
	}
	if configuration.TUI.PaneFrame != TUIFrameSplit {
		t.Fatalf("PaneFrame = %q", configuration.TUI.PaneFrame)
	}
}

func TestParseAndApplyBufferLimits(t *testing.T) {
	configuration, err := Parse([]byte(`
[terminal]
history_bytes = 1024
max_total_history_bytes = 4096
read_buffer_bytes = 128
attachment_queue_bytes = 2048
observer_queue_bytes = 512

[transport]
max_frame_bytes = 256
write_queue_frames = 7
outbound_queue_bytes = 8192
outbound_queue_frames = 64
per_stream_queue_bytes = 1024
per_stream_queue_frames = 8
`))
	if err != nil {
		t.Fatalf("Parse limits: %v", err)
	}
	manager := pty.DefaultManagerConfig()
	stream := streammux.DefaultConfig()
	peer := streammux.DefaultPeerConfig()
	configuration.ApplyManager(&manager)
	configuration.ApplyStream(&stream)
	configuration.ApplyPeer(&peer)
	if manager.HistoryBytes != 1024 || manager.MaxTotalHistoryBytes != 4096 || manager.ReadBufferBytes != 128 ||
		manager.AttachmentQueueBytes != 2048 || manager.ObserverQueueBytes != 512 {
		t.Fatalf("Manager limits = %+v", manager)
	}
	if stream.MaxFrameBytes != 256 || stream.WriteQueue != 7 {
		t.Fatalf("Stream limits = %+v", stream)
	}
	if peer.OutboundQueueBytes != 8192 || peer.OutboundQueueFrames != 64 ||
		peer.PerStreamQueueBytes != 1024 || peer.PerStreamQueueFrames != 8 {
		t.Fatalf("Peer limits = %+v", peer)
	}
}

func TestZeroHistoryDisablesRetention(t *testing.T) {
	configuration, err := Parse([]byte("[terminal]\nhistory_bytes = 0\nmax_total_history_bytes = 0\n"))
	if err != nil {
		t.Fatalf("Parse disabled history: %v", err)
	}
	if configuration.Terminal.HistoryBytes != 0 || configuration.Terminal.MaxTotalHistoryBytes != 0 {
		t.Fatalf("history limits = %+v", configuration.Terminal)
	}
}

func TestLoadMissingAndExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	configuration, err := Load(path)
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if configuration != Default() {
		t.Fatalf("missing config = %+v", configuration)
	}
	if err := os.WriteFile(path, []byte("shell = \"fish\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	configuration, err = Load(path)
	if err != nil {
		t.Fatalf("Load existing: %v", err)
	}
	if configuration.Shell != "fish" || configuration.DetachKey != DefaultDetachKey {
		t.Fatalf("existing config = %+v", configuration)
	}
}

func TestLoadRejectsOversize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", DefaultMaxBytes+1)), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := Load(path); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsEmptyPath(t *testing.T) {
	if _, err := Load(""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Load error = %v", err)
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("shell = \"/bin/sh\"\n"))
	f.Add([]byte("unknown = true\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Parse(data)
	})
}
