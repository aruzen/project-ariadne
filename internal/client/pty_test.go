package client

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/aruzen/streammux"
	"github.com/aruzen/streammux/pty"
)

type ptyTestProcess struct {
	reader  *io.PipeReader
	writer  *io.PipeWriter
	input   lockedBuffer
	resized chan pty.Size
	done    chan struct{}
	once    sync.Once
}

type lockedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (buffer *lockedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.Buffer.Write(data)
}

func (buffer *lockedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.Buffer.String()
}

func newPTYTestProcess() *ptyTestProcess {
	reader, writer := io.Pipe()
	return &ptyTestProcess{reader: reader, writer: writer, resized: make(chan pty.Size, 4), done: make(chan struct{})}
}

func (process *ptyTestProcess) Output() io.Reader { return process.reader }
func (process *ptyTestProcess) Input() io.Writer  { return &process.input }
func (process *ptyTestProcess) Resize(size pty.Size) error {
	process.resized <- size
	return nil
}
func (process *ptyTestProcess) Terminate() error { process.finish(); return nil }
func (process *ptyTestProcess) Kill() error      { process.finish(); return nil }
func (process *ptyTestProcess) WaitStatus() (pty.ExitStatus, error) {
	<-process.done
	return pty.ExitStatus{Reason: pty.ExitReasonKilled}, nil
}
func (process *ptyTestProcess) Close() error {
	_ = process.reader.Close()
	_ = process.writer.Close()
	return nil
}
func (process *ptyTestProcess) finish() {
	process.once.Do(func() {
		_ = process.writer.Close()
		close(process.done)
	})
}

type ptyTestFactory struct{ process *ptyTestProcess }

func (factory ptyTestFactory) StartManaged(context.Context, pty.ProcessSpec) (pty.ManagedProcess, error) {
	return factory.process, nil
}

func testPTYMessageTypes() pty.MessageTypes {
	return pty.MessageTypes{
		Open: 0x1100, List: 0x1101, Inspect: 0x1102, Attach: 0x1103,
		Detach: 0x1104, Input: 0x1105, Output: 0x1106, Resize: 0x1107,
		Kill: 0x1108, Exit: 0x1109, ReplayBegin: 0x110a, ReplayEnd: 0x110b, Error: 0x110c,
	}
}

func TestPTYAttachReplayInputResizeAndDetach(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	process := newPTYTestProcess()
	managerConfig := pty.DefaultManagerConfig()
	managerConfig.MaxAttachmentsPerSession = 1
	manager, err := pty.NewManager(ctx, ptyTestFactory{process: process}, managerConfig)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer manager.Close()
	session, err := manager.Open(ctx, pty.ProcessSpec{Command: "test", InitialSize: pty.Size{Cols: 80, Rows: 24}})
	if err != nil {
		t.Fatalf("Manager Open: %v", err)
	}
	serverConnection, clientConnection := net.Pipe()
	muxConnection, err := streammux.Open(ctx, serverConnection, streammux.DefaultConfig())
	if err != nil {
		t.Fatalf("server streammux Open: %v", err)
	}
	serverPeer, err := streammux.NewPeer(muxConnection, streammux.DefaultPeerConfig())
	if err != nil {
		t.Fatalf("server NewPeer: %v", err)
	}
	serverConfig := pty.DefaultProtocolConfig()
	serverConfig.Types = testPTYMessageTypes()
	serverProtocol, err := pty.RegisterProtocol(serverPeer, manager, serverConfig)
	if err != nil {
		t.Fatalf("RegisterProtocol: %v", err)
	}
	defer serverProtocol.Close()
	go func() { _ = serverPeer.Serve(ctx) }()

	frontend, err := Open(ctx, clientConnection, DefaultConfig())
	if err != nil {
		t.Fatalf("client Open: %v", err)
	}
	defer frontend.Close()
	ptyClient, err := RegisterPTY(frontend, testPTYMessageTypes())
	if err != nil {
		t.Fatalf("RegisterPTY: %v", err)
	}
	// Sequential editors and concurrent frontend features share one protocol.
	var registrations sync.WaitGroup
	for i := 0; i < 16; i++ {
		registrations.Add(1)
		go func() {
			defer registrations.Done()
			reused, err := RegisterPTY(frontend, testPTYMessageTypes())
			if err != nil || reused != ptyClient {
				t.Errorf("RegisterPTY reuse: client=%p want=%p err=%v", reused, ptyClient, err)
			}
		}()
	}
	registrations.Wait()
	attachment, _, err := ptyClient.Attach(ctx, session.ID(), pty.ReplayHistory)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if _, _, err := ptyClient.Attach(ctx, session.ID(), pty.ReplayHistory); err != pty.ErrAlreadyAttached {
		t.Fatalf("duplicate Attach error = %v", err)
	}
	waitPTYEvent(t, attachment, PTYReplayBegin)
	waitPTYEvent(t, attachment, PTYReplayEnd)
	if _, err := process.writer.Write([]byte("output")); err != nil {
		t.Fatalf("write process output: %v", err)
	}
	event := waitPTYEvent(t, attachment, PTYOutput)
	if string(event.Data) != "output" {
		t.Fatalf("output = %q", event.Data)
	}
	if err := ptyClient.Input(ctx, session.ID(), []byte("input")); err != nil {
		t.Fatalf("Input: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for process.input.String() != "input" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if process.input.String() != "input" {
		t.Fatalf("process input = %q", process.input.String())
	}
	wantSize := pty.Size{Cols: 120, Rows: 40}
	if err := ptyClient.Resize(ctx, session.ID(), wantSize); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	select {
	case got := <-process.resized:
		if got != wantSize {
			t.Fatalf("resize = %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("Resize was not delivered")
	}
	if err := attachment.Detach(ctx); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	reconnected, _, err := ptyClient.Attach(ctx, session.ID(), pty.ReplayHistory)
	if err != nil {
		t.Fatalf("reattach: %v", err)
	}
	waitPTYEvent(t, reconnected, PTYReplayBegin)
	replayed := waitPTYEvent(t, reconnected, PTYOutput)
	if string(replayed.Data) != "output" {
		t.Fatalf("replayed output = %q", replayed.Data)
	}
	waitPTYEvent(t, reconnected, PTYReplayEnd)
	process.finish()
	exited := waitPTYEvent(t, reconnected, PTYExit)
	if exited.Exit == nil || exited.Exit.Reason != pty.ExitReasonKilled {
		t.Fatalf("exit event = %+v", exited.Exit)
	}
}

func waitPTYEvent(t *testing.T, attachment *PTYAttachment, kind PTYEventKind) PTYEvent {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-attachment.Events():
			if event.Kind == kind {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for PTY event %d", kind)
		}
	}
}
