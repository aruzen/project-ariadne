//go:build darwin || linux

package localipc

import (
	"os"
	"path/filepath"
	"testing"
)

func testEndpoint(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "ariadne-ipc-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return filepath.Join(directory, "ariadned.sock")
}
