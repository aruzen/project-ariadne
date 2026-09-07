// Package paths resolves per-user Ariadne configuration and state paths.
package paths

import (
	"errors"
	"os"
	"path/filepath"
)

var ErrHomeUnavailable = errors.New("paths: user home is unavailable")

func DefaultConfigPath() (string, error) {
	if directory := os.Getenv("XDG_CONFIG_HOME"); directory != "" {
		if !filepath.IsAbs(directory) {
			return "", ErrHomeUnavailable
		}
		return filepath.Join(directory, "ariadne", "config.toml"), nil
	}
	directory, err := os.UserConfigDir()
	if err != nil || directory == "" {
		return "", ErrHomeUnavailable
	}
	return filepath.Join(directory, "ariadne", "config.toml"), nil
}

func DefaultStatePath() (string, error) {
	if directory := os.Getenv("XDG_STATE_HOME"); directory != "" {
		if !filepath.IsAbs(directory) {
			return "", ErrHomeUnavailable
		}
		return filepath.Join(directory, "ariadne", "state.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", ErrHomeUnavailable
	}
	return filepath.Join(home, ".local", "state", "ariadne", "state.json"), nil
}
