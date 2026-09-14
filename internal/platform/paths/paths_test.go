package paths

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestXDGPaths(t *testing.T) {
	t.Setenv("ARIADNE_CONFIG_PATH", "")
	configurationDirectory := filepath.Join(t.TempDir(), "config")
	stateDirectory := filepath.Join(t.TempDir(), "state")
	t.Setenv("XDG_CONFIG_HOME", configurationDirectory)
	t.Setenv("XDG_STATE_HOME", stateDirectory)
	configuration, err := DefaultConfigPath()
	if err != nil || configuration != filepath.Join(configurationDirectory, "ariadne", "config.toml") {
		t.Fatalf("DefaultConfigPath = %q, %v", configuration, err)
	}
	state, err := DefaultStatePath()
	if err != nil || state != filepath.Join(stateDirectory, "ariadne", "state.json") {
		t.Fatalf("DefaultStatePath = %q, %v", state, err)
	}
}

func TestDefaultConfigLivesUnderDotConfig(t *testing.T) {
	t.Setenv("ARIADNE_CONFIG_PATH", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configuration, err := DefaultConfigPath()
	if err != nil {
		t.Fatalf("DefaultConfigPath: %v", err)
	}
	if want := filepath.Join(home, ".config", "ariadne", "config.toml"); configuration != want {
		t.Fatalf("DefaultConfigPath = %q, want %q", configuration, want)
	}
}

func TestRelativeXDGPathsAreRejected(t *testing.T) {
	t.Setenv("ARIADNE_CONFIG_PATH", "")
	t.Setenv("XDG_CONFIG_HOME", "relative")
	if _, err := DefaultConfigPath(); !errors.Is(err, ErrHomeUnavailable) {
		t.Fatalf("DefaultConfigPath error = %v", err)
	}
	t.Setenv("XDG_STATE_HOME", "relative")
	if _, err := DefaultStatePath(); !errors.Is(err, ErrHomeUnavailable) {
		t.Fatalf("DefaultStatePath error = %v", err)
	}
}

func TestAriadneConfigPathHasHighestPriority(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "ariadne-config")
	t.Setenv("ARIADNE_CONFIG_PATH", directory)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "xdg"))
	configuration, err := DefaultConfigPath()
	if err != nil || configuration != filepath.Join(directory, "config.toml") {
		t.Fatalf("DefaultConfigPath = %q, %v", configuration, err)
	}
}

func TestRelativeAriadneConfigPathIsRejected(t *testing.T) {
	t.Setenv("ARIADNE_CONFIG_PATH", "relative")
	if _, err := DefaultConfigPath(); !errors.Is(err, ErrHomeUnavailable) {
		t.Fatalf("DefaultConfigPath error = %v", err)
	}
}
