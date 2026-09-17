//go:build darwin || linux || windows

package cui

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/aruzen/ariadne/internal/client"
	"github.com/aruzen/ariadne/internal/config"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/plugin/external"
)

func runPlugin(ctx context.Context, frontend *client.Client, args []string, stdout, stderr io.Writer, configuration config.Config) error {
	jsonOutput := false
	if len(args) > 0 && args[0] == "--json" {
		jsonOutput = true
		args = args[1:]
	}
	if len(args) > 0 && args[0] != "run" {
		clean := make([]string, 0, len(args))
		for _, a := range args {
			if a == "--json" {
				jsonOutput = true
			} else {
				clean = append(clean, a)
			}
		}
		args = clean
	}
	request, err := external.ParseManagement(args)
	if err != nil {
		return err
	}
	if request.Directory != "" {
		request.Directory, err = filepath.Abs(request.Directory)
		if err != nil {
			return err
		}
	}
	// CLI commands do not project live Core state, but must drain events during
	// lengthy dialogues or monitoring callbacks to avoid frontend queue overflow.
	go func() {
		for {
			select {
			case <-frontend.Events():
			case <-ctx.Done():
				return
			case <-frontend.Done():
				return
			}
		}
	}()
	if requireAttachTTY(stdout) == nil {
		var dialogueMu sync.Mutex
		frontend.SetPluginInteractionHandler(func(dialogCtx context.Context, request v1.InteractionRequest) (v1.InteractionResult, error) {
			if !dialogueMu.TryLock() {
				return v1.InteractionResult{}, errors.New("plugin: frontend is already in a dialogue")
			}
			defer dialogueMu.Unlock()
			interaction := request.Interaction
			switch interaction.Kind {
			case "prompt", "confirm":
				_, _ = fmt.Fprintf(stderr, "%s: %s", request.Context.PluginID, interaction.Message)
				if interaction.Kind == "confirm" {
					_, _ = fmt.Fprint(stderr, " [y/N] ")
				}
				type inputResult struct {
					text string
					err  error
				}
				done := make(chan inputResult, 1)
				readerDone := make(chan struct{})
				readCtx, cancelRead := context.WithCancel(dialogCtx)
				go func() {
					defer close(readerDone)
					reader := bufio.NewReader(contextTerminalReader{readCtx, os.Stdin})
					line, err := reader.ReadString('\n')
					done <- inputResult{strings.TrimRight(line, "\r\n"), err}
				}()
				defer func() { cancelRead(); <-readerDone }()
				select {
				case result := <-done:
					if result.err != nil {
						return v1.InteractionResult{}, result.err
					}
					if strings.ContainsRune(result.text, 3) {
						return v1.InteractionResult{}, errors.New("plugin: dialogue cancelled")
					}
					if interaction.Kind == "confirm" {
						return v1.InteractionResult{Confirmed: strings.EqualFold(result.text, "y") || strings.EqualFold(result.text, "yes")}, nil
					}
					return v1.InteractionResult{Text: result.text}, nil
				case <-dialogCtx.Done():
					return v1.InteractionResult{}, dialogCtx.Err()
				}
			case "editor":
				cwd, _ := os.Getwd()
				argv := configuration.EditorCommand(defaultEditor())
				opened, err := frontend.Plugin(dialogCtx, v1.ManageRequest{Action: "editor.open", Editor: &v1.EditorRequest{Argv: argv, CWD: cwd, Env: os.Environ(), Text: interaction.Text}})
				if err != nil {
					return v1.InteractionResult{}, err
				}
				id := opened.Editor.PaneID
				defer func() {
					cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					_, _ = frontend.Plugin(cleanupCtx, v1.ManageRequest{Action: "editor.cancel", PaneID: id})
				}()
				if err := attachTerminal(dialogCtx, frontend, core.TerminalID(opened.Editor.TerminalID), stdout); err != nil {
					return v1.InteractionResult{}, err
				}
				result, err := frontend.Plugin(dialogCtx, v1.ManageRequest{Action: "editor.finish", PaneID: id})
				if err != nil {
					return v1.InteractionResult{}, err
				}
				return v1.InteractionResult{Text: result.Editor.Text}, nil
			}
			return v1.InteractionResult{}, errors.New("plugin: unsupported interaction")
		})
	}
	result, err := frontend.Plugin(ctx, request)
	if err != nil {
		return err
	}
	if jsonOutput {
		return json.NewEncoder(stdout).Encode(result)
	}
	if result.RegistryError != "" {
		return fmt.Errorf("plugin: registry preserved; automatic startup stopped: %s", result.RegistryError)
	}
	if result.Command != nil {
		if result.Command.Text != "" {
			_, err = fmt.Fprintln(stdout, result.Command.Text)
			return err
		}
		if len(result.Command.JSON) > 0 {
			_, err = fmt.Fprintln(stdout, string(result.Command.JSON))
			return err
		}
		return nil
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tVERSION\tINSTALLED\tENABLED\tRUNNING\tGRANTS\tERROR")
	for _, p := range result.Plugins {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%t\t%t\t%t\t%d\t%s\n", p.Manifest.ID, p.Manifest.Version, p.Installed, p.Enabled, p.Running, len(p.Grants), p.Error)
		if request.Action == "status" {
			for _, g := range p.Grants {
				_, _ = fmt.Fprintf(w, "  %s\t%s %v\n", g.Capability, g.Scope.Kind, g.Scope.IDs)
			}
			for _, c := range p.Manifest.Commands {
				_, _ = fmt.Fprintf(w, "  command\tplugin run %s %s — %s\n", p.Manifest.ID, c.Name, c.Description)
			}
		}
	}
	return w.Flush()
}
