package tui

import (
	"strings"
	"testing"

	"github.com/aruzen/ariadne/internal/vt/libghostty"
)

func TestBusyVTInputRetriesWithoutLosingOrder(t *testing.T) {
	terminal, err := libghostty.NewTerminal(20, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	if err := terminal.Write([]byte("\x1b[?2004h\x1b[?1002h\x1b[?1006h")); err != nil {
		t.Fatal(err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	if err := terminal.SetClipboardHandler(func(libghostty.ClipboardRequest) libghostty.ClipboardResponse {
		close(entered)
		<-release
		return libghostty.ClipboardResponse{}
	}); err != nil {
		t.Fatal(err)
	}
	go func() { done <- terminal.Write([]byte("\x1b]52;c;aGk=\x07")) }()
	<-entered
	view := paneView{content: &terminalPaneContent{terminal: terminal, cols: 20, rows: 3}}
	for _, input := range []paneInput{
		{data: []byte("echo 界"), paste: true},
		{data: []byte("\r")}, {data: []byte("next")},
		{mouse: &paneMouseInput{event: MouseEvent{Button: 1}, cols: 20, rows: 3, pressed: true}},
		{mouse: &paneMouseInput{event: MouseEvent{Button: 1, Action: 1}, cols: 20, rows: 3}},
	} {
		if err := view.enqueueInput(input); err != nil {
			t.Fatal(err)
		}
	}
	var writes []string
	send := func(data []byte) error { writes = append(writes, string(data)); return nil }
	if err := view.drainInput(send); err != nil {
		t.Fatal(err)
	}
	if len(writes) != 0 || len(view.inputQueue) != 4 {
		t.Fatalf("busy input lost or overtaken: %+v %q", view.inputQueue, writes)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := view.drainInput(send); err != nil {
		t.Fatal(err)
	}
	want := []string{"\x1b[200~echo 界\x1b[201~", "\rnext", "\x1b[<0;1;1M", "\x1b[<0;1;1m"}
	if strings.Join(writes, "|") != strings.Join(want, "|") || len(view.inputQueue) != 0 || view.inputBytes != 0 {
		t.Fatalf("writes=%q queue=%+v bytes=%d", writes, view.inputQueue, view.inputBytes)
	}
}

func TestPendingInputQueueIsBoundedAndOwnsData(t *testing.T) {
	view := paneView{}
	data := []byte("owned")
	if err := view.enqueueInput(paneInput{data: data, paste: true}); err != nil {
		t.Fatal(err)
	}
	data[0] = 'X'
	if string(view.inputQueue[0].data) != "owned" {
		t.Fatal("input ownership was not copied")
	}
	if err := view.enqueueInput(paneInput{data: make([]byte, 4<<20), paste: true}); err == nil {
		t.Fatal("unbounded pending input")
	}
	for len(view.inputQueue) < 64 {
		if err := view.enqueueInput(paneInput{data: []byte("x"), paste: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := view.enqueueInput(paneInput{data: []byte("x"), paste: true}); err == nil {
		t.Fatal("unbounded pending input count")
	}
}

func TestPendingMouseMotionPreservesPressAndRelease(t *testing.T) {
	view := paneView{}
	for _, e := range []MouseEvent{{Button: 1}, {Button: 1, Action: 2, X: 1}, {Button: 1, Action: 2, X: 9}, {Button: 1, Action: 1, X: 9}} {
		if err := view.enqueueInput(paneInput{mouse: &paneMouseInput{event: e}}); err != nil {
			t.Fatal(err)
		}
	}
	if len(view.inputQueue) != 3 || view.inputQueue[0].mouse.event.Action != 0 || view.inputQueue[1].mouse.event.X != 9 || view.inputQueue[2].mouse.event.Action != 1 {
		t.Fatalf("gesture=%+v", view.inputQueue)
	}
}
