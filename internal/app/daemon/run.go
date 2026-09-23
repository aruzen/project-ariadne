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
	"path/filepath"
	"runtime"
	"sync"
	"time"

	ariadneconfig "github.com/aruzen/ariadne/internal/config"
	"github.com/aruzen/ariadne/internal/daemon"
	"github.com/aruzen/ariadne/internal/platform/localipc"
	"github.com/aruzen/ariadne/internal/platform/paths"
	ariadneprotocol "github.com/aruzen/ariadne/internal/protocol"
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
	resolvedConfigPath, err := filepath.Abs(*configPath)
	if err != nil {
		return fmt.Errorf("resolve configuration path: %w", err)
	}
	fileConfiguration, err := ariadneconfig.Load(resolvedConfigPath)
	if err != nil {
		return err
	}

	listener, err := localipc.Listen(*socketPath)
	if err != nil {
		return err
	}
	configuration := daemon.DefaultConfig(*statePath)
	configuration.ExternalPlugin = fileConfiguration.Plugins
	configuration.ClosePaneOnSuccessfulExit = fileConfiguration.Terminal.SuccessfulExit == ariadneconfig.SuccessfulExitClose
	configuration.Clipboard.ReadPolicy = fileConfiguration.Clipboard.Read
	configuration.Clipboard.WritePolicy = fileConfiguration.Clipboard.Write
	configuration.Clipboard.MaxTextBytes = fileConfiguration.Clipboard.MaxTextBytes
	configuration.Clipboard.Timeout = time.Duration(fileConfiguration.Clipboard.CommandTimeoutMS) * time.Millisecond
	configuration.Core.MaxAttentionEntries = fileConfiguration.Attention.MaxEntries
	configuration.Plugin.TerminalQueueBytes = fileConfiguration.Attention.PluginQueueBytes
	configuration.AgentMarkerBytes = fileConfiguration.Attention.MarkerBytes
	frontendOptions := frontendGUIOptions(fileConfiguration, resolvedConfigPath)
	var frontendOptionsMu sync.RWMutex
	configuration.AriadneProtocol.GUI = frontendOptions
	configuration.AriadneProtocol.GUIProvider = func() ariadneprotocol.GUIOptions {
		frontendOptionsMu.RLock()
		defer frontendOptionsMu.RUnlock()
		return cloneGUIOptions(frontendOptions)
	}
	configuration.AriadneProtocol.ReloadFrontendConfig = func(context.Context) (ariadneprotocol.GUIOptions, error) {
		reloaded, err := ariadneconfig.Load(resolvedConfigPath)
		if err != nil {
			return ariadneprotocol.GUIOptions{}, err
		}
		next := frontendGUIOptions(reloaded, resolvedConfigPath)
		frontendOptionsMu.Lock()
		frontendOptions = next
		frontendOptionsMu.Unlock()
		return cloneGUIOptions(next), nil
	}
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

func cloneBindings(source ariadneconfig.Keybindings) map[string]string {
	result := make(map[string]string, len(source))
	for key, command := range source {
		result[key] = command
	}
	return result
}

func frontendGUIOptions(configuration ariadneconfig.Config, configPath string) ariadneprotocol.GUIOptions {
	return ariadneprotocol.GUIOptions{
		ConfigPath: configPath,
		FontFamily: configuration.GUI.FontFamily, FontSize: configuration.GUI.FontSize,
		SoftwareRendering: configuration.GUI.SoftwareRendering,
		Background:        configuration.GUI.Background, Foreground: configuration.GUI.Foreground,
		Selection: configuration.GUI.Selection, Accent: configuration.GUI.Accent,
		ColorTable:         append([]string(nil), configuration.GUI.ColorTable...),
		Shell:              configuration.ShellCommand(defaultGUIShell()),
		Editor:             configuration.EditorCommand(defaultGUIEditor()),
		Keybindings:        cloneBindings(configuration.Keybindings.Normal),
		DefaultKeybindings: cloneBindings(ariadneconfig.DefaultKeybindings()),
	}
}

func cloneGUIOptions(options ariadneprotocol.GUIOptions) ariadneprotocol.GUIOptions {
	options.ColorTable = append([]string(nil), options.ColorTable...)
	options.Shell = append([]string(nil), options.Shell...)
	options.Editor = append([]string(nil), options.Editor...)
	options.Keybindings = cloneStringMap(options.Keybindings)
	options.DefaultKeybindings = cloneStringMap(options.DefaultKeybindings)
	return options
}

func cloneStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func defaultGUIEditor() []string {
	if editor := os.Getenv("VISUAL"); editor != "" {
		return []string{editor}
	}
	if editor := os.Getenv("EDITOR"); editor != "" {
		return []string{editor}
	}
	return []string{"notepad.exe"}
}

func defaultGUIShell() []string {
	if runtime.GOOS == "windows" {
		if shell := os.Getenv("COMSPEC"); shell != "" {
			return []string{shell}
		}
		return []string{"powershell.exe", "-NoLogo"}
	}
	if shell := os.Getenv("SHELL"); shell != "" {
		return []string{shell}
	}
	return []string{"/bin/sh"}
}

func normalizeServeError(err error) error {
	if errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
