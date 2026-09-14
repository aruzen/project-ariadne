package tui

import (
	"reflect"
	"testing"

	"github.com/aruzen/ariadne/internal/core"
)

func TestParsePromptCommandQuotesAndEscapes(t *testing.T) {
	fields, err := parsePromptCommand(`run 4 -- sh -c "printf 'hello world'" "" escaped\ value`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"run", "4", "--", "sh", "-c", "printf 'hello world'", "", "escaped value"}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("fields = %#v, want %#v", fields, want)
	}
}

func TestSplitPromptCommands(t *testing.T) {
	commands, err := splitPromptCommands(`:focus left; run -- sh -c "printf 'a;b'"; send-key \;`)
	if err != nil {
		t.Fatalf("splitPromptCommands: %v", err)
	}
	want := []string{"focus left", `run -- sh -c "printf 'a;b'"`, `send-key \;`}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %#v, want %#v", commands, want)
	}
	for _, invalid := range []string{"", "help;", "help;;zoom", `run "unfinished`} {
		if _, err := splitPromptCommands(invalid); err == nil {
			t.Fatalf("splitPromptCommands(%q) succeeded", invalid)
		}
	}
}

func TestPromptCommandHelpers(t *testing.T) {
	for _, test := range []struct {
		value  string
		resize bool
		want   inputAction
	}{
		{value: "left", want: actionFocusLeft},
		{value: "j", want: actionFocusDown},
		{value: "k", resize: true, want: actionResizeUp},
		{value: "right", resize: true, want: actionResizeRight},
	} {
		got, ok := directionalAction(test.value, test.resize)
		if !ok || got != test.want {
			t.Fatalf("directionalAction(%q, %t) = %v, %t", test.value, test.resize, got, ok)
		}
	}
	if id, ok := optionalID([]string{"close"}, 7); !ok || id != 7 {
		t.Fatalf("optional fallback = %d, %t", id, ok)
	}
	if id, ok := optionalID([]string{"close", "9"}, 7); !ok || id != 9 {
		t.Fatalf("optional explicit = %d, %t", id, ok)
	}
	if id, name, ok := namedTarget([]string{"rename-window", "9", "build logs"}, 7); !ok || id != 9 || name != "build logs" {
		t.Fatalf("named target = %d, %q, %t", id, name, ok)
	}
}

func TestExecutePromptReportsParseAndUsageErrors(t *testing.T) {
	session := session{focus: core.PaneID(1)}
	session.executePrompt(`run "unterminated`)
	if session.message != "command parse error: unterminated quote" {
		t.Fatalf("parse error = %q", session.message)
	}
	session.executePrompt("resize nowhere")
	if session.message != "usage: resize left|down|up|right" {
		t.Fatalf("usage error = %q", session.message)
	}
	session.executePrompt("help split")
	if session.message == "" {
		t.Fatal("help did not produce a description")
	}
}

func TestCommandSequenceStopsWhenCommandEntersAMode(t *testing.T) {
	session := session{}
	session.executeCommandSequence([]string{"command-prompt rename-window", "zoom on"})
	if session.inputMode != inputModePrompt || session.prompt != "rename-window " {
		t.Fatalf("prompt mode = %d, prompt = %q", session.inputMode, session.prompt)
	}
	if session.zoom {
		t.Fatal("command after modal transition was executed")
	}
}

func TestPromptHistoryCompletionAndEditing(t *testing.T) {
	session := session{promptHistory: []string{"zoom on", "diagnostics"}}
	session.beginPrompt(":", "")
	session.handleModalInput([]byte("diag\t"))
	if session.prompt != "diagnostics " {
		t.Fatalf("completion = %q", session.prompt)
	}
	session.handleModalInput([]byte{0x15})
	session.handleModalInput([]byte("run echo value   "))
	session.handleModalInput([]byte{0x17})
	if session.prompt != "run echo" {
		t.Fatalf("erase word = %q", session.prompt)
	}
	session.handleModalInput([]byte("\x1b[A"))
	if session.prompt != "diagnostics" {
		t.Fatalf("previous history = %q", session.prompt)
	}
	session.handleModalInput([]byte{0x10})
	if session.prompt != "zoom on" {
		t.Fatalf("oldest history = %q", session.prompt)
	}
	session.handleModalInput([]byte{0x0e})
	if session.prompt != "diagnostics" {
		t.Fatalf("next history = %q", session.prompt)
	}
}

func TestSearchPromptDoesNotUseCommandHistory(t *testing.T) {
	session := session{promptHistory: []string{"diagnostics"}}
	session.beginPromptWithCallback("/", "needle", func(string) {})
	session.handleModalInput([]byte{0x10})
	if session.prompt != "needle" {
		t.Fatalf("search prompt used command history: %q", session.prompt)
	}
	session.recordPromptHistory("search")
	if len(session.promptHistory) != 1 {
		t.Fatalf("search was recorded: %#v", session.promptHistory)
	}
}

func TestParsePromptCommandRejectsIncompleteInput(t *testing.T) {
	for _, input := range []string{`run "unterminated`, `run trailing\`} {
		if _, err := parsePromptCommand(input); err == nil {
			t.Fatalf("parsePromptCommand(%q) succeeded", input)
		}
	}
}

func TestPromptCommandCatalogHasUniqueNames(t *testing.T) {
	seen := make(map[string]struct{})
	for _, command := range promptCommandCatalog {
		name := command.Usage
		for index, value := range name {
			if value == ' ' {
				name = name[:index]
				break
			}
		}
		if _, exists := seen[name]; exists {
			t.Fatalf("duplicate command %q", name)
		}
		seen[name] = struct{}{}
	}
}
