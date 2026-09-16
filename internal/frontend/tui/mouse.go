package tui

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/protocol"
)

// MouseEvent uses cell coordinates relative to the content for pane handlers.
type MouseEvent struct {
	X, Y, Button, Action, Mods int
	Wheel                      int
}
type mousePaneContent interface {
	HandleMouse(MouseEvent) (bool, error)
}
type splitBoundary struct {
	node            core.LayoutNode
	rect            Rect
	index, position int
	lengths         []int
}
type mouseCapture struct {
	route         string
	pane          core.PaneID
	terminal      core.TerminalID
	window        core.WindowID
	focus         core.PaneID
	rect          Rect
	width, height int
	layout        *core.LayoutNode
	button        int
	boundary      splitBoundary
	weights       []uint32
	zoom          bool
	frame         PaneFrameMode
	preview       core.PaneID
	last          MouseEvent
	appPressed    bool
}

func parseMouse(sequence []byte) (MouseEvent, bool) {
	s := string(sequence)
	if !strings.HasPrefix(s, "\x1b[<") || len(s) < 7 || (s[len(s)-1] != 'm' && s[len(s)-1] != 'M') {
		return MouseEvent{}, false
	}
	parts := strings.Split(s[3:len(s)-1], ";")
	if len(parts) != 3 {
		return MouseEvent{}, false
	}
	values := [3]int{}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 65535 {
			return MouseEvent{}, false
		}
		values[i] = n
	}
	code := values[0]
	if code > 255 || values[1] == 0 || values[2] == 0 {
		return MouseEvent{}, false
	}
	e := MouseEvent{X: values[1] - 1, Y: values[2] - 1}
	if code&4 != 0 {
		e.Mods |= 1
	}
	if code&16 != 0 {
		e.Mods |= 2
	}
	if code&8 != 0 {
		e.Mods |= 4
	}
	if code&64 != 0 {
		e.Button = 4 + (code & 3)
		if code&3 == 0 {
			e.Wheel = -1
		} else if code&3 == 1 {
			e.Wheel = 1
		}
	} else {
		switch code & 3 {
		case 0:
			e.Button = 1
		case 1:
			e.Button = 3
		case 2:
			e.Button = 2
		}
	}
	if code&32 != 0 {
		e.Action = 2
	}
	if s[len(s)-1] == 'm' {
		e.Action = 1
	}
	return e, true
}

func (rect Rect) Contains(x, y int) bool {
	return x >= rect.X && y >= rect.Y && x < rect.X+rect.W && y < rect.Y+rect.H
}

func (session *session) validateMouseCapture() {
	g := session.gesture
	if g == nil {
		return
	}
	w, ok := session.currentWindow()
	valid := ok && session.window == g.window && session.focus == g.focus && session.width == g.width && session.height == g.height && session.zoom == g.zoom && session.paneFrame == g.frame && session.previewPane == g.preview && reflect.DeepEqual(w.Layout, g.layout)
	if g.pane != 0 {
		pane, exists := session.pane(g.pane)
		valid = valid && exists
		if g.terminal != 0 {
			valid = valid && pane.Terminal != nil && pane.Terminal.ID != nil && *pane.Terminal.ID == g.terminal && pane.Terminal.State == core.TerminalRunning
		}
	}
	if !valid {
		session.cancelMouseCapture()
	}
}

func (session *session) cancelMouseCapture() {
	if g := session.gesture; g != nil && g.route == "app" && g.appPressed {
		// Release the application button too, so a cancelled drag cannot leave
		// the child application in a permanently pressed state.
		if view := session.views[g.pane]; view != nil && view.terminalID == g.terminal {
			if content, ok := view.content.(*terminalPaneContent); ok && content.terminal != nil {
				if view.attachment != nil {
					release := g.last
					release.Action, release.Button = 1, g.button
					session.queuePTYInput(view, paneInput{mouse: &paneMouseInput{event: release, cols: content.cols, rows: content.rows}})
				}
			}
		}
	}
	session.gesture = nil
	session.dragLayout = nil
}

func (session *session) capture(route string, pane core.PaneID, rect Rect, button int) *mouseCapture {
	w, _ := session.currentWindow()
	g := &mouseCapture{route: route, pane: pane, window: session.window, focus: session.focus, rect: rect, button: button, width: session.width, height: session.height, layout: w.Layout, zoom: session.zoom, frame: session.paneFrame, preview: session.previewPane}
	if view := session.views[pane]; view != nil {
		g.terminal = view.terminalID
	}
	session.gesture = g
	return g
}

func (session *session) handleMouse(e MouseEvent) {
	if !session.presentation.Mouse || session.inputMode != inputModeNormal {
		return
	}
	session.validateMouseCapture()
	if g := session.gesture; g != nil {
		if e.Wheel != 0 {
			return
		}
		if g.route == "split" {
			if e.Action == 2 || e.Action == 1 {
				session.previewSplitDrag(e, g)
			}
			if e.Action == 1 {
				weights := append([]uint32(nil), g.weights...)
				id := g.boundary.node.SplitID
				session.cancelMouseCapture()
				session.relayout()
				session.syncViews()
				session.dirty = true
				if len(weights) != 0 {
					_, err := callTUI[core.ResizeSplitResult](session, protocol.OperationResizeSplit, protocol.ResizeSplitParams{SplitID: id, Weights: weights})
					session.reportCommand(err, "pane resized")
				}
			}
			return
		}
		session.routePaneMouse(e, g)
		if e.Action == 1 {
			session.cancelMouseCapture()
		}
		return
	}
	// A release after cancelled capture must never start a new route.
	if e.Action == 1 {
		return
	}
	if e.Y >= session.contentHeight() || e.X < 0 || e.X >= session.width || e.Y < 0 {
		return
	}
	if e.Action == 0 && e.Button == 1 && e.Wheel == 0 && !session.zoom && !session.compact && session.previewPane == 0 && session.paneFrame != PaneFrameNone {
		if w, ok := session.currentWindow(); ok && w.Layout != nil {
			if boundary, found := findSplitBoundary(*w.Layout, Rect{W: session.width, H: session.contentHeight()}, e.X, e.Y, session.paneFrame); found {
				if session.paneFrame == PaneFrameSplit || session.mouseOnChrome(e) {
					g := session.capture("split", 0, Rect{}, 1)
					g.boundary = boundary
					return
				}
			}
		}
	}
	for _, placement := range session.placements {
		if !placement.Rect.Contains(e.X, e.Y) {
			continue
		}
		pane, ok := session.pane(placement.PaneID)
		if !ok {
			return
		}
		renderer, err := session.paneRenderer(pane)
		if err != nil || !renderer.Focusable(pane) {
			return
		}
		rect := session.chrome(pane, renderer, placement.Rect).ContentRect(placement.Rect)
		if pane.ID != session.focus {
			if e.Action == 0 && e.Wheel == 0 {
				if session.copyMode {
					session.leaveCopyMode()
				}
				session.focusPane(pane.ID)
				session.capture("consume", pane.ID, rect, e.Button)
			}
			return
		}
		if !rect.Contains(e.X, e.Y) {
			return
		}
		route := "tool"
		if pane.Terminal != nil {
			route = "selection"
			if content := session.activeTerminalContent(); content != nil && content.terminal != nil && content.terminal.MouseTracking() && e.Mods&1 == 0 && !session.copyMode {
				route = "app"
			}
		}
		g := session.capture(route, pane.ID, rect, e.Button)
		session.routePaneMouse(e, g)
		if e.Wheel != 0 || e.Action != 0 || e.Button == 0 {
			session.cancelMouseCapture()
		}
		return
	}
}

func (session *session) mouseOnChrome(e MouseEvent) bool {
	for _, p := range session.placements {
		if !p.Rect.Contains(e.X, e.Y) {
			continue
		}
		pane, _ := session.pane(p.PaneID)
		r, err := session.paneRenderer(pane)
		return err == nil && !session.chrome(pane, r, p.Rect).ContentRect(p.Rect).Contains(e.X, e.Y)
	}
	return false
}

func (session *session) routePaneMouse(e MouseEvent, g *mouseCapture) {
	if g.route == "consume" {
		return
	}
	view := session.views[g.pane]
	if view == nil {
		return
	}
	e.X = min(max(0, e.X-g.rect.X), max(0, g.rect.W-1))
	e.Y = min(max(0, e.Y-g.rect.Y), max(0, g.rect.H-1))
	if g.route == "tool" {
		if content, ok := view.content.(mousePaneContent); ok {
			_, err := safeMousePane(content, e)
			if err != nil {
				session.setMessage(err.Error())
			}
			session.dirty = true
		}
		return
	}
	content, ok := view.content.(*terminalPaneContent)
	if !ok || content.terminal == nil {
		return
	}
	if g.route == "app" {
		g.last = e
		if e.Action == 0 && e.Wheel == 0 && e.Button != 0 {
			g.appPressed = true
		} else if e.Action == 1 {
			g.appPressed = false
		}
		if view.attachment != nil {
			session.queuePTYInput(view, paneInput{mouse: &paneMouseInput{event: e, cols: content.cols, rows: content.rows, pressed: e.Action != 1 && e.Wheel == 0 && g.button != 0}})
		}
		return
	}
	if e.Wheel != 0 {
		if !session.copyMode {
			session.enterCopyMode()
		}
		_ = content.scroll(e.Wheel * 3)
		session.dirty = true
		return
	}
	if g.button != 1 {
		return
	}
	if e.Action == 0 {
		if !session.copyMode {
			session.enterCopyMode()
		}
		session.copySelecting = true
		session.copyStartX, session.copyStartY = e.X, e.Y
	}
	session.copyX, session.copyY = e.X, e.Y
	if err := content.terminal.SelectRange(session.copyStartX, session.copyStartY, e.X, e.Y); err != nil {
		session.setMessage(err.Error())
	}
	session.dirty = true
}

func safeMousePane(content mousePaneContent, e MouseEvent) (handled bool, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("pane mouse panic: %v", p)
		}
	}()
	return content.HandleMouse(e)
}

func (content *builtinToolContent) HandleMouse(e MouseEvent) (bool, error) {
	if e.Wheel != 0 {
		content.scroll = max(0, content.scroll+e.Wheel*3)
	} else if e.Action == 0 && e.Button == 1 && content.selectable() {
		content.selected = min(max(1, content.scroll+e.Y), max(1, len(content.lines())-1))
	}
	return true, nil
}

func findSplitBoundary(node core.LayoutNode, rect Rect, x, y int, frame PaneFrameMode) (splitBoundary, bool) {
	if node.Kind != core.LayoutSplit || !rect.Contains(x, y) {
		return splitBoundary{}, false
	}
	axis := rect.W
	if node.Direction == core.SplitVertical {
		axis = rect.H
	}
	separators := 0
	if frame == PaneFrameSplit {
		separators = max(0, len(node.Children)-1)
	}
	lengths := weightedLengths(max(0, axis-separators), node.Weights, len(node.Children))
	offset := 0
	for i, child := range node.Children {
		childRect := rect
		if node.Direction == core.SplitHorizontal {
			childRect.X += offset
			childRect.W = lengths[i]
		} else {
			childRect.Y += offset
			childRect.H = lengths[i]
		}
		if boundary, ok := findSplitBoundary(child, childRect, x, y, frame); ok {
			return boundary, true
		}
		offset += lengths[i]
		if i < len(node.Children)-1 {
			position := rect.X + offset
			coordinate := x
			if node.Direction == core.SplitVertical {
				position = rect.Y + offset
				coordinate = y
			}
			if coordinate == position || (frame == PaneFrameFull && coordinate == position-1) {
				return splitBoundary{node: node, rect: rect, index: i, position: position, lengths: lengths}, true
			}
			if frame == PaneFrameSplit {
				offset++
			}
		}
	}
	return splitBoundary{}, false
}

func (session *session) previewSplitDrag(e MouseEvent, g *mouseCapture) {
	b := g.boundary
	i := b.index
	delta := e.X - b.position
	if b.node.Direction == core.SplitVertical {
		delta = e.Y - b.position
	}
	leftMin, lh := session.nodeMinimum(b.node.Children[i])
	rightMin, rh := session.nodeMinimum(b.node.Children[i+1])
	if b.node.Direction == core.SplitVertical {
		leftMin, rightMin = lh, rh
	}
	if b.lengths[i]+b.lengths[i+1] < leftMin+rightMin {
		return
	}
	delta = min(max(delta, leftMin-b.lengths[i]), b.lengths[i+1]-rightMin)
	lengths := append([]int(nil), b.lengths...)
	lengths[i] += delta
	lengths[i+1] -= delta
	g.weights = make([]uint32, len(lengths))
	for index, n := range lengths {
		g.weights[index] = uint32(max(1, n))
	}
	session.dragLayout = layoutWithWeights(g.layout, b.node.SplitID, g.weights)
	session.relayout()
	session.syncViews()
	session.dirty = true
}

func layoutWithWeights(root *core.LayoutNode, id core.SplitID, weights []uint32) *core.LayoutNode {
	if root == nil {
		return nil
	}
	copy := *root
	copy.Weights = append([]uint32(nil), root.Weights...)
	copy.Children = make([]core.LayoutNode, len(root.Children))
	if root.SplitID == id {
		copy.Weights = append([]uint32(nil), weights...)
	}
	for i := range root.Children {
		copy.Children[i] = *layoutWithWeights(&root.Children[i], id, weights)
	}
	return &copy
}
