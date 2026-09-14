//go:build darwin || linux || windows

package daemonapp

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
	"time"

	ariadneconfig "github.com/aruzen/ariadne/internal/config"
	"github.com/aruzen/ariadne/internal/daemon"
	"github.com/aruzen/ariadne/internal/platform/localipc"
	"github.com/aruzen/ariadne/internal/platform/paths"
)

// Run starts the foreground daemon and blocks until it shuts down.
func Run(arguments []string) error {
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
	flags := flag.NewFlagSet("ariadne daemon serve", flag.ContinueOnError)
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
	fileConfiguration, err := ariadneconfig.Load(*configPath)
	if err != nil {
		return err
	}

	listener, err := localipc.Listen(*socketPath)
	if err != nil {
		return err
	}
	configuration := daemon.DefaultConfig(*statePath)
	configuration.ClosePaneOnSuccessfulExit = fileConfiguration.Terminal.SuccessfulExit == ariadneconfig.SuccessfulExitClose
	configuration.Clipboard.ReadPolicy = fileConfiguration.Clipboard.Read
	configuration.Clipboard.WritePolicy = fileConfiguration.Clipboard.Write
	configuration.Clipboard.MaxTextBytes = fileConfiguration.Clipboard.MaxTextBytes
	configuration.Clipboard.Timeout = time.Duration(fileConfiguration.Clipboard.CommandTimeoutMS) * time.Millisecond
	configuration.Core.MaxAttentionEntries = fileConfiguration.Attention.MaxEntries
	configuration.Plugin.TerminalQueueBytes = fileConfiguration.Attention.PluginQueueBytes
	configuration.AgentMarkerBytes = fileConfiguration.Attention.MarkerBytes
	fileConfiguration.ApplyManager(&configuration.Manager)
	fileConfiguration.ApplyStream(&configuration.Stream)
	fileConfiguration.ApplyPeer(&configuration.Peer)
	server, loaded, err := daemon.Open(context.Background(), daemonManagedFactory(), configuration)
	if err != nil {
		_ = listener.Close()
		_ = listener.Cleanup()
		return err
	}
	if loaded.RecoveryCause != nil {
		log.Printf("ariadne daemon: recovered invalid state to %s: %v", loaded.QuarantinedPath, loaded.RecoveryCause)
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
