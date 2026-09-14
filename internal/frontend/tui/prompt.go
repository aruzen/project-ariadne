package tui

import (
	"fmt"
	"strings"
	"unicode"
)

type promptCommandInfo struct {
	Usage   string
	Summary string
}

var promptCommandCatalog = []promptCommandInfo{
	{Usage: "help [COMMAND]", Summary: "show commands or one command's usage"},
	{Usage: "command-palette", Summary: "open the command palette"},
	{Usage: "detach", Summary: "leave the TUI without stopping the daemon"},
	{Usage: "split h|v [-- command...]", Summary: "split with a shell or explicit command"},
	{Usage: "focus left|down|up|right|PANE", Summary: "move focus by direction or Pane ID"},
	{Usage: "resize left|down|up|right", Summary: "grow the focused Pane by one cell"},
	{Usage: "zoom [on|off|toggle]", Summary: "change the frontend-local zoom state"},
	{Usage: "close [PANE]", Summary: "kill or dismiss a Pane according to its state"},
	{Usage: "dismiss [PANE]", Summary: "remove an exited Pane"},
	{Usage: "restart [PANE]", Summary: "restart from the saved LaunchSpec"},
	{Usage: "run [PANE --] command [args...]", Summary: "run a new command in an exited Pane"},
	{Usage: "new-window [NAME]", Summary: "create a Window and its first terminal"},
	{Usage: "next-window", Summary: "select the next Window"},
	{Usage: "previous-window", Summary: "select the previous Window"},
	{Usage: "window ID", Summary: "select a Window by ID"},
	{Usage: "new-workspace NAME", Summary: "create a Workspace and main Window"},
	{Usage: "next-workspace", Summary: "select the next Workspace"},
	{Usage: "previous-workspace", Summary: "select the previous Workspace"},
	{Usage: "workspace ID", Summary: "select a Workspace by ID"},
	{Usage: "rename-window [ID] NAME", Summary: "rename a Window"},
	{Usage: "rename-workspace [ID] NAME", Summary: "rename a Workspace"},
	{Usage: "delete-window [ID]", Summary: "delete a Window"},
	{Usage: "delete-workspace [ID]", Summary: "delete a Workspace"},
	{Usage: "move-pane WINDOW [TARGET h|v]", Summary: "move the focused Pane to another Window"},
	{Usage: "stash-pane [PANE]", Summary: "stash a Pane"},
	{Usage: "stash-window [WINDOW]", Summary: "stash a Window"},
	{Usage: "stash-list", Summary: "open the stash ToolPane"},
	{Usage: "stash-show", Summary: "show a compact stash summary"},
	{Usage: "restore-pane PANE [WINDOW TARGET h|v]", Summary: "restore a stashed Pane"},
	{Usage: "restore-window WINDOW [WORKSPACE]", Summary: "restore a stashed Window"},
	{Usage: "preview-pane PANE", Summary: "temporarily show a stashed Pane"},
	{Usage: "preview-exit", Summary: "leave a stashed Pane preview"},
	{Usage: "tool TYPE [INSTANCE] [h|v]", Summary: "create a ToolPane"},
	{Usage: "open-tool TYPE", Summary: "open or focus a built-in ToolPane"},
	{Usage: "workspaces", Summary: "open the Workspace and Window list"},
	{Usage: "agent-status", Summary: "open the agent attention list"},
	{Usage: "diagnostics", Summary: "open daemon and plugin diagnostics"},
	{Usage: "attention next|prev|ack [ID]|list", Summary: "navigate, acknowledge, or list attention"},
	{Usage: "copy-mode", Summary: "enter scrollback copy mode"},
	{Usage: "paste", Summary: "paste the shared clipboard"},
}

func promptCommandUsages() []string {
	result := make([]string, len(promptCommandCatalog))
	for index, command := range promptCommandCatalog {
		result[index] = command.Usage
	}
	return result
}

func promptCommandNames() []string {
	result := make([]string, 0, len(promptCommandCatalog))
	for _, command := range promptCommandCatalog {
		result = append(result, strings.Fields(command.Usage)[0])
	}
	return result
}

func promptHelpLines() []string {
	lines := []string{
		"Ariadne help — j/k or arrows to scroll",
		": prompt — Tab complete, Up/Down or ^P/^N history, ^U clear, ^W erase word",
		"^A h/j/k/l focus   ^A ^H/^J/^K/^L resize",
		"^A % / ^A \" split   ^A z zoom   ^A d detach",
		"^A s stash   ^A S stash list   ^A [ copy   ^A ] paste",
		"^A a/A attention next/previous   ^A m acknowledge",
		"", ": commands",
	}
	for _, command := range promptCommandCatalog {
		lines = append(lines, ":"+command.Usage+" — "+command.Summary)
	}
	return lines
}

func promptCommandDescription(name string) (string, bool) {
	for _, command := range promptCommandCatalog {
		if strings.Fields(command.Usage)[0] == name {
			return ":" + command.Usage + " — " + command.Summary, true
		}
	}
	return "", false
}

// parsePromptCommand implements quoting without invoking a shell. Quotes are
// removed, backslash escapes the next rune, and empty quoted arguments survive.
func parsePromptCommand(line string) ([]string, error) {
	var fields []string
	var current strings.Builder
	var quote rune
	escaped := false
	started := false
	flush := func() {
		if started {
			fields = append(fields, current.String())
			current.Reset()
			started = false
		}
	}
	for _, value := range line {
		if escaped {
			current.WriteRune(value)
			started = true
			escaped = false
			continue
		}
		if value == '\\' && quote != '\'' {
			escaped = true
			started = true
			continue
		}
		if quote != 0 {
			if value == quote {
				quote = 0
			} else {
				current.WriteRune(value)
			}
			started = true
			continue
		}
		switch {
		case value == '\'' || value == '"':
			quote = value
			started = true
		case unicode.IsSpace(value):
			flush()
		default:
			current.WriteRune(value)
			started = true
		}
	}
	if escaped {
		return nil, fmt.Errorf("unfinished escape")
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	flush()
	return fields, nil
}
