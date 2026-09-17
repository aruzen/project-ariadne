package core

func (c *Core) createTransientPane(command CreateTransientPaneCommand) (any, *Event, error) {
	window, ok := c.state.windows[command.WindowID]
	if !ok {
		return nil, nil, ErrNotFound
	}
	if _, stashed := c.state.stashedWindows[window.ID]; stashed {
		return nil, nil, ErrInvalidState
	}
	spec := PaneSpec{Kind: PaneTerminal, Title: "Plugin editor", Terminal: &TerminalInstance{State: TerminalStarting, Launch: cloneLaunch(command.Launch)}}
	if err := c.state.validatePaneSpec(spec); err != nil {
		return nil, nil, err
	}
	id, err := c.state.allocatePaneID()
	if err != nil {
		return nil, nil, err
	}
	pane := paneFromSpec(id, window.ID, spec)
	pane.Transient = true
	c.state.panes[id] = pane
	c.state.paneOrder = append(c.state.paneOrder, id)
	stash := StashedPane{PaneID: id, OriginWorkspaceID: window.WorkspaceID, OriginWindowID: window.ID}
	c.state.stashedPanes[id] = stash
	result := CreatePaneResult{Pane: clonePane(pane), Window: cloneWindow(window)}

	return result, &Event{Kind: EventPaneStashed, Payload: PaneStashEvent{Pane: clonePane(pane), Window: cloneWindow(window), Stashed: stash}}, nil
}
