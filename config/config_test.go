package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDefaultsAndOverrides(t *testing.T) {
	configuration, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse defaults: %v", err)
	}
	if configuration != Default() {
		t.Fatalf("defaults = %+v, want %+v", configuration, Default())
	}
	configuration, err = Parse([]byte("shell = \"/bin/zsh\"\ndetach_key = \"ctrl-b x\"\n"))
	if err != nil {
		t.Fatalf("Parse overrides: %v", err)
	}
	if configuration.Shell != "/bin/zsh" || configuration.DetachKey != "ctrl-b x" {
		t.Fatalf("overrides = %+v", configuration)
	}
}

func TestParseIsStrict(t *testing.T) {
	for _, test := range []struct {
		name string
		data string
	}{
		{name: "unknown key", data: "unknown = true\n"},
		{name: "unknown table", data: "[unknown]\nvalue = 1\n"},
		{name: "wrong type", data: "detach_key = 1\n"},
		{name: "duplicate", data: "shell = \"a\"\nshell = \"b\"\n"},
		{name: "blank detach", data: "detach_key = \"   \"\n"},
		{name: "blank shell", data: "shell = \"   \"\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Parse([]byte(test.data)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("Parse error = %v", err)
			}
		})
	}
}

func TestLoadMissingAndExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	configuration, err := Load(path)
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if configuration != Default() {
		t.Fatalf("missing config = %+v", configuration)
	}
	if err := os.WriteFile(path, []byte("shell = \"fish\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	configuration, err = Load(path)
	if err != nil {
		t.Fatalf("Load existing: %v", err)
	}
	if configuration.Shell != "fish" || configuration.DetachKey != DefaultDetachKey {
		t.Fatalf("existing config = %+v", configuration)
	}
}

func TestLoadRejectsOversize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(strings.Repeat(" ", DefaultMaxBytes+1)), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := Load(path); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsEmptyPath(t *testing.T) {
	if _, err := Load(""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Load error = %v", err)
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("shell = \"/bin/sh\"\n"))
	f.Add([]byte("unknown = true\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Parse(data)
	})
}
