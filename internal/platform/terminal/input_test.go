//go:build darwin || linux || windows

package terminal

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestReadContextCancellationReleasesReaderAndPreservesInput(t *testing.T) {
	input, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer output.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := ReadContext(ctx, input, make([]byte, 16)); done <- err }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled read is still consuming input")
	}
	if _, err := output.Write([]byte("next")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 16)
	nextCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	count, err := ReadContext(nextCtx, input, buffer)
	if err != nil || string(buffer[:count]) != "next" {
		t.Fatalf("next dialogue input: %q %v", buffer[:count], err)
	}
}
