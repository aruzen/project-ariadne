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
	"github.com/aruzen/ariadne/internal/frontend/tui"
	"github.com/aruzen/ariadne/internal/platform/localipc"
	"github.com/aruzen/ariadne/internal/platform/paths"
	platformterminal "github.com/aruzen/ariadne/internal/platform/terminal"
	"github.com/aruzen/ariadne/internal/protocol"
	"github.com/aruzen/streammux/pty"
)

const (
	commandTimeout = 15 * time.Second
	readyTimeout   = 3 * time.Second
)

// Run executes the command-line frontend against endpoint with explicitly supplied I/O.
func Run(endpoint string, arguments []string, stdout, stderr io.Writer) error {
	if handled, err := HandleHelp(arguments, stdout); handled {
		return err
	}
	lifetimeCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	command := arguments[0]
	if !knownCommand(command) {
		return fmt.Errorf("unknown command %q; run 'ariadne --help' to list commands", command)
	}
	if command == "init" {
		return runInit(arguments[1:], stdout)
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
	if command == "open" || command == "attach" || command == "tui" {
		if err := requireAttachTTY(stdout); err != nil {
			return err
		}
	}
	configPath, err := paths.DefaultConfigPath()
	if err != nil {
		return err
	}
	fileConfiguration, err := ariadneconfig.Load(configPath)
	if err != nil {
		return err
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
	clientConfiguration := client.DefaultConfig()
	fileConfiguration.ApplyStream(&clientConfiguration.Stream)
	fileConfiguration.ApplyPeer(&clientConfiguration.Peer)
	frontend, err := client.Open(lifetimeCtx, connection, clientConfiguration)
	if err != nil {
		_ = connection.Close()
		return err
	}
	defer frontend.Close()
	syncCtx, cancelSync := context.WithTimeout(lifetimeCtx, commandTimeout)
	synchronized, err := frontend.Sync(syncCtx)
	cancelSync()
	if err != nil {
		return err
	}
	operationCtx := lifetimeCtx
	if command != "open" && command != "attach" && command != "tui" {
		var cancelOperation context.CancelFunc
		operationCtx, cancelOperation = context.WithTimeout(lifetimeCtx, commandTimeout)
		defer cancelOperation()
	}

	switch command {
	case "tui":
		options, err := parseTUIOptions(arguments[1:], fileConfiguration.TUI, stderr)
		if err != nil {
			return err
		}
		options.Shell = fileConfiguration.ShellCommand(options.Shell)
		options.Editor = fileConfiguration.EditorCommand(options.Editor)
		options.Clipboard = fileConfiguration.Clipboard
		options.Keybindings = fileConfiguration.Keybindings
		return tui.Run(operationCtx, frontend, synchronized.Snapshot, stdout, options)
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
	case "run":
		return runTerminal(operationCtx, frontend, arguments[1:], stdout, stderr)
	case "kill":
		return runPaneCommand(operationCtx, frontend, protocol.OperationKillTerminal, arguments[1:], stdout, "killed")
	case "dismiss":
		return runPaneCommand(operationCtx, frontend, protocol.OperationDismissTerminal, arguments[1:], stdout, "dismissed")
	case "stash":
		return runStash(operationCtx, frontend, arguments[1:], stdout)
	case "restore":
		return runRestore(operationCtx, frontend, arguments[1:], stdout, stderr)
	case "tool":
		return runTool(operationCtx, frontend, synchronized.Snapshot, arguments[1:], stdout, stderr)
	case "attention":
		return runAttention(operationCtx, frontend, synchronized.Snapshot, arguments[1:], stdout)
	case "daemon":
		return runDaemon(operationCtx, frontend, arguments[1:], stdout, stderr)
	}
	return nil
}

func parseTUIOptions(arguments []string, defaults ariadneconfig.TUIOptions, stderr io.Writer) (tui.Options, error) {
	flags := flag.NewFlagSet("tui", flag.ContinueOnError)
	flags.SetOutput(stderr)
	paneFrame := flags.String("pane-frame", string(defaults.PaneFrame), "Pane frame: full, split, or none")
	if err := flags.Parse(arguments); err != nil {
		return tui.Options{}, err
	}
	if flags.NArg() != 0 {
		return tui.Options{}, errors.New("tui does not accept positional arguments")
	}
	mode := tui.PaneFrameMode(*paneFrame)
	switch mode {
	case tui.PaneFrameFull, tui.PaneFrameSplit, tui.PaneFrameNone:
		cwd, err := os.Getwd()
		if err != nil {
			return tui.Options{}, err
		}
		return tui.Options{PaneFrame: mode, Shell: defaultShell(), Editor: defaultEditor(), CWD: cwd, Env: os.Environ()}, nil
	default:
		return tui.Options{}, fmt.Errorf("invalid TUI Pane frame mode %q", *paneFrame)
	}
}

func knownCommand(command string) bool {
	switch command {
	case "init", "tui", "new", "open", "attach", "list", "restart", "run", "kill", "dismiss", "stash", "restore", "tool", "attention", "daemon":
		return true
	default:
		return false
	}
}

func runInit(arguments []string, stdout io.Writer) error {
	if len(arguments) != 0 {
		return errors.New("usage: init")
	}
	path, err := paths.DefaultConfigPath()
	if err != nil {
		return err
	}
	if err := ariadneconfig.WriteTemplate(path); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "initialized %s\n", path)
	return err
}

func runTool(ctx context.Context, frontend *client.Client, snapshot core.Snapshot, arguments []string, stdout, stderr io.Writer) error {
	if len(arguments) == 1 && arguments[0] == "list" {
		writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(writer, "PANE\tPROVIDER\tTYPE\tINSTANCE\tGENERATION")
		for _, pane := range snapshot.Panes {
			if pane.Tool == nil {
				continue
			}
			generation := uint64(0)
			for _, tool := range snapshot.ToolInstances {
				if tool.Descriptor == *pane.Tool {
					generation = tool.Generation
					break
				}
			}
			_, _ = fmt.Fprintf(writer, "%d\t%s\t%s\t%s\t%d\n", pane.ID, pane.Tool.Provider, pane.Tool.Type, pane.Tool.Instance, generation)
		}
		return writer.Flush()
	}
	if len(arguments) == 0 || arguments[0] != "new" {
		return errors.New("usage: tool new [options] TYPE | tool list")
	}
	flags := flag.NewFlagSet("tool new", flag.ContinueOnError)
	flags.SetOutput(stderr)
	provider := flags.String("provider", "ariadne", "Tool provider")
	instance := flags.String("instance", "default", "Tool instance")
	windowID := flags.Uint64("window", 0, "destination Window ID")
	targetID := flags.Uint64("target", 0, "split target Pane ID")
	direction := flags.String("direction", "horizontal", "horizontal or vertical")
	if err := flags.Parse(arguments[1:]); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: tool new [options] TYPE")
	}
	window := core.WindowID(*windowID)
	target := core.PaneID(*targetID)
	if window == 0 {
		if len(snapshot.Workspaces) == 0 || len(snapshot.Workspaces[0].WindowIDs) == 0 {
			return errors.New("no destination Window")
		}
		window = snapshot.Workspaces[0].WindowIDs[0]
	}
	var destination core.Window
	for _, candidate := range snapshot.Windows {
		if candidate.ID == window {
			destination = candidate
			break
		}
	}
	if destination.ID == 0 {
		return fmt.Errorf("window %d not found", window)
	}
	if destination.Layout != nil && target == 0 {
		target = firstPaneID(*destination.Layout)
	}
	descriptor := core.ToolDescriptor{Provider: *provider, Type: flags.Arg(0), Instance: *instance}
	tool := core.ToolInstance{Descriptor: descriptor, StateVersion: 1, Generation: 1, State: json.RawMessage(`{}`)}
	for _, existing := range snapshot.ToolInstances {
		if existing.Descriptor == descriptor {
			tool = existing
			break
		}
	}
	var pane core.Pane
	if destination.Layout == nil {
		result, err := client.Call[core.CreatePaneResult](ctx, frontend, protocol.OperationCreatePane, protocol.CreatePaneParams{WindowID: window, Kind: core.PaneTool, Title: descriptor.Type, Tool: &tool})
		if err != nil {
			return err
		}
		pane = result.Pane
	} else {
		value := core.SplitDirection(*direction)
		if value != core.SplitHorizontal && value != core.SplitVertical {
			return errors.New("direction must be horizontal or vertical")
		}
		result, err := client.Call[core.CreatePaneResult](ctx, frontend, protocol.OperationSplitPane, protocol.SplitPaneParams{TargetPaneID: target, Direction: value, Kind: core.PaneTool, Title: descriptor.Type, Tool: &tool})
		if err != nil {
			return err
		}
		pane = result.Pane
	}
	_, err := fmt.Fprintf(stdout, "tool pane %d created\n", pane.ID)
	return err
}

func runAttention(ctx context.Context, frontend *client.Client, snapshot core.Snapshot, arguments []string, stdout io.Writer) error {
	if len(arguments) == 1 && arguments[0] == "list" {
		writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(writer, "ID\tPANE\tCLASS\tSEVERITY\tACK\tMESSAGE")
		for _, value := range snapshot.Attentions {
			_, _ = fmt.Fprintf(writer, "%d\t%d\t%s\t%s\t%t\t%s\n", value.ID, value.PaneID, value.Class, value.Severity, value.AcknowledgedAt != nil, value.Message)
		}
		return writer.Flush()
	}
	if len(arguments) == 2 && arguments[0] == "ack" {
		id, err := strconv.ParseUint(arguments[1], 10, 64)
		if err != nil || id == 0 {
			return errors.New("attention ack requires a positive ID")
		}
		_, err = client.Call[core.AttentionResult](ctx, frontend, protocol.OperationAcknowledgeAttention, protocol.AcknowledgeAttentionParams{ID: id, At: time.Now()})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "attention %d acknowledged\n", id)
		return err
	}
	return errors.New("usage: attention list | attention ack ID")
}

func firstPaneID(node core.LayoutNode) core.PaneID {
	if node.Kind == core.LayoutPane {
		return node.PaneID
	}
	for _, child := range node.Children {
		if id := firstPaneID(child); id != 0 {
			return id
		}
	}
	return 0
}

func runStash(ctx context.Context, frontend *client.Client, arguments []string, stdout io.Writer) error {
	if len(arguments) == 1 && arguments[0] == "list" {
		listed, err := client.Call[protocol.StashListResult](ctx, frontend, protocol.OperationListStash, nil)
		if err != nil {
			return err
		}
		writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(writer, "TYPE\tID\tSTATE\tORIGIN")
		for _, entry := range listed.Panes {
			state := string(entry.Pane.Kind)
			if entry.Pane.Terminal != nil {
				state = string(entry.Pane.Terminal.State)
				if entry.Pane.Terminal.HistoryAvailable {
					state += "+history"
				}
			}
			_, _ = fmt.Fprintf(writer, "pane\t%d\t%s\t%d/%d\n", entry.Pane.ID, state,
				entry.Stashed.OriginWorkspaceID, entry.Stashed.OriginWindowID)
		}
		for _, entry := range listed.Windows {
			_, _ = fmt.Fprintf(writer, "window\t%d\t%d panes\t%d\n", entry.Window.ID, len(entry.Panes), entry.Stashed.OriginWorkspaceID)
		}
		return writer.Flush()
	}
	if len(arguments) != 2 {
		return errors.New("usage: stash pane PANE | stash window WINDOW | stash list")
	}
	id, err := strconv.ParseUint(arguments[1], 10, 64)
	if err != nil || id == 0 {
		return errors.New("invalid stash target ID")
	}
	switch arguments[0] {
	case "pane":
		_, err = client.Call[core.StashPaneResult](ctx, frontend, protocol.OperationStashPane, protocol.PaneParams{PaneID: core.PaneID(id)})
	case "window":
		_, err = client.Call[core.StashWindowResult](ctx, frontend, protocol.OperationStashWindow, protocol.DeleteWindowParams{WindowID: core.WindowID(id)})
	default:
		return errors.New("usage: stash pane PANE | stash window WINDOW | stash list")
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s %d stashed\n", arguments[0], id)
	return err
}

func runRestore(ctx context.Context, frontend *client.Client, arguments []string, stdout, stderr io.Writer) error {
	if len(arguments) < 2 {
		return errors.New("usage: restore pane|window ID [options]")
	}
	id, err := strconv.ParseUint(arguments[1], 10, 64)
	if err != nil || id == 0 {
		return errors.New("invalid restore target ID")
	}
	switch arguments[0] {
	case "pane":
		flags := flag.NewFlagSet("restore pane", flag.ContinueOnError)
		flags.SetOutput(stderr)
		windowID := flags.Uint64("window", 0, "destination Window ID")
		targetID := flags.Uint64("target", 0, "target Pane ID")
		direction := flags.String("direction", "", "horizontal or vertical")
		if err := flags.Parse(arguments[2:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("unexpected restore pane arguments")
		}
		params := protocol.RestorePaneParams{
			PaneID: core.PaneID(id), DestinationWindowID: core.WindowID(*windowID), TargetPaneID: core.PaneID(*targetID),
			Direction: core.SplitDirection(*direction),
		}
		if _, err := client.Call[core.RestorePaneResult](ctx, frontend, protocol.OperationRestorePane, params); err != nil {
			return err
		}
	case "window":
		flags := flag.NewFlagSet("restore window", flag.ContinueOnError)
		flags.SetOutput(stderr)
		workspaceID := flags.Uint64("workspace", 0, "destination Workspace ID")
		if err := flags.Parse(arguments[2:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("unexpected restore window arguments")
		}
		if _, err := client.Call[core.RestoreWindowResult](ctx, frontend, protocol.OperationRestoreWindow, protocol.RestoreWindowParams{
			WindowID: core.WindowID(id), WorkspaceID: core.WorkspaceID(*workspaceID),
		}); err != nil {
			return err
		}
	default:
		return errors.New("usage: restore pane|window ID [options]")
	}
	_, err = fmt.Fprintf(stdout, "%s %d restored\n", arguments[0], id)
	return err
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
	chrome := flags.String("chrome", "auto", "Pane chrome: auto, border, or none")
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
	presentation, err := parsePanePresentation(*chrome)
	if err != nil {
		return protocol.NewTerminalParams{}, err
	}
	return protocol.NewTerminalParams{
		WindowID: core.WindowID(*windowID), TargetPaneID: core.PaneID(*targetID),
		Direction: core.SplitDirection(*direction), Title: *title, Presentation: presentation,
		Argv: argv, CWD: *workingDirectory, Env: os.Environ(), InitialSize: terminalSize(os.Stdin),
	}, nil
}

func parsePanePresentation(chrome string) (core.PanePresentation, error) {
	value := core.PaneChrome(chrome)
	if value == "auto" {
		value = core.PaneChromeAuto
	}
	switch value {
	case core.PaneChromeAuto, core.PaneChromeBorder, core.PaneChromeNone:
		return core.PanePresentation{Chrome: value}, nil
	default:
		return core.PanePresentation{}, fmt.Errorf("invalid Pane chrome %q", chrome)
	}
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

func runTerminal(ctx context.Context, frontend *client.Client, arguments []string, stdout, stderr io.Writer) error {
	separator := -1
	for index, argument := range arguments {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator == len(arguments)-1 {
		return errors.New("run requires -- followed by a command")
	}
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	cwd := flags.String("cwd", "", "working directory; defaults to the Pane's previous directory")
	if err := flags.Parse(arguments[:separator]); err != nil {
		return err
	}
	paneID, err := exactlyOnePaneID(flags.Args())
	if err != nil {
		return err
	}
	fallbackCWD, err := os.Getwd()
	if err != nil {
		return err
	}
	result, err := client.Call[protocol.TerminalOperationResult](ctx, frontend, protocol.OperationRunTerminal, protocol.RunTerminalParams{
		PaneID: paneID, Argv: append([]string(nil), arguments[separator+1:]...), CWD: *cwd, FallbackCWD: fallbackCWD,
		Env: os.Environ(), InitialSize: terminalSize(os.Stdin),
	})
	if err != nil {
		return err
	}
	return printTerminalResult(stdout, "started", result.Pane)
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
	return configuration.ShellCommand(defaultShell()), nil
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
