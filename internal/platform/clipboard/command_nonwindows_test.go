//go:build !windows

package clipboard

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestReadCommandBoundsOutputAndHonorsTimeout(t *testing.T) {
	data, err := readCommand(context.Background(), 5, "/usr/bin/printf", "hello")
	if err != nil || string(data) != "hello" {
		t.Fatalf("readCommand = %q, %v", data, err)
	}
	if _, err := readCommand(context.Background(), 4, "/usr/bin/printf", "hello"); err == nil {
		t.Fatal("readCommand accepted oversized output")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = readCommand(ctx, 1, "/bin/sleep", "1")
	if err == nil || (!errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil) {
		t.Fatalf("timed readCommand error = %v", err)
	}
}

func TestWriteCommandUsesDirectProcessInput(t *testing.T) {
	if err := writeCommand(context.Background(), "/usr/bin/true", nil, []byte("clipboard text")); err != nil {
		t.Fatalf("writeCommand: %v", err)
	}
}
