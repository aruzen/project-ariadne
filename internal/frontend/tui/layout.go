package tui

import "github.com/aruzen/ariadne/internal/core"

type Rect struct {
	X int
	Y int
	W int
	H int
}

func (rect Rect) Interior() Rect {
	if rect.W < 3 || rect.H < 3 {
		return Rect{}
	}
	return Rect{X: rect.X + 1, Y: rect.Y + 1, W: rect.W - 2, H: rect.H - 2}
}

type Placement struct {
	PaneID core.PaneID
	Rect   Rect
}

// Separator is one internal split line. It never occupies an outer screen
// edge; its cell is reserved between sibling layout nodes.
type Separator struct {
	Rect      Rect
	Direction core.SplitDirection
}

type SplitLayout struct {
	Placements []Placement
	Separators []Separator
}

func CalculateLayout(root *core.LayoutNode, available Rect) []Placement {
	if root == nil || available.W <= 0 || available.H <= 0 {
		return nil
	}
	placements := make([]Placement, 0)
	calculateNode(*root, available, &placements)
	return placements
}

// CalculateSplitLayout gives Pane content all non-separator cells. Each split
// reserves one cell between adjacent children and does not reserve an outer
// frame, so a single Pane receives the complete available rectangle.
func CalculateSplitLayout(root *core.LayoutNode, available Rect) SplitLayout {
	if root == nil || available.W <= 0 || available.H <= 0 {
		return SplitLayout{}
	}
	result := SplitLayout{}
	calculateSplitNode(*root, available, &result)
	return result
}

func calculateNode(node core.LayoutNode, rect Rect, placements *[]Placement) {
	if rect.W <= 0 || rect.H <= 0 {
		return
	}
	if node.Kind == core.LayoutPane {
		*placements = append(*placements, Placement{PaneID: node.PaneID, Rect: rect})
		return
	}
	if len(node.Children) == 0 {
		return
	}
	axis := rect.W
	if node.Direction == core.SplitVertical {
		axis = rect.H
	}
	lengths := weightedLengths(axis, node.Weights, len(node.Children))
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
		calculateNode(child, childRect, placements)
		offset += lengths[index]
	}
}

func calculateSplitNode(node core.LayoutNode, rect Rect, result *SplitLayout) {
	if rect.W <= 0 || rect.H <= 0 {
		return
	}
	if node.Kind == core.LayoutPane {
		result.Placements = append(result.Placements, Placement{PaneID: node.PaneID, Rect: rect})
		return
	}
	if len(node.Children) == 0 {
		return
	}
	axis := rect.W
	if node.Direction == core.SplitVertical {
		axis = rect.H
	}
	separatorCount := min(len(node.Children)-1, max(0, axis-1))
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
		calculateSplitNode(child, childRect, result)
		offset += lengths[index]
		if index >= separatorCount {
			continue
		}
		separator := Rect{X: rect.X, Y: rect.Y, W: rect.W, H: 1}
		if node.Direction == core.SplitVertical {
			separator.Y += offset
		} else {
			separator = Rect{X: rect.X + offset, Y: rect.Y, W: 1, H: rect.H}
		}
		result.Separators = append(result.Separators, Separator{Rect: separator, Direction: node.Direction})
		offset++
	}
}

func weightedLengths(total int, weights []uint32, count int) []int {
	result := make([]int, count)
	if total <= 0 || count == 0 {
		return result
	}
	weightTotal := uint64(0)
	for index := 0; index < count; index++ {
		weight := uint32(1)
		if index < len(weights) && weights[index] != 0 {
			weight = weights[index]
		}
		weightTotal += uint64(weight)
	}
	remaining := total
	remainingWeight := weightTotal
	for index := 0; index < count; index++ {
		weight := uint32(1)
		if index < len(weights) && weights[index] != 0 {
			weight = weights[index]
		}
		if index == count-1 {
			result[index] = remaining
			break
		}
		length := int(uint64(remaining) * uint64(weight) / remainingWeight)
		result[index] = length
		remaining -= length
		remainingWeight -= uint64(weight)
	}
	return result
}

func placementFor(placements []Placement, paneID core.PaneID) (Placement, bool) {
	for _, placement := range placements {
		if placement.PaneID == paneID {
			return placement, true
		}
	}
	return Placement{}, false
}
