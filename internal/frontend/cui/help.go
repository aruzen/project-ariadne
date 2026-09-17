//go:build darwin || linux || windows

package cui

import (
	"fmt"
	"io"
	"strings"
)

const overviewHelp = `Ariadne is a persistent terminal multiplexer.

Usage:
  ariadne [global options] <command> [arguments]
  ariadne help [command [subcommand]]

Getting started:
  tui         Open the full-screen terminal interface
  init        Create a commented configuration template

Terminal commands:
  new         Create a terminal without attaching
  open        Create and attach to a terminal
  attach      Attach to an existing terminal
  list        List windows (or panes/workspaces)
  delete      Delete an inactive pane or empty window/workspace
  restart     Restart a retained or placeholder pane
  run         Start a new command in an existing pane
  kill        Stop a terminal, retaining its pane and history
  dismiss     Alias for 'delete pane'

Workspace commands:
  stash       Stash a pane or window
  restore     Restore a stashed pane or window
  tool        Create or list tool panes
  attention   List or acknowledge attention events
  daemon      Inspect, stop, or run the daemon
  plugin      Manage trusted local plugins and run their commands

Global options:
  --socket PATH  Override the local IPC endpoint
  -h, --help     Show help

Run 'ariadne help <command>' for command-specific help.
`

var helpPages = map[string]string{
	"plugin": `Manage trusted local process/native plugins.

Usage:
  ariadne plugin list [--json]
  ariadne plugin status [ID] [--json]
  ariadne plugin install DIRECTORY
  ariadne plugin update ID DIRECTORY
  ariadne plugin uninstall ID [--purge]
  ariadne plugin enable|disable|restart ID
  ariadne plugin grant|revoke ID CAPABILITY [all|context|workspace:IDs|pane:IDs]
  ariadne plugin [--json] run ID COMMAND [ARGS...]

Install/update leaves plugins disabled. Approve each requested capability explicitly.
Uninstall keeps ToolPanes, shared Tool state, and private data unless --purge is used.
Native libraries run in the same executable's disposable helper process.
Only trusted code should be installed; API grants do not sandbox OS access.
`,
	"init": `Create an Ariadne configuration template.

Usage:
  ariadne init

The file is created at the first applicable location:
  $ARIADNE_CONFIG_PATH/config.toml
  $XDG_CONFIG_HOME/ariadne/config.toml
  ~/.config/ariadne/config.toml

Existing files are never replaced.
`,
	"tui": `Open the full-screen terminal interface.

Usage:
  ariadne tui [options]

Options:
  --pane-frame MODE  Pane frame mode: full, split, or none
`,
	"new": `Create a terminal without attaching to it.

Usage:
  ariadne new [options] [-- COMMAND [ARG...]]

Options:
  --window ID          Destination window
  --target ID          Pane to split
  --direction MODE     Split direction: horizontal or vertical
  --title TEXT         Pane title
  --chrome MODE        Pane chrome: auto, border, or none
  --cwd DIR            Initial working directory

If COMMAND is omitted, the configured default shell is used.
`,
	"open": `Create a terminal and attach to it.

Usage:
  ariadne open [options] [-- COMMAND [ARG...]]

Options:
  --window ID          Destination window
  --target ID          Pane to split
  --direction MODE     Split direction: horizontal or vertical
  --title TEXT         Pane title
  --chrome MODE        Pane chrome: auto, border, or none
  --cwd DIR            Initial working directory

If COMMAND is omitted, the configured default shell is used.
`,
	"attach": `Attach to an existing terminal.

Usage:
  ariadne attach TERMINAL_ID
`,
	"list": `List resources, including stashed resources.

Usage:
  ariadne list [pane|window|workspace] [options]

The default unit is window.

Options:
  --json  Emit machine-readable JSON
`,
	"delete": `Delete an inactive pane or empty window/workspace.

Usage:
  ariadne delete [pane|window|workspace] ID

The default unit is window. Running panes must be stopped first with kill.
Nonempty/stashed windows and the last workspace cannot be deleted.
`,
	"restart": `Restart a retained or placeholder pane with its previous command.

Usage:
  ariadne restart PANE_ID
`,
	"run": `Start a new command in a retained or placeholder pane.

Usage:
  ariadne run [options] PANE_ID -- COMMAND [ARG...]

Options:
  --cwd DIR  Working directory; defaults to the pane's previous directory
`,
	"kill": `Stop a terminal, retaining its pane, command and history.

Usage:
  ariadne kill PANE_ID

Use restart/run to reuse the pane, or delete pane to remove it.
`,
	"dismiss": `Remove a retained pane and its terminal history.

Usage:
  ariadne dismiss PANE_ID
`,
	"stash": `Stash panes or windows without discarding their layout metadata.

Usage:
  ariadne stash pane PANE_ID
  ariadne stash window WINDOW_ID
  ariadne stash list
`,
	"stash pane": `Stash a pane.

Usage:
  ariadne stash pane PANE_ID
`,
	"stash window": `Stash a window.

Usage:
  ariadne stash window WINDOW_ID
`,
	"stash list": `List stashed panes and windows.

Usage:
  ariadne stash list
`,
	"restore": `Restore a stashed pane or window.

Usage:
  ariadne restore pane PANE_ID [options]
  ariadne restore window WINDOW_ID [options]

Run 'ariadne help restore pane' or 'ariadne help restore window' for options.
`,
	"restore pane": `Restore a stashed pane.

Usage:
  ariadne restore pane PANE_ID [options]

Options:
  --window ID       Destination window
  --target ID       Pane to split
  --direction MODE  Split direction: horizontal or vertical
`,
	"restore window": `Restore a stashed window.

Usage:
  ariadne restore window WINDOW_ID [options]

Options:
  --workspace ID  Destination workspace
`,
	"tool": `Create or list tool panes.

Usage:
  ariadne tool new [options] TYPE
  ariadne tool list

Run 'ariadne help tool new' for creation options.
`,
	"tool new": `Create a tool pane.

Usage:
  ariadne tool new [options] TYPE

Options:
  --provider NAME    Tool provider (default: ariadne)
  --instance NAME    Tool instance (default: default)
  --window ID        Destination window
  --target ID        Pane to split
  --direction MODE   Split direction: horizontal or vertical
`,
	"tool list": `List tool panes.

Usage:
  ariadne tool list
`,
	"attention": `List or acknowledge attention events.

Usage:
  ariadne attention list
  ariadne attention ack ID
`,
	"attention list": `List attention events.

Usage:
  ariadne attention list
`,
	"attention ack": `Acknowledge an attention event.

Usage:
  ariadne attention ack ID
`,
	"daemon": `Inspect, stop, or run the Ariadne daemon.

Usage:
  ariadne daemon status
  ariadne daemon stop [--force]
  ariadne daemon serve [options]

The daemon is normally started automatically.
`,
	"daemon status": `Show daemon status without starting it.

Usage:
  ariadne daemon status
`,
	"daemon stop": `Stop the daemon.

Usage:
  ariadne daemon stop [options]

Options:
  --force  Stop even when terminals are active
`,
	"daemon serve": `Run the daemon in the foreground.

Usage:
  ariadne [--socket PATH] daemon serve [options]

Options:
  --state PATH   Override the state file
  --config PATH  Override the configuration file
`,
}

// HandleHelp prints help for no arguments and explicit command-level help.
// It ignores arguments after -- because they belong to the spawned command.
func HandleHelp(arguments []string, output io.Writer) (bool, error) {
	if len(arguments) == 0 {
		return true, WriteHelp(nil, output)
	}
	if arguments[0] == "help" {
		return true, WriteHelp(arguments[1:], output)
	}
	if isHelpFlag(arguments[0]) {
		return true, WriteHelp(nil, output)
	}
	if len(arguments) >= 2 && arguments[0] == "plugin" && (arguments[1] == "run" || len(arguments) >= 3 && arguments[1] == "--json" && arguments[2] == "run") {
		return false, nil
	}
	for _, argument := range arguments[1:] {
		if argument == "--" {
			break
		}
		if isHelpFlag(argument) {
			return true, WriteHelp(invocationTopic(arguments), output)
		}
	}
	return false, nil
}

// WriteHelp writes the overview or one command-specific help page.
func WriteHelp(topic []string, output io.Writer) error {
	key := strings.Join(topic, " ")
	page := overviewHelp
	if key != "" {
		var ok bool
		page, ok = helpPages[key]
		if !ok {
			return fmt.Errorf("unknown help topic %q; run 'ariadne --help' to list commands", key)
		}
	}
	_, err := io.WriteString(output, page)
	return err
}

func invocationTopic(arguments []string) []string {
	topic := []string{arguments[0]}
	if len(arguments) < 2 || strings.HasPrefix(arguments[1], "-") {
		return topic
	}
	switch arguments[0] {
	case "attention", "daemon", "restore", "stash", "tool":
		return append(topic, arguments[1])
	default:
		return topic
	}
}

func isHelpFlag(argument string) bool {
	return argument == "-h" || argument == "--help"
}
