package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/plugin/external"
)

type pluginResult struct {
	opened     bool
	result     v1.ManageResult
	err        error
	content    *externalToolContent
	generation uint64
	render     bool
	management bool
	editor     bool
}
type pluginDialogue struct {
	ctx     context.Context
	request v1.InteractionRequest
	done    chan dialogueResult
}
type dialogueResult struct {
	result v1.InteractionResult
	err    error
}
type editorPreview struct {
	workspace                                         core.WorkspaceID
	window                                            core.WindowID
	seen                                              bool
	pane                                              core.PaneID
	previousPane, previousFocus, previousPreviewFocus core.PaneID
	zoom                                              bool
	dialogue                                          pluginDialogue
	finished                                          bool
}

func (s *session) pluginCommand(args []string) {
	if len(args) == 0 {
		s.openBuiltinTool("plugin-manager")
		return
	}
	request, err := external.ParseManagement(args)
	if err != nil {
		s.setMessage(err.Error())
		return
	}
	if request.Directory != "" && !filepath.IsAbs(request.Directory) {
		request.Directory = filepath.Join(s.cwd, request.Directory)
	}
	request.PaneID = uint64(s.focus)
	if s.pluginManaging {
		s.setMessage("plugin operation is already running")
		return
	}
	s.pluginManaging = true
	s.commandPending = true
	go func() {
		result, err := s.client.Plugin(s.ctx, request)
		s.sendPluginResult(pluginResult{result: result, err: err, management: true})
	}()
}
func (s *session) sendPluginResult(result pluginResult) {
	select {
	case s.pluginResults <- result:
	case <-s.ctx.Done():
	}
}
func (s *session) applyPluginResult(result pluginResult) {
	if result.management {
		s.pluginManaging = false
		if result.err != nil {
			s.pendingCommands = nil
			s.setMessage(result.err.Error())
			return
		}
		if result.result.RegistryError != "" {
			s.setMessage("plugin registry error: " + result.result.RegistryError)
		}
		if result.result.Command == nil {
			s.pluginStatus = result.result
		}
		if c := result.result.Command; c != nil {
			message := c.Text
			if message == "" && len(c.JSON) > 0 {
				message = string(c.JSON)
			}
			s.setMessage(sanitizeWidgetText(message))
		}
		if len(s.pendingCommands) != 0 && s.inputMode == inputModeNormal && !s.copyMode && !s.quitRequested {
			commands := append([]string(nil), s.pendingCommands...)
			s.pendingCommands = nil
			s.executeCommandSequence(commands)
		}
	}
	if c := result.content; c != nil && !c.closed {
		if result.render {
			c.running = false
			c.opened = c.opened || result.opened
			if c.generation != result.generation {
				return
			}
			if result.err != nil {
				c.err = result.err.Error()
				c.frame = nil
				c.next = time.Now().Add(time.Second)
			} else {
				c.err = ""
				c.frame = result.result.Frame
			}
		}
		if result.err != nil && !result.render {
			s.setMessage(result.err.Error())
		}
	}
	s.dirty = true
}
func (s *session) refreshPlugins() {
	if !s.pluginManaging {
		s.pluginManaging = true
		go func() {
			result, err := s.client.Plugin(s.ctx, v1.ManageRequest{Action: "list"})
			s.sendPluginResult(pluginResult{result: result, err: err, management: true})
		}()
	}
}
func (s *session) pluginHelp() []string {
	var lines []string
	for _, p := range s.pluginStatus.Plugins {
		if !p.Installed {
			continue
		}
		for _, c := range p.Manifest.Commands {
			lines = append(lines, "plugin run "+p.Manifest.ID+" "+c.Name+" — "+c.Description)
		}
	}
	return lines
}
func (s *session) pluginManagerLines() []string {
	lines := []string{"Plugins — Enter details, i install, u update, d uninstall, e toggle, r restart, g/v grant/revoke"}
	if s.pluginStatus.RegistryError != "" {
		return append(lines, "Registry preserved: "+s.pluginStatus.RegistryError)
	}
	for _, p := range s.pluginStatus.Plugins {
		state := "disabled"
		if !p.Installed {
			state = "uninstalled"
		} else if p.Running {
			state = "running"
		} else if p.Enabled {
			state = "stopped"
		}
		lines = append(lines, fmt.Sprintf("%s %s [%s] grants:%d %s", p.Manifest.ID, p.Manifest.Version, state, len(p.Grants), p.Error))
	}
	return lines
}
func (s *session) pluginManagerAction(index int, action string) {
	if action == "install" {
		s.beginPromptWithCallback("Plugin package directory", "", func(directory string) {
			if directory != "" {
				s.pluginCommand([]string{"install", directory})
			}
		})
		return
	}
	if index < 0 || index >= len(s.pluginStatus.Plugins) {
		return
	}
	p := s.pluginStatus.Plugins[index]
	switch action {
	case "update":
		s.beginPromptWithCallback("Update "+p.Manifest.ID+" package directory", "", func(directory string) {
			if directory != "" {
				s.pluginCommand([]string{"update", p.Manifest.ID, directory})
			}
		})
		return
	case "uninstall":
		s.beginConfirmation("Uninstall "+p.Manifest.ID+"? Pane/state/data will be retained", func(yes bool) {
			if yes {
				s.pluginCommand([]string{"uninstall", p.Manifest.ID})
			}
		})
		return
	case "grant", "revoke":
		s.beginPromptWithCallback(action+" "+p.Manifest.ID+" CAPABILITY SCOPE", "", func(text string) {
			fields := strings.Fields(text)
			if len(fields) < 1 || len(fields) > 2 {
				s.setMessage("expected CAPABILITY [all|context|workspace:IDs|pane:IDs]")
				return
			}
			s.pluginCommand(append([]string{action, p.Manifest.ID}, fields...))
		})
		return
	}
	if action == "details" {
		lines := []string{p.Manifest.ID + " " + p.Manifest.Version, "runtime: " + p.Manifest.Runtime}
		for _, cap := range p.Manifest.Capabilities {
			lines = append(lines, "requested: "+string(cap))
		}
		for _, g := range p.Grants {
			lines = append(lines, fmt.Sprintf("grant: %s %s %v", g.Capability, g.Scope.Kind, g.Scope.IDs))
		}
		for _, c := range p.Manifest.Commands {
			lines = append(lines, "plugin run "+p.Manifest.ID+" "+c.Name)
		}
		s.setMessage(strings.Join(lines, " | "))
		return
	}
	if action == "toggle" {
		action = "enable"
		if p.Enabled {
			action = "disable"
		}
	}
	s.pluginCommand([]string{action, p.Manifest.ID})
}
func (s *session) handlePluginDialogue(dialogue pluginDialogue) {
	if s.inputMode != inputModeNormal || s.pluginEditor != nil {
		dialogue.done <- dialogueResult{err: errors.New("plugin: frontend is already in a dialogue")}
		return
	}
	s.activePluginDialogue = &dialogue
	respond := func(result v1.InteractionResult) {
		dialogue.done <- dialogueResult{result: result}
		s.activePluginDialogue = nil
	}
	lead := dialogue.request.Context.PluginID + ": " + dialogue.request.Interaction.Message
	switch dialogue.request.Interaction.Kind {
	case "prompt":
		s.beginPromptWithCallback(lead, dialogue.request.Interaction.Text, func(text string) { respond(v1.InteractionResult{Text: text}) })
	case "confirm":
		s.beginConfirmation(lead, func(yes bool) { respond(v1.InteractionResult{Confirmed: yes}) })
	case "editor":
		// Editor creation is asynchronous; the normal TUI event loop keeps draining
		// PTY/Core events while the process runs in a transient stashed Pane.
		request := v1.ManageRequest{Action: "editor.open", Editor: &v1.EditorRequest{Argv: append([]string(nil), s.editor...), CWD: s.cwd, Env: append([]string(nil), s.env...), Text: dialogue.request.Interaction.Text}}
		s.pluginEditor = &editorPreview{workspace: s.workspace, window: s.window, previousPane: s.previewPane, previousFocus: s.focus, previousPreviewFocus: s.previewPreviousFocus, zoom: s.zoom, dialogue: dialogue}
		go func() {
			result, err := s.client.Plugin(dialogue.ctx, request)
			s.sendPluginResult(pluginResult{result: result, err: err, editor: true})
		}()
	default:
		dialogue.done <- dialogueResult{err: errors.New("plugin: unknown dialogue")}
		s.activePluginDialogue = nil
	}
	s.dirty = true
}
func (s *session) pollPluginDialogue() {
	if d := s.activePluginDialogue; d != nil {
		select {
		case <-d.ctx.Done():
			s.cancelPluginDialogue()
		default:
		}
	}
	if e := s.pluginEditor; e != nil && e.pane != 0 && !e.finished {
		p, ok := s.pane(e.pane)
		if !ok {
			if e.seen {
				s.finishPluginEditor(false)
			}
			return
		}
		e.seen = true
		if s.previewPane != e.pane {
			s.finishPluginEditor(false)
			return
		}
		if p.Terminal != nil && (p.Terminal.State == core.TerminalExited || p.Terminal.State == core.TerminalFailed) {
			normal := p.Terminal.Exit != nil && p.Terminal.Exit.Kind == core.TerminalExitProcess && p.Terminal.Exit.Code == 0
			s.finishPluginEditor(normal)
		}
	}
}
func (s *session) cancelPluginDialogue() {
	if s.pluginEditor != nil {
		s.finishPluginEditor(false)
		return
	}
	if d := s.activePluginDialogue; d != nil {
		select {
		case d.done <- dialogueResult{err: errors.New("plugin: dialogue cancelled")}:
		default:
		}
		s.activePluginDialogue = nil
		s.clearInputMode()
	}
}
func (s *session) applyEditorResult(result pluginResult) bool {
	e := s.pluginEditor
	if e == nil {
		return false
	}
	if result.result.Editor != nil {
		if e.pane == 0 {
			cancelled := e.finished
			e.finished = false
			e.pane = core.PaneID(result.result.Editor.PaneID)
			if cancelled {
				s.finishPluginEditor(false)
				return true
			}
			s.previewPane = e.pane
			s.previewPreviousFocus = e.previousFocus
			s.focus = e.pane
			s.zoom = false
			s.relayout()
			s.syncViews()
			s.dirty = true
			return true
		}
		if e.finished {
			if result.err != nil {
				e.dialogue.done <- dialogueResult{err: result.err}
			} else {
				e.dialogue.done <- dialogueResult{result: v1.InteractionResult{Text: result.result.Editor.Text}}
			}
			s.restoreEditorPreview()
			return true
		}
	}
	if result.err != nil && e.finished && e.pane != 0 {
		e.dialogue.done <- dialogueResult{err: result.err}
		if s.client != nil {
			pane := e.pane
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, _ = s.client.Plugin(ctx, v1.ManageRequest{Action: "editor.cancel", PaneID: uint64(pane)})
			}()
		}
		s.restoreEditorPreview()
		return true
	}
	if result.err != nil && e.pane == 0 {
		e.dialogue.done <- dialogueResult{err: result.err}
		s.restoreEditorPreview()
		return true
	}
	return false
}
func (s *session) finishPluginEditor(normal bool) {
	e := s.pluginEditor
	if e == nil || e.finished {
		return
	}
	e.finished = true
	if e.pane == 0 {
		return
	} // Pending editor.open is cancelled via the dialogue context.
	action := "editor.cancel"
	if normal {
		action = "editor.finish"
	}
	pane := e.pane
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		result, err := s.client.Plugin(ctx, v1.ManageRequest{Action: action, PaneID: uint64(pane)})
		if !normal && err == nil {
			err = errors.New("plugin: editor cancelled")
		}
		s.sendPluginResult(pluginResult{result: result, err: err, editor: true})
	}()
}
func (s *session) restoreEditorPreview() {
	e := s.pluginEditor
	if e == nil {
		return
	}
	if _, ok := s.windowByID(e.window); ok {
		s.workspace = e.workspace
		s.window = e.window
	}
	s.previewPane = e.previousPane
	s.focus = e.previousFocus
	s.previewPreviousFocus = e.previousPreviewFocus
	s.zoom = e.zoom
	s.pluginEditor = nil
	s.activePluginDialogue = nil
	s.relayout()
	s.syncViews()
	s.dirty = true
}
