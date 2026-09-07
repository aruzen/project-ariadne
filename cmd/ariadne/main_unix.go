//go:build darwin || linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/aruzen/ariadne/client"
	ariadneconfig "github.com/aruzen/ariadne/config"
	"github.com/aruzen/ariadne/core"
	"github.com/aruzen/ariadne/platform/paths"
	"github.com/aruzen/ariadne/platform/unixsocket"
	"github.com/aruzen/ariadne/protocol"
	"github.com/aruzen/streammux/pty"
	"golang.org/x/sys/unix"
)

const (
	commandTimeout = 15 * time.Second
	readyTimeout   = 3 * time.Second
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "ariadne: %v\n", err)
		os.Exit(1)
	}
}

func run(arguments []string, stdout, stderr io.Writer) error {
	global := flag.NewFlagSet("ariadne", flag.ContinueOnError)
	global.SetOutput(stderr)
	defaultSocket, err := unixsocket.DefaultPath()
	if err != nil {
		return err
	}
	socketPath := global.String("socket", defaultSocket, "Unix socket path")
	if err := global.Parse(arguments); err != nil {
		return err
	}
	remaining := global.Args()
	if len(remaining) == 0 {
		return errors.New("command is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	command := remaining[0]
	autoStart := command != "daemon"
	connection, err := connect(ctx, *socketPath, autoStart)
	if err != nil {
		if command == "daemon" && len(remaining) >= 2 && remaining[1] == "status" && isDaemonAbsent(err) {
			_, _ = fmt.Fprintln(stdout, "stopped")
			return nil
		}
		return err
	}
	frontend, err := client.Open(ctx, connection, client.DefaultConfig())
	if err != nil {
		_ = connection.Close()
		return err
	}
	defer frontend.Close()
	if _, err := frontend.Sync(ctx); err != nil {
		return err
	}

	switch command {
	case "new":
		return runNew(ctx, frontend, remaining[1:], stdout, stderr)
	case "list":
		return runList(ctx, frontend, remaining[1:], stdout, stderr)
	case "restart":
		return runRestart(ctx, frontend, remaining[1:], stdout, stderr)
	case "kill":
		return runPaneCommand(ctx, frontend, protocol.OperationKillTerminal, remaining[1:], stdout, "killed")
	case "dismiss":
		return runPaneCommand(ctx, frontend, protocol.OperationDismissTerminal, remaining[1:], stdout, "dismissed")
	case "daemon":
		return runDaemon(ctx, frontend, remaining[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func runNew(ctx context.Context, frontend *client.Client, arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("new", flag.ContinueOnError)
	flags.SetOutput(stderr)
	windowID := flags.Uint64("window", 0, "destination Window ID")
	targetID := flags.Uint64("target", 0, "split target Pane ID")
	direction := flags.String("direction", "", "horizontal or vertical")
	title := flags.String("title", "", "Pane title")
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	workingDirectory := flags.String("cwd", cwd, "working directory")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	argv, err := resolveArgv(flags.Args())
	if err != nil {
		return err
	}
	params := protocol.NewTerminalParams{
		WindowID: core.WindowID(*windowID), TargetPaneID: core.PaneID(*targetID),
		Direction: core.SplitDirection(*direction), Title: *title,
		Argv: argv, CWD: *workingDirectory, Env: os.Environ(), InitialSize: terminalSize(os.Stdin),
	}
	result, err := client.Call[protocol.TerminalOperationResult](ctx, frontend, protocol.OperationNewTerminal, params)
	if err != nil {
		return err
	}
	return printTerminalResult(stdout, "created", result.Pane)
}

func runRestart(ctx context.Context, frontend *client.Client, arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("restart", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	paneID, err := exactlyOnePaneID(flags.Args())
	if err != nil {
		return err
	}
	result, err := client.Call[protocol.TerminalOperationResult](ctx, frontend, protocol.OperationRestartTerminal, protocol.RestartTerminalParams{
		PaneID: paneID, Env: os.Environ(), InitialSize: terminalSize(os.Stdin),
	})
	if err != nil {
		return err
	}
	return printTerminalResult(stdout, "restarted", result.Pane)
}

func runPaneCommand(ctx context.Context, frontend *client.Client, operation protocol.Operation, arguments []string, stdout io.Writer, verb string) error {
	paneID, err := exactlyOnePaneID(arguments)
	if err != nil {
		return err
	}
	result, err := client.Call[protocol.TerminalOperationResult](ctx, frontend, operation, protocol.PaneParams{PaneID: paneID})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s pane=%d\n", verb, result.Pane.ID)
	return err
}

func runList(ctx context.Context, frontend *client.Client, arguments []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("list does not accept positional arguments")
	}
	result, err := client.Call[protocol.ListTerminalsResult](ctx, frontend, protocol.OperationListTerminals, nil)
	if err != nil {
		return err
	}
	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "PANE\tTERMINAL\tSTATE\tATTACHED\tCOMMAND\tCWD"); err != nil {
		return err
	}
	for _, entry := range result.Entries {
		terminalID := "-"
		state := "-"
		command := "-"
		cwd := "-"
		if entry.Pane.Terminal != nil {
			state = string(entry.Pane.Terminal.State)
			command = formatArgv(entry.Pane.Terminal.Launch.Argv)
			cwd = entry.Pane.Terminal.Launch.CWD
			if entry.Pane.Terminal.ID != nil {
				terminalID = strconv.FormatUint(uint64(*entry.Pane.Terminal.ID), 10)
			}
		}
		if _, err := fmt.Fprintf(writer, "%d\t%s\t%s\t%d\t%s\t%s\n", entry.Pane.ID, terminalID, state, entry.AttachmentCount, command, cwd); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func runDaemon(ctx context.Context, frontend *client.Client, arguments []string, stdout, stderr io.Writer) error {
	if len(arguments) == 0 {
		return errors.New("daemon subcommand is required")
	}
	switch arguments[0] {
	case "status":
		if len(arguments) != 1 {
			return errors.New("daemon status does not accept arguments")
		}
		status, err := client.Call[protocol.DaemonStatusResult](ctx, frontend, protocol.OperationDaemonStatus, nil)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "running connections=%d sessions=%d active=%d retained=%d\n",
			status.Connections, status.Sessions, status.ActiveTerminals, status.RetainedTerminals)
		return err
	case "stop":
		flags := flag.NewFlagSet("daemon stop", flag.ContinueOnError)
		flags.SetOutput(stderr)
		force := flags.Bool("force", false, "stop active terminals")
		if err := flags.Parse(arguments[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("daemon stop does not accept positional arguments")
		}
		_, err := client.Call[protocol.DaemonStatusResult](ctx, frontend, protocol.OperationDaemonStop, protocol.DaemonStopParams{Force: *force})
		if err == nil {
			_, err = fmt.Fprintln(stdout, "stopping")
		}
		return err
	default:
		return fmt.Errorf("unknown daemon subcommand %q", arguments[0])
	}
}

func connect(ctx context.Context, socketPath string, autoStart bool) (net.Conn, error) {
	connection, err := unixsocket.DialContext(ctx, socketPath, os.Getuid())
	if err == nil || !autoStart || !isDaemonAbsent(err) {
		return connection, err
	}
	if err := startDaemon(socketPath); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(readyTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		attemptCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		connection, lastErr = unixsocket.DialContext(attemptCtx, socketPath, os.Getuid())
		cancel()
		if lastErr == nil {
			return connection, nil
		}
		if !isDaemonAbsent(lastErr) {
			return nil, lastErr
		}
		time.Sleep(25 * time.Millisecond)
	}
	return nil, fmt.Errorf("daemon did not become ready: %w", lastErr)
}

func startDaemon(socketPath string) error {
	daemonPath, err := findDaemon()
	if err != nil {
		return err
	}
	command := exec.Command(daemonPath, "-socket", socketPath)
	command.Stdin = nil
	command.Stdout = nil
	command.Stderr = nil
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	if err := command.Process.Release(); err != nil {
		return fmt.Errorf("release daemon process: %w", err)
	}
	return nil
}

func findDaemon() (string, error) {
	executable, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(executable), "ariadned")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	path, err := exec.LookPath("ariadned")
	if err != nil {
		return "", errors.New("ariadned executable was not found")
	}
	return path, nil
}

func resolveArgv(explicit []string) ([]string, error) {
	if len(explicit) != 0 {
		return append([]string(nil), explicit...), nil
	}
	configPath, err := paths.DefaultConfigPath()
	if err != nil {
		return nil, err
	}
	configuration, err := ariadneconfig.Load(configPath)
	if err != nil {
		return nil, err
	}
	if configuration.Shell != "" {
		return []string{configuration.Shell}, nil
	}
	if shell := os.Getenv("SHELL"); shell != "" {
		return []string{shell}, nil
	}
	return []string{"/bin/sh"}, nil
}

func exactlyOnePaneID(arguments []string) (core.PaneID, error) {
	if len(arguments) != 1 {
		return 0, errors.New("exactly one Pane ID is required")
	}
	value, err := strconv.ParseUint(arguments[0], 10, 64)
	if err != nil || value == 0 {
		return 0, fmt.Errorf("invalid Pane ID %q", arguments[0])
	}
	return core.PaneID(value), nil
}

func terminalSize(file *os.File) pty.Size {
	if file != nil {
		if size, err := unix.IoctlGetWinsize(int(file.Fd()), unix.TIOCGWINSZ); err == nil && size.Col != 0 && size.Row != 0 {
			return pty.Size{Cols: int(size.Col), Rows: int(size.Row)}
		}
	}
	return pty.Size{Cols: 80, Rows: 24}
}

func printTerminalResult(writer io.Writer, verb string, pane core.Pane) error {
	terminalID := "-"
	if pane.Terminal != nil && pane.Terminal.ID != nil {
		terminalID = strconv.FormatUint(uint64(*pane.Terminal.ID), 10)
	}
	_, err := fmt.Fprintf(writer, "%s pane=%d terminal=%s\n", verb, pane.ID, terminalID)
	return err
}

func formatArgv(argv []string) string {
	quoted := make([]string, len(argv))
	for index, argument := range argv {
		if strings.ContainsAny(argument, " \t\n\"'") {
			quoted[index] = strconv.Quote(argument)
		} else {
			quoted[index] = argument
		}
	}
	return strings.Join(quoted, " ")
}

func isDaemonAbsent(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED)
}
