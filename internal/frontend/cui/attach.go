//go:build darwin || linux || windows

package cui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/aruzen/ariadne/internal/client"
	ariadneconfig "github.com/aruzen/ariadne/internal/config"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/daemon"
	"github.com/aruzen/ariadne/internal/platform/paths"
	platformterminal "github.com/aruzen/ariadne/internal/platform/terminal"
	"github.com/aruzen/streammux/pty"
)

type inputKind uint8

const (
	inputData inputKind = iota + 1
	inputDetach
	inputFailure
)

type inputMessage struct {
	kind inputKind
	data []byte
	err  error
}

func attachTerminal(ctx context.Context, frontend *client.Client, terminalID core.TerminalID, stdout io.Writer) (resultErr error) {
	if err := requireAttachTTY(stdout); err != nil {
		return err
	}
	detach, err := configuredDetachSequence()
	if err != nil {
		return err
	}
	ptyClient, err := client.RegisterPTY(frontend, daemon.DefaultPTYMessageTypes())
	if err != nil {
		return err
	}
	attachment, _, err := ptyClient.Attach(ctx, terminalID, pty.ReplayHistory)
	if err != nil {
		return err
	}
	detached := false
	defer func() {
		if !detached {
			detachCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = attachment.Detach(detachCtx)
			cancel()
		}
	}()
	state, err := platformterminal.MakeRaw(os.Stdin)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, platformterminal.Restore(os.Stdin, state)) }()
	outputFile := stdout.(*os.File)
	outputState, err := platformterminal.EnableOutput(outputFile)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, platformterminal.RestoreOutput(outputFile, outputState)) }()

	resize, stopResize := watchTerminalResize(ctx, os.Stdin)
	defer stopResize()
	terminate := make(chan os.Signal, 1)
	signal.Notify(terminate, attachTerminationSignals()...)
	defer signal.Stop(terminate)
	if err := ptyClient.Resize(ctx, terminalID, terminalSize(os.Stdin)); err != nil {
		return err
	}
	input := make(chan inputMessage, 16)
	go readTerminalInput(os.Stdin, detach, input)
	for {
		select {
		case message := <-input:
			switch message.kind {
			case inputData:
				if err := ptyClient.Input(ctx, terminalID, message.data); err != nil {
					return err
				}
			case inputDetach:
				err := attachment.Detach(ctx)
				detached = true
				return err
			case inputFailure:
				return message.err
			}
		case event := <-attachment.Events():
			switch event.Kind {
			case client.PTYOutput:
				if err := writeAll(stdout, event.Data); err != nil {
					return err
				}
			case client.PTYExit:
				return nil
			case client.PTYError:
				if event.Error == nil {
					return pty.ErrInvalidPayload
				}
				return fmt.Errorf("PTY %s: %s", event.Error.Code, event.Error.Message)
			}
		case _, ok := <-resize:
			if !ok {
				resize = nil
				continue
			}
			if err := ptyClient.Resize(ctx, terminalID, terminalSize(os.Stdin)); err != nil {
				return err
			}
		case received := <-terminate:
			return fmt.Errorf("received %s", received)
		case <-frontend.Done():
			if err := frontend.Err(); err != nil {
				return err
			}
			return io.EOF
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func requireAttachTTY(stdout io.Writer) error {
	outputFile, ok := stdout.(*os.File)
	if !ok || !platformterminal.IsTerminal(os.Stdin) || !platformterminal.IsTerminal(outputFile) {
		return platformterminal.ErrNotTerminal
	}
	return nil
}

func configuredDetachSequence() ([]byte, error) {
	configurationPath, err := paths.DefaultConfigPath()
	if err != nil {
		return nil, err
	}
	configuration, err := ariadneconfig.Load(configurationPath)
	if err != nil {
		return nil, err
	}
	return parseDetachSequence(configuration.DetachKey)
}

func parseDetachSequence(value string) ([]byte, error) {
	parts := strings.Fields(strings.ToLower(value))
	if len(parts) != 2 {
		return nil, errors.New("detach_key must contain exactly two keys")
	}
	prefix, err := parseKey(parts[0])
	if err != nil {
		return nil, err
	}
	key, err := parseKey(parts[1])
	if err != nil {
		return nil, err
	}
	return []byte{prefix, key}, nil
}

func parseKey(value string) (byte, error) {
	if strings.HasPrefix(value, "ctrl-") && len(value) == len("ctrl-")+1 {
		character := value[len(value)-1]
		if character >= 'a' && character <= 'z' {
			return character - 'a' + 1, nil
		}
	}
	if len(value) == 1 {
		return value[0], nil
	}
	return 0, fmt.Errorf("unsupported detach key %q", value)
}

func readTerminalInput(reader io.Reader, detach []byte, output chan<- inputMessage) {
	buffer := make([]byte, 4096)
	filter := newTerminalInputFilter(detach)
	for {
		count, err := reader.Read(buffer)
		if count != 0 {
			data, detached := filter.Feed(buffer[:count])
			if len(data) != 0 {
				output <- inputMessage{kind: inputData, data: data}
			}
			if detached {
				output <- inputMessage{kind: inputDetach}
				return
			}
		}
		if err != nil {
			if data := filter.Flush(); len(data) != 0 {
				output <- inputMessage{kind: inputData, data: data}
			}
			output <- inputMessage{kind: inputFailure, err: err}
			return
		}
	}
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) != 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}
