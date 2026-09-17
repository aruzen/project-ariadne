//go:build darwin || linux || windows

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/aruzen/ariadne/internal/client"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/platform/localipc"
	"github.com/aruzen/ariadne/internal/protocol"
)

func TestExternalPluginEndToEnd(t *testing.T) {
	if os.Getenv("ARIADNE_RUN_E2E") != "1" {
		t.Skip("set ARIADNE_RUN_E2E=1 to run real process/native plugin E2E")
	}
	_, source, _, _ := runtime.Caller(0)
	repository := filepath.Dir(filepath.Dir(source))
	directory, err := os.MkdirTemp("", "ap-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	binary := filepath.Join(os.Getenv("ARIADNE_E2E_BIN_DIR"), "ariadne"+suffix)
	if os.Getenv("ARIADNE_E2E_BIN_DIR") == "" {
		binary = filepath.Join(directory, "ariadne"+suffix)
		command := exec.Command("go", "build", "-buildvcs=false", "-o", binary, "./cmd/ariadne")
		command.Dir = repository
		if data, err := command.CombinedOutput(); err != nil {
			t.Fatalf("build: %v\n%s", err, data)
		}
	}
	endpoint := filepath.Join(directory, "plugin.sock")
	if runtime.GOOS == "windows" {
		endpoint = fmt.Sprintf(`\\.\pipe\ariadne-plugin-%d-%d`, os.Getpid(), time.Now().UnixNano())
	}
	state := filepath.Join(directory, "state.json")
	configuration := filepath.Join(directory, "config.toml")
	if err := os.WriteFile(configuration, []byte("[plugins]\napi_ms=200\ncommand_ms=1000\n"), 0600); err != nil {
		t.Fatal(err)
	}
	environment := append(os.Environ(), "ARIADNE_CONFIG_PATH="+directory)
	daemon := exec.Command(binary, "--socket", endpoint, "daemon", "serve", "--state", state, "--config", configuration)
	daemon.Env = environment
	log, err := os.Create(filepath.Join(directory, "daemon.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	daemon.Stdout = log
	daemon.Stderr = log
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop := exec.Command(binary, "--socket", endpoint, "daemon", "stop", "--force")
		stop.Env = environment
		_ = stop.Run()
		_ = daemon.Process.Kill()
		_ = daemon.Wait()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	waitForDaemon := func() {
		for {
			command := exec.CommandContext(ctx, binary, "--socket", endpoint, "daemon", "status")
			command.Env = environment
			data, err := command.CombinedOutput()
			if err == nil && !strings.Contains(string(data), "stopped") {
				break
			}
			if ctx.Err() != nil {
				t.Fatal("daemon did not start", string(data))
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	waitForDaemon()
	run := func(args ...string) string {
		t.Helper()
		command := exec.CommandContext(ctx, binary, append([]string{"--socket", endpoint}, args...)...)
		command.Env = environment
		data, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, data)
		}
		return string(data)
	}
	processDirectory := filepath.Join(directory, "process")
	if err := os.Mkdir(processDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	build := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-o", filepath.Join(processDirectory, "plugin-process"+suffix), "./examples/plugins/process")
	build.Dir = repository
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build process: %v\n%s", err, data)
	}
	data, err := os.ReadFile(filepath.Join(repository, "examples/plugins/process/manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(processDirectory, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	run("plugin", "install", processDirectory)
	disabled := exec.CommandContext(ctx, binary, "--socket", endpoint, "plugin", "run", "example-process", "echo", "unexpected")
	disabled.Env = environment
	if data, err := disabled.CombinedOutput(); err == nil {
		t.Fatal("disabled plugin ran", string(data))
	}
	run("plugin", "enable", "example-process")
	if text := run("plugin", "run", "example-process", "echo", "hello", "world"); strings.TrimSpace(text) != "hello world" {
		t.Fatal(text)
	}
	run("plugin", "grant", "example-process", "core.read", "workspace:1")
	var snapshot v1.Snapshot
	if err := json.Unmarshal([]byte(run("plugin", "run", "example-process", "snapshot")), &snapshot); err != nil || len(snapshot.Workspaces) != 1 {
		t.Fatal("authorized Core projection", err)
	}
	run("plugin", "grant", "example-process", "frontend.interact", "all")
	headless := exec.CommandContext(ctx, binary, "--socket", endpoint, "plugin", "run", "example-process", "prompt")
	headless.Env = environment
	if data, err := headless.CombinedOutput(); err == nil || !strings.Contains(string(data), "headless") {
		t.Fatal("headless dialogue", err, string(data))
	}
	connection, err := localipc.DialContext(ctx, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	frontend, err := client.Open(ctx, connection, client.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer frontend.Close()
	if _, err := frontend.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			select {
			case <-frontend.Events():
			case <-ctx.Done():
				return
			case <-frontend.Done():
				return
			}
		}
	}()
	// This command requests an editor, while its frontend makes other daemon
	// requests on the same stream. Real PTY and transient cleanup are exercised.
	frontend.SetPluginInteractionHandler(func(dialogueCtx context.Context, r v1.InteractionRequest) (v1.InteractionResult, error) {
		if r.Interaction.Kind != "editor" {
			return v1.InteractionResult{Text: "answered"}, nil
		}
		editor := []string{"/bin/sh", "-c", `printf edited > "$1"`, "editor"}
		if runtime.GOOS == "windows" {
			script := filepath.Join(directory, "editor.ps1")
			if err := os.WriteFile(script, []byte(`[System.IO.File]::WriteAllText($args[0], "edited")`), 0600); err != nil {
				return v1.InteractionResult{}, err
			}
			editor = []string{"powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script}
		}
		opened, err := frontend.Plugin(dialogueCtx, v1.ManageRequest{Action: "editor.open", Editor: &v1.EditorRequest{Argv: editor, CWD: directory, Env: environment, Text: r.Interaction.Text}})
		if err != nil {
			return v1.InteractionResult{}, err
		}
		id := opened.Editor.PaneID
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, _ = frontend.Plugin(cleanupCtx, v1.ManageRequest{Action: "editor.cancel", PaneID: id})
		}()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			result, err := frontend.Plugin(dialogueCtx, v1.ManageRequest{Action: "editor.finish", PaneID: id})
			if err == nil {
				return v1.InteractionResult{Text: result.Editor.Text}, nil
			}
			time.Sleep(10 * time.Millisecond)
		}
		return v1.InteractionResult{}, fmt.Errorf("editor did not finish")
	})
	dialogue, err := frontend.Plugin(ctx, v1.ManageRequest{Action: "run", ID: "example-process", Command: "prompt"})
	if err != nil || dialogue.Command.Text != "answered" {
		t.Fatal("prompt interaction", err)
	}
	edited, err := frontend.Plugin(ctx, v1.ManageRequest{Action: "run", ID: "example-process", Command: "editor"})
	if err != nil || edited.Command.Text != "edited" {
		t.Fatal("PTY editor", err)
	}
	toolJSON := run("tool", "new", "--provider", "example-process", "demo")
	var paneID uint64
	if _, err := fmt.Sscanf(toolJSON, "tool pane %d created", &paneID); err != nil {
		t.Fatal(toolJSON, err)
	}
	view := v1.View{ID: "first", Generation: 1, PaneID: paneID, Width: 40, Height: 4}
	frame, err := frontend.Plugin(ctx, v1.ManageRequest{Action: "render", ID: "example-process", View: &view})
	if err != nil || frame.Frame.Width != 40 {
		t.Fatal("process Tool frame", err)
	}
	view.RuntimeGeneration = frame.Frame.RuntimeGeneration
	if _, err := frontend.Plugin(ctx, v1.ManageRequest{Action: "input", ID: "example-process", Input: &v1.Input{View: view, Data: []byte("hello"), Paste: true}}); err != nil {
		t.Fatal(err)
	}
	widget, err := frontend.Plugin(ctx, v1.ManageRequest{Action: "widget", ID: "example-process", Widget: "input-count"})
	if err != nil || widget.Widget.Text != "input:1" {
		t.Fatal("process widget", err)
	}
	t.Run("native", func(t *testing.T) {
		compiler := os.Getenv("CC")
		if compiler == "" {
			compiler = "cc"
			if runtime.GOOS == "windows" {
				compiler = "gcc"
			}
		}
		argv := strings.Fields(compiler)
		if _, err := exec.LookPath(argv[0]); err != nil {
			if _, err := exec.LookPath("zig"); err != nil {
				t.Skip("native compiler unavailable")
			}
			argv = []string{"zig", "cc"}
		}
		nativeDirectory := filepath.Join(directory, "native")
		if err := os.Mkdir(nativeDirectory, 0700); err != nil {
			t.Fatal(err)
		}
		name := "plugin.so"
		flags := []string{"-shared", "-fPIC"}
		if runtime.GOOS == "darwin" {
			name = "plugin.dylib"
			flags = []string{"-dynamiclib"}
		}
		if runtime.GOOS == "windows" {
			name = "plugin.dll"
			flags = []string{"-shared"}
		}
		compile := exec.CommandContext(ctx, argv[0], append(append(argv[1:], flags...), "-o", filepath.Join(nativeDirectory, name), filepath.Join(repository, "examples/plugins/native/plugin.c"))...)
		if data, err := compile.CombinedOutput(); err != nil {
			t.Fatalf("native build: %v\n%s", err, data)
		}
		cpp := exec.CommandContext(ctx, argv[0], append(append(argv[1:], flags...), "-x", "c++", "-Wall", "-Wextra", "-Werror", "-o", filepath.Join(nativeDirectory, "cpp-"+name), filepath.Join(repository, "examples/plugins/native/plugin.c"))...)
		if data, err := cpp.CombinedOutput(); err != nil {
			t.Fatalf("C++ ABI build: %v\n%s", err, data)
		}
		manifest, err := os.ReadFile(filepath.Join(repository, "examples/plugins/native/manifest.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nativeDirectory, "manifest.json"), manifest, 0600); err != nil {
			t.Fatal(err)
		}
		run("plugin", "install", nativeDirectory)
		run("plugin", "enable", "example-native")
		if text := run("plugin", "run", "example-native", "hello"); !strings.Contains(text, "Hello from native C/C++") {
			t.Fatal(text)
		}
		toolJSON := run("tool", "new", "--provider", "example-native", "demo")
		var nativePaneID uint64
		if _, err := fmt.Sscanf(toolJSON, "tool pane %d created", &nativePaneID); err != nil {
			t.Fatal(toolJSON, err)
		}
		view := v1.View{ID: "native", Generation: 1, PaneID: nativePaneID, Width: 40, Height: 4}
		frame, err := frontend.Plugin(ctx, v1.ManageRequest{Action: "render", ID: "example-native", View: &view})
		if err != nil || len(frame.Frame.Cells) != 160 {
			t.Fatal("native Tool frame", err)
		}
		run("plugin", "disable", "example-native")
		// Faults run inside the same helper binary used for installed native plugins.
		// The healthy process plugin must survive every fault and manual restart.
		faultDirectory := filepath.Join(directory, "native-fault")
		if err := os.Mkdir(faultDirectory, 0700); err != nil {
			t.Fatal(err)
		}
		source, err := os.ReadFile(filepath.Join(repository, "examples/plugins/native/plugin.c"))
		if err != nil {
			t.Fatal(err)
		}
		code := strings.Replace(string(source), `"../../../api/plugin/ariadne_plugin.h"`, fmt.Sprintf("%q", filepath.ToSlash(filepath.Join(repository, "api/plugin/ariadne_plugin.h"))), 1)
		code = strings.Replace(code, `else if(strstr(json,"\"method\":\"command\"")) result`, `else if(strstr(json,"\"method\":\"command\"")) {
 if(strstr(json,"\"name\":\"crash\"")) abort();
 if(strstr(json,"\"name\":\"hang\"")) { for(;;) {} }
 if(strstr(json,"\"name\":\"malformed\"")) { host->send((const uint8_t *)"{}",2); free(json); return 0; }
 result`, 1)
		code = strings.Replace(code, `"{\"text\":\"Hello from native C/C++\"}";`, `"{\"text\":\"Hello from native C/C++\"}"; }`, 1)
		faultSource := filepath.Join(faultDirectory, "fault.c")
		if err := os.WriteFile(faultSource, []byte(code), 0600); err != nil {
			t.Fatal(err)
		}
		compileFault := exec.CommandContext(ctx, argv[0], append(append(argv[1:], flags...), "-o", filepath.Join(faultDirectory, name), faultSource)...)
		if data, err := compileFault.CombinedOutput(); err != nil {
			t.Fatalf("fault build: %v\n%s", err, data)
		}
		var faultManifest v1.Manifest
		if err := json.Unmarshal(manifest, &faultManifest); err != nil {
			t.Fatal(err)
		}
		faultManifest.ID = "native-fault"
		faultManifest.Commands = []v1.Declaration{{Name: "crash"}, {Name: "hang"}, {Name: "malformed"}}
		faultManifest.Tools = nil
		faultManifest.Widgets = nil
		faultJSON, _ := json.Marshal(faultManifest)
		if err := os.WriteFile(filepath.Join(faultDirectory, "manifest.json"), faultJSON, 0600); err != nil {
			t.Fatal(err)
		}
		run("plugin", "install", faultDirectory)
		run("plugin", "enable", "native-fault")
		for _, fault := range []string{"crash", "hang", "malformed"} {
			command := exec.CommandContext(ctx, binary, "--socket", endpoint, "plugin", "run", "native-fault", fault)
			command.Env = environment
			if data, err := command.CombinedOutput(); err == nil {
				t.Fatal("native fault succeeded", fault, string(data))
			}
			if text := run("plugin", "run", "example-process", "echo", "still alive"); strings.TrimSpace(text) != "still alive" {
				t.Fatal(text)
			}
			var status v1.ManageResult
			if err := json.Unmarshal([]byte(run("plugin", "--json", "status", "native-fault")), &status); err != nil || len(status.Plugins) != 1 || !status.Plugins[0].Enabled || status.Plugins[0].Running {
				t.Fatal("fault isolation/status", err, status)
			}
			run("plugin", "restart", "native-fault")
		}
		run("plugin", "disable", "native-fault")
		logs, err := os.ReadFile(filepath.Join(directory, "plugins", "data", "example-native", "stderr.log"))
		if err != nil || !strings.Contains(string(logs), "native stdout is a log") {
			t.Fatal("native stdout separation", err, string(logs))
		}
	})
	// Restore both the plugin registry/private directory and Core Tool state in a
	// new daemon process, rather than reusing the old Core executor.
	descriptor := core.ToolDescriptor{Provider: "example-process", Type: "demo", Instance: "default"}
	if _, err := client.Call[core.ToolStateResult](ctx, frontend, protocol.OperationUpdateToolState, protocol.UpdateToolStateParams{Descriptor: descriptor, ExpectedGeneration: 1, StateVersion: 1, State: json.RawMessage(`{"persisted":true}`)}); err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(directory, "plugins", "data", "example-process", "private.txt")
	if err := os.WriteFile(privatePath, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	_ = frontend.Close()
	run("daemon", "stop", "--force")
	if err := daemon.Wait(); err != nil {
		t.Fatal("stop original daemon", err)
	}
	daemon = exec.Command(binary, "--socket", endpoint, "daemon", "serve", "--state", state, "--config", configuration)
	daemon.Env = environment
	daemon.Stdout = log
	daemon.Stderr = log
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	waitForDaemon()
	var restored v1.ManageResult
	if err := json.Unmarshal([]byte(run("plugin", "--json", "status", "example-process")), &restored); err != nil || len(restored.Plugins) != 1 || !restored.Plugins[0].Enabled || !restored.Plugins[0].Running || len(restored.Plugins[0].Grants) != 2 {
		t.Fatal("registry restore", err, restored)
	}
	if data, err := os.ReadFile(privatePath); err != nil || string(data) != "retained" {
		t.Fatal("private data restore", err)
	}
	if err := json.Unmarshal([]byte(run("plugin", "run", "example-process", "snapshot")), &snapshot); err != nil {
		t.Fatal(err)
	}
	persisted := false
	for _, raw := range snapshot.ToolInstances {
		var tool v1.ToolInstance
		if err := json.Unmarshal(raw, &tool); err != nil {
			t.Fatal(err)
		}
		if tool.Descriptor.Provider == "example-process" && tool.Descriptor.Type == "demo" && string(tool.State) == `{"persisted":true}` && tool.Generation == 2 {
			persisted = true
		}
	}
	if !persisted {
		t.Fatal("Tool state not restored", snapshot)
	}
}
