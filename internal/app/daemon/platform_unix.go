//go:build darwin || linux

package daemonapp

import (
	"os"
	"syscall"

	"github.com/aruzen/streammux/pty"
	"github.com/aruzen/streammux/pty/unixpty"
)

func daemonManagedFactory() pty.ManagedFactory { return unixpty.ManagedFactory{} }

func shutdownSignals() []os.Signal { return []os.Signal{syscall.SIGINT, syscall.SIGTERM} }
