//go:build windows

package daemonapp

import (
	"os"

	"github.com/aruzen/streammux/pty"
	"github.com/aruzen/streammux/pty/windowspty"
)

func daemonManagedFactory() pty.ManagedFactory { return windowspty.ManagedFactory{} }

func shutdownSignals() []os.Signal { return []os.Signal{os.Interrupt} }
