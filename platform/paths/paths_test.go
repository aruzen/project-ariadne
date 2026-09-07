package paths

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestXDGPaths(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/config")
	t.Setenv("XDG_STATE_HOME", "/state")
	configuration, err := DefaultConfigPath()
	if err != nil || configuration != filepath.Join("/config", "ariadne", "config.toml") {
		t.Fatalf("DefaultConfigPath = %q, %v", configuration, err)
	}
	state, err := DefaultStatePath()
	if err != nil || state != filepath.Join("/state", "ariadne", "state.json") {
		t.Fatalf("DefaultStatePath = %q, %v", state, err)
	}
}

func TestRelativeXDGPathsAreRejected(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "relative")
	if _, err := DefaultConfigPath(); !errors.Is(err, ErrHomeUnavailable) {
		t.Fatalf("DefaultConfigPath error = %v", err)
	}
	t.Setenv("XDG_STATE_HOME", "relative")
	if _, err := DefaultStatePath(); !errors.Is(err, ErrHomeUnavailable) {
		t.Fatalf("DefaultStatePath error = %v", err)
	}
}
