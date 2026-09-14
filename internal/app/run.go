//go:build darwin || linux || windows

// Package app selects the process role implemented by the single Ariadne executable.
package app

import (
	"errors"
	"flag"
	"io"

	daemonapp "github.com/aruzen/ariadne/internal/app/daemon"
	"github.com/aruzen/ariadne/internal/frontend/cui"
	"github.com/aruzen/ariadne/internal/platform/localipc"
)

// Run dispatches to either the frontend or the foreground daemon role.
func Run(arguments []string, stdout, stderr io.Writer) error {
	global := flag.NewFlagSet("ariadne", flag.ContinueOnError)
	global.SetOutput(stderr)
	endpoint := global.String("socket", "", "local IPC endpoint")
	if err := global.Parse(arguments); err != nil {
		return err
	}
	remaining := global.Args()
	if len(remaining) == 0 {
		return errors.New("command is required")
	}
	if remaining[0] == "init" {
		return cui.Run("", remaining, stdout, stderr)
	}
	if *endpoint == "" {
		defaultEndpoint, err := localipc.DefaultEndpoint()
		if err != nil {
			return err
		}
		*endpoint = defaultEndpoint
	}
	if remaining[0] == "daemon" && len(remaining) >= 2 && remaining[1] == "serve" {
		daemonArguments := append([]string{"-socket", *endpoint}, remaining[2:]...)
		return daemonapp.Run(daemonArguments)
	}
	return cui.Run(*endpoint, remaining, stdout, stderr)
}
