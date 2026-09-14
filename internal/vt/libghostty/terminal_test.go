//go:build cgo && (darwin || (linux && amd64) || (windows && amd64))

package libghostty

import (
	"strings"
	"testing"
)

func TestTerminalScreen(t *testing.T) {
	terminal, err := NewTerminal(8, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	if err := terminal.Write([]byte("one\r\n\x1b[1;32mtwo\x1b[0m")); err != nil {
		t.Fatal(err)
	}
	screen, err := terminal.Screen()
	if err != nil {
		t.Fatal(err)
	}
	if screen.Cols != 8 || screen.Rows != 3 || screen.At(0, 0).Text != "o" || screen.At(2, 1).Text != "o" {
		t.Fatalf("unexpected screen: %+v", screen)
	}
	green := screen.At(0, 1).Style.Foreground
	if green.G <= green.R || green.G <= green.B {
		t.Fatalf("green foreground not preserved: %+v", green)
	}
	if err := terminal.Resize(10, 4); err != nil {
		t.Fatal(err)
	}
	resized, err := terminal.Screen()
	if err != nil || resized.Cols != 10 || resized.Rows != 4 {
		t.Fatalf("resized screen = %dx%d, %v", resized.Cols, resized.Rows, err)
	}
}

func TestTerminalRejectsInvalidSizeAndUseAfterClose(t *testing.T) {
	if _, err := NewTerminal(0, 1); err == nil {
		t.Fatal("NewTerminal accepted zero columns")
	}
	terminal, err := NewTerminal(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	terminal.Close()
	if err := terminal.Write([]byte("x")); err == nil {
		t.Fatal("Write accepted a closed terminal")
	}
}

func TestTerminalReturnsPTYResponses(t *testing.T) {
	terminal, err := NewTerminal(8, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	response, err := terminal.WriteWithResponse([]byte("\x1b[6n"))
	if err != nil {
		t.Fatal(err)
	}
	if string(response) != "\x1b[1;1R" {
		t.Fatalf("response = %q", response)
	}
}

func TestTerminalScrollSelectionSearchAndPaste(t *testing.T) {
	terminal, err := NewTerminal(8, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	if err := terminal.Write([]byte("zero\r\none\r\ntwo one\r\nthree\r\nfour")); err != nil {
		t.Fatal(err)
	}
	before, err := terminal.Scrollbar()
	if err != nil || before.Total <= before.Length {
		t.Fatalf("scrollbar before = %+v, %v", before, err)
	}
	if err := terminal.Scroll(-1); err != nil {
		t.Fatal(err)
	}
	after, err := terminal.Scrollbar()
	if err != nil || after.Offset >= before.Offset {
		t.Fatalf("scrollbar after = %+v, before=%+v, err=%v", after, before, err)
	}
	total, first, err := terminal.Search("one", true)
	if err != nil || total != 2 {
		t.Fatalf("Search = total %d, selected %d, %v", total, first, err)
	}
	_, second, err := terminal.Search("one", true)
	if err != nil || second == first {
		t.Fatalf("next Search selected %d after %d, %v", second, first, err)
	}
	if err := terminal.ScrollBottom(); err != nil {
		t.Fatal(err)
	}
	if err := terminal.BeginSelection(0, 2); err != nil {
		t.Fatal(err)
	}
	if err := terminal.AdjustSelection(SelectionRight); err != nil {
		t.Fatal(err)
	}
	if text, err := terminal.SelectionText(); err != nil || text != "fo" {
		t.Fatalf("SelectionText = %q, %v", text, err)
	}
	if err := terminal.ClearSelection(); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.SelectionText(); err != ErrNoSelection {
		t.Fatalf("SelectionText after clear error = %v", err)
	}
	if err := terminal.Write([]byte("\x1b[?2004h")); err != nil {
		t.Fatal(err)
	}
	response, err := terminal.Paste([]byte("hello"), false)
	if err != nil || string(response) != "\x1b[200~hello\x1b[201~" {
		t.Fatalf("Paste = %q, %v", response, err)
	}
}

func TestTerminalUnsafePasteRequiresConfirmation(t *testing.T) {
	terminal, err := NewTerminal(8, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	if _, err := terminal.Paste([]byte("one\ntwo"), false); err != ErrUnsafePaste {
		t.Fatalf("unsafe Paste error = %v", err)
	}
	response, err := terminal.Paste([]byte("one\ntwo"), true)
	if err != nil || string(response) != "one\rtwo" {
		t.Fatalf("confirmed Paste = %q, %v", response, err)
	}
}

func TestTerminalClipboardProtocolsAndSplitInput(t *testing.T) {
	terminal, err := NewTerminal(8, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	var writes []ClipboardRequest
	if err := terminal.SetClipboardHandler(func(request ClipboardRequest) ClipboardResponse {
		if request.Operation == ClipboardWrite {
			writes = append(writes, request)
			return ClipboardResponse{Allowed: true}
		}
		return ClipboardResponse{Allowed: true, Text: "read-value"}
	}); err != nil {
		t.Fatal(err)
	}
	if response, err := terminal.WriteWithResponse([]byte("\x1b]52;c;aGVs")); err != nil || len(response) != 0 {
		t.Fatalf("first OSC 52 fragment = %q, %v", response, err)
	}
	if _, err := terminal.WriteWithResponse([]byte("bG8=\x07")); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.WriteWithResponse([]byte("\x1b]1337;Copy=:aVRlcm0=\x1b\\")); err != nil {
		t.Fatal(err)
	}
	kitty := "\x1b]5522;type=write:id=c1\x1b\\" +
		"\x1b]5522;type=wdata:mime=dGV4dC9wbGFpbg==;R2hvc3Q=\x1b\\" +
		"\x1b]5522;type=wdata:mime=dGV4dC9wbGFpbg==;dHk=\x1b\\" +
		"\x1b]5522;type=wdata\x1b\\"
	response, err := terminal.WriteWithResponse([]byte(kitty))
	if err != nil || !strings.Contains(string(response), "type=write:status=DONE:id=c1") {
		t.Fatalf("Kitty write response = %q, %v", response, err)
	}
	if len(writes) != 3 || writes[0].Text != "hello" || writes[1].Text != "iTerm" || writes[2].Text != "Ghostty" {
		t.Fatalf("clipboard writes = %+v", writes)
	}

	response, err = terminal.WriteWithResponse([]byte("\x1b]52;c;?\x07"))
	if err != nil || !strings.Contains(string(response), "cmVhZC12YWx1ZQ==") {
		t.Fatalf("OSC 52 read response = %q, %v", response, err)
	}
	response, err = terminal.WriteWithResponse([]byte("\x1b]5522;type=read:id=r1;dGV4dC9wbGFpbg==\x1b\\"))
	if err != nil || !strings.Contains(string(response), "type=read:status=DATA:id=r1") || !strings.Contains(string(response), "cmVhZC12YWx1ZQ==") {
		t.Fatalf("Kitty read response = %q, %v", response, err)
	}
}

func TestTerminalClipboardLimitAndDeniedReadDoNotLeakText(t *testing.T) {
	terminal, err := NewTerminal(8, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	if err := terminal.SetClipboardMaxBytes(4); err != nil {
		t.Fatal(err)
	}
	writes := 0
	if err := terminal.SetClipboardHandler(func(request ClipboardRequest) ClipboardResponse {
		if request.Operation == ClipboardWrite {
			writes++
		}
		return ClipboardResponse{Allowed: false, Text: "must-not-leak"}
	}); err != nil {
		t.Fatal(err)
	}
	if response, err := terminal.WriteWithResponse([]byte("\x1b]52;c;aGVsbG8=\x07")); err != nil || len(response) != 0 {
		t.Fatalf("oversized write response = %q, %v", response, err)
	}
	if writes != 0 {
		t.Fatalf("oversized clipboard write reached handler %d times", writes)
	}
	response, err := terminal.WriteWithResponse([]byte("\x1b]52;c;?\x07"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(response), "bXVzdC1ub3QtbGVhaw==") || strings.Contains(string(response), "must-not-leak") {
		t.Fatalf("denied clipboard text leaked in response: %q", response)
	}
}
