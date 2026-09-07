//go:build !windows

package paths

import (
	"os"
	"path/filepath"
)

func defaultStateDirectory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state"), nil
}
