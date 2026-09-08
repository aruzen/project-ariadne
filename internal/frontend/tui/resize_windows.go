//go:build windows

package tui

import (
	"context"
	"os"
	"time"

	platformterminal "github.com/aruzen/ariadne/internal/platform/terminal"
)

func watchResize(ctx context.Context, file *os.File) (<-chan struct{}, func()) {
	changes := make(chan struct{}, 1)
	watchCtx, cancel := context.WithCancel(ctx)
	lastCols, lastRows, _ := platformterminal.Size(file)
	go func() {
		defer close(changes)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				cols, rows, err := platformterminal.Size(file)
				if err == nil && (cols != lastCols || rows != lastRows) {
					lastCols, lastRows = cols, rows
					select {
					case changes <- struct{}{}:
					default:
					}
				}
			case <-watchCtx.Done():
				return
			}
		}
	}()
	return changes, cancel
}

func terminationSignals() []os.Signal { return []os.Signal{os.Interrupt} }
