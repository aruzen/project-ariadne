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
	"time"

	"github.com/aruzen/ariadne/internal/client"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/daemon"
	platformterminal "github.com/aruzen/ariadne/internal/platform/terminal"
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
	terminalID   core.TerminalID
	terminal     *libghostty.Terminal
	attachment   *client.PTYAttachment
	attachCancel context.CancelFunc
	cols         int
	rows         int
	ptyCols      int
	ptyRows      int
	errorMessage string
}

type paneEvent struct {
	paneID     core.PaneID
	attachment *client.PTYAttachment
	event      client.PTYEvent
}

type inputMessage struct {
	data []byte
	err  error
}

type session struct {
	ctx         context.Context
	cancel      context.CancelFunc
	client      *client.Client
	pty         *client.PTYClient
	output      io.Writer
	snapshot    core.Snapshot
	workspace   core.WorkspaceID
	window      core.WindowID
	focus       core.PaneID
	width       int
	height      int
	placements  []Placement
	views       map[core.PaneID]*paneView
	ptyEvents   chan paneEvent
	statusBar   StatusBar
	message     string
	messageTime time.Time
	dirty       bool
}

// Run enters the full-screen frontend using an already synchronized client.
func Run(parent context.Context, frontend *client.Client, snapshot core.Snapshot, stdout io.Writer) (resultErr error) {
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
	if err := writeAll(stdout, []byte("\x1b[?1049h\x1b[?25l\x1b[2J\x1b[H")); err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, writeAll(stdout, []byte("\x1b[0m\x1b[?25h\x1b[?1049l")))
	}()

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	cols, rows, err := platformterminal.Size(output)
	if err != nil {
		return err
	}
	value := &session{
		ctx: ctx, cancel: cancel, client: frontend, pty: ptyClient, output: stdout,
		snapshot: snapshot, width: cols, height: rows, views: make(map[core.PaneID]*paneView),
		ptyEvents: make(chan paneEvent, 256), statusBar: DefaultStatusBar(), dirty: true,
	}
	value.selectInitialWindow()
	value.relayout()
	value.syncViews()
	defer value.closeViews()
	return value.loop(output)
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
	decoder := inputDecoder{}

	for {
		select {
		case message, ok := <-input:
			if !ok {
				return io.EOF
			}
			if message.err != nil {
				return message.err
			}
			data, actions := decoder.Feed(message.data)
			for _, action := range actions {
				if action == actionQuit {
					return nil
				}
				session.moveFocus(action)
			}
			if len(data) != 0 {
				session.sendInput(data)
			}
		case event, ok := <-session.client.Events():
			if !ok {
				return io.EOF
			}
			next, err := core.ApplyEvent(session.snapshot, event)
			if err != nil {
				return err
			}
			session.snapshot = next
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
			session.dirty = true
		case <-frames.C:
			if session.dirty {
				if err := session.render(); err != nil {
					return err
				}
				session.dirty = false
			}
		case received := <-termination:
			return fmt.Errorf("received %s", received)
		case <-session.client.Done():
			if err := session.client.Err(); err != nil {
				return err
			}
			return io.EOF
		case <-session.ctx.Done():
			return session.ctx.Err()
		}
	}
}

func (session *session) selectInitialWindow() {
	if len(session.snapshot.Workspaces) == 0 {
		return
	}
	workspace := session.snapshot.Workspaces[0]
	session.workspace = workspace.ID
	if len(workspace.WindowIDs) != 0 {
		session.window = workspace.WindowIDs[0]
	}
}

func (session *session) relayout() {
	window, exists := session.currentWindow()
	if !exists {
		session.placements = nil
		return
	}
	contentHeight := session.height - 1
	if contentHeight < 0 {
		contentHeight = 0
	}
	session.placements = CalculateLayout(window.Layout, Rect{W: session.width, H: contentHeight})
	if _, exists := placementFor(session.placements, session.focus); !exists {
		session.focus = session.preferredFocus()
	}
}

func (session *session) preferredFocus() core.PaneID {
	for _, placement := range session.placements {
		pane, exists := session.pane(placement.PaneID)
		if exists && pane.Terminal != nil && pane.Terminal.State == core.TerminalRunning {
			return placement.PaneID
		}
	}
	if len(session.placements) != 0 {
		return session.placements[0].PaneID
	}
	return 0
}

func (session *session) syncViews() {
	wanted := make(map[core.PaneID]Placement, len(session.placements))
	for _, placement := range session.placements {
		wanted[placement.PaneID] = placement
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
		var terminalID core.TerminalID
		if pane.Terminal != nil && pane.Terminal.ID != nil {
			terminalID = *pane.Terminal.ID
		}
		view := session.views[paneID]
		if view == nil {
			view = &paneView{paneID: paneID, terminalID: terminalID}
			session.views[paneID] = view
		} else if view.terminalID != terminalID {
			session.detachView(view)
			if view.terminal != nil {
				view.terminal.Close()
				view.terminal = nil
			}
			view.terminalID = terminalID
			view.errorMessage = ""
		}
		interior := placement.Rect.Interior()
		cols, rows := interior.W, interior.H
		if cols < 1 || rows < 1 {
			continue
		}
		if view.terminal == nil {
			terminal, err := libghostty.NewTerminal(cols, rows)
			if err != nil {
				view.errorMessage = err.Error()
				continue
			}
			view.terminal = terminal
		} else if view.cols != cols || view.rows != rows {
			if err := view.terminal.Resize(cols, rows); err != nil {
				view.errorMessage = err.Error()
				continue
			}
		}
		view.cols, view.rows = cols, rows
		if terminalID != 0 && view.attachment == nil {
			attachment, _, err := session.pty.Attach(session.ctx, terminalID, pty.ReplayHistory)
			if err != nil {
				view.errorMessage = err.Error()
				continue
			}
			view.attachment = attachment
			attachmentCtx, attachmentCancel := context.WithCancel(session.ctx)
			view.attachCancel = attachmentCancel
			view.ptyCols, view.ptyRows = 0, 0
			go session.forwardPTY(attachmentCtx, view.paneID, attachment)
		}
		if view.attachment != nil && pane.Terminal != nil && pane.Terminal.State == core.TerminalRunning &&
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

func (session *session) forwardPTY(ctx context.Context, paneID core.PaneID, attachment *client.PTYAttachment) {
	for {
		select {
		case event, ok := <-attachment.Events():
			if !ok {
				return
			}
			select {
			case session.ptyEvents <- paneEvent{paneID: paneID, attachment: attachment, event: event}:
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
		if view.terminal == nil {
			return errors.New("TUI terminal view is unavailable")
		}
		response, err := view.terminal.WriteWithResponse(message.event.Data)
		if err != nil {
			return err
		}
		if len(response) != 0 {
			session.sendPTYInput(view, response)
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
	if view == nil || view.attachment == nil || !exists || pane.Terminal == nil || pane.Terminal.State != core.TerminalRunning {
		session.setMessage("focused pane is not running")
		return
	}
	session.sendPTYInput(view, data)
}

func (session *session) sendPTYInput(view *paneView, data []byte) {
	ctx, cancel := context.WithTimeout(session.ctx, commandTimeout)
	err := session.pty.Input(ctx, view.terminalID, data)
	cancel()
	if err != nil {
		session.setMessage(err.Error())
	}
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

func (session *session) render() error {
	base := Style{Foreground: Color{R: 220, G: 220, B: 220}, Background: Color{R: 18, G: 20, B: 24}}
	surface := NewSurface(session.width, session.height, base)
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
		drawPaneBorder(surface, placement.Rect, paneTitle(pane), focused)
		interior := placement.Rect.Interior()
		view := session.views[pane.ID]
		if view == nil || view.terminal == nil || interior.W == 0 || interior.H == 0 {
			continue
		}
		screen, err := view.terminal.Screen()
		if err != nil {
			view.errorMessage = err.Error()
			continue
		}
		for y := 0; y < interior.H && y < screen.Rows; y++ {
			for x := 0; x < interior.W && x < screen.Cols; x++ {
				source := screen.At(x, y)
				style := Style{
					Foreground: colorFromGhostty(source.Style.Foreground), Background: colorFromGhostty(source.Style.Background),
					Bold: source.Style.Bold, Italic: source.Style.Italic, Underline: source.Style.Underline,
					Strikethrough: source.Style.Strikethrough, Faint: source.Style.Faint, Blink: source.Style.Blink,
				}
				target := (interior.Y+y)*surface.Width + interior.X + x
				if target >= 0 && target < len(surface.Cells) {
					surface.Cells[target] = Cell{Text: source.Text, Width: source.Width, Style: style}
				}
			}
		}
		if focused && screen.Cursor.Visible && screen.Cursor.X < interior.W && screen.Cursor.Y < interior.H {
			cursor = Cursor{X: interior.X + screen.Cursor.X, Y: interior.Y + screen.Cursor.Y, Visible: true}
		}
		if view.errorMessage != "" && interior.H != 0 {
			warning := Style{Foreground: Color{R: 255, G: 210, B: 120}, Background: base.Background}
			surface.Text(interior.X, interior.Y, interior.W, fitText(view.errorMessage, interior.W), warning)
		}
	}
	workspaceName, windowName := session.names()
	pane, _ := session.pane(session.focus)
	state := core.TerminalState("")
	if pane.Terminal != nil {
		state = pane.Terminal.State
	}
	message := session.message
	if message == "" || (!session.messageTime.IsZero() && time.Since(session.messageTime) > 4*time.Second) {
		message = "^A d quit · ^A h/j/k/l focus"
	}
	session.statusBar.Draw(surface, session.height-1, StatusContext{
		Workspace: workspaceName, Window: windowName, PaneID: pane.ID, PaneTitle: pane.Title,
		State: state, Message: message, Now: time.Now(),
	})
	return writeAll(session.output, EncodeFrame(surface, cursor))
}

func drawPaneBorder(surface *Surface, rect Rect, title string, focused bool) {
	if rect.W <= 0 || rect.H <= 0 {
		return
	}
	style := Style{Foreground: Color{R: 85, G: 90, B: 100}, Background: Color{R: 18, G: 20, B: 24}}
	if focused {
		style.Foreground = Color{R: 110, G: 180, B: 255}
		style.Bold = true
	}
	for x := rect.X; x < rect.X+rect.W; x++ {
		surface.Set(x, rect.Y, Cell{Text: "─", Width: 1, Style: style})
		surface.Set(x, rect.Y+rect.H-1, Cell{Text: "─", Width: 1, Style: style})
	}
	for y := rect.Y; y < rect.Y+rect.H; y++ {
		surface.Set(rect.X, y, Cell{Text: "│", Width: 1, Style: style})
		surface.Set(rect.X+rect.W-1, y, Cell{Text: "│", Width: 1, Style: style})
	}
	surface.Set(rect.X, rect.Y, Cell{Text: "┌", Width: 1, Style: style})
	surface.Set(rect.X+rect.W-1, rect.Y, Cell{Text: "┐", Width: 1, Style: style})
	surface.Set(rect.X, rect.Y+rect.H-1, Cell{Text: "└", Width: 1, Style: style})
	surface.Set(rect.X+rect.W-1, rect.Y+rect.H-1, Cell{Text: "┘", Width: 1, Style: style})
	if rect.W > 4 {
		surface.Text(rect.X+2, rect.Y, rect.W-4, fitText(" "+title+" ", rect.W-4), style)
	}
}

func (session *session) currentWindow() (core.Window, bool) {
	for _, window := range session.snapshot.Windows {
		if window.ID == session.window {
			return window, true
		}
	}
	return core.Window{}, false
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
	if view.terminal != nil {
		view.terminal.Close()
		view.terminal = nil
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

func colorFromGhostty(color libghostty.Color) Color {
	return Color{R: color.R, G: color.G, B: color.B}
}
