//go:build darwin || linux || windows

package terminal

import (
	"errors"
	"os"
	"testing"
)

func TestRegularFileIsNotTerminal(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "regular-")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer file.Close()
	if IsTerminal(file) {
		t.Fatal("regular file reported as terminal")
	}
	if _, err := MakeRaw(file); !errors.Is(err, ErrNotTerminal) {
		t.Fatalf("MakeRaw error = %v", err)
	}
	if _, err := EnableOutput(file); !errors.Is(err, ErrNotTerminal) {
		t.Fatalf("EnableOutput error = %v", err)
	}
	if _, _, err := Size(file); !errors.Is(err, ErrNotTerminal) {
		t.Fatalf("Size error = %v", err)
	}
}

func TestNilRestoreStateIsRejected(t *testing.T) {
	if err := Restore(nil, nil); !errors.Is(err, ErrNotTerminal) {
		t.Fatalf("Restore error = %v", err)
	}
	if err := RestoreOutput(nil, nil); !errors.Is(err, ErrNotTerminal) {
		t.Fatalf("RestoreOutput error = %v", err)
	}
}
