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
