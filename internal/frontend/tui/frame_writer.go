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

	mu           sync.Mutex
	pending      *frameSubmission
	controls     [][]byte
	controlBytes int
	previous     *Surface
	cursor       Cursor
	closed       bool
	err          error
	wake         chan struct{}
	done         chan struct{}
}

type frameSubmission struct {
	bytes   []byte
	surface *Surface
	cursor  Cursor
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
	writer.pending = &frameSubmission{bytes: frame}
	writer.signal()
	return nil
}

// SubmitSurface transfers an immutable, complete Surface to the writer.
func (writer *latestFrameWriter) SubmitSurface(surface *Surface, cursor Cursor) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		if writer.err != nil {
			return writer.err
		}
		return errFrameWriterClosed
	}
	writer.pending = &frameSubmission{surface: surface, cursor: cursor}
	writer.signal()
	return nil
}

func (writer *latestFrameWriter) SubmitControl(data []byte) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		return errFrameWriterClosed
	}
	if len(writer.controls) >= 64 || writer.controlBytes+len(data) > 4<<20 {
		return errors.New("TUI control output queue limit exceeded")
	}
	writer.controls = append(writer.controls, append([]byte(nil), data...))
	writer.controlBytes += len(data)
	writer.signal()
	return nil
}

// Close discards any pending frame, writes final after the current write, and
// waits for the writer to stop. It is safe to call more than once.
func (writer *latestFrameWriter) Close(final []byte) error {
	writer.mu.Lock()
	if !writer.closed {
		writer.pending = &frameSubmission{bytes: final}
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
		controls := writer.controls
		writer.controls, writer.controlBytes = nil, 0
		writer.mu.Unlock()

		var data []byte
		for _, control := range controls {
			data = append(data, control...)
		}
		if frame != nil {
			if frame.surface != nil {
				data = append(data, EncodeDiff(writer.previous, frame.surface, writer.cursor, frame.cursor)...)
			} else {
				data = append(data, frame.bytes...)
			}
		}
		if len(data) != 0 {
			if err := writeAll(writer.writer, data); err != nil {
				writer.mu.Lock()
				writer.err = err
				writer.closed = true
				writer.pending = nil
				writer.mu.Unlock()
				return
			}
			if frame != nil {
				if frame.surface != nil {
					writer.previous, writer.cursor = frame.surface, frame.cursor
				} else {
					writer.previous = nil
				}
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
