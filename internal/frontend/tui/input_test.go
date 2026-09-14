package tui

import (
	"reflect"
	"testing"

	ariadneconfig "github.com/aruzen/ariadne/internal/config"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/vt/libghostty"
)

func TestInputDecoder(t *testing.T) {
	decoder := inputDecoder{}
	data, actions := decoder.Feed([]byte{'a', 0x01})
	if string(data) != "a" || len(actions) != 0 {
		t.Fatalf("first feed = %q, %v", data, actions)
	}
	data, actions = decoder.Feed([]byte{'j', 0x01, 0x01, 0x01, 'x'})
	if string(data) != "\x01" || !reflect.DeepEqual(actions, []inputAction{actionFocusDown, actionClosePane}) {
		t.Fatalf("second feed = %q, %v", data, actions)
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

func TestInputDecoderRecognizesPhaseNineBindings(t *testing.T) {
	decoder := inputDecoder{}
	input := []byte{
		0x01, 0x08, 0x01, 0x0a, 0x01, 0x0b, 0x01, 0x0c,
		0x01, 'z', 0x01, '%', 0x01, '"', 0x01, 'c', 0x01, ':',
		0x01, 's', 0x01, 'S',
	}
	data, actions := decoder.Feed(input)
	want := []inputAction{
		actionResizeLeft, actionResizeDown, actionResizeUp, actionResizeRight,
		actionZoom, actionSplitHorizontal, actionSplitVertical, actionNewWindow, actionCommandPrompt,
		actionStashPane, actionListStash,
	}
	if len(data) != 0 || !reflect.DeepEqual(actions, want) {
		t.Fatalf("Feed = %q, %v, want no data and %v", data, actions, want)
	}
}

func TestInputDecoderRecognizesToolAndAttentionBindings(t *testing.T) {
	decoder := inputDecoder{}
	data, actions := decoder.Feed([]byte{
		0x01, 'a', 0x01, 'A', 0x01, 'm', 0x01, '?',
	})
	want := []inputAction{
		actionNextAttention, actionPreviousAttention,
		actionAcknowledgeAttention, actionHelp,
	}
	if len(data) != 0 || !reflect.DeepEqual(actions, want) {
		t.Fatalf("Feed = %q, %v, want no data and %v", data, actions, want)
	}
}

func TestInputDecoderPreservesDataAndActionOrdering(t *testing.T) {
	decoder := inputDecoder{}
	tokens := decoder.FeedOrdered([]byte("before\x01:after\r"))
	want := []inputToken{
		{data: []byte("before")},
		{action: actionCommandPrompt},
		{data: []byte("after\r")},
	}
	if !reflect.DeepEqual(tokens, want) {
		t.Fatalf("tokens = %#v, want %#v", tokens, want)
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
