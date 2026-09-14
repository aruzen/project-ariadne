//go:build linux

package clipboard

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxCommandSelectsAvailableBackendPerOperation(t *testing.T) {
	directory := t.TempDir()
	installTestExecutable(t, directory, "wl-paste")
	t.Setenv("PATH", directory)
	t.Setenv("WAYLAND_DISPLAY", "wayland-0")
	t.Setenv("DISPLAY", "")
	name, arguments, err := linuxCommand(false)
	if err != nil || name != "wl-paste" || len(arguments) != 1 || arguments[0] != "--no-newline" {
		t.Fatalf("Wayland read = %q %v, %v", name, arguments, err)
	}
	if _, _, err := linuxCommand(true); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Wayland write without wl-copy error = %v", err)
	}

	installTestExecutable(t, directory, "xclip")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("DISPLAY", ":1")
	name, arguments, err = linuxCommand(true)
	if err != nil || name != "xclip" || len(arguments) != 2 {
		t.Fatalf("X11 write = %q %v, %v", name, arguments, err)
	}
}

func installTestExecutable(t *testing.T, directory, name string) {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
