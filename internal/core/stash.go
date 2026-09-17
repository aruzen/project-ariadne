package core

import "fmt"

func (c *Core) stashPane(command StashPaneCommand, frontends map[FrontendID]*frontend) (any, *Event, error) {
	pane, exists := c.state.panes[command.PaneID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: pane %d", ErrNotFound, command.PaneID)
	}
	if _, exists := c.state.stashedPanes[pane.ID]; exists {
		return nil, nil, fmt.Errorf("%w: pane %d is already stashed", ErrInvalidState, pane.ID)
	}
	if _, exists := c.state.stashedWindows[pane.WindowID]; exists {
		return nil, nil, fmt.Errorf("%w: pane %d belongs to a stashed window", ErrInvalidState, pane.ID)
	}
	window, exists := c.state.windows[pane.WindowID]
	if !exists || window.Layout == nil {
		return nil, nil, fmt.Errorf("%w: pane %d has no visible layout", ErrInvalidState, pane.ID)
	}
	hint, found := paneStashHint(*window.Layout, pane.ID)
	if !found {
		return nil, nil, fmt.Errorf("%w: pane %d missing from layout", ErrInvalidState, pane.ID)
	}
	hint.PaneID = pane.ID
	hint.OriginWorkspaceID = window.WorkspaceID
	hint.OriginWindowID = window.ID
	layout, removed := removePaneFromLayout(window.Layout, pane.ID)
	if !removed {
		return nil, nil, fmt.Errorf("%w: pane %d missing from layout", ErrInvalidState, pane.ID)
	}
	window.Layout = layout
	c.state.windows[window.ID] = window
	c.state.stashedPanes[pane.ID] = hint
	c.repairFrontendAfterPaneHidden(frontends, pane.ID, window)
	result := StashPaneResult{Pane: clonePane(pane), Window: cloneWindow(window), Stashed: hint}
	return result, &Event{Kind: EventPaneStashed, Payload: PaneStashEvent(result)}, nil
}

func (c *Core) restorePane(command RestorePaneCommand, frontends map[FrontendID]*frontend) (any, *Event, error) {
	if command.DestinationWindowID == 0 && (command.TargetPaneID != 0 || command.Direction != "") {
		return nil, nil, fmt.Errorf("%w: placement requires a destination window", ErrInvalidArgument)
	}
	if p := c.state.panes[command.PaneID]; p.Transient {
		return nil, nil, fmt.Errorf("%w: temporary editor cannot be restored", ErrInvalidState)
	}
	hint, exists := c.state.stashedPanes[command.PaneID]
	if !exists {
		if _, paneExists := c.state.panes[command.PaneID]; !paneExists {
			return nil, nil, fmt.Errorf("%w: pane %d", ErrNotFound, command.PaneID)
		}
		return nil, nil, fmt.Errorf("%w: pane %d is not stashed", ErrInvalidState, command.PaneID)
	}
	pane := c.state.panes[command.PaneID]
	destination, target, direction, before, err := c.restorePaneDestination(command, hint, frontends)
	if err != nil {
		return nil, nil, err
	}

	if destination.Layout == nil {
		leaf := paneLeaf(pane.ID)
		destination.Layout = &leaf
	} else {
		layout := cloneLayout(*destination.Layout)
		required, found := insertionRequiresSplit(layout, target, direction)
		if !found {
			return nil, nil, fmt.Errorf("%w: target pane %d missing from layout", ErrInvalidState, target)
		}
		splitID := SplitID(0)
		if required {
			splitID = hint.OriginalSplitID
			if splitID == 0 || c.state.splitIDExists(splitID) {
				splitID, err = c.state.allocateSplitID()
				if err != nil {
					return nil, nil, err
				}
			}
		}
		weight, targetWeight := uint32(1), uint32(1)
		if command.DestinationWindowID == 0 && destination.ID == hint.OriginWindowID && target == hint.TargetPaneID && direction == hint.Direction {
			weight, targetWeight = hint.Weight, hint.TargetWeight
		}
		if !insertRelative(&layout, target, pane.ID, direction, before, weight, targetWeight, splitID) {
			return nil, nil, fmt.Errorf("%w: target pane %d missing from layout", ErrInvalidState, target)
		}
		destination.Layout = &layout
	}
	pane.WindowID = destination.ID
	c.state.panes[pane.ID] = pane
	c.state.windows[destination.ID] = destination
	delete(c.state.stashedPanes, pane.ID)
	result := RestorePaneResult{Pane: clonePane(pane), Window: cloneWindow(destination), Stashed: hint}
	return result, &Event{Kind: EventPaneRestored, Payload: PaneStashEvent(result)}, nil
}

func (c *Core) stashWindow(command StashWindowCommand, frontends map[FrontendID]*frontend) (any, *Event, error) {
	window, exists := c.state.windows[command.WindowID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: window %d", ErrNotFound, command.WindowID)
	}
	if _, exists := c.state.stashedWindows[window.ID]; exists {
		return nil, nil, fmt.Errorf("%w: window %d is already stashed", ErrInvalidState, window.ID)
	}
	for _, stashed := range c.state.stashedPanes {
		if stashed.OriginWindowID == window.ID {
			return nil, nil, fmt.Errorf("%w: window %d has individually stashed panes", ErrInvalidState, window.ID)
		}
	}
	workspace, exists := c.state.workspaces[window.WorkspaceID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: window %d has no workspace", ErrInvalidState, window.ID)
	}
	index := indexWindowID(workspace.WindowIDs, window.ID)
	if index < 0 {
		return nil, nil, fmt.Errorf("%w: window %d is not visible", ErrInvalidState, window.ID)
	}
	hint := StashedWindow{WindowID: window.ID, OriginWorkspaceID: workspace.ID, OriginalWindowIndex: index}
	workspace.WindowIDs = removeWindowID(workspace.WindowIDs, window.ID)
	c.state.workspaces[workspace.ID] = workspace
	c.state.stashedWindows[window.ID] = hint
	for _, frontend := range frontends {
		if frontend.state.WindowID == window.ID {
			frontend.state = c.initialFrontendState(frontend.state.ID)
		}
	}
	result := StashWindowResult{Window: cloneWindow(window), Workspace: cloneWorkspace(workspace), Stashed: hint}
	return result, &Event{Kind: EventWindowStashed, Payload: WindowStashEvent(result)}, nil
}

func (c *Core) restoreWindow(command RestoreWindowCommand, frontends map[FrontendID]*frontend) (any, *Event, error) {
	hint, exists := c.state.stashedWindows[command.WindowID]
	if !exists {
		if _, windowExists := c.state.windows[command.WindowID]; !windowExists {
			return nil, nil, fmt.Errorf("%w: window %d", ErrNotFound, command.WindowID)
		}
		return nil, nil, fmt.Errorf("%w: window %d is not stashed", ErrInvalidState, command.WindowID)
	}
	window := c.state.windows[command.WindowID]
	workspaceID := command.WorkspaceID
	if workspaceID == 0 {
		if _, exists := c.state.workspaces[hint.OriginWorkspaceID]; exists {
			workspaceID = hint.OriginWorkspaceID
		} else if frontend := frontends[command.FrontendID]; frontend != nil {
			workspaceID = frontend.state.WorkspaceID
		}
		if workspaceID == 0 {
			workspaceID = c.state.firstWorkspaceID()
		}
	}
	workspace, exists := c.state.workspaces[workspaceID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: workspace %d", ErrNotFound, workspaceID)
	}
	for _, id := range workspace.WindowIDs {
		if c.state.windows[id].Name == window.Name {
			return nil, nil, fmt.Errorf("%w: window name %q in workspace %d", ErrAlreadyExists, window.Name, workspace.ID)
		}
	}
	index := len(workspace.WindowIDs)
	if command.WorkspaceID == 0 && workspace.ID == hint.OriginWorkspaceID && hint.OriginalWindowIndex < index {
		index = hint.OriginalWindowIndex
	}
	workspace.WindowIDs = insertWindowID(workspace.WindowIDs, index, window.ID)
	window.WorkspaceID = workspace.ID
	c.state.windows[window.ID] = window
	c.state.workspaces[workspace.ID] = workspace
	delete(c.state.stashedWindows, window.ID)
	result := RestoreWindowResult{Window: cloneWindow(window), Workspace: cloneWorkspace(workspace), Stashed: hint}
	return result, &Event{Kind: EventWindowRestored, Payload: WindowStashEvent(result)}, nil
}

func paneStashHint(node LayoutNode, paneID PaneID) (StashedPane, bool) {
	if node.Kind == LayoutPane {
		return StashedPane{}, node.PaneID == paneID
	}
	for index, child := range node.Children {
		if child.Kind == LayoutPane && child.PaneID == paneID {
			if len(node.Children) < 2 {
				return StashedPane{}, false
			}
			targetIndex, before := index-1, false
			if index == 0 {
				targetIndex, before = 1, true
			}
			return StashedPane{
				TargetPaneID: firstPane(node.Children[targetIndex]), Direction: node.Direction, Before: before,
				Weight: node.Weights[index], TargetWeight: node.Weights[targetIndex], OriginalSplitID: node.SplitID,
			}, true
		}
		if hint, found := paneStashHint(child, paneID); found {
			return hint, true
		}
	}
	return StashedPane{}, false
}

func (c *Core) restorePaneDestination(command RestorePaneCommand, hint StashedPane, frontends map[FrontendID]*frontend) (Window, PaneID, SplitDirection, bool, error) {
	if command.DestinationWindowID != 0 {
		window, exists := c.state.visibleWindow(command.DestinationWindowID)
		if !exists {
			return Window{}, 0, "", false, fmt.Errorf("%w: destination window %d", ErrNotFound, command.DestinationWindowID)
		}
		target, direction, err := validateRestorePlacement(c.state, window, command.TargetPaneID, command.Direction)
		return window, target, direction, false, err
	}
	if window, exists := c.state.visibleWindow(hint.OriginWindowID); exists {
		if window.Layout == nil {
			return window, 0, "", false, nil
		}
		if target, exists := c.state.panes[hint.TargetPaneID]; exists && target.WindowID == window.ID && layoutContainsPane(window.Layout, target.ID) {
			return window, target.ID, hint.Direction, hint.Before, nil
		}
	}
	if frontend := frontends[command.FrontendID]; frontend != nil {
		if window, exists := c.state.visibleWindow(frontend.state.WindowID); exists {
			target, direction, err := defaultRestorePlacement(window)
			return window, target, direction, false, err
		}
	}
	window, exists := c.state.firstVisibleWindow()
	if !exists {
		return Window{}, 0, "", false, fmt.Errorf("%w: no visible destination window", ErrInvalidState)
	}
	target, direction, err := defaultRestorePlacement(window)
	return window, target, direction, false, err
}

func validateRestorePlacement(state *state, window Window, target PaneID, direction SplitDirection) (PaneID, SplitDirection, error) {
	if window.Layout == nil {
		if target != 0 || direction != "" {
			return 0, "", fmt.Errorf("%w: empty destination does not accept placement", ErrInvalidArgument)
		}
		return 0, "", nil
	}
	if target == 0 || !validDirection(direction) {
		return 0, "", fmt.Errorf("%w: target pane and direction are required", ErrInvalidArgument)
	}
	pane, exists := state.panes[target]
	if !exists || pane.WindowID != window.ID || !layoutContainsPane(window.Layout, target) {
		return 0, "", fmt.Errorf("%w: target pane %d", ErrNotFound, target)
	}
	return target, direction, nil
}

func defaultRestorePlacement(window Window) (PaneID, SplitDirection, error) {
	if window.Layout == nil {
		return 0, "", nil
	}
	return firstPane(*window.Layout), SplitVertical, nil
}

func (c *Core) repairFrontendAfterPaneHidden(frontends map[FrontendID]*frontend, paneID PaneID, window Window) {
	for _, frontend := range frontends {
		if frontend.state.PaneID != paneID {
			continue
		}
		frontend.state.PaneID = 0
		if window.Layout != nil {
			frontend.state.PaneID = firstPane(*window.Layout)
		}
	}
}

func (s *state) visibleWindow(id WindowID) (Window, bool) {
	window, exists := s.windows[id]
	if !exists {
		return Window{}, false
	}
	if _, stashed := s.stashedWindows[id]; stashed {
		return Window{}, false
	}
	workspace, exists := s.workspaces[window.WorkspaceID]
	return window, exists && indexWindowID(workspace.WindowIDs, id) >= 0
}

func (s *state) firstVisibleWindow() (Window, bool) {
	for _, workspaceID := range s.workspaceOrder {
		workspace := s.workspaces[workspaceID]
		for _, windowID := range workspace.WindowIDs {
			if window, exists := s.visibleWindow(windowID); exists {
				return window, true
			}
		}
	}
	return Window{}, false
}

func (s *state) firstWorkspaceID() WorkspaceID {
	if len(s.workspaceOrder) == 0 {
		return 0
	}
	return s.workspaceOrder[0]
}

func (s *state) splitIDExists(id SplitID) bool {
	for _, window := range s.windows {
		if window.Layout != nil && splitByID(window.Layout, id) != nil {
			return true
		}
	}
	return false
}

func layoutContainsPane(layout *LayoutNode, paneID PaneID) bool {
	if layout == nil {
		return false
	}
	if layout.Kind == LayoutPane {
		return layout.PaneID == paneID
	}
	for index := range layout.Children {
		if layoutContainsPane(&layout.Children[index], paneID) {
			return true
		}
	}
	return false
}

func insertRelative(node *LayoutNode, target, inserted PaneID, direction SplitDirection, before bool, weight, targetWeight uint32, splitID SplitID) bool {
	if node.Kind == LayoutPane {
		if node.PaneID != target {
			return false
		}
		children := []LayoutNode{paneLeaf(target), paneLeaf(inserted)}
		weights := []uint32{targetWeight, weight}
		if before {
			children[0], children[1] = children[1], children[0]
			weights[0], weights[1] = weights[1], weights[0]
		}
		*node = LayoutNode{Kind: LayoutSplit, SplitID: splitID, Direction: direction, Children: children, Weights: weights}
		return true
	}
	for index := range node.Children {
		child := &node.Children[index]
		if child.Kind == LayoutPane && child.PaneID == target && node.Direction == direction {
			insertAt := index + 1
			if before {
				insertAt = index
			}
			node.Children = append(node.Children, LayoutNode{})
			copy(node.Children[insertAt+1:], node.Children[insertAt:])
			node.Children[insertAt] = paneLeaf(inserted)
			node.Weights = append(node.Weights, 0)
			copy(node.Weights[insertAt+1:], node.Weights[insertAt:])
			node.Weights[insertAt] = weight
			return true
		}
		if insertRelative(child, target, inserted, direction, before, weight, targetWeight, splitID) {
			return true
		}
	}
	return false
}

func indexWindowID(ids []WindowID, target WindowID) int {
	for index, id := range ids {
		if id == target {
			return index
		}
	}
	return -1
}

func insertWindowID(ids []WindowID, index int, id WindowID) []WindowID {
	ids = append(ids, 0)
	copy(ids[index+1:], ids[index:])
	ids[index] = id
	return ids
}
