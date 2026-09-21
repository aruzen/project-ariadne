//go:build darwin || linux || windows

// Package app selects the process role implemented by the single Ariadne executable.
package app

import (
	"flag"
	"fmt"
	"io"

	daemonapp "github.com/aruzen/ariadne/internal/app/daemon"
	"github.com/aruzen/ariadne/internal/frontend/cui"
	"github.com/aruzen/ariadne/internal/platform/localipc"
	"github.com/aruzen/ariadne/internal/plugin/external"
	"github.com/aruzen/ariadne/internal/plugin/native"
	"github.com/aruzen/ariadne/internal/version"
)

// Run dispatches to either the frontend or the foreground daemon role.
func Run(arguments []string, stdout, stderr io.Writer) error {
	if (len(arguments) == 2 || len(arguments) == 3) && arguments[0] == "plugin-helper" {
		if len(arguments) == 2 {
			return native.Run(arguments[1])
		}
		var configuration external.Config
		if err := external.DecodeParameters([]byte(arguments[2]), &configuration); err != nil {
			return err
		}
		return native.RunConfigured(arguments[1], configuration)
	}
	global := flag.NewFlagSet("ariadne", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	endpoint := global.String("socket", "", "local IPC endpoint")
	showHelp := false
	showVersion := false
	global.BoolVar(&showHelp, "help", false, "show help")
	global.BoolVar(&showHelp, "h", false, "show help")
	global.BoolVar(&showVersion, "version", false, "show version")
	if err := global.Parse(arguments); err != nil {
		return fmt.Errorf("%w; run 'ariadne --help' for usage", err)
	}
	remaining := global.Args()
	if showHelp {
		return cui.WriteHelp(remaining, stdout)
	}
	if showVersion || len(remaining) == 1 && remaining[0] == "version" {
		_, err := fmt.Fprintln(stdout, version.String())
		return err
	}
	if handled, err := cui.HandleHelp(remaining, stdout); handled {
		return err
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
