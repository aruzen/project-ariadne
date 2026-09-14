package tui

import (
	"errors"

	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/protocol"
)

var errPaneMinimumSize = errors.New("pane is already at its minimum size")

type layoutAncestor struct {
	node       core.LayoutNode
	rect       Rect
	childIndex int
}

func (session *session) resizeFocusedPane(action inputAction) {
	window, exists := session.currentWindow()
	if !exists || window.Layout == nil || session.focus == 0 {
		return
	}
	contentHeight := max(0, session.height-1)
	rootRect := Rect{W: session.width, H: contentHeight}
	separatorCells := session.paneFrame == PaneFrameSplit
	path, found := layoutPath(*window.Layout, rootRect, session.focus, separatorCells, nil)
	if !found {
		return
	}
	direction, neighborDelta := resizeDirection(action)
	for index := len(path) - 1; index >= 0; index-- {
		ancestor := path[index]
		if ancestor.node.Direction != direction {
			continue
		}
		neighbor := ancestor.childIndex + neighborDelta
		if neighbor < 0 || neighbor >= len(ancestor.node.Children) {
			continue
		}
		axis := ancestor.rect.W
		if direction == core.SplitVertical {
			axis = ancestor.rect.H
		}
		separators := 0
		if separatorCells {
			separators = min(len(ancestor.node.Children)-1, max(0, axis-1))
		}
		lengths := weightedLengths(axis-separators, ancestor.node.Weights, len(ancestor.node.Children))
		minimum := minimumAxisSize(ancestor.node.Children[neighbor], direction, session.paneFrame)
		if lengths[neighbor] <= minimum {
			session.setMessage(errPaneMinimumSize.Error())
			return
		}
		lengths[ancestor.childIndex]++
		lengths[neighbor]--
		weights := make([]uint32, len(lengths))
		for weightIndex, length := range lengths {
			weights[weightIndex] = uint32(length)
		}
		_, err := callTUI[core.ResizeSplitResult](session, protocol.OperationResizeSplit, protocol.ResizeSplitParams{
			SplitID: ancestor.node.SplitID, Weights: weights,
		})
		session.reportCommand(err, "pane resized")
		return
	}
	session.setMessage("no adjacent pane in that direction")
}

func resizeDirection(action inputAction) (core.SplitDirection, int) {
	switch action {
	case actionResizeLeft:
		return core.SplitHorizontal, -1
	case actionResizeRight:
		return core.SplitHorizontal, 1
	case actionResizeUp:
		return core.SplitVertical, -1
	default:
		return core.SplitVertical, 1
	}
}

func layoutPath(node core.LayoutNode, rect Rect, paneID core.PaneID, separatorCells bool, path []layoutAncestor) ([]layoutAncestor, bool) {
	if node.Kind == core.LayoutPane {
		return path, node.PaneID == paneID
	}
	axis := rect.W
	if node.Direction == core.SplitVertical {
		axis = rect.H
	}
	separatorCount := 0
	if separatorCells {
		separatorCount = min(len(node.Children)-1, max(0, axis-1))
	}
	lengths := weightedLengths(axis-separatorCount, node.Weights, len(node.Children))
	offset := 0
	for index, child := range node.Children {
		childRect := rect
		if node.Direction == core.SplitVertical {
			childRect.Y += offset
			childRect.H = lengths[index]
		} else {
			childRect.X += offset
			childRect.W = lengths[index]
		}
		next := append(path, layoutAncestor{node: node, rect: rect, childIndex: index})
		if result, found := layoutPath(child, childRect, paneID, separatorCells, next); found {
			return result, true
		}
		offset += lengths[index]
		if index < separatorCount {
			offset++
		}
	}
	return nil, false
}

func minimumAxisSize(node core.LayoutNode, direction core.SplitDirection, frame PaneFrameMode) int {
	leafMinimum := 1
	if frame == PaneFrameFull {
		leafMinimum = 3
	}
	if node.Kind == core.LayoutPane {
		return leafMinimum
	}
	values := make([]int, len(node.Children))
	for index, child := range node.Children {
		values[index] = minimumAxisSize(child, direction, frame)
	}
	if node.Direction == direction {
		total := 0
		for _, value := range values {
			total += value
		}
		if frame == PaneFrameSplit {
			total += len(values) - 1
		}
		return total
	}
	maximum := 0
	for _, value := range values {
		maximum = max(maximum, value)
	}
	return maximum
}
