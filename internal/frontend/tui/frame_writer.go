package tui

import (
	"errors"
	"io"
	"sync"
)

var errFrameWriterClosed = errors.New("TUI frame writer is closed")

// latestFrameWriter keeps terminal I/O off the frontend event loop. While a
// write is blocked, newer complete frames replace the pending frame.
type latestFrameWriter struct {
	writer io.Writer

	mu      sync.Mutex
	pending []byte
	closed  bool
	err     error
	wake    chan struct{}
	done    chan struct{}
}

func newLatestFrameWriter(writer io.Writer) *latestFrameWriter {
	output := &latestFrameWriter{writer: writer, wake: make(chan struct{}, 1), done: make(chan struct{})}
	go output.run()
	return output
}

// Submit transfers ownership of frame to the writer without waiting for I/O.
func (writer *latestFrameWriter) Submit(frame []byte) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		if writer.err != nil {
			return writer.err
		}
		return errFrameWriterClosed
	}
	writer.pending = frame
	writer.signal()
	return nil
}

// Close discards any pending frame, writes final after the current write, and
// waits for the writer to stop. It is safe to call more than once.
func (writer *latestFrameWriter) Close(final []byte) error {
	writer.mu.Lock()
	if !writer.closed {
		writer.pending = final
		writer.closed = true
		writer.signal()
	}
	done := writer.done
	writer.mu.Unlock()
	<-done
	return writer.Err()
}

func (writer *latestFrameWriter) Done() <-chan struct{} { return writer.done }

func (writer *latestFrameWriter) Err() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.err
}

func (writer *latestFrameWriter) run() {
	defer close(writer.done)
	for {
		writer.mu.Lock()
		frame := writer.pending
		writer.pending = nil
		closed := writer.closed
		writer.mu.Unlock()

		if frame != nil {
			if err := writeAll(writer.writer, frame); err != nil {
				writer.mu.Lock()
				writer.err = err
				writer.closed = true
				writer.pending = nil
				writer.mu.Unlock()
				return
			}
			continue
		}
		if closed {
			return
		}
		<-writer.wake
	}
}

func (writer *latestFrameWriter) signal() {
	select {
	case writer.wake <- struct{}{}:
	default:
	}
}
