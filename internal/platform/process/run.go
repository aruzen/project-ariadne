// Package process runs short frontend helpers in a bounded, disposable process tree.
package process

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

var ErrOutputLimit = errors.New("helper output limit exceeded")

type boundedOutput struct {
	data     []byte
	limit    int
	cancel   context.CancelFunc
	overflow bool
}

func (output *boundedOutput) Write(data []byte) (int, error) {
	if len(output.data)+len(data) > output.limit {
		output.overflow = true
		output.cancel()
		return 0, ErrOutputLimit
	}
	output.data = append(output.data, data...)
	return len(data), nil
}

func Run(ctx context.Context, argv []string, cwd string, env []string, maxBytes int) ([]byte, error) {
	if len(argv) == 0 || maxBytes < 1 {
		return nil, errors.New("invalid helper command/limit")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Dir = cwd
	command.Env = env
	command.Stdin = nil
	command.Stderr = io.Discard
	output := &boundedOutput{limit: maxBytes, cancel: cancel}
	command.Stdout = output
	command.WaitDelay = 200 * time.Millisecond
	install, cleanup, err := prepare(command)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	if err = command.Start(); err != nil {
		return nil, err
	}
	if err = install(); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, err
	}
	err = command.Wait()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if output.overflow {
		err = ErrOutputLimit
	}
	return output.data, err
}
