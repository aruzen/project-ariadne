//go:build darwin || linux

package cui

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func watchTerminalResize(ctx context.Context, _ *os.File) (<-chan struct{}, func()) {
	signals := make(chan os.Signal, 1)
	changes := make(chan struct{}, 1)
	watchCtx, cancel := context.WithCancel(ctx)
	signal.Notify(signals, syscall.SIGWINCH)
	go func() {
		defer close(changes)
		for {
			select {
			case <-signals:
				select {
				case changes <- struct{}{}:
				default:
				}
			case <-watchCtx.Done():
				return
			}
		}
	}()
	return changes, func() {
		signal.Stop(signals)
		cancel()
	}
}

func attachTerminationSignals() []os.Signal {
	return []os.Signal{syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT}
}
