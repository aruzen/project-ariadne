//go:build darwin || linux

package unixsocket

import (
	"fmt"
	"os"
	"path/filepath"
)

func DefaultPath() (string, error) {
	return defaultPath(os.LookupEnv, os.TempDir(), os.Getuid())
}

func defaultPath(lookup func(string) (string, bool), fallback string, uid int) (string, error) {
	if runtimeDirectory, exists := lookup("XDG_RUNTIME_DIR"); exists && runtimeDirectory != "" {
		if !filepath.IsAbs(runtimeDirectory) {
			return "", ErrInvalidPath
		}
		return filepath.Join(runtimeDirectory, "ariadne", "ariadned.sock"), nil
	}
	if temporaryDirectory, exists := lookup("TMPDIR"); exists && temporaryDirectory != "" {
		if !filepath.IsAbs(temporaryDirectory) {
			return "", ErrInvalidPath
		}
		return filepath.Join(temporaryDirectory, "ariadne", "ariadned.sock"), nil
	}
	if fallback == "" || !filepath.IsAbs(fallback) || uid < 0 {
		return "", ErrInvalidPath
	}
	return filepath.Join(fallback, fmt.Sprintf("ariadne-%d", uid), "ariadned.sock"), nil
}
