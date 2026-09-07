//go:build darwin || linux

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func missingEndpoint(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "ariadne-cli-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return filepath.Join(directory, "missing.sock")
}
