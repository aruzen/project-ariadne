// Package config loads Ariadne's strict, startup-only TOML configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

const (
	DefaultDetachKey = "ctrl-a d"
	DefaultMaxBytes  = 1 << 20
)

var (
	ErrInvalid      = errors.New("config: invalid configuration")
	ErrTooLarge     = errors.New("config: file too large")
	ErrInvalidInput = errors.New("config: invalid input")
)

// Config is loaded once when the daemon starts. An empty Shell selects the
// runtime $SHELL and /bin/sh fallback chain.
type Config struct {
	Shell     string `toml:"shell"`
	DetachKey string `toml:"detach_key"`
}

func Default() Config {
	return Config{DetachKey: DefaultDetachKey}
}

// Parse rejects unknown keys and TOML type mismatches.
func Parse(data []byte) (Config, error) {
	if len(data) > DefaultMaxBytes {
		return Config{}, fmt.Errorf("%w: %d bytes", ErrTooLarge, len(data))
	}
	configuration := Default()
	decoder := toml.NewDecoder(bytes.NewReader(data)).DisallowUnknownFields()
	if err := decoder.Decode(&configuration); err != nil {
		return Config{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := configuration.validate(); err != nil {
		return Config{}, err
	}
	return configuration, nil
}

// Load returns defaults when path does not exist.
func Load(path string) (Config, error) {
	if path == "" {
		return Config{}, fmt.Errorf("%w: empty path", ErrInvalidInput)
	}
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("config: open: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, DefaultMaxBytes+1))
	if err != nil {
		return Config{}, fmt.Errorf("config: read: %w", err)
	}
	if len(data) > DefaultMaxBytes {
		return Config{}, fmt.Errorf("%w: exceeds %d bytes", ErrTooLarge, DefaultMaxBytes)
	}
	return Parse(data)
}

func (configuration Config) validate() error {
	if configuration.DetachKey == "" || strings.TrimSpace(configuration.DetachKey) == "" {
		return fmt.Errorf("%w: detach_key is empty", ErrInvalid)
	}
	if strings.ContainsRune(configuration.DetachKey, 0) {
		return fmt.Errorf("%w: detach_key contains NUL", ErrInvalid)
	}
	if configuration.Shell != "" {
		if strings.TrimSpace(configuration.Shell) == "" {
			return fmt.Errorf("%w: shell contains only whitespace", ErrInvalid)
		}
		if strings.ContainsRune(configuration.Shell, 0) {
			return fmt.Errorf("%w: shell contains NUL", ErrInvalid)
		}
	}
	return nil
}
