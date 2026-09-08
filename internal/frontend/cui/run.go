//go:build darwin || linux || windows

package cui

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
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aruzen/ariadne/internal/client"
	ariadneconfig "github.com/aruzen/ariadne/internal/config"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/platform/localipc"
	"github.com/aruzen/ariadne/internal/platform/paths"
	platformterminal "github.com/aruzen/ariadne/internal/platform/terminal"
	"github.com/aruzen/ariadne/internal/protocol"
	"github.com/aruzen/ariadne/internal/vt/libghostty"
	"github.com/aruzen/streammux/pty"
)

const (
	commandTimeout = 15 * time.Second
	readyTimeout   = 3 * time.Second
)

// Run executes the command-line frontend against endpoint with explicitly supplied I/O.
func Run(endpoint string, arguments []string, stdout, stderr io.Writer) error {
	if _, err := libghostty.Version(); err != nil {
		return fmt.Errorf("initialize VT engine: %w", err)
	}
	if len(arguments) == 0 {
		return errors.New("command is required")
	}
	lifetimeCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	command := arguments[0]
	if !knownCommand(command) {
		return fmt.Errorf("unknown command %q", command)
	}
	if command == "daemon" {
		if len(arguments) == 1 {
			return errors.New("daemon subcommand is required")
		}
		switch arguments[1] {
		case "status", "stop":
		default:
			return fmt.Errorf("unknown daemon subcommand %q", arguments[1])
		}
	}
	if command == "open" || command == "attach" {
		if err := requireAttachTTY(stdout); err != nil {
			return err
		}
	}
	autoStart := command != "daemon"
	connectCtx, cancelConnect := context.WithTimeout(lifetimeCtx, commandTimeout)
	connection, err := connect(connectCtx, endpoint, autoStart)
	cancelConnect()
	if err != nil {
		if command == "daemon" && len(arguments) >= 2 && arguments[1] == "status" && isDaemonAbsent(err) {
			_, _ = fmt.Fprintln(stdout, "stopped")
			return nil
		}
		return err
	}
	frontend, err := client.Open(lifetimeCtx, connection, client.DefaultConfig())
	if err != nil {
		_ = connection.Close()
		return err
	}
	defer frontend.Close()
	syncCtx, cancelSync := context.WithTimeout(lifetimeCtx, commandTimeout)
	_, err = frontend.Sync(syncCtx)
	cancelSync()
	if err != nil {
		return err
	}
	operationCtx := lifetimeCtx
	if command != "open" && command != "attach" {
		var cancelOperation context.CancelFunc
		operationCtx, cancelOperation = context.WithTimeout(lifetimeCtx, commandTimeout)
		defer cancelOperation()
	}

	switch command {
	case "new":
		return runNew(operationCtx, frontend, arguments[1:], stdout, stderr)
	case "open":
		return runOpen(operationCtx, frontend, arguments[1:], stdout, stderr)
	case "attach":
		return runAttach(operationCtx, frontend, arguments[1:], stdout)
	case "list":
		return runList(operationCtx, frontend, arguments[1:], stdout, stderr)
	case "restart":
		return runRestart(operationCtx, frontend, arguments[1:], stdout, stderr)
	case "kill":
		return runPaneCommand(operationCtx, frontend, protocol.OperationKillTerminal, arguments[1:], stdout, "killed")
	case "dismiss":
		return runPaneCommand(operationCtx, frontend, protocol.OperationDismissTerminal, arguments[1:], stdout, "dismissed")
	case "daemon":
		return runDaemon(operationCtx, frontend, arguments[1:], stdout, stderr)
	}
	return nil
}

func knownCommand(command string) bool {
	switch command {
	case "new", "open", "attach", "list", "restart", "kill", "dismiss", "daemon":
		return true
	default:
		return false
	}
}

func runNew(ctx context.Context, frontend *client.Client, arguments []string, stdout, stderr io.Writer) error {
	params, err := parseNewParams(arguments, stderr)
	if err != nil {
		return err
	}
	result, err := client.Call[protocol.TerminalOperationResult](ctx, frontend, protocol.OperationNewTerminal, params)
	if err != nil {
		return err
	}
	return printTerminalResult(stdout, "created", result.Pane)
}

func parseNewParams(arguments []string, stderr io.Writer) (protocol.NewTerminalParams, error) {
	flags := flag.NewFlagSet("new", flag.ContinueOnError)
	flags.SetOutput(stderr)
	windowID := flags.Uint64("window", 0, "destination Window ID")
	targetID := flags.Uint64("target", 0, "split target Pane ID")
	direction := flags.String("direction", "", "horizontal or vertical")
	title := flags.String("title", "", "Pane title")
	cwd, err := os.Getwd()
	if err != nil {
		return protocol.NewTerminalParams{}, err
	}
	workingDirectory := flags.String("cwd", cwd, "working directory")
	if err := flags.Parse(arguments); err != nil {
		return protocol.NewTerminalParams{}, err
	}
	argv, err := resolveArgv(flags.Args())
	if err != nil {
		return protocol.NewTerminalParams{}, err
	}
	return protocol.NewTerminalParams{
		WindowID: core.WindowID(*windowID), TargetPaneID: core.PaneID(*targetID),
		Direction: core.SplitDirection(*direction), Title: *title,
		Argv: argv, CWD: *workingDirectory, Env: os.Environ(), InitialSize: terminalSize(os.Stdin),
	}, nil
}

func runOpen(ctx context.Context, frontend *client.Client, arguments []string, stdout, stderr io.Writer) error {
	params, err := parseNewParams(arguments, stderr)
	if err != nil {
		return err
	}
	createCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	result, err := client.Call[protocol.TerminalOperationResult](createCtx, frontend, protocol.OperationNewTerminal, params)
	cancel()
	if err != nil {
		return err
	}
	if result.Pane.Terminal == nil || result.Pane.Terminal.ID == nil {
		return errors.New("created Pane has no Terminal ID")
	}
	return attachTerminal(ctx, frontend, *result.Pane.Terminal.ID, stdout)
}

func runAttach(ctx context.Context, frontend *client.Client, arguments []string, stdout io.Writer) error {
	if len(arguments) != 1 {
		return errors.New("attach requires one Terminal ID")
	}
	id, err := strconv.ParseUint(arguments[0], 10, 64)
	if err != nil || id == 0 {
		return fmt.Errorf("invalid Terminal ID %q", arguments[0])
	}
	return attachTerminal(ctx, frontend, core.TerminalID(id), stdout)
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
	connection, err := localipc.DialContext(ctx, socketPath)
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
		connection, lastErr = localipc.DialContext(attemptCtx, socketPath)
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
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve ariadne executable: %w", err)
	}
	command := exec.Command(executable, "-socket", socketPath, "daemon", "serve")
	command.Stdin = nil
	command.Stdout = nil
	command.Stderr = nil
	configureDetachedProcess(command)
	if err := command.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	if err := command.Process.Release(); err != nil {
		return fmt.Errorf("release daemon process: %w", err)
	}
	return nil
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
	return defaultShell(), nil
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
	if cols, rows, err := platformterminal.Size(file); err == nil && cols != 0 && rows != 0 {
		return pty.Size{Cols: cols, Rows: rows}
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
	return localipc.IsAbsent(err)
}
