package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
	if !reflect.DeepEqual(configuration, Default()) {
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
		{name: "unknown successful exit policy", data: "[terminal]\nsuccessful_exit = \"unknown\"\n"},
		{name: "unknown clipboard read policy", data: "[clipboard]\nread = \"unknown\"\n"},
		{name: "unknown clipboard write policy", data: "[clipboard]\nwrite = \"unknown\"\n"},
		{name: "zero clipboard bytes", data: "[clipboard]\nmax_text_bytes = 0\n"},
		{name: "zero clipboard timeout", data: "[clipboard]\ncommand_timeout_ms = 0\n"},
		{name: "negative history", data: "[terminal]\nhistory_bytes = -1\n"},
		{name: "partial history disable", data: "[terminal]\nhistory_bytes = 0\n"},
		{name: "history exceeds total", data: "[terminal]\nhistory_bytes = 1024\nmax_total_history_bytes = 512\n"},
		{name: "zero attachment queue", data: "[terminal]\nattachment_queue_bytes = 0\n"},
		{name: "negative write queue", data: "[transport]\nwrite_queue_frames = -1\n"},
		{name: "stream bytes exceed total", data: "[transport]\nper_stream_queue_bytes = 32\noutbound_queue_bytes = 16\n"},
		{name: "stream frames exceed total", data: "[transport]\nper_stream_queue_frames = 32\noutbound_queue_frames = 16\n"},
		{name: "zero attention entries", data: "[attention]\nmax_entries = 0\n"},
		{name: "zero marker bytes", data: "[attention]\nmarker_bytes = 0\n"},
		{name: "zero plugin queue", data: "[attention]\nplugin_queue_bytes = 0\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Parse([]byte(test.data)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Parse error = %v", err)
			}
		})
	}
}

func TestParseAttentionLimits(t *testing.T) {
	configuration, err := Parse([]byte("[attention]\nmax_entries = 32\nmarker_bytes = 4096\nplugin_queue_bytes = 65536\n"))
	if err != nil {
		t.Fatalf("Parse attention limits: %v", err)
	}
	if configuration.Attention.MaxEntries != 32 || configuration.Attention.MarkerBytes != 4096 || configuration.Attention.PluginQueueBytes != 65536 {
		t.Fatalf("Attention = %+v", configuration.Attention)
	}
}

func TestParseSuccessfulExitPolicy(t *testing.T) {
	configuration, err := Parse([]byte("[terminal]\nsuccessful_exit = \"close\"\n"))
	if err != nil {
		t.Fatalf("Parse successful exit policy: %v", err)
	}
	if configuration.Terminal.SuccessfulExit != SuccessfulExitClose {
		t.Fatalf("SuccessfulExit = %q", configuration.Terminal.SuccessfulExit)
	}
}

func TestParseClipboardOptions(t *testing.T) {
	configuration, err := Parse([]byte("[clipboard]\nread = \"deny\"\nwrite = \"allow\"\nmax_text_bytes = 4096\ncommand_timeout_ms = 75\n"))
	if err != nil {
		t.Fatalf("Parse clipboard options: %v", err)
	}
	if configuration.Clipboard.Read != ClipboardDeny || configuration.Clipboard.Write != ClipboardAllow ||
		configuration.Clipboard.MaxTextBytes != 4096 || configuration.Clipboard.CommandTimeoutMS != 75 {
		t.Fatalf("Clipboard = %+v", configuration.Clipboard)
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

func TestDefaultCommandArgvAndLegacyShell(t *testing.T) {
	configuration, err := Parse([]byte("[commands]\nshell = [\"/bin/zsh\", \"-l\"]\neditor = [\"nvim\", \"-f\"]\n"))
	if err != nil {
		t.Fatalf("Parse commands: %v", err)
	}
	if got := configuration.ShellCommand([]string{"fallback"}); !reflect.DeepEqual(got, []string{"/bin/zsh", "-l"}) {
		t.Fatalf("shell command = %#v", got)
	}
	if got := configuration.EditorCommand([]string{"fallback"}); !reflect.DeepEqual(got, []string{"nvim", "-f"}) {
		t.Fatalf("editor command = %#v", got)
	}

	legacy, err := Parse([]byte("shell = \"fish\"\n"))
	if err != nil {
		t.Fatalf("Parse legacy shell: %v", err)
	}
	if got := legacy.ShellCommand(nil); !reflect.DeepEqual(got, []string{"fish"}) {
		t.Fatalf("legacy shell command = %#v", got)
	}
}

func TestDefaultCommandValidation(t *testing.T) {
	for _, data := range []string{
		"[commands]\nshell = [\"\"]\n",
		"[commands]\neditor = [\"vi\\u0000bad\"]\n",
		"shell = \"sh\"\n[commands]\nshell = [\"zsh\"]\n",
	} {
		if _, err := Parse([]byte(data)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Parse(%q) error = %v", data, err)
		}
	}
}

func TestParseKeybindingOverridesAndUnbinds(t *testing.T) {
	configuration, err := Parse([]byte("[keybindings.normal]\n\"ctrl-a h\" = \"focus right; zoom on\"\n\"ctrl-a x\" = \"\"\n\"ctrl-a q\" = \"detach\"\n"))
	if err != nil {
		t.Fatalf("Parse keybindings: %v", err)
	}
	if got := configuration.Keybindings.Normal["ctrl-a h"]; got != "focus right; zoom on" {
		t.Fatalf("overridden binding = %q", got)
	}
	if got := configuration.Keybindings.Normal["ctrl-a x"]; got != "" {
		t.Fatalf("disabled binding = %q", got)
	}
	if got := configuration.Keybindings.Normal["ctrl-a q"]; got != "detach" {
		t.Fatalf("new binding = %q", got)
	}
	if got := configuration.Keybindings.Normal["ctrl-a j"]; got != "focus down" {
		t.Fatalf("default binding was not preserved: %q", got)
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
	if !reflect.DeepEqual(configuration, Default()) {
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

func TestTemplateParsesAsDefaults(t *testing.T) {
	configuration, err := Parse([]byte(Template()))
	if err != nil {
		t.Fatalf("Parse template: %v", err)
	}
	if !reflect.DeepEqual(configuration, Default()) {
		t.Fatalf("template configuration = %+v, want defaults", configuration)
	}
}

func TestWriteTemplateCreatesAndDoesNotReplace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.toml")
	if err := WriteTemplate(path); err != nil {
		t.Fatalf("WriteTemplate: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != Template() {
		t.Fatal("created file does not contain the embedded template")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if permission := info.Mode().Perm(); permission != 0o600 {
			t.Fatalf("file permission = %o, want 600", permission)
		}
	}
	if err := WriteTemplate(path); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("second WriteTemplate error = %v", err)
	}
	dataAfter, err := os.ReadFile(path)
	if err != nil || string(dataAfter) != string(data) {
		t.Fatalf("existing file changed: data=%q error=%v", dataAfter, err)
	}
}

func TestWriteTemplateRejectsEmptyPath(t *testing.T) {
	if err := WriteTemplate(""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("WriteTemplate error = %v", err)
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

func TestPluginLimitsAndStatusWidgetConfiguration(t *testing.T) {
	c, err := Parse([]byte("[plugins]\ncommand_ms=1234\ncontrol_queue=7\n[tui.status]\nright=[\"example/status\"]\n"))
	if err != nil || c.Plugins.CommandMS != 1234 || c.Plugins.ControlQueue != 7 || c.Plugins.MessageBytes != 8<<20 {
		t.Fatal("plugin configuration", err, c.Plugins)
	}
	for _, data := range []string{
		"[plugins]\napi_ms=-1\n",
		"[plugins]\ncommand_ms=9223372036854775807\n",
		"[tui.status.widgets.clock]\nplugin=\"example/status\"\n",
		"[tui.status.widgets.x]\nplugin=\"example/status\"\ncommand=[\"shell\"]\n",
	} {
		if _, err := Parse([]byte(data)); err == nil {
			t.Fatal("invalid plugin limits/builtin override accepted", data)
		}
	}
}
