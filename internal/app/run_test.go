//go:build darwin || linux || windows

package app

import (
	"bytes"
	"testing"
)

func TestRunRoutesDaemonServe(t *testing.T) {
	var output bytes.Buffer
	err := Run([]string{"daemon", "serve", "unexpected"}, &output, &output)
	if err == nil || err.Error() != "unexpected arguments" {
		t.Fatalf("Run daemon serve error = %v", err)
	}
}

func TestRunRequiresCommand(t *testing.T) {
	var output bytes.Buffer
	err := Run(nil, &output, &output)
	if err == nil || err.Error() != "command is required" {
		t.Fatalf("Run error = %v", err)
	}
}

func TestRunInitDoesNotResolveIPC(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("ARIADNE_CONFIG_PATH", directory)
	t.Setenv("XDG_RUNTIME_DIR", "relative-path")
	var output bytes.Buffer
	if err := Run([]string{"init"}, &output, &output); err != nil {
		t.Fatalf("Run init: %v", err)
	}
}
