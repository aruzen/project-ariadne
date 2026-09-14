//go:build windows

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aruzen/ariadne/internal/platform/localipc"
	"github.com/aruzen/streammux/pty"
	"github.com/aruzen/streammux/pty/windowspty"
)

const windowsRunE2EEnvironment = "ARIADNE_RUN_E2E"
const windowsE2EBinaryDirectoryEnvironment = "ARIADNE_E2E_BIN_DIR"

type windowsE2ERuntime struct {
	clientPath  string
	endpoint    string
	environment []string
}

type windowsListResult struct {
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

type windowsTerminalRead struct {
	data []byte
	err  error
}

type windowsTerminalSession struct {
	name    string
	process pty.ManagedProcess
	reads   <-chan windowsTerminalRead
	output  strings.Builder
}

func TestWindowsCLIEndToEnd(t *testing.T) {
	if os.Getenv(windowsRunE2EEnvironment) != "1" {
		t.Skip("set ARIADNE_RUN_E2E=1 to run the Windows daemon/ConPTY integration test")
	}
	instance := newWindowsE2ERuntime(t)
	instance.testConcurrentAutoStart(t)
	instance.testLifecycle(t)
	instance.testStashRestore(t)
	instance.testDaemonRestart(t)
	instance.testOpenDetachReattachResize(t)
}

func (runtime *windowsE2ERuntime) testStashRestore(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := runtime.run(ctx, "new", "--", "cmd.exe", "/d", "/s", "/c", "ping -n 31 127.0.0.1 >NUL")
	if err != nil {
		t.Fatal(err)
	}
	paneID, _ := windowsParseTerminalResult(t, output, "created")
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
	runtime.waitForEntries(t, ctx, func(result windowsListResult) bool { return len(result.Entries) == 0 })
}

func newWindowsE2ERuntime(t *testing.T) *windowsE2ERuntime {
	t.Helper()
	binaryDirectory := os.Getenv(windowsE2EBinaryDirectoryEnvironment)
	if binaryDirectory == "" {
		_, source, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("resolve integration source path")
		}
		repository := filepath.Dir(filepath.Dir(source))
		binaryDirectory = t.TempDir()
		windowsBuildBinary(t, repository, filepath.Join(binaryDirectory, "ariadne.exe"), "./cmd/ariadne")
	}
	clientPath := filepath.Join(binaryDirectory, "ariadne.exe")
	if info, err := os.Stat(clientPath); err != nil || info.IsDir() {
		t.Fatalf("E2E executable %q is unavailable: %v", clientPath, err)
	}

	runtimeDirectory := t.TempDir()
	instance := &windowsE2ERuntime{
		clientPath: clientPath,
		endpoint:   fmt.Sprintf(`\\.\pipe\ariadne-e2e-%d-%d`, os.Getpid(), time.Now().UnixNano()),
		environment: windowsOverrideEnvironment(map[string]string{
			"XDG_CONFIG_HOME": filepath.Join(runtimeDirectory, "config"),
			"XDG_STATE_HOME":  filepath.Join(runtimeDirectory, "state"),
		}),
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = instance.run(ctx, "daemon", "stop", "--force")
	})
	return instance
}

func windowsBuildBinary(t *testing.T, repository, output, packagePath string) {
	t.Helper()
	command := exec.Command("go", "build", "-race", "-o", output, packagePath)
	command.Dir = repository
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", packagePath, err, data)
	}
}

func windowsOverrideEnvironment(overrides map[string]string) []string {
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[strings.ToUpper(name)]; !replaced {
			environment = append(environment, entry)
		}
	}
	for name, value := range overrides {
		environment = append(environment, name+"="+value)
	}
	return environment
}

func (runtime *windowsE2ERuntime) command(ctx context.Context, executable string, arguments ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Env = runtime.environment
	return command
}

func (runtime *windowsE2ERuntime) run(ctx context.Context, arguments ...string) (string, error) {
	arguments = append([]string{"-socket", runtime.endpoint}, arguments...)
	command := runtime.command(ctx, runtime.clientPath, arguments...)
	data, err := command.CombinedOutput()
	if err != nil {
		return string(data), fmt.Errorf("ariadne %s: %w: %s", strings.Join(arguments[2:], " "), err, data)
	}
	return string(data), nil
}

func (runtime *windowsE2ERuntime) testConcurrentAutoStart(t *testing.T) {
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
				var result windowsListResult
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
	duplicate := runtime.command(ctx, runtime.clientPath, "-socket", runtime.endpoint, "daemon", "serve")
	if data, err := duplicate.CombinedOutput(); err == nil {
		t.Fatalf("second daemon unexpectedly started: %s", data)
	}
}

func (runtime *windowsE2ERuntime) testLifecycle(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := runtime.run(ctx, "new", "--", "cmd.exe", "/d", "/s", "/c", "ping -n 31 127.0.0.1 >NUL")
	if err != nil {
		t.Fatal(err)
	}
	paneID, _ := windowsParseTerminalResult(t, output, "created")
	if _, err := runtime.run(ctx, "kill", fmt.Sprint(paneID)); err != nil {
		t.Fatal(err)
	}
	runtime.waitForEntries(t, ctx, func(result windowsListResult) bool { return len(result.Entries) == 0 })

	output, err = runtime.run(ctx, "new", "--", "cmd.exe", "/d", "/s", "/c", "exit /b 7")
	if err != nil {
		t.Fatal(err)
	}
	paneID, _ = windowsParseTerminalResult(t, output, "created")
	runtime.waitForEntries(t, ctx, func(result windowsListResult) bool {
		return len(result.Entries) == 1 && result.Entries[0].Pane.ID == paneID &&
			result.Entries[0].Pane.Terminal != nil && result.Entries[0].Pane.Terminal.State == "exited"
	})
	if _, err := runtime.run(ctx, "dismiss", fmt.Sprint(paneID)); err != nil {
		t.Fatal(err)
	}
	runtime.waitForEntries(t, ctx, func(result windowsListResult) bool { return len(result.Entries) == 0 })
}

func (runtime *windowsE2ERuntime) testDaemonRestart(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	output, err := runtime.run(ctx, "new", "--", "cmd.exe", "/d", "/s", "/c", "ping -n 31 127.0.0.1 >NUL")
	if err != nil {
		t.Fatal(err)
	}
	paneID, _ := windowsParseTerminalResult(t, output, "created")
	if _, err := runtime.run(ctx, "daemon", "stop", "--force"); err != nil {
		t.Fatal(err)
	}
	runtime.waitDaemonAbsent(t, ctx)
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
	_, terminalID := windowsParseTerminalResult(t, restarted, "restarted")
	runtime.waitForEntries(t, ctx, func(result windowsListResult) bool {
		return len(result.Entries) == 1 && result.Entries[0].Pane.Terminal != nil &&
			result.Entries[0].Pane.Terminal.ID != nil && *result.Entries[0].Pane.Terminal.ID == terminalID &&
			result.Entries[0].Pane.Terminal.State == "running"
	})
	if _, err := runtime.run(ctx, "kill", fmt.Sprint(paneID)); err != nil {
		t.Fatal(err)
	}
	runtime.waitForEntries(t, ctx, func(result windowsListResult) bool { return len(result.Entries) == 0 })
}

func (runtime *windowsE2ERuntime) testOpenDetachReattachResize(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	open := runtime.startTerminal(t, ctx, "open", "--", "cmd.exe", "/q", "/d")
	if _, err := open.process.Input().Write([]byte("echo ARIADNE_WINDOWS_PTY\r\n")); err != nil {
		t.Fatalf("write open terminal: %v", err)
	}
	open.readUntil(t, "ARIADNE_WINDOWS_PTY")
	if err := open.process.Resize(pty.Size{Cols: 111, Rows: 42}); err != nil {
		t.Fatalf("resize frontend ConPTY: %v", err)
	}
	resizeCommand := `ping -n 2 127.0.0.1 >NUL & powershell.exe -NoLogo -NoProfile -Command "$s=$Host.UI.RawUI.WindowSize; Write-Output ($s.Height.ToString() + ' ' + $s.Width.ToString())"` + "\r\n"
	if _, err := open.process.Input().Write([]byte(resizeCommand)); err != nil {
		t.Fatalf("write resized size command: %v", err)
	}
	open.readUntil(t, "42 111")
	if _, err := open.process.Input().Write(windowsDetachInput()); err != nil {
		t.Fatalf("write detach sequence: %v", err)
	}
	open.wait(t, 15*time.Second)

	result := runtime.waitForEntries(t, ctx, func(result windowsListResult) bool {
		return len(result.Entries) == 1 && result.Entries[0].Pane.Terminal != nil &&
			result.Entries[0].Pane.Terminal.ID != nil && result.Entries[0].Pane.Terminal.State == "running"
	})
	terminalID := *result.Entries[0].Pane.Terminal.ID
	attached := runtime.startTerminal(t, ctx, "attach", fmt.Sprint(terminalID))
	attached.readUntil(t, "ARIADNE_WINDOWS_PTY")
	if _, err := attached.process.Input().Write([]byte("exit\r\n")); err != nil {
		t.Fatalf("write shell exit: %v", err)
	}
	attached.wait(t, 15*time.Second)
	runtime.waitForEntries(t, ctx, func(result windowsListResult) bool {
		return len(result.Entries) == 1 && result.Entries[0].Pane.ID != 0 && result.Entries[0].Pane.Terminal != nil &&
			result.Entries[0].Pane.Terminal.State == "exited"
	})
	if _, err := runtime.run(ctx, "dismiss", fmt.Sprint(result.Entries[0].Pane.ID)); err != nil {
		t.Fatal(err)
	}
	runtime.waitForEntries(t, ctx, func(result windowsListResult) bool { return len(result.Entries) == 0 })
}

func windowsDetachInput() []byte {
	return []byte("\x1b[17;29;0;1;8;1_" +
		"\x1b[65;30;1;1;8;1_" +
		"\x1b[65;30;1;0;8;1_" +
		"\x1b[17;29;0;0;0;1_" +
		"\x1b[68;32;100;1;0;1_" +
		"\x1b[68;32;100;0;0;1_")
}

func (runtime *windowsE2ERuntime) startTerminal(t *testing.T, ctx context.Context, arguments ...string) *windowsTerminalSession {
	t.Helper()
	process, err := (windowspty.ManagedFactory{}).StartManaged(ctx, pty.ProcessSpec{
		Command: runtime.clientPath,
		Args:    append([]string{"-socket", runtime.endpoint}, arguments...),
		Env:     runtime.environment,
		InitialSize: pty.Size{
			Cols: 90,
			Rows: 30,
		},
	})
	if err != nil {
		t.Fatalf("start frontend ConPTY: %v", err)
	}
	t.Cleanup(func() {
		_ = process.Kill()
		_ = process.Close()
	})
	reads := make(chan windowsTerminalRead, 64)
	go func(reader io.Reader) {
		buffer := make([]byte, 4096)
		for {
			count, err := reader.Read(buffer)
			result := windowsTerminalRead{err: err}
			if count != 0 {
				result.data = append([]byte(nil), buffer[:count]...)
			}
			reads <- result
			if err != nil {
				return
			}
		}
	}(process.Output())
	return &windowsTerminalSession{name: strings.Join(arguments, " "), process: process, reads: reads}
}

func (terminal *windowsTerminalSession) readUntil(t *testing.T, expected string) {
	t.Helper()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	for {
		select {
		case result := <-terminal.reads:
			terminal.output.Write(result.data)
			if strings.Contains(terminal.output.String(), expected) {
				return
			}
			if result.err != nil {
				t.Fatalf("read ConPTY before %q: %v; output=%q", expected, result.err, terminal.output.String())
			}
		case <-timer.C:
			t.Fatalf("ConPTY output did not contain %q: %q", expected, terminal.output.String())
		}
	}
}

func (terminal *windowsTerminalSession) wait(t *testing.T, timeout time.Duration) {
	t.Helper()
	done := make(chan struct {
		status pty.ExitStatus
		err    error
	}, 1)
	go func() {
		status, err := terminal.process.WaitStatus()
		done <- struct {
			status pty.ExitStatus
			err    error
		}{status: status, err: err}
	}()
	select {
	case result := <-done:
		if result.err != nil || result.status.Reason != pty.ExitReasonExited || result.status.Code != 0 {
			t.Fatalf("frontend ConPTY exit = %+v, %v", result.status, result.err)
		}
		if err := terminal.process.Close(); err != nil {
			t.Fatalf("close frontend ConPTY: %v", err)
		}
	case <-time.After(timeout):
		_ = terminal.process.Kill()
		_ = terminal.process.Close()
		t.Fatalf("frontend ConPTY %q did not exit within %s; output=%q", terminal.name, timeout, terminal.output.String())
	}
}

func (runtime *windowsE2ERuntime) list(ctx context.Context) (windowsListResult, error) {
	output, err := runtime.run(ctx, "list", "--json")
	if err != nil {
		return windowsListResult{}, err
	}
	var result windowsListResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return windowsListResult{}, fmt.Errorf("decode list output %q: %w", output, err)
	}
	return result, nil
}

func (runtime *windowsE2ERuntime) waitForEntries(t *testing.T, ctx context.Context, predicate func(windowsListResult) bool) windowsListResult {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
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

func (runtime *windowsE2ERuntime) waitDaemonAbsent(t *testing.T, ctx context.Context) {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		attempt, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		connection, err := localipc.DialContext(attempt, runtime.endpoint)
		cancel()
		if connection != nil {
			_ = connection.Close()
		}
		if localipc.IsAbsent(err) {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for daemon named pipe removal: %v (last error=%v)", ctx.Err(), err)
		case <-ticker.C:
		}
	}
}

func windowsParseTerminalResult(t *testing.T, output, verb string) (uint64, uint64) {
	t.Helper()
	var paneID, terminalID uint64
	if _, err := fmt.Sscanf(strings.TrimSpace(output), verb+" pane=%d terminal=%d", &paneID, &terminalID); err != nil {
		t.Fatalf("parse %s result %q: %v", verb, output, err)
	}
	return paneID, terminalID
}
