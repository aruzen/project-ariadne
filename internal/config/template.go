package config

import (
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed default_config.toml
var defaultTemplate string

var ErrAlreadyExists = errors.New("config: file already exists")

// Template returns the configuration template embedded in the executable.
func Template() string {
	return defaultTemplate
}

// WriteTemplate creates path and its parent directory without replacing an
// existing file. A failed write removes only the file created by this call.
func WriteTemplate(path string) (err error) {
	if path == "" {
		return fmt.Errorf("%w: empty path", ErrInvalidInput)
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("config: create directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w: %s", ErrAlreadyExists, path)
	}
	if err != nil {
		return fmt.Errorf("config: create: %w", err)
	}
	complete := false
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("config: close: %w", closeErr)
		}
		if !complete || err != nil {
			_ = os.Remove(path)
		}
	}()
	if _, err = io.WriteString(file, defaultTemplate); err != nil {
		return fmt.Errorf("config: write: %w", err)
	}
	if err = file.Sync(); err != nil {
		return fmt.Errorf("config: sync: %w", err)
	}
	complete = true
	return nil
}
