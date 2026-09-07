package core

import (
	"fmt"
	"math"
	"strings"
)

func (c *Core) execute(command Command, frontends map[FrontendID]*frontend) (any, *Event, error) {
	switch value := command.(type) {
	case CreateWorkspaceCommand:
		return c.createWorkspace(value)
	case CreateWindowCommand:
		return c.createWindow(value)
	case CreatePaneCommand:
		return c.createPane(value)
	case SplitPaneCommand:
		return c.splitPane(value)
	case MovePaneCommand:
		return c.movePane(value, frontends)
	case ClosePaneCommand:
		return c.closePane(value, frontends)
	case SetFocusCommand:
		return c.setFocus(value, frontends)
	default:
		return nil, nil, ErrInvalidCommand
	}
}

func (c *Core) createWorkspace(command CreateWorkspaceCommand) (any, *Event, error) {
	if strings.TrimSpace(command.Name) == "" {
		return nil, nil, fmt.Errorf("%w: workspace name is empty", ErrInvalidArgument)
	}
	for _, workspace := range c.state.workspaces {
		if workspace.Name == command.Name {
			return nil, nil, fmt.Errorf("%w: workspace name %q", ErrAlreadyExists, command.Name)
		}
	}
	id, err := c.state.allocateWorkspaceID()
	if err != nil {
		return nil, nil, err
	}
	workspace := Workspace{ID: id, Name: command.Name, WindowIDs: []WindowID{}}
	c.state.workspaces[id] = workspace
	c.state.workspaceOrder = append(c.state.workspaceOrder, id)
	result := CreateWorkspaceResult{Workspace: cloneWorkspace(workspace)}
	event := Event{Kind: EventWorkspaceCreated, Payload: WorkspaceCreatedEvent{Workspace: cloneWorkspace(workspace)}}
	return result, &event, nil
}

func (c *Core) createWindow(command CreateWindowCommand) (any, *Event, error) {
	workspace, exists := c.state.workspaces[command.WorkspaceID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: workspace %d", ErrNotFound, command.WorkspaceID)
	}
	if strings.TrimSpace(command.Name) == "" {
		return nil, nil, fmt.Errorf("%w: window name is empty", ErrInvalidArgument)
	}
	for _, windowID := range workspace.WindowIDs {
		if c.state.windows[windowID].Name == command.Name {
			return nil, nil, fmt.Errorf("%w: window name %q in workspace %d", ErrAlreadyExists, command.Name, workspace.ID)
		}
	}
	id, err := c.state.allocateWindowID()
	if err != nil {
		return nil, nil, err
	}
	window := Window{ID: id, WorkspaceID: workspace.ID, Name: command.Name}
	c.state.windows[id] = window
	c.state.windowOrder = append(c.state.windowOrder, id)
	workspace.WindowIDs = append(workspace.WindowIDs, id)
	c.state.workspaces[workspace.ID] = workspace
	result := CreateWindowResult{Window: cloneWindow(window)}
	event := Event{Kind: EventWindowCreated, Payload: WindowCreatedEvent{Window: cloneWindow(window)}}
	return result, &event, nil
}

func (c *Core) createPane(command CreatePaneCommand) (any, *Event, error) {
	window, exists := c.state.windows[command.WindowID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: window %d", ErrNotFound, command.WindowID)
	}
	if window.Layout != nil {
		return nil, nil, fmt.Errorf("%w: window %d is not empty", ErrInvalidState, window.ID)
	}
	if err := c.state.validatePaneSpec(command.Pane); err != nil {
		return nil, nil, err
	}
	id, err := c.state.allocatePaneID()
	if err != nil {
		return nil, nil, err
	}
	pane := paneFromSpec(id, window.ID, command.Pane)
	layout := paneLeaf(id)
	window.Layout = &layout
	c.state.windows[window.ID] = window
	c.state.panes[id] = pane
	c.state.paneOrder = append(c.state.paneOrder, id)
	result := CreatePaneResult{Pane: clonePane(pane), Window: cloneWindow(window)}
	event := Event{Kind: EventPaneCreated, Payload: PaneCreatedEvent{Pane: clonePane(pane), Window: cloneWindow(window)}}
	return result, &event, nil
}

func (c *Core) splitPane(command SplitPaneCommand) (any, *Event, error) {
	target, exists := c.state.panes[command.TargetPaneID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: pane %d", ErrNotFound, command.TargetPaneID)
	}
	if !validDirection(command.Direction) {
		return nil, nil, fmt.Errorf("%w: split direction %q", ErrInvalidArgument, command.Direction)
	}
	if err := c.state.validatePaneSpec(command.Pane); err != nil {
		return nil, nil, err
	}
	window := c.state.windows[target.WindowID]
	layout := cloneLayout(*window.Layout)
	id, err := c.state.allocatePaneID()
	if err != nil {
		return nil, nil, err
	}
	if !insertSplit(&layout, target.ID, id, command.Direction) {
		return nil, nil, fmt.Errorf("%w: pane %d missing from layout", ErrInvalidState, target.ID)
	}
	pane := paneFromSpec(id, window.ID, command.Pane)
	window.Layout = &layout
	c.state.windows[window.ID] = window
	c.state.panes[id] = pane
	c.state.paneOrder = append(c.state.paneOrder, id)
	result := CreatePaneResult{Pane: clonePane(pane), Window: cloneWindow(window)}
	event := Event{Kind: EventPaneCreated, Payload: PaneCreatedEvent{
		Pane: clonePane(pane), Window: cloneWindow(window), TargetPaneID: target.ID, Direction: command.Direction,
	}}
	return result, &event, nil
}

func (c *Core) movePane(command MovePaneCommand, frontends map[FrontendID]*frontend) (any, *Event, error) {
	pane, exists := c.state.panes[command.PaneID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: pane %d", ErrNotFound, command.PaneID)
	}
	destination, exists := c.state.windows[command.DestinationID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: window %d", ErrNotFound, command.DestinationID)
	}
	if command.TargetPaneID == pane.ID {
		return nil, nil, fmt.Errorf("%w: pane cannot target itself", ErrInvalidArgument)
	}
	if destination.Layout == nil {
		if command.TargetPaneID != 0 {
			return nil, nil, fmt.Errorf("%w: empty destination has no target pane", ErrInvalidArgument)
		}
		if command.Direction != "" {
			return nil, nil, fmt.Errorf("%w: empty destination does not use a split direction", ErrInvalidArgument)
		}
	} else {
		if command.TargetPaneID == 0 || !validDirection(command.Direction) {
			return nil, nil, fmt.Errorf("%w: target pane and direction are required", ErrInvalidArgument)
		}
		target, exists := c.state.panes[command.TargetPaneID]
		if !exists || target.WindowID != destination.ID {
			return nil, nil, fmt.Errorf("%w: target pane %d in destination window", ErrNotFound, command.TargetPaneID)
		}
	}

	source := c.state.windows[pane.WindowID]
	if source.ID == destination.ID && destination.Layout != nil {
		layout, removed := removePaneFromLayout(source.Layout, pane.ID)
		if !removed || layout == nil {
			return nil, nil, fmt.Errorf("%w: cannot move the only pane within its window", ErrInvalidState)
		}
		if !insertSplit(layout, command.TargetPaneID, pane.ID, command.Direction) {
			return nil, nil, fmt.Errorf("%w: target pane %d missing from layout", ErrInvalidState, command.TargetPaneID)
		}
		source.Layout = layout
		c.state.windows[source.ID] = source
		result := MovePaneResult{Pane: clonePane(pane), SourceWindow: cloneWindow(source), DestinationWindow: cloneWindow(source)}
		event := Event{Kind: EventPaneMoved, Payload: PaneMovedEvent{
			Pane: clonePane(pane), SourceWindow: cloneWindow(source), DestinationWindow: cloneWindow(source),
			TargetPaneID: command.TargetPaneID, Direction: command.Direction,
		}}
		return result, &event, nil
	}

	sourceLayout, removed := removePaneFromLayout(source.Layout, pane.ID)
	if !removed {
		return nil, nil, fmt.Errorf("%w: pane %d missing from layout", ErrInvalidState, pane.ID)
	}
	var destinationLayout *LayoutNode
	if destination.Layout == nil {
		leaf := paneLeaf(pane.ID)
		destinationLayout = &leaf
	} else {
		layout := cloneLayout(*destination.Layout)
		if !insertSplit(&layout, command.TargetPaneID, pane.ID, command.Direction) {
			return nil, nil, fmt.Errorf("%w: target pane %d missing from layout", ErrInvalidState, command.TargetPaneID)
		}
		destinationLayout = &layout
	}
	source.Layout = sourceLayout
	destination.Layout = destinationLayout
	pane.WindowID = destination.ID
	c.state.windows[source.ID] = source
	c.state.windows[destination.ID] = destination
	c.state.panes[pane.ID] = pane
	for _, frontend := range frontends {
		if frontend.state.PaneID == pane.ID {
			frontend.state.WorkspaceID = destination.WorkspaceID
			frontend.state.WindowID = destination.ID
		}
	}
	result := MovePaneResult{Pane: clonePane(pane), SourceWindow: cloneWindow(source), DestinationWindow: cloneWindow(destination)}
	event := Event{Kind: EventPaneMoved, Payload: PaneMovedEvent{
		Pane: clonePane(pane), SourceWindow: cloneWindow(source), DestinationWindow: cloneWindow(destination),
		TargetPaneID: command.TargetPaneID, Direction: command.Direction,
	}}
	return result, &event, nil
}

func (c *Core) closePane(command ClosePaneCommand, frontends map[FrontendID]*frontend) (any, *Event, error) {
	pane, exists := c.state.panes[command.PaneID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: pane %d", ErrNotFound, command.PaneID)
	}
	window := c.state.windows[pane.WindowID]
	layout, removed := removePaneFromLayout(window.Layout, pane.ID)
	if !removed {
		return nil, nil, fmt.Errorf("%w: pane %d missing from layout", ErrInvalidState, pane.ID)
	}
	window.Layout = layout
	c.state.windows[window.ID] = window
	delete(c.state.panes, pane.ID)
	c.state.paneOrder = removePaneID(c.state.paneOrder, pane.ID)
	for _, frontend := range frontends {
		if frontend.state.PaneID == pane.ID {
			frontend.state.PaneID = 0
			if window.Layout != nil {
				frontend.state.PaneID = firstPane(*window.Layout)
			}
		}
	}
	result := ClosePaneResult{Pane: clonePane(pane), Window: cloneWindow(window)}
	event := Event{Kind: EventPaneClosed, Payload: PaneClosedEvent{Pane: clonePane(pane), Window: cloneWindow(window)}}
	return result, &event, nil
}

func (c *Core) setFocus(command SetFocusCommand, frontends map[FrontendID]*frontend) (any, *Event, error) {
	frontend, exists := frontends[command.FrontendID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: frontend %d", ErrNotFound, command.FrontendID)
	}
	pane, exists := c.state.panes[command.PaneID]
	if !exists {
		return nil, nil, fmt.Errorf("%w: pane %d", ErrNotFound, command.PaneID)
	}
	window := c.state.windows[pane.WindowID]
	frontend.state.WorkspaceID = window.WorkspaceID
	frontend.state.WindowID = window.ID
	frontend.state.PaneID = pane.ID
	return SetFocusResult{Focus: frontend.state}, nil, nil
}

func paneFromSpec(id PaneID, windowID WindowID, spec PaneSpec) Pane {
	pane := Pane{ID: id, WindowID: windowID, Kind: spec.Kind, Title: spec.Title}
	if spec.TerminalID != nil {
		terminalID := *spec.TerminalID
		pane.TerminalID = &terminalID
	}
	return pane
}

func (s *state) validatePaneSpec(spec PaneSpec) error {
	pane := Pane{Kind: spec.Kind, TerminalID: spec.TerminalID}
	if !validPane(pane) {
		return fmt.Errorf("%w: invalid pane specification", ErrInvalidArgument)
	}
	if spec.TerminalID != nil {
		for _, existing := range s.panes {
			if existing.TerminalID != nil && *existing.TerminalID == *spec.TerminalID {
				return fmt.Errorf("%w: terminal %d is already displayed", ErrAlreadyExists, *spec.TerminalID)
			}
		}
	}
	return nil
}

func (s *state) allocateWorkspaceID() (WorkspaceID, error) {
	if s.nextWorkspaceID == 0 || uint64(s.nextWorkspaceID) == math.MaxUint64 {
		return 0, fmt.Errorf("%w: workspace ID exhausted", ErrInvalidState)
	}
	id := s.nextWorkspaceID
	s.nextWorkspaceID++
	return id, nil
}

func (s *state) allocateWindowID() (WindowID, error) {
	if s.nextWindowID == 0 || uint64(s.nextWindowID) == math.MaxUint64 {
		return 0, fmt.Errorf("%w: window ID exhausted", ErrInvalidState)
	}
	id := s.nextWindowID
	s.nextWindowID++
	return id, nil
}

func (s *state) allocatePaneID() (PaneID, error) {
	if s.nextPaneID == 0 || uint64(s.nextPaneID) == math.MaxUint64 {
		return 0, fmt.Errorf("%w: pane ID exhausted", ErrInvalidState)
	}
	id := s.nextPaneID
	s.nextPaneID++
	return id, nil
}

func removePaneID(ids []PaneID, removed PaneID) []PaneID {
	result := make([]PaneID, 0, len(ids)-1)
	for _, id := range ids {
		if id != removed {
			result = append(result, id)
		}
	}
	return result
}
