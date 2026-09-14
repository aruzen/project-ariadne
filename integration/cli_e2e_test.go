//go:build darwin || linux

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

const runE2EEnvironment = "ARIADNE_RUN_E2E"
const e2eBinaryDirectoryEnvironment = "ARIADNE_E2E_BIN_DIR"

type e2eRuntime struct {
	clientPath  string
	socketPath  string
	environment []string
}

type terminalRead struct {
	data []byte
	err  error
}

type terminalSession struct {
	*os.File
	reads  <-chan terminalRead
	output strings.Builder
}

type listResult struct {
	Entries []struct {
		Pane struct {
			ID       uint64 `json:"id"`
			Terminal *struct {
				ID    *uint64 `json:"id"`
				State string  `json:"state"`
			} `json:"terminal"`
		} `json:"pane"`
	} `json:"entries"`
}

func TestCLIEndToEnd(t *testing.T) {
	if os.Getenv(runE2EEnvironment) != "1" {
		t.Skip("set ARIADNE_RUN_E2E=1 to run the real daemon/PTY integration test")
	}
	runtime := newE2ERuntime(t)
	runtime.testConcurrentAutoStart(t)
	runtime.testNewKill(t)
	runtime.testStashRestore(t)
	runtime.testAbnormalExitDismiss(t)
	runtime.testRestart(t)
	runtime.testDaemonRestartRestoresPlaceholder(t)
	runtime.testOpenDetachReattachResize(t)
}

func (runtime *e2eRuntime) testStashRestore(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := runtime.run(ctx, "new", "--", "/bin/sh", "-c", "sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	paneID, _ := parseTerminalResult(t, output, "created")
	if _, err := runtime.run(ctx, "stash", "pane", fmt.Sprint(paneID)); err != nil {
		t.Fatal(err)
	}
	listed, err := runtime.run(ctx, "stash", "list")
	if err != nil || !strings.Contains(listed, "pane") || !strings.Contains(listed, fmt.Sprint(paneID)) || !strings.Contains(listed, "running") {
		t.Fatalf("stash list = %q, %v", listed, err)
	}
	if _, err := runtime.run(ctx, "restore", "pane", fmt.Sprint(paneID)); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.run(ctx, "kill", fmt.Sprint(paneID)); err != nil {
		t.Fatal(err)
	}
	runtime.waitForEntries(t, ctx, func(result listResult) bool { return len(result.Entries) == 0 })
}

func newE2ERuntime(t *testing.T) *e2eRuntime {
	t.Helper()
	binaryDirectory := os.Getenv(e2eBinaryDirectoryEnvironment)
	if binaryDirectory == "" {
		_, source, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("resolve integration source path")
		}
		repository := filepath.Dir(filepath.Dir(source))
		binaryDirectory = t.TempDir()
		buildBinary(t, repository, filepath.Join(binaryDirectory, "ariadne"), "./cmd/ariadne")
	}
	clientPath := filepath.Join(binaryDirectory, "ariadne")
	if info, err := os.Stat(clientPath); err != nil || info.IsDir() {
		t.Fatalf("E2E executable %q is unavailable: %v", clientPath, err)
	}

	runtimeDirectory, err := os.MkdirTemp("", "ariadne-e2e-")
	if err != nil {
		t.Fatalf("create runtime directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDirectory) })
	instance := &e2eRuntime{
		clientPath: clientPath,
		socketPath: filepath.Join(runtimeDirectory, "ariadned.sock"),
		environment: overrideEnvironment(map[string]string{
			"XDG_CONFIG_HOME": filepath.Join(runtimeDirectory, "config"),
			"XDG_STATE_HOME":  filepath.Join(runtimeDirectory, "state"),
			"TMPDIR":          runtimeDirectory,
		}),
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = instance.run(ctx, "daemon", "stop", "--force")
	})
	return instance
}

func buildBinary(t *testing.T, repository, output, packagePath string) {
	t.Helper()
	command := exec.Command("go", "build", "-race", "-o", output, packagePath)
	command.Dir = repository
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", packagePath, err, data)
	}
}

func overrideEnvironment(overrides map[string]string) []string {
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[name]; !replaced {
			environment = append(environment, entry)
		}
	}
	for name, value := range overrides {
		environment = append(environment, name+"="+value)
	}
	return environment
}

func (runtime *e2eRuntime) command(ctx context.Context, executable string, arguments ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Env = runtime.environment
	return command
}

func (runtime *e2eRuntime) run(ctx context.Context, arguments ...string) (string, error) {
	arguments = append([]string{"-socket", runtime.socketPath}, arguments...)
	command := runtime.command(ctx, runtime.clientPath, arguments...)
	data, err := command.CombinedOutput()
	if err != nil {
		return string(data), fmt.Errorf("ariadne %s: %w: %s", strings.Join(arguments[2:], " "), err, data)
	}
	return string(data), nil
}

func (runtime *e2eRuntime) testConcurrentAutoStart(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const clients = 12
	errorsByClient := make(chan error, clients)
	var group sync.WaitGroup
	for index := 0; index < clients; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			output, err := runtime.run(ctx, "list", "--json")
			if err == nil {
				var result listResult
				err = json.Unmarshal([]byte(output), &result)
			}
			errorsByClient <- err
		}()
	}
	group.Wait()
	close(errorsByClient)
	for err := range errorsByClient {
		if err != nil {
			t.Fatalf("concurrent daemon auto-start: %v", err)
		}
	}

	duplicate := runtime.command(ctx, runtime.clientPath, "-socket", runtime.socketPath, "daemon", "serve")
	if data, err := duplicate.CombinedOutput(); err == nil {
		t.Fatalf("second daemon unexpectedly started: %s", data)
	}
	status, err := runtime.run(ctx, "daemon", "status")
	if err != nil || !strings.HasPrefix(status, "running ") {
		t.Fatalf("daemon status = %q, %v", status, err)
	}
}

func (runtime *e2eRuntime) testNewKill(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, err := runtime.run(ctx, "new", "--", "/bin/sh", "-c", "sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	paneID, _ := parseTerminalResult(t, output, "created")
	if _, err := runtime.run(ctx, "kill", fmt.Sprint(paneID)); err != nil {
		t.Fatal(err)
	}
	runtime.waitForEntries(t, ctx, func(result listResult) bool { return len(result.Entries) == 0 })
}

func (runtime *e2eRuntime) testAbnormalExitDismiss(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, err := runtime.run(ctx, "new", "--", "/bin/sh", "-c", "exit 7")
	if err != nil {
		t.Fatal(err)
	}
	paneID, _ := parseTerminalResult(t, output, "created")
	runtime.waitForEntries(t, ctx, func(result listResult) bool {
		return len(result.Entries) == 1 && result.Entries[0].Pane.ID == paneID &&
			result.Entries[0].Pane.Terminal != nil && result.Entries[0].Pane.Terminal.State == "exited"
	})
	if _, err := runtime.run(ctx, "dismiss", fmt.Sprint(paneID)); err != nil {
		t.Fatal(err)
	}
	runtime.waitForEntries(t, ctx, func(result listResult) bool { return len(result.Entries) == 0 })
}

func (runtime *e2eRuntime) testRestart(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	marker := filepath.Join(filepath.Dir(runtime.socketPath), "restart-marker")
	script := `if [ -e "$1" ]; then sleep 30; else : >"$1"; exit 8; fi`
	output, err := runtime.run(ctx, "new", "--", "/bin/sh", "-c", script, "ariadne-e2e", marker)
	if err != nil {
		t.Fatal(err)
	}
	paneID, oldTerminalID := parseTerminalResult(t, output, "created")
	runtime.waitForEntries(t, ctx, func(result listResult) bool {
		return len(result.Entries) == 1 && result.Entries[0].Pane.Terminal != nil &&
			result.Entries[0].Pane.Terminal.State == "exited"
	})
	restarted, err := runtime.run(ctx, "restart", fmt.Sprint(paneID))
	if err != nil {
		t.Fatal(err)
	}
	_, newTerminalID := parseTerminalResult(t, restarted, "restarted")
	if newTerminalID == oldTerminalID {
		t.Fatalf("restart reused runtime TerminalID %d", oldTerminalID)
	}
	runtime.waitForEntries(t, ctx, func(result listResult) bool {
		return len(result.Entries) == 1 && result.Entries[0].Pane.Terminal != nil &&
			result.Entries[0].Pane.Terminal.ID != nil && *result.Entries[0].Pane.Terminal.ID == newTerminalID &&
			result.Entries[0].Pane.Terminal.State == "running"
	})
	if _, err := runtime.run(ctx, "kill", fmt.Sprint(paneID)); err != nil {
		t.Fatal(err)
	}
	runtime.waitForEntries(t, ctx, func(result listResult) bool { return len(result.Entries) == 0 })
}

func (runtime *e2eRuntime) testDaemonRestartRestoresPlaceholder(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := runtime.run(ctx, "new", "--", "/bin/sh", "-c", "sleep 30")
	if err != nil {
		t.Fatal(err)
	}
	paneID, _ := parseTerminalResult(t, output, "created")
	if _, err := runtime.run(ctx, "daemon", "stop", "--force"); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Lstat(runtime.socketPath); errors.Is(err, os.ErrNotExist) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for daemon socket removal: %v", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}

	result, err := runtime.list(ctx)
	if err != nil {
		t.Fatalf("auto-start daemon after stop: %v", err)
	}
	if len(result.Entries) != 1 || result.Entries[0].Pane.ID != paneID || result.Entries[0].Pane.Terminal == nil ||
		result.Entries[0].Pane.Terminal.ID != nil || result.Entries[0].Pane.Terminal.State != "placeholder" {
		t.Fatalf("restored placeholder = %+v", result)
	}
	restarted, err := runtime.run(ctx, "restart", fmt.Sprint(paneID))
	if err != nil {
		t.Fatal(err)
	}
	_, terminalID := parseTerminalResult(t, restarted, "restarted")
	runtime.waitForEntries(t, ctx, func(result listResult) bool {
		return len(result.Entries) == 1 && result.Entries[0].Pane.Terminal != nil &&
			result.Entries[0].Pane.Terminal.ID != nil && *result.Entries[0].Pane.Terminal.ID == terminalID &&
			result.Entries[0].Pane.Terminal.State == "running"
	})
	if _, err := runtime.run(ctx, "kill", fmt.Sprint(paneID)); err != nil {
		t.Fatal(err)
	}
	runtime.waitForEntries(t, ctx, func(result listResult) bool { return len(result.Entries) == 0 })
}

func (runtime *e2eRuntime) testOpenDetachReattachResize(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	open, terminal := runtime.startPTY(t, ctx, "open", "--", "/bin/sh")
	if _, err := terminal.Write([]byte("printf 'ARIADNE_PTY_E2E\\n'; stty size\n")); err != nil {
		t.Fatalf("write open terminal: %v", err)
	}
	readUntil(t, terminal, "ARIADNE_PTY_E2E", "30 90")
	if err := pty.Setsize(terminal.File, &pty.Winsize{Rows: 42, Cols: 111}); err != nil {
		t.Fatalf("resize frontend PTY: %v", err)
	}
	if _, err := terminal.Write([]byte("sleep 1; stty size\n")); err != nil {
		t.Fatalf("write resized stty command: %v", err)
	}
	readUntil(t, terminal, "42 111")
	if _, err := terminal.Write([]byte{1, 'd'}); err != nil {
		t.Fatalf("write detach sequence: %v", err)
	}
	waitCommand(t, open, 10*time.Second)
	_ = terminal.Close()

	result := runtime.waitForEntries(t, ctx, func(result listResult) bool {
		return len(result.Entries) == 1 && result.Entries[0].Pane.Terminal != nil &&
			result.Entries[0].Pane.Terminal.ID != nil && result.Entries[0].Pane.Terminal.State == "running"
	})
	terminalID := *result.Entries[0].Pane.Terminal.ID
	attach, attachedTerminal := runtime.startPTY(t, ctx, "attach", fmt.Sprint(terminalID))
	readUntil(t, attachedTerminal, "ARIADNE_PTY_E2E")
	if _, err := attachedTerminal.Write([]byte("exit\n")); err != nil {
		t.Fatalf("write shell exit: %v", err)
	}
	waitCommand(t, attach, 10*time.Second)
	_ = attachedTerminal.Close()
	runtime.waitForEntries(t, ctx, func(result listResult) bool {
		return len(result.Entries) == 1 && result.Entries[0].Pane.ID != 0 && result.Entries[0].Pane.Terminal != nil &&
			result.Entries[0].Pane.Terminal.State == "exited"
	})
	if _, err := runtime.run(ctx, "dismiss", fmt.Sprint(result.Entries[0].Pane.ID)); err != nil {
		t.Fatal(err)
	}
	runtime.waitForEntries(t, ctx, func(result listResult) bool { return len(result.Entries) == 0 })
}

func (runtime *e2eRuntime) startPTY(t *testing.T, ctx context.Context, arguments ...string) (*exec.Cmd, *terminalSession) {
	t.Helper()
	arguments = append([]string{"-socket", runtime.socketPath}, arguments...)
	command := runtime.command(ctx, runtime.clientPath, arguments...)
	terminal, err := pty.StartWithSize(command, &pty.Winsize{Rows: 30, Cols: 90})
	if err != nil {
		t.Fatalf("start PTY command: %v", err)
	}
	reads := make(chan terminalRead, 64)
	go func() {
		buffer := make([]byte, 4096)
		for {
			count, err := terminal.Read(buffer)
			result := terminalRead{err: err}
			if count != 0 {
				result.data = append([]byte(nil), buffer[:count]...)
			}
			reads <- result
			if err != nil {
				return
			}
		}
	}()
	return command, &terminalSession{File: terminal, reads: reads}
}

func (runtime *e2eRuntime) list(ctx context.Context) (listResult, error) {
	output, err := runtime.run(ctx, "list", "--json")
	if err != nil {
		return listResult{}, err
	}
	var result listResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return listResult{}, fmt.Errorf("decode list output %q: %w", output, err)
	}
	return result, nil
}

func (runtime *e2eRuntime) waitForEntries(t *testing.T, ctx context.Context, predicate func(listResult) bool) listResult {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		result, err := runtime.list(ctx)
		if err == nil && predicate(result) {
			return result
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for list state: %v (last result=%+v, last error=%v)", ctx.Err(), result, err)
		case <-ticker.C:
		}
	}
}

func parseTerminalResult(t *testing.T, output, verb string) (uint64, uint64) {
	t.Helper()
	var paneID, terminalID uint64
	if _, err := fmt.Sscanf(strings.TrimSpace(output), verb+" pane=%d terminal=%d", &paneID, &terminalID); err != nil {
		t.Fatalf("parse %s result %q: %v", verb, output, err)
	}
	return paneID, terminalID
}

func readUntil(t *testing.T, terminal *terminalSession, expected ...string) string {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case result := <-terminal.reads:
			terminal.output.Write(result.data)
			matched := true
			for _, value := range expected {
				matched = matched && strings.Contains(terminal.output.String(), value)
			}
			if matched {
				return terminal.output.String()
			}
			if result.err != nil {
				t.Fatalf("read PTY before %q: %v; output=%q", expected, result.err, terminal.output.String())
			}
		case <-timer.C:
			t.Fatalf("PTY output did not contain %q: %q", expected, terminal.output.String())
		}
	}
}

func waitCommand(t *testing.T, command *exec.Cmd, timeout time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("PTY command exited: %v", err)
		}
	case <-time.After(timeout):
		_ = command.Process.Kill()
		t.Fatalf("PTY command did not exit within %s", timeout)
	}
}
