//go:build darwin || linux || windows

package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/aruzen/ariadne/internal/client"
	ariadneconfig "github.com/aruzen/ariadne/internal/config"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/daemon"
	platformterminal "github.com/aruzen/ariadne/internal/platform/terminal"
	"github.com/aruzen/ariadne/internal/plugin/external"
	"github.com/aruzen/ariadne/internal/protocol"
	"github.com/aruzen/ariadne/internal/vt/libghostty"
	"github.com/aruzen/streammux/pty"
)

const (
	frameInterval  = time.Second / 60
	commandTimeout = 2 * time.Second
)

type paneView struct {
	paneID       core.PaneID
	kind         core.PaneKind
	terminalID   core.TerminalID
	content      paneContent
	attachment   *client.PTYAttachment
	attachCancel context.CancelFunc
	cols         int
	rows         int
	ptyCols      int
	ptyRows      int
	errorMessage string
	inputQueue   []paneInput
	inputBytes   int
}

type paneEvent struct {
	paneID     core.PaneID
	attachment *client.PTYAttachment
	event      client.PTYEvent
	response   []byte
	err        error
}

type inputMessage struct {
	data []byte
	err  error
}

type session struct {
	pluginLimits         external.Config
	ctx                  context.Context
	cancel               context.CancelFunc
	client               *client.Client
	pty                  *client.PTYClient
	output               *latestFrameWriter
	snapshot             core.Snapshot
	workspace            core.WorkspaceID
	window               core.WindowID
	focus                core.PaneID
	pendingFocus         core.PaneID
	pendingWindow        core.WindowID
	width                int
	height               int
	placements           []Placement
	allPlacements        []Placement
	separators           []Separator
	paneFrame            PaneFrameMode
	views                map[core.PaneID]*paneView
	renderers            paneRendererRegistry
	ptyEvents            chan paneEvent
	statusBar            StatusBar
	message              string
	messageTime          time.Time
	zoom                 bool
	previewPane          core.PaneID
	previewPreviousFocus core.PaneID
	attentionCursor      uint64
	inputMode            tuiInputMode
	prompt               string
	promptLead           string
	promptHistory        []string
	promptHistoryIndex   int
	promptDraft          string
	confirm              string
	confirmCallback      func(bool)
	promptCallback       func(string)
	shell                []string
	editor               []string
	cwd                  string
	env                  []string
	clipboardRead        ariadneconfig.ClipboardPolicy
	clipboardWrite       ariadneconfig.ClipboardPolicy
	clipboardMax         int
	clipboardRequests    chan clipboardRequest
	copyMode             bool
	copySelecting        bool
	copyX                int
	copyY                int
	copyStartX           int
	copyStartY           int
	searchQuery          string
	copyNewOutput        bool
	pendingClipboard     []clipboardRequest
	outerClipboard       []byte
	dirty                bool
	daemonStatus         protocol.DaemonStatusResult
	quitRequested        bool
	inputDecoder         inputDecoder
	keybindings          ariadneconfig.Keybindings
	copyDecoder          inputDecoder
	promptDecoder        inputDecoder
	modeKeyTime          time.Time
	presentation         ariadneconfig.TUIOptions
	theme                presentationTheme
	widgets              *widgetRunner
	compact              bool
	gesture              *mouseCapture
	dragLayout           *core.LayoutNode
	inputFrames          inputFramer
	pendingViewSync      bool
	pluginResults        chan pluginResult
	pluginDialogues      chan pluginDialogue
	pluginStatus         v1.ManageResult
	pluginManaging       bool
	pendingCommands      []string
	commandPending       bool
	activePluginDialogue *pluginDialogue
	pluginEditor         *editorPreview
}

// Run enters the full-screen frontend using an already synchronized client.
func Run(parent context.Context, frontend *client.Client, snapshot core.Snapshot, stdout io.Writer, options Options) (resultErr error) {
	if err := options.validate(); err != nil {
		return err
	}
	decoder, err := newInputDecoder(options.Keybindings)
	if err != nil {
		return err
	}
	maps := ariadneconfig.DefaultKeymaps()
	if options.CopyKeys == nil {
		options.CopyKeys = maps.Copy
	}
	if options.PromptKeys == nil {
		options.PromptKeys = maps.Prompt
	}
	copyDecoder, _ := modeDecoder(options.CopyKeys, true)
	promptDecoder, _ := modeDecoder(options.PromptKeys, false)
	if options.Presentation.Theme.Base.Foreground == "" {
		options.Presentation = ariadneconfig.Default().TUI
	}
	theme, _ := resolveTheme(options.Presentation.Theme)
	output, ok := stdout.(*os.File)
	if !ok || !platformterminal.IsTerminal(os.Stdin) || !platformterminal.IsTerminal(output) {
		return platformterminal.ErrNotTerminal
	}
	ptyClient, err := client.RegisterPTY(frontend, daemon.DefaultPTYMessageTypes())
	if err != nil {
		return err
	}
	inputState, err := platformterminal.MakeRaw(os.Stdin)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, platformterminal.Restore(os.Stdin, inputState)) }()
	outputState, err := platformterminal.EnableOutput(output)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, platformterminal.RestoreOutput(output, outputState)) }()
	initial := "\x1b[?1049h\x1b[?25l\x1b[?2004h\x1b[2J\x1b[H"
	if options.Presentation.Mouse {
		initial += "\x1b[?1002h\x1b[?1006h"
	}
	if err := writeAll(stdout, []byte(initial)); err != nil {
		return err
	}
	frameOutput := newLatestFrameWriter(stdout)
	defer func() {
		resultErr = errors.Join(resultErr, frameOutput.Close([]byte("\x1b[?1002l\x1b[?1006l\x1b[?2004l\x1b[0m\x1b[0 q\x1b[?7h\x1b[?25h\x1b[?1049l")))
	}()

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	cols, rows, err := platformterminal.Size(output)
	if err != nil {
		return err
	}
	value := &session{
		pluginLimits: options.PluginLimits,
		ctx:          ctx, cancel: cancel, client: frontend, pty: ptyClient, output: frameOutput,
		snapshot: snapshot, width: cols, height: rows, views: make(map[core.PaneID]*paneView),
		renderers: defaultPaneRendererRegistry(), ptyEvents: make(chan paneEvent, 256), clipboardRequests: make(chan clipboardRequest, 16),
		statusBar: DefaultStatusBar(), paneFrame: options.PaneFrame, dirty: true,
		shell: append([]string(nil), options.Shell...), editor: append([]string(nil), options.Editor...),
		cwd: options.CWD, env: append([]string(nil), options.Env...),
		clipboardRead: options.Clipboard.Read, clipboardWrite: options.Clipboard.Write,
		clipboardMax: options.Clipboard.MaxTextBytes,
		inputDecoder: decoder,
		keybindings:  resolvedKeybindings(options.Keybindings),
		copyDecoder:  copyDecoder, promptDecoder: promptDecoder, presentation: options.Presentation, theme: theme,
	}
	if len(value.shell) == 0 {
		value.shell = fallbackShellCommand()
	}
	if len(value.editor) == 0 {
		value.editor = fallbackEditorCommand()
	}
	if value.cwd == "" {
		value.cwd, _ = os.Getwd()
	}
	widgetOptions := make(map[string]ariadneconfig.WidgetOptions)
	for _, name := range append(append([]string(nil), options.Presentation.Status.Left...), options.Presentation.Status.Right...) {
		widget, ok := options.Presentation.Status.Widgets[name]
		if !ok && strings.Contains(name, "/") {
			widget.Plugin = name
			ok = true
		}
		if ok {
			widgetOptions[name] = widget
		}
	}
	value.pluginResults = make(chan pluginResult, 128)
	value.pluginDialogues = make(chan pluginDialogue, 16)
	frontend.SetPluginInteractionHandler(func(dialogCtx context.Context, request v1.InteractionRequest) (v1.InteractionResult, error) {
		dialogue := pluginDialogue{ctx: dialogCtx, request: request, done: make(chan dialogueResult, 1)}
		select {
		case value.pluginDialogues <- dialogue:
		case <-ctx.Done():
			return v1.InteractionResult{}, ctx.Err()
		case <-dialogCtx.Done():
			return v1.InteractionResult{}, dialogCtx.Err()
		}
		select {
		case result := <-dialogue.done:
			return result.result, result.err
		case <-ctx.Done():
			return v1.InteractionResult{}, ctx.Err()
		case <-dialogCtx.Done():
			return v1.InteractionResult{}, dialogCtx.Err()
		}
	})
	defer frontend.SetPluginInteractionHandler(nil)
	value.widgets = newWidgetRunner(ctx, widgetOptions)
	value.refreshPlugins()
	defer value.widgets.close()
	value.statusBar = value.configuredStatusBar()
	if value.clipboardRead == "" {
		value.clipboardRead = ariadneconfig.ClipboardAsk
	}
	if value.clipboardWrite == "" {
		value.clipboardWrite = ariadneconfig.ClipboardAsk
	}
	if value.clipboardMax == 0 {
		value.clipboardMax = ariadneconfig.DefaultClipboardMaxBytes
	}
	value.selectInitialWindow()
	value.relayout()
	value.syncViews()
	defer value.closeViews()
	return value.loop(output)
}

func fallbackShellCommand() []string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return []string{shell}
	}
	if shell := os.Getenv("COMSPEC"); shell != "" {
		return []string{shell}
	}
	if runtime.GOOS == "windows" {
		return []string{"cmd.exe"}
	}
	return []string{"/bin/sh"}
}

func fallbackEditorCommand() []string {
	if editor := os.Getenv("VISUAL"); editor != "" {
		return []string{editor}
	}
	if editor := os.Getenv("EDITOR"); editor != "" {
		return []string{editor}
	}
	if runtime.GOOS == "windows" {
		return []string{"notepad.exe"}
	}
	return []string{"vi"}
}

func (session *session) loop(output *os.File) error {
	input := make(chan inputMessage, 16)
	go readInput(session.ctx, os.Stdin, input)
	resize, stopResize := watchResize(session.ctx, output)
	defer stopResize()
	termination := make(chan os.Signal, 1)
	signal.Notify(termination, terminationSignals()...)
	defer signal.Stop(termination)
	frames := time.NewTicker(frameInterval)
	defer frames.Stop()
	clock := time.NewTicker(time.Second)
	defer clock.Stop()
	for {
		select {
		case message, ok := <-input:
			if !ok {
				return io.EOF
			}
			if message.err != nil {
				return message.err
			}
			session.processInput(message.data)
		case event, ok := <-session.client.Events():
			if !ok {
				return io.EOF
			}
			next, err := core.ApplyEvent(session.snapshot, event)
			if err != nil {
				return err
			}
			session.snapshot = next
			if session.previewPane != 0 {
				if _, exists := session.pane(session.previewPane); !exists {
					session.exitPreview()
				}
			}
			if _, exists := session.currentWindow(); !exists {
				session.selectInitialWindow()
				session.zoom = false
			}
			if session.pendingFocus != 0 && session.window == session.pendingWindow && session.currentLayoutContains(session.pendingFocus) {
				session.focus = session.pendingFocus
				session.pendingFocus = 0
				session.pendingWindow = 0
			}
			session.relayout()
			session.syncViews()
			session.dirty = true
		case message, ok := <-session.ptyEvents:
			if !ok {
				return io.EOF
			}
			if err := session.handlePTYEvent(message); err != nil {
				session.setMessage(err.Error())
			}
			session.dirty = true
		case result := <-session.pluginResults:
			if !result.editor || !session.applyEditorResult(result) {
				session.applyPluginResult(result)
			}
		case dialogue := <-session.pluginDialogues:
			session.handlePluginDialogue(dialogue)
		case request := <-session.clipboardRequests:
			session.handleClipboardRequest(request)
		case _, ok := <-resize:
			if !ok {
				resize = nil
				continue
			}
			cols, rows, err := platformterminal.Size(output)
			if err != nil {
				session.setMessage(err.Error())
				continue
			}
			session.width, session.height = cols, rows
			session.relayout()
			session.syncViews()
			session.dirty = true
		case <-clock.C:
			session.refreshDiagnostics()
			session.refreshPlugins()
			session.dirty = true
		case <-frames.C:
			session.pollPluginDialogue()
			for _, view := range session.views {
				if content, ok := view.content.(*externalToolContent); ok {
					content.refresh(time.Now())
				}
			}
			for _, view := range session.views {
				session.flushPTYInput(view)
			}
			if session.pendingViewSync {
				session.syncViews()
				session.dirty = true
			}
			session.flushInputFrames()
			session.flushModeEscape()
			session.refreshWidgets(time.Now())
			if session.dirty {
				surface, cursor, err := session.render()
				if err != nil {
					return err
				}
				if err = session.output.SubmitSurface(surface, cursor); err != nil {
					return err
				}
				session.dirty = false
			}
		case <-session.output.Done():
			return session.output.Err()
		case result := <-session.widgets.results:
			session.applyWidgetResult(result)
		case received := <-termination:
			return fmt.Errorf("received %s", received)
		case <-session.client.Done():
			if err := session.client.Err(); err != nil {
				return err
			}
			return io.EOF
		case <-session.ctx.Done():
			if session.quitRequested {
				return nil
			}
			return session.ctx.Err()
		}
	}
}

// processInput changes routing immediately when a command enters or leaves a
// mode, even when both sides of the transition arrived in one terminal read.
func (session *session) processKeys(data []byte) {
	forward := make([]byte, 0, len(data))
	flush := func() {
		if len(forward) != 0 {
			session.sendInput(forward)
			forward = forward[:0]
		}
	}
	for index := 0; index < len(data); {
		if session.quitRequested {
			break
		}
		if session.inputMode != inputModeNormal {
			flush()
			size := modalInputUnitSize(data[index:])
			if session.inputMode == inputModeConfirm {
				session.handleModalInput(data[index : index+size])
			} else {
				session.modeInput(data[index:index+size], false)
			}
			index += size
			continue
		}
		if session.copyMode {
			flush()
			session.modeInput(data[index:index+1], true)
			index++
			continue
		}
		for _, token := range session.inputDecoder.Feed(data[index : index+1]) {
			if len(token.data) != 0 {
				forward = append(forward, token.data...)
			}
			if len(token.commands) != 0 {
				flush()
				session.executeCommandSequence(token.commands)
			}
		}
		index++
	}
	flush()
}

func modalInputUnitSize(data []byte) int {
	if len(data) >= 3 && data[0] == 0x1b && data[1] == '[' && (data[2] == 'A' || data[2] == 'B') {
		return 3
	}
	return 1
}

func (session *session) selectInitialWindow() {
	session.workspace = 0
	session.window = 0
	session.focus = 0
	if len(session.snapshot.Workspaces) == 0 {
		return
	}
	session.workspace = session.snapshot.Workspaces[0].ID
	for _, workspace := range session.snapshot.Workspaces {
		if len(workspace.WindowIDs) == 0 {
			continue
		}
		session.workspace = workspace.ID
		session.window = workspace.WindowIDs[0]
		return
	}
}

func (session *session) relayout() {
	session.validateMouseCapture()
	window, exists := session.currentWindow()
	if !exists {
		session.placements = nil
		session.allPlacements = nil
		session.separators = nil
		return
	}
	contentHeight := session.contentHeight()
	if contentHeight < 0 {
		contentHeight = 0
	}
	available := Rect{W: session.width, H: contentHeight}
	root := window.Layout
	if session.dragLayout != nil {
		root = session.dragLayout
	}
	if session.paneFrame == PaneFrameSplit {
		layout := CalculateSplitLayout(root, available)
		session.allPlacements = layout.Placements
		session.separators = layout.Separators
	} else {
		session.allPlacements = CalculateLayout(root, available)
		session.separators = nil
	}
	if session.previewPane != 0 {
		session.placements = []Placement{{PaneID: session.previewPane, Rect: available}}
		session.separators = nil
		return
	}
	if _, exists := placementFor(session.allPlacements, session.focus); !exists {
		if !session.currentLayoutContains(session.focus) {
			session.focus = session.preferredFocus()
			if session.focus == 0 && window.Layout != nil {
				session.focus = firstLayoutPane(*window.Layout)
			}
			session.zoom = false
			session.copyMode = false
			session.copySelecting = false
		}
	}
	session.placements = session.allPlacements
	session.compact = false
	if window.Layout != nil {
		w, h := session.nodeMinimum(*window.Layout)
		session.compact = available.W < w || available.H < h
		for _, placement := range session.allPlacements {
			if !session.placementFits(placement) {
				session.compact = true
				break
			}
		}
	}
	if (session.zoom || session.compact) && session.focus != 0 {
		session.placements = []Placement{{PaneID: session.focus, Rect: available}}
		session.separators = nil
	}
}

func (session *session) preferredFocus() core.PaneID {
	for _, placement := range session.viewPlacements() {
		pane, exists := session.pane(placement.PaneID)
		if !exists {
			continue
		}
		renderer, err := session.paneRenderer(pane)
		if err == nil && renderer.Focusable(pane) && pane.Terminal != nil && pane.Terminal.State == core.TerminalRunning {
			return placement.PaneID
		}
	}
	for _, placement := range session.viewPlacements() {
		pane, exists := session.pane(placement.PaneID)
		if !exists {
			continue
		}
		renderer, err := session.paneRenderer(pane)
		if err == nil && renderer.Focusable(pane) {
			return placement.PaneID
		}
	}
	return 0
}

func (session *session) syncViews() {
	session.pendingViewSync = false
	placements := session.viewPlacements()
	wanted := make(map[core.PaneID]Placement, len(placements))
	for _, placement := range placements {
		pane, exists := session.pane(placement.PaneID)
		if !exists {
			continue
		}
		_, err := session.paneRenderer(pane)
		if err == nil {
			wanted[placement.PaneID] = placement
		}
	}
	if session.zoom || session.compact {
		if placement, exists := placementFor(session.placements, session.focus); exists {
			wanted[session.focus] = placement
		}
	}
	if session.previewPane != 0 {
		if placement, exists := placementFor(session.placements, session.previewPane); exists {
			wanted[session.previewPane] = placement
		}
	}
	for paneID, view := range session.views {
		if _, exists := wanted[paneID]; !exists {
			session.closeView(view)
			delete(session.views, paneID)
		}
	}
	for paneID, placement := range wanted {
		pane, exists := session.pane(paneID)
		if !exists {
			continue
		}
		renderer, err := session.paneRenderer(pane)
		if err != nil {
			continue
		}
		var terminalID core.TerminalID
		if pane.Terminal != nil && pane.Terminal.ID != nil {
			terminalID = *pane.Terminal.ID
		}
		view := session.views[paneID]
		if view == nil || view.kind != pane.Kind || view.terminalID != terminalID {
			if view != nil {
				session.closeView(view)
			}
			view = &paneView{
				paneID: paneID, kind: pane.Kind, terminalID: terminalID, content: safeNewPaneContent(renderer, session, pane),
			}
			session.views[paneID] = view
		}
		content := session.chrome(pane, renderer, placement.Rect).ContentRect(placement.Rect)
		cols, rows := content.W, content.H
		if cols < 1 || rows < 1 {
			continue
		}
		if view.content == nil {
			view.errorMessage = "TUI Pane content is unavailable"
			continue
		}
		if view.cols != cols || view.rows != rows {
			if err := safeResizePane(view.content, cols, rows); err != nil {
				if errors.Is(err, libghostty.ErrBusy) {
					session.pendingViewSync = true
					continue
				}
				view.errorMessage = err.Error()
				continue
			}
		}
		view.cols, view.rows = cols, rows
		if renderer.UsesTerminal() && terminalID != 0 && view.attachment == nil {
			attachment, _, err := session.pty.Attach(session.ctx, terminalID, pty.ReplayHistory)
			if err != nil {
				view.errorMessage = err.Error()
				continue
			}
			view.attachment = attachment
			attachmentCtx, attachmentCancel := context.WithCancel(session.ctx)
			view.attachCancel = attachmentCancel
			view.ptyCols, view.ptyRows = 0, 0
			if content, ok := view.content.(*terminalPaneContent); ok {
				content.setClipboardHandler(session.clipboardHandler(attachmentCtx, paneID), session.clipboardMax)
			}
			go session.forwardPTY(attachmentCtx, view.paneID, attachment, view.content)
		}
		if renderer.UsesTerminal() && view.attachment != nil && pane.Terminal != nil && pane.Terminal.State == core.TerminalRunning &&
			(view.ptyCols != cols || view.ptyRows != rows) {
			ctx, cancel := context.WithTimeout(session.ctx, commandTimeout)
			err := session.pty.Resize(ctx, terminalID, pty.Size{Cols: cols, Rows: rows})
			cancel()
			if err != nil {
				view.errorMessage = err.Error()
			} else {
				view.ptyCols, view.ptyRows = cols, rows
			}
		}
	}
}

func (session *session) viewPlacements() []Placement {
	if session.allPlacements != nil {
		return session.allPlacements
	}
	return session.placements
}

func (session *session) forwardPTY(ctx context.Context, paneID core.PaneID, attachment *client.PTYAttachment, content paneContent) {
	for {
		select {
		case event, ok := <-attachment.Events():
			if !ok {
				return
			}
			message := paneEvent{paneID: paneID, attachment: attachment, event: event}
			if event.Kind == client.PTYOutput {
				terminal, ok := content.(ptyPaneContent)
				if !ok {
					message.err = errors.New("TUI terminal view is unavailable")
				} else {
					message.response, message.err = safeWritePTY(terminal, event.Data)
				}
			}
			select {
			case session.ptyEvents <- message:
			case <-ctx.Done():
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

func (session *session) handlePTYEvent(message paneEvent) error {
	view := session.views[message.paneID]
	if view == nil || view.attachment != message.attachment {
		return nil
	}
	switch message.event.Kind {
	case client.PTYOutput:
		if message.err != nil {
			return message.err
		}
		if len(message.response) != 0 {
			// VT replies are protocol traffic, not user input. They must not wait
			// behind a paste whose encoding is waiting for the VT writer.
			session.writePTYInput(view, message.response)
		}
		if session.copyMode && message.paneID == session.focus {
			session.copyNewOutput = true
		}
	case client.PTYExit:
		view.errorMessage = "exited"
	case client.PTYError:
		if message.event.Error != nil {
			view.errorMessage = message.event.Error.Message
		}
	}
	return nil
}

func (session *session) sendInput(data []byte) {
	view := session.views[session.focus]
	pane, exists := session.pane(session.focus)
	if view == nil || view.content == nil || !exists {
		session.setMessage("focused pane does not accept input")
		return
	}
	handled, err := safeInputPane(view.content, data)
	if err != nil {
		session.setMessage(err.Error())
		return
	}
	if handled {
		session.dirty = true
		return
	}
	if view.attachment == nil || pane.Terminal == nil || pane.Terminal.State != core.TerminalRunning {
		session.setMessage("focused pane is not running")
		return
	}
	session.sendPTYInput(view, data)
}

func (session *session) sendPTYInput(view *paneView, data []byte) {
	if len(view.inputQueue) != 0 {
		session.queuePTYInput(view, paneInput{data: data})
		return
	}
	session.writePTYInput(view, data)
}

func (session *session) writePTYInput(view *paneView, data []byte) error {
	ctx, cancel := context.WithTimeout(session.ctx, commandTimeout)
	err := session.pty.Input(ctx, view.terminalID, data)
	cancel()
	if err != nil {
		session.setMessage(err.Error())
	}
	return err
}

func (session *session) moveFocus(action inputAction) {
	current, exists := placementFor(session.placements, session.focus)
	if !exists {
		return
	}
	cx, cy := center(current.Rect)
	bestID := core.PaneID(0)
	bestScore := int(^uint(0) >> 1)
	for _, candidate := range session.placements {
		if candidate.PaneID == current.PaneID {
			continue
		}
		pane, exists := session.pane(candidate.PaneID)
		renderer, err := session.paneRenderer(pane)
		if !exists || err != nil || !renderer.Focusable(pane) {
			continue
		}
		x, y := center(candidate.Rect)
		primary, secondary, valid := directionalDistance(action, cx, cy, x, y)
		if !valid {
			continue
		}
		score := primary*10000 + secondary
		if score < bestScore {
			bestID, bestScore = candidate.PaneID, score
		}
	}
	if bestID == 0 {
		return
	}
	session.focus = bestID
	session.dirty = true
	ctx, cancel := context.WithTimeout(session.ctx, commandTimeout)
	_, err := client.Call[core.SetFocusResult](ctx, session.client, protocol.OperationSetFocus, protocol.SetFocusParams{PaneID: bestID})
	cancel()
	if err != nil {
		session.setMessage(err.Error())
	}
}

func directionalDistance(action inputAction, cx, cy, x, y int) (int, int, bool) {
	switch action {
	case actionFocusLeft:
		return cx - x, abs(cy - y), x < cx
	case actionFocusRight:
		return x - cx, abs(cy - y), x > cx
	case actionFocusUp:
		return cy - y, abs(cx - x), y < cy
	case actionFocusDown:
		return y - cy, abs(cx - x), y > cy
	default:
		return 0, 0, false
	}
}

func (session *session) render() (*Surface, Cursor, error) {
	if session.theme.styles == nil {
		session.theme, _ = resolveTheme(ariadneconfig.DefaultTheme())
	}
	base := session.theme.styles["base"]
	surface := NewSurface(session.width, session.height, base)
	surface.Theme = &session.theme
	cursor := Cursor{}
	if len(session.placements) == 0 && session.height > 1 {
		surface.Text(2, 1, max(0, session.width-4), "No panes. Run `ariadne new -- <command>` from another shell.", base)
	}
	for _, placement := range session.placements {
		pane, exists := session.pane(placement.PaneID)
		if !exists {
			continue
		}
		focused := pane.ID == session.focus
		renderer, err := session.paneRenderer(pane)
		if err != nil {
			session.setMessage(err.Error())
			continue
		}
		chrome := session.chrome(pane, renderer, placement.Rect)
		title := session.displayPaneTitle(pane)
		if count, _ := session.paneAttention(pane.ID); count != 0 {
			title = fmt.Sprintf("!%d %s", count, title)
		}
		chrome.Draw(surface, placement.Rect, title, focused)
		content := chrome.ContentRect(placement.Rect)
		view := session.views[pane.ID]
		if content.W <= 0 || content.H <= 0 {
			continue
		}
		if view == nil || view.content == nil {
			continue
		}
		paneCursor, err := safeDrawPane(view.content, surface, content, pane, focused, base)
		if err != nil {
			if view != nil {
				view.errorMessage = err.Error()
			} else {
				session.setMessage(err.Error())
			}
			continue
		}
		if paneCursor.Visible {
			cursor = paneCursor
		}
		if session.copyMode && focused {
			if session.copySelecting {
				drawCopySelection(surface, content, session.copyStartX, session.copyStartY, session.copyX, session.copyY)
			}
			cursor = Cursor{X: content.X + session.copyX, Y: content.Y + session.copyY, Visible: true}
		}
		if view.errorMessage != "" {
			warning := session.theme.styles["warning"]
			warning.Background = base.Background
			surface.Text(content.X, content.Y, content.W, fitText(view.errorMessage, content.W), warning)
		}
	}
	if session.paneFrame == PaneFrameSplit {
		drawSplitSeparators(surface, session.separators, session.placements, session.focus)
	}
	workspaceName, windowName := session.names()
	pane, _ := session.pane(session.focus)
	state := core.TerminalState("")
	if pane.Terminal != nil {
		state = pane.Terminal.State
	}
	message := session.message
	if message == "" || (!session.messageTime.IsZero() && time.Since(session.messageTime) > 4*time.Second) {
		message = keybindingStatus(session.keybindings)
	}
	if session.inputMode == inputModePrompt {
		message = session.promptLead + session.prompt
	} else if session.inputMode == inputModeConfirm {
		message = session.confirm + " [y/N]"
	} else if session.copyMode {
		message = "COPY h/j/k/l move · ^U/^D page · g/G ends · Space select · Enter copy · / search · q cancel"
		if session.copyNewOutput {
			message += " · new output"
		}
	}
	if session.height > 1 {
		session.statusBar.Draw(surface, session.height-1, StatusContext{
			Workspace: workspaceName, Window: windowName, PaneID: pane.ID, PaneTitle: session.displayPaneTitle(pane),
			ManualPaneTitle: sanitizeWidgetText(pane.Title), TerminalTitle: session.terminalTitle(pane),
			State: state, Message: message, Now: time.Now(), UnreadAttention: unreadAttentionCount(session.snapshot.Attentions), AttentionSeverity: highestAttentionSeverity(session.snapshot.Attentions),
			CWD: session.paneCWD(pane),
		})
	}
	if session.inputMode != inputModeNormal || session.copyMode {
		surface.Text(0, session.height-1, session.width, fitText(message, session.width), session.theme.styles["prompt"])
		if session.inputMode == inputModePrompt {
			cursor = Cursor{Visible: true, X: min(max(0, session.width-1), textWidth(session.promptLead+session.prompt)), Y: session.height - 1, Shape: 6}
		} else if session.inputMode == inputModeConfirm {
			cursor = Cursor{}
		}
	}
	if len(session.outerClipboard) != 0 {
		if err := session.output.SubmitControl(session.outerClipboard); err != nil {
			return nil, Cursor{}, err
		}
		session.outerClipboard = nil
	}
	return surface, cursor, nil
}

func (session *session) paneAttention(id core.PaneID) (int, core.AttentionSeverity) {
	count := 0
	highest := core.AttentionSeverity("")
	for _, attention := range session.snapshot.Attentions {
		if attention.PaneID == id && attention.AcknowledgedAt == nil {
			count++
			if severityRank(attention.Severity) > severityRank(highest) {
				highest = attention.Severity
			}
		}
	}
	return count, highest
}

func unreadAttentionCount(values []core.Attention) int {
	count := 0
	for _, value := range values {
		if value.AcknowledgedAt == nil {
			count++
		}
	}
	return count
}
func highestAttentionSeverity(values []core.Attention) core.AttentionSeverity {
	highest := core.AttentionSeverity("")
	for _, value := range values {
		if value.AcknowledgedAt == nil && severityRank(value.Severity) > severityRank(highest) {
			highest = value.Severity
		}
	}
	return highest
}

func drawCopySelection(surface *Surface, content Rect, startX, startY, endX, endY int) {
	if content.W <= 0 || content.H <= 0 {
		return
	}
	start, end := startY*content.W+startX, endY*content.W+endX
	if start > end {
		start, end = end, start
	}
	start = max(0, start)
	end = min(content.W*content.H-1, end)
	if surface.At(content.X+start%content.W, content.Y+start/content.W).Width == 0 && start%content.W > 0 {
		start--
	}
	if surface.At(content.X+end%content.W, content.Y+end/content.W).Width == 2 && end%content.W+1 < content.W {
		end++
	}
	for index := start; index <= end; index++ {
		x, y := content.X+index%content.W, content.Y+index/content.W
		if y >= content.Y+content.H {
			break
		}
		cell := surface.At(x, y)
		cell.Style.Foreground, cell.Style.Background = cell.Style.Background, cell.Style.Foreground
		if cell.Style.Foreground == cell.Style.Background {
			cell.Style.Background = surface.themeStyle("selection").Background
		}
		if x >= 0 && x < surface.Width && y >= 0 && y < surface.Height {
			surface.Cells[y*surface.Width+x] = cell
		}
	}
}

func drawPaneBorder(surface *Surface, rect Rect, title string, focused bool) {
	if rect.W <= 0 || rect.H <= 0 {
		return
	}
	style := surface.frameStyle(focused)
	for x := rect.X; x < rect.X+rect.W; x++ {
		surface.Set(x, rect.Y, Cell{Text: surface.lineGlyph("─"), Width: 1, Style: style})
		surface.Set(x, rect.Y+rect.H-1, Cell{Text: surface.lineGlyph("─"), Width: 1, Style: style})
	}
	for y := rect.Y; y < rect.Y+rect.H; y++ {
		surface.Set(rect.X, y, Cell{Text: surface.lineGlyph("│"), Width: 1, Style: style})
		surface.Set(rect.X+rect.W-1, y, Cell{Text: surface.lineGlyph("│"), Width: 1, Style: style})
	}
	surface.Set(rect.X, rect.Y, Cell{Text: surface.lineGlyph("┌"), Width: 1, Style: style})
	surface.Set(rect.X+rect.W-1, rect.Y, Cell{Text: surface.lineGlyph("┐"), Width: 1, Style: style})
	surface.Set(rect.X, rect.Y+rect.H-1, Cell{Text: surface.lineGlyph("└"), Width: 1, Style: style})
	surface.Set(rect.X+rect.W-1, rect.Y+rect.H-1, Cell{Text: surface.lineGlyph("┘"), Width: 1, Style: style})
	if rect.W > 4 {
		surface.Text(rect.X+2, rect.Y, rect.W-4, fitText(" "+title+" ", rect.W-4), style)
	}
}

type separatorPoint struct {
	x int
	y int
}

type separatorCell struct {
	horizontal bool
	vertical   bool
	focused    bool
}

func drawSplitSeparators(surface *Surface, separators []Separator, placements []Placement, focus core.PaneID) {
	cells := make(map[separatorPoint]separatorCell)
	for _, separator := range separators {
		focused := separatorTouchesPane(separator, placements, focus)
		for y := separator.Rect.Y; y < separator.Rect.Y+separator.Rect.H; y++ {
			for x := separator.Rect.X; x < separator.Rect.X+separator.Rect.W; x++ {
				point := separatorPoint{x: x, y: y}
				cell := cells[point]
				if separator.Direction == core.SplitVertical {
					cell.horizontal = true
				} else {
					cell.vertical = true
				}
				cell.focused = cell.focused || focused
				cells[point] = cell
			}
		}
	}
	for point, cell := range cells {
		left := cells[separatorPoint{x: point.x - 1, y: point.y}].horizontal
		right := cells[separatorPoint{x: point.x + 1, y: point.y}].horizontal
		up := cells[separatorPoint{x: point.x, y: point.y - 1}].vertical
		down := cells[separatorPoint{x: point.x, y: point.y + 1}].vertical
		surface.Set(point.x, point.y, Cell{Text: surface.lineGlyph(separatorGlyph(cell, left, right, up, down)), Width: 1, Style: surface.frameStyle(cell.focused)})
	}
}

func separatorTouchesPane(separator Separator, placements []Placement, focus core.PaneID) bool {
	placement, exists := placementFor(placements, focus)
	if !exists {
		return false
	}
	pane := placement.Rect
	line := separator.Rect
	if separator.Direction == core.SplitVertical {
		return intervalsOverlap(pane.X, pane.X+pane.W, line.X, line.X+line.W) &&
			(pane.Y+pane.H == line.Y || pane.Y == line.Y+line.H)
	}
	return intervalsOverlap(pane.Y, pane.Y+pane.H, line.Y, line.Y+line.H) &&
		(pane.X+pane.W == line.X || pane.X == line.X+line.W)
}

func intervalsOverlap(aStart, aEnd, bStart, bEnd int) bool {
	return aStart < bEnd && bStart < aEnd
}

func separatorGlyph(cell separatorCell, left, right, up, down bool) string {
	if cell.horizontal {
		left = left || !right
		right = right || !left
	}
	if cell.vertical {
		up = up || !down
		down = down || !up
	}
	switch {
	case left && right && up && down:
		return "┼"
	case left && right && down:
		return "┬"
	case left && right && up:
		return "┴"
	case up && down && right:
		return "├"
	case up && down && left:
		return "┤"
	case right && down:
		return "┌"
	case left && down:
		return "┐"
	case right && up:
		return "└"
	case left && up:
		return "┘"
	case left || right:
		return "─"
	default:
		return "│"
	}
}

func (session *session) currentWindow() (core.Window, bool) {
	for _, window := range session.snapshot.Windows {
		if window.ID != session.window {
			continue
		}
		for _, workspace := range session.snapshot.Workspaces {
			for _, windowID := range workspace.WindowIDs {
				if windowID == window.ID {
					return window, true
				}
			}
		}
		return core.Window{}, false
	}
	return core.Window{}, false
}

func (session *session) currentLayoutContains(paneID core.PaneID) bool {
	window, exists := session.currentWindow()
	if !exists || window.Layout == nil {
		return false
	}
	var contains func(core.LayoutNode) bool
	contains = func(node core.LayoutNode) bool {
		if node.Kind == core.LayoutPane {
			return node.PaneID == paneID
		}
		for _, child := range node.Children {
			if contains(child) {
				return true
			}
		}
		return false
	}
	return contains(*window.Layout)
}

func (session *session) pane(id core.PaneID) (core.Pane, bool) {
	for _, pane := range session.snapshot.Panes {
		if pane.ID == id {
			return pane, true
		}
	}
	return core.Pane{}, false
}

func (session *session) names() (string, string) {
	workspaceName, windowName := "-", "-"
	for _, workspace := range session.snapshot.Workspaces {
		if workspace.ID == session.workspace {
			workspaceName = workspace.Name
			break
		}
	}
	if window, exists := session.currentWindow(); exists {
		windowName = window.Name
	}
	return workspaceName, windowName
}

func paneTitle(pane core.Pane) string {
	if pane.Title != "" {
		return pane.Title
	}
	if pane.Terminal != nil && len(pane.Terminal.Launch.Argv) != 0 {
		return filepath.Base(pane.Terminal.Launch.Argv[0])
	}
	return fmt.Sprintf("pane %d", pane.ID)
}

func (session *session) setMessage(message string) {
	session.message = message
	session.messageTime = time.Now()
	session.dirty = true
}

func (session *session) detachView(view *paneView) {
	view.inputQueue, view.inputBytes = nil, 0
	if view.attachment == nil {
		return
	}
	if view.attachCancel != nil {
		view.attachCancel()
		view.attachCancel = nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	_ = view.attachment.Detach(ctx)
	cancel()
	view.attachment = nil
	view.ptyCols, view.ptyRows = 0, 0
}

func (session *session) closeView(view *paneView) {
	session.detachView(view)
	if view.content != nil {
		safeClosePaneContent(view.content)
		view.content = nil
	}
}

func (session *session) closeViews() {
	for _, view := range session.views {
		session.closeView(view)
	}
}

func readInput(ctx context.Context, reader io.Reader, output chan<- inputMessage) {
	buffer := make([]byte, 4096)
	for {
		count, err := reader.Read(buffer)
		if count != 0 {
			data := append([]byte(nil), buffer[:count]...)
			select {
			case output <- inputMessage{data: data}:
			case <-ctx.Done():
				return
			}
		}
		if err != nil {
			select {
			case output <- inputMessage{err: err}:
			case <-ctx.Done():
			}
			return
		}
	}
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) != 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func center(rect Rect) (int, int) { return rect.X + rect.W/2, rect.Y + rect.H/2 }
func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}
