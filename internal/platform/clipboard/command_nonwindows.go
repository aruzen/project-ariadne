//go:build !windows

package clipboard

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
)

func writeCommand(ctx context.Context, name string, arguments []string, data []byte) error {
	command := exec.CommandContext(ctx, name, arguments...)
	command.Stdin = bytes.NewReader(data)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("clipboard: %s: %w: %s", name, err, bytes.TrimSpace(output))
	}
	return nil
}

func readCommand(ctx context.Context, maxBytes int, name string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	output, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(output, int64(maxBytes)+1))
	waitErr := command.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if waitErr != nil {
		return nil, fmt.Errorf("clipboard: %s: %w: %s", name, waitErr, bytes.TrimSpace(stderr.Bytes()))
	}
	if len(data) > maxBytes {
		return nil, fmt.Errorf("clipboard: content exceeds %d bytes", maxBytes)
	}
	return data, nil
}
