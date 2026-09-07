//go:build darwin || linux || windows

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"

	ariadneconfig "github.com/aruzen/ariadne/config"
	"github.com/aruzen/ariadne/daemon"
	"github.com/aruzen/ariadne/platform/localipc"
	"github.com/aruzen/ariadne/platform/paths"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Printf("ariadned: %v", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	defaultSocket, err := localipc.DefaultEndpoint()
	if err != nil {
		return err
	}
	defaultState, err := paths.DefaultStatePath()
	if err != nil {
		return err
	}
	defaultConfiguration, err := paths.DefaultConfigPath()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("ariadned", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	socketPath := flags.String("socket", defaultSocket, "local IPC endpoint")
	statePath := flags.String("state", defaultState, "state JSON path")
	configPath := flags.String("config", defaultConfiguration, "configuration TOML path")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if _, err := ariadneconfig.Load(*configPath); err != nil {
		return err
	}

	listener, err := localipc.Listen(*socketPath)
	if err != nil {
		return err
	}
	configuration := daemon.DefaultConfig(*statePath)
	server, loaded, err := daemon.Open(context.Background(), daemonManagedFactory(), configuration)
	if err != nil {
		_ = listener.Close()
		_ = listener.Cleanup()
		return err
	}
	if loaded.RecoveryCause != nil {
		log.Printf("ariadned: recovered invalid state to %s: %v", loaded.QuarantinedPath, loaded.RecoveryCause)
	}

	signals := make(chan os.Signal, 2)
	signal.Notify(signals, shutdownSignals()...)
	defer signal.Stop(signals)
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	select {
	case err := <-serveDone:
		return normalizeServeError(err)
	case <-signals:
	}
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- server.Close(context.Background()) }()
	select {
	case <-signals:
		os.Exit(2)
	case shutdownErr := <-shutdownDone:
		serveErr := <-serveDone
		return errors.Join(shutdownErr, normalizeServeError(serveErr))
	}
	return nil
}

func normalizeServeError(err error) error {
	if errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
