//go:build linux

package clipboard

import (
	"context"
	"os"
	"os/exec"
)

type systemBackend struct{}

func NewSystemBackend() Backend { return systemBackend{} }

func (systemBackend) Read(ctx context.Context, maxBytes int) ([]byte, error) {
	name, arguments, err := linuxCommand(false)
	if err != nil {
		return nil, err
	}
	return readCommand(ctx, maxBytes, name, arguments...)
}

func (systemBackend) Write(ctx context.Context, data []byte) error {
	name, arguments, err := linuxCommand(true)
	if err != nil {
		return err
	}
	return writeCommand(ctx, name, arguments, data)
}

func linuxCommand(write bool) (string, []string, error) {
	if os.Getenv("WAYLAND_DISPLAY") != "" {
		name := "wl-paste"
		arguments := []string{"--no-newline"}
		if write {
			name = "wl-copy"
			arguments = []string{"--type", "text/plain;charset=utf-8"}
		}
		if _, err := exec.LookPath(name); err == nil {
			return name, arguments, nil
		}
	}
	if os.Getenv("DISPLAY") != "" {
		if _, err := exec.LookPath("xclip"); err == nil {
			arguments := []string{"-selection", "clipboard"}
			if !write {
				arguments = append(arguments, "-out")
			}
			return "xclip", arguments, nil
		}
		if _, err := exec.LookPath("xsel"); err == nil {
			mode := "--output"
			if write {
				mode = "--input"
			}
			return "xsel", []string{"--clipboard", mode}, nil
		}
	}
	return "", nil, ErrUnavailable
}
