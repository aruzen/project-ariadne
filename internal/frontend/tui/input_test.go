package tui

import (
	"reflect"
	"testing"

	ariadneconfig "github.com/aruzen/ariadne/internal/config"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/vt/libghostty"
)

func TestInputDecoder(t *testing.T) {
	decoder, err := newInputDecoder(nil)
	if err != nil {
		t.Fatalf("newInputDecoder: %v", err)
	}
	tokens := decoder.Feed([]byte{'a', 0x01})
	if !reflect.DeepEqual(tokens, []inputToken{{data: []byte{'a'}}}) {
		t.Fatalf("first feed = %#v", tokens)
	}
	tokens = decoder.Feed([]byte{'j', 0x01, 0x01, 0x01, 'x'})
	want := []inputToken{
		{commands: []string{"focus down"}},
		{commands: []string{"send-key ctrl-a"}},
		{commands: []string{"close-confirm"}},
	}
	if !reflect.DeepEqual(tokens, want) {
		t.Fatalf("second feed = %#v, want %#v", tokens, want)
	}
}

func TestSSHClipboardQueuesOuterOSC52(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "client server")
	session := session{}
	session.queueOuterClipboard("hello")
	if got := string(session.outerClipboard); got != "\x1b]52;c;aGVsbG8=\x07" {
		t.Fatalf("outer clipboard = %q", got)
	}
}

func TestDrawCopySelectionHighlightsRange(t *testing.T) {
	style := Style{Foreground: Color{R: 1}, Background: Color{B: 2}}
	surface := NewSurface(5, 2, style)
	drawCopySelection(surface, Rect{W: 5, H: 2}, 3, 0, 1, 1)
	for index, cell := range surface.Cells {
		selected := index >= 3 && index <= 6
		changed := cell.Style.Foreground != style.Foreground || cell.Style.Background != style.Background
		if selected != changed {
			t.Fatalf("cell %d selected=%t style=%+v", index, selected, cell.Style)
		}
	}
}

func TestClipboardDenyAndAskAreHandledAsModalPolicy(t *testing.T) {
	denied := clipboardRequest{
		paneID: 1, request: libghostty.ClipboardRequest{Operation: libghostty.ClipboardRead, Protocol: "osc52"},
		response: make(chan libghostty.ClipboardResponse, 1),
	}
	session := session{clipboardRead: ariadneconfig.ClipboardDeny}
	session.handleClipboardRequest(denied)
	if response := <-denied.response; response.Allowed || session.inputMode != inputModeNormal {
		t.Fatalf("denied response=%+v mode=%d", response, session.inputMode)
	}

	asked := clipboardRequest{
		paneID: 2, request: libghostty.ClipboardRequest{Operation: libghostty.ClipboardWrite, Protocol: "kitty", Text: "text"},
		response: make(chan libghostty.ClipboardResponse, 1),
	}
	session.clipboardWrite = ariadneconfig.ClipboardAsk
	session.handleClipboardRequest(asked)
	if session.inputMode != inputModeConfirm || session.confirm == "" {
		t.Fatalf("ask mode=%d message=%q", session.inputMode, session.confirm)
	}
	session.handleModalInput([]byte{'n'})
	if response := <-asked.response; response.Allowed || session.inputMode != inputModeNormal {
		t.Fatalf("rejected ask response=%+v mode=%d", response, session.inputMode)
	}
}

func TestPreferredFocusChoosesRunningPane(t *testing.T) {
	runningID := core.TerminalID(3)
	session := session{
		placements: []Placement{{PaneID: 1}, {PaneID: 2}},
		snapshot: core.Snapshot{Panes: []core.Pane{
			{ID: 1, Kind: core.PaneTerminal, Terminal: &core.TerminalInstance{State: core.TerminalExited}},
			{ID: 2, Kind: core.PaneTerminal, Terminal: &core.TerminalInstance{ID: &runningID, State: core.TerminalRunning}},
		}},
	}
	if got := session.preferredFocus(); got != 2 {
		t.Fatalf("preferred focus = %d", got)
	}
}

func TestDirectionalDistance(t *testing.T) {
	primary, secondary, valid := directionalDistance(actionFocusRight, 10, 10, 20, 13)
	if !valid || primary != 10 || secondary != 3 {
		t.Fatalf("distance = %d, %d, %t", primary, secondary, valid)
	}
	if _, _, valid := directionalDistance(actionFocusLeft, 10, 10, 20, 13); valid {
		t.Fatal("accepted candidate on the wrong side")
	}
}

func TestInputDecoderPreservesDataAndCommandOrdering(t *testing.T) {
	decoder, err := newInputDecoder(nil)
	if err != nil {
		t.Fatalf("newInputDecoder: %v", err)
	}
	tokens := decoder.Feed([]byte("before\x01:after\r"))
	want := []inputToken{
		{data: []byte("before")},
		{commands: []string{"command-prompt"}},
		{data: []byte("after\r")},
	}
	if !reflect.DeepEqual(tokens, want) {
		t.Fatalf("tokens = %#v, want %#v", tokens, want)
	}
}

func TestInputDecoderUsesConfiguredCommandChainAndForwardsUnknownInput(t *testing.T) {
	decoder, err := newInputDecoder(ariadneconfig.Keybindings{
		"ctrl-b x": "focus left; zoom on",
	})
	if err != nil {
		t.Fatalf("newInputDecoder: %v", err)
	}
	tokens := decoder.Feed([]byte("a\x02yb\x02x"))
	want := []inputToken{
		{data: []byte("a\x02yb")},
		{commands: []string{"focus left", "zoom on"}},
	}
	if !reflect.DeepEqual(tokens, want) {
		t.Fatalf("tokens = %#v, want %#v", tokens, want)
	}
}

func TestInputDecoderRejectsAmbiguousAndDuplicateSequences(t *testing.T) {
	for _, bindings := range []ariadneconfig.Keybindings{
		{"ctrl-a": "help", "ctrl-a x": "close"},
		{"ctrl-h": "help", "c-h": "close"},
	} {
		if _, err := newInputDecoder(bindings); err == nil {
			t.Fatalf("newInputDecoder(%v) accepted conflicting bindings", bindings)
		}
	}
}

func TestInputDecoderRejectsMalformedOrUnknownCommands(t *testing.T) {
	for _, command := range []string{"help;", `run "unfinished`, "typo-command"} {
		if _, err := newInputDecoder(ariadneconfig.Keybindings{"ctrl-a q": command}); err == nil {
			t.Fatalf("newInputDecoder accepted %q", command)
		}
	}
}

func TestParseKeySequence(t *testing.T) {
	got, err := parseKeySequence("C-a ctrl+H up space alt-x f5 λ")
	if err != nil {
		t.Fatalf("parseKeySequence: %v", err)
	}
	want := append([]byte{0x01, 0x08}, []byte("\x1b[A \x1bx\x1b[15~λ")...)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sequence = % x, want % x", got, want)
	}
}

func TestModalPromptEditingAndCancel(t *testing.T) {
	session := session{inputMode: inputModePrompt, promptLead: ":", prompt: "abc"}
	session.handleModalInput([]byte{0x7f, 'd'})
	if session.prompt != "abd" || session.inputMode != inputModePrompt {
		t.Fatalf("edited prompt = %q mode=%d", session.prompt, session.inputMode)
	}
	session.handleModalInput([]byte{0x1b})
	if session.inputMode != inputModeNormal || session.prompt != "" {
		t.Fatalf("cancelled prompt = %q mode=%d", session.prompt, session.inputMode)
	}
}

func TestProcessInputPreservesModeTransitionsWithinOneRead(t *testing.T) {
	decoder, err := newInputDecoder(ariadneconfig.Keybindings{"ctrl-a q": "command-prompt"})
	if err != nil {
		t.Fatalf("newInputDecoder: %v", err)
	}
	session := session{inputDecoder: decoder}
	session.processInput([]byte{'\x01', 'q', 0x1b, '\x01', 'q'})
	if session.inputMode != inputModePrompt {
		t.Fatalf("input mode = %d, want prompt", session.inputMode)
	}
}
