package tui

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"
)

type gatedFrameOutput struct {
	started chan struct{}
	release chan struct{}
	writes  chan []byte
	once    sync.Once
}

func (output *gatedFrameOutput) Write(data []byte) (int, error) {
	output.once.Do(func() {
		close(output.started)
		<-output.release
	})
	output.writes <- append([]byte(nil), data...)
	return len(data), nil
}

func TestLatestFrameWriterReplacesPendingFrame(t *testing.T) {
	output := &gatedFrameOutput{
		started: make(chan struct{}), release: make(chan struct{}), writes: make(chan []byte, 3),
	}
	writer := newLatestFrameWriter(output)
	if err := writer.Submit([]byte("one")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-output.started:
	case <-time.After(time.Second):
		t.Fatal("frame writer did not start")
	}
	if err := writer.Submit([]byte("discarded")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Submit([]byte("latest")); err != nil {
		t.Fatal(err)
	}
	close(output.release)
	for _, expected := range [][]byte{[]byte("one"), []byte("latest")} {
		select {
		case actual := <-output.writes:
			if !bytes.Equal(actual, expected) {
				t.Fatalf("write = %q, want %q", actual, expected)
			}
		case <-time.After(time.Second):
			t.Fatalf("missing write %q", expected)
		}
	}
	if err := writer.Close([]byte("final")); err != nil {
		t.Fatal(err)
	}
	if actual := <-output.writes; !bytes.Equal(actual, []byte("final")) {
		t.Fatalf("final write = %q", actual)
	}
}

type failingFrameOutput struct{ err error }

func (output failingFrameOutput) Write([]byte) (int, error) { return 0, output.err }

func TestLatestFrameWriterReportsOutputFailure(t *testing.T) {
	expected := errors.New("output failed")
	writer := newLatestFrameWriter(failingFrameOutput{err: expected})
	if err := writer.Submit([]byte("frame")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-writer.Done():
	case <-time.After(time.Second):
		t.Fatal("frame writer did not report failure")
	}
	if !errors.Is(writer.Err(), expected) {
		t.Fatalf("Err() = %v, want %v", writer.Err(), expected)
	}
	if err := writer.Close([]byte("final")); !errors.Is(err, expected) {
		t.Fatalf("Close() = %v, want %v", err, expected)
	}
}
