//go:build darwin || linux || windows

package app

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunRoutesDaemonServe(t *testing.T) {
	var output bytes.Buffer
	err := Run([]string{"daemon", "serve", "unexpected"}, &output, &output)
	if err == nil || err.Error() != "unexpected arguments" {
		t.Fatalf("Run daemon serve error = %v", err)
	}
}

func TestRunWithoutCommandShowsHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := Run(nil, &stdout, &stderr); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage:\n") || !strings.Contains(stdout.String(), "ariadne help") || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestRunHelpFormsDoNotResolveIPC(t *testing.T) {
	for _, arguments := range [][]string{
		{"--help"},
		{"-h"},
		{"help"},
		{"help", "new"},
		{"new", "--help"},
		{"daemon", "stop", "--help"},
	} {
		t.Run(strings.Join(arguments, "_"), func(t *testing.T) {
			t.Setenv("XDG_RUNTIME_DIR", "relative-path")
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			if err := Run(arguments, &stdout, &stderr); err != nil {
				t.Fatalf("Run(%q): %v", arguments, err)
			}
			if !strings.Contains(stdout.String(), "Usage:\n") || stderr.Len() != 0 {
				t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunUnknownGlobalOptionSuggestsHelp(t *testing.T) {
	var output bytes.Buffer
	err := Run([]string{"--unknown"}, &output, &output)
	if err == nil || !strings.Contains(err.Error(), "ariadne --help") {
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
