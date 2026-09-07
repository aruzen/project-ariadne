package core

import (
	"fmt"

	"github.com/aruzen/streammux"
)

type WorkspaceID uint64
type WindowID uint64
type PaneID uint64
type FrontendID uint64

// TerminalID is intentionally an alias. The PTY manager's StreamID is the
// runtime identity; Core does not maintain a second identifier for it.
type TerminalID = streammux.StreamID

type PaneKind string

const (
	PaneTerminal PaneKind = "terminal"
	PaneFixed    PaneKind = "fixed"
)

type SplitDirection string

const (
	SplitHorizontal SplitDirection = "horizontal"
	SplitVertical   SplitDirection = "vertical"
)

type LayoutNodeKind string

const (
	LayoutPane  LayoutNodeKind = "pane"
	LayoutSplit LayoutNodeKind = "split"
)

// LayoutNode is either a Pane leaf or an N-way Split. Split weights are
// positive integer ratios and have the same length as Children.
type LayoutNode struct {
	Kind      LayoutNodeKind `json:"kind"`
	PaneID    PaneID         `json:"pane_id,omitempty"`
	Direction SplitDirection `json:"direction,omitempty"`
	Children  []LayoutNode   `json:"children,omitempty"`
	Weights   []uint32       `json:"weights,omitempty"`
}

type Workspace struct {
	ID        WorkspaceID `json:"id"`
	Name      string      `json:"name"`
	WindowIDs []WindowID  `json:"window_ids"`
}

type Window struct {
	ID          WindowID    `json:"id"`
	WorkspaceID WorkspaceID `json:"workspace_id"`
	Name        string      `json:"name"`
	Layout      *LayoutNode `json:"layout,omitempty"`
}

type Pane struct {
	ID         PaneID      `json:"id"`
	WindowID   WindowID    `json:"window_id"`
	Kind       PaneKind    `json:"kind"`
	Title      string      `json:"title,omitempty"`
	TerminalID *TerminalID `json:"terminal_id,omitempty"`
}

// Snapshot is an immutable point-in-time copy of persistent Core state.
// Callers may mutate their copy without affecting Core.
type Snapshot struct {
	Revision        uint64      `json:"revision"`
	NextWorkspaceID WorkspaceID `json:"next_workspace_id"`
	NextWindowID    WindowID    `json:"next_window_id"`
	NextPaneID      PaneID      `json:"next_pane_id"`
	Workspaces      []Workspace `json:"workspaces"`
	Windows         []Window    `json:"windows"`
	Panes           []Pane      `json:"panes"`
}

type FrontendState struct {
	ID          FrontendID  `json:"id"`
	WorkspaceID WorkspaceID `json:"workspace_id"`
	WindowID    WindowID    `json:"window_id"`
	PaneID      PaneID      `json:"pane_id,omitempty"`
}

type state struct {
	revision        uint64
	nextWorkspaceID WorkspaceID
	nextWindowID    WindowID
	nextPaneID      PaneID
	workspaces      map[WorkspaceID]Workspace
	windows         map[WindowID]Window
	panes           map[PaneID]Pane
	workspaceOrder  []WorkspaceID
	windowOrder     []WindowID
	paneOrder       []PaneID
}

func defaultState() *state {
	workspace := Workspace{ID: 1, Name: "default", WindowIDs: []WindowID{1}}
	window := Window{ID: 1, WorkspaceID: workspace.ID, Name: "main"}
	return &state{
		nextWorkspaceID: 2,
		nextWindowID:    2,
		nextPaneID:      1,
		workspaces:      map[WorkspaceID]Workspace{workspace.ID: workspace},
		windows:         map[WindowID]Window{window.ID: window},
		panes:           make(map[PaneID]Pane),
		workspaceOrder:  []WorkspaceID{workspace.ID},
		windowOrder:     []WindowID{window.ID},
	}
}

func stateFromSnapshot(snapshot Snapshot) (*state, error) {
	s := &state{
		revision:        snapshot.Revision,
		nextWorkspaceID: snapshot.NextWorkspaceID,
		nextWindowID:    snapshot.NextWindowID,
		nextPaneID:      snapshot.NextPaneID,
		workspaces:      make(map[WorkspaceID]Workspace, len(snapshot.Workspaces)),
		windows:         make(map[WindowID]Window, len(snapshot.Windows)),
		panes:           make(map[PaneID]Pane, len(snapshot.Panes)),
	}
	for _, workspace := range snapshot.Workspaces {
		workspace = cloneWorkspace(workspace)
		if workspace.ID == 0 || workspace.Name == "" {
			return nil, fmt.Errorf("%w: invalid workspace", ErrInvalidState)
		}
		if _, exists := s.workspaces[workspace.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate workspace ID %d", ErrInvalidState, workspace.ID)
		}
		for _, other := range s.workspaces {
			if other.Name == workspace.Name {
				return nil, fmt.Errorf("%w: duplicate workspace name %q", ErrInvalidState, workspace.Name)
			}
		}
		s.workspaces[workspace.ID] = workspace
		s.workspaceOrder = append(s.workspaceOrder, workspace.ID)
	}
	for _, window := range snapshot.Windows {
		window = cloneWindow(window)
		if window.ID == 0 || window.Name == "" {
			return nil, fmt.Errorf("%w: invalid window", ErrInvalidState)
		}
		if _, exists := s.windows[window.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate window ID %d", ErrInvalidState, window.ID)
		}
		if _, exists := s.workspaces[window.WorkspaceID]; !exists {
			return nil, fmt.Errorf("%w: window %d references workspace %d", ErrInvalidState, window.ID, window.WorkspaceID)
		}
		for _, other := range s.windows {
			if other.WorkspaceID == window.WorkspaceID && other.Name == window.Name {
				return nil, fmt.Errorf("%w: duplicate window name %q", ErrInvalidState, window.Name)
			}
		}
		s.windows[window.ID] = window
		s.windowOrder = append(s.windowOrder, window.ID)
	}
	terminalIDs := make(map[TerminalID]struct{})
	for _, pane := range snapshot.Panes {
		pane = clonePane(pane)
		if pane.ID == 0 || !validPane(pane) {
			return nil, fmt.Errorf("%w: invalid pane", ErrInvalidState)
		}
		if _, exists := s.panes[pane.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate pane ID %d", ErrInvalidState, pane.ID)
		}
		if _, exists := s.windows[pane.WindowID]; !exists {
			return nil, fmt.Errorf("%w: pane %d references window %d", ErrInvalidState, pane.ID, pane.WindowID)
		}
		if pane.TerminalID != nil {
			if _, exists := terminalIDs[*pane.TerminalID]; exists {
				return nil, fmt.Errorf("%w: duplicate terminal ID %d", ErrInvalidState, *pane.TerminalID)
			}
			terminalIDs[*pane.TerminalID] = struct{}{}
		}
		s.panes[pane.ID] = pane
		s.paneOrder = append(s.paneOrder, pane.ID)
	}
	if err := s.validateHierarchy(); err != nil {
		return nil, err
	}
	if s.nextWorkspaceID == 0 || s.nextWindowID == 0 || s.nextPaneID == 0 ||
		s.nextWorkspaceID <= WorkspaceID(maxKey(s.workspaces)) ||
		s.nextWindowID <= WindowID(maxKey(s.windows)) ||
		s.nextPaneID <= PaneID(maxKey(s.panes)) {
		return nil, fmt.Errorf("%w: next ID does not exceed allocated IDs", ErrInvalidState)
	}
	return s, nil
}

func maxKey[K ~uint64, V any](values map[K]V) uint64 {
	var maximum uint64
	for key := range values {
		if uint64(key) > maximum {
			maximum = uint64(key)
		}
	}
	return maximum
}

func (s *state) validateHierarchy() error {
	seenWindows := make(map[WindowID]struct{}, len(s.windows))
	for _, workspaceID := range s.workspaceOrder {
		workspace := s.workspaces[workspaceID]
		for _, windowID := range workspace.WindowIDs {
			window, exists := s.windows[windowID]
			if !exists || window.WorkspaceID != workspace.ID {
				return fmt.Errorf("%w: invalid workspace/window relationship", ErrInvalidState)
			}
			if _, exists := seenWindows[windowID]; exists {
				return fmt.Errorf("%w: window %d referenced more than once", ErrInvalidState, windowID)
			}
			seenWindows[windowID] = struct{}{}
		}
	}
	if len(seenWindows) != len(s.windows) {
		return fmt.Errorf("%w: unreferenced window", ErrInvalidState)
	}

	seenPanes := make(map[PaneID]struct{}, len(s.panes))
	for _, windowID := range s.windowOrder {
		window := s.windows[windowID]
		if window.Layout == nil {
			continue
		}
		if err := validateLayout(*window.Layout, func(paneID PaneID) error {
			pane, exists := s.panes[paneID]
			if !exists || pane.WindowID != window.ID {
				return fmt.Errorf("%w: invalid window/pane relationship", ErrInvalidState)
			}
			if _, exists := seenPanes[paneID]; exists {
				return fmt.Errorf("%w: pane %d referenced more than once", ErrInvalidState, paneID)
			}
			seenPanes[paneID] = struct{}{}
			return nil
		}); err != nil {
			return err
		}
	}
	if len(seenPanes) != len(s.panes) {
		return fmt.Errorf("%w: unreferenced pane", ErrInvalidState)
	}
	return nil
}

func validateLayout(node LayoutNode, visit func(PaneID) error) error {
	switch node.Kind {
	case LayoutPane:
		if node.PaneID == 0 || node.Direction != "" || len(node.Children) != 0 || len(node.Weights) != 0 {
			return fmt.Errorf("%w: invalid pane layout node", ErrInvalidState)
		}
		return visit(node.PaneID)
	case LayoutSplit:
		if !validDirection(node.Direction) || node.PaneID != 0 || len(node.Children) < 2 || len(node.Children) != len(node.Weights) {
			return fmt.Errorf("%w: invalid split layout node", ErrInvalidState)
		}
		for index, child := range node.Children {
			if node.Weights[index] == 0 {
				return fmt.Errorf("%w: zero split weight", ErrInvalidState)
			}
			if err := validateLayout(child, visit); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("%w: unknown layout node kind %q", ErrInvalidState, node.Kind)
	}
}

func (s *state) snapshot() Snapshot {
	snapshot := Snapshot{
		Revision:        s.revision,
		NextWorkspaceID: s.nextWorkspaceID,
		NextWindowID:    s.nextWindowID,
		NextPaneID:      s.nextPaneID,
		Workspaces:      make([]Workspace, 0, len(s.workspaces)),
		Windows:         make([]Window, 0, len(s.windows)),
		Panes:           make([]Pane, 0, len(s.panes)),
	}
	for _, id := range s.workspaceOrder {
		snapshot.Workspaces = append(snapshot.Workspaces, cloneWorkspace(s.workspaces[id]))
	}
	for _, id := range s.windowOrder {
		snapshot.Windows = append(snapshot.Windows, cloneWindow(s.windows[id]))
	}
	for _, id := range s.paneOrder {
		snapshot.Panes = append(snapshot.Panes, clonePane(s.panes[id]))
	}
	return snapshot
}

func cloneWorkspace(workspace Workspace) Workspace {
	workspace.WindowIDs = append([]WindowID(nil), workspace.WindowIDs...)
	return workspace
}

func cloneWindow(window Window) Window {
	if window.Layout != nil {
		layout := cloneLayout(*window.Layout)
		window.Layout = &layout
	}
	return window
}

func clonePane(pane Pane) Pane {
	if pane.TerminalID != nil {
		terminalID := *pane.TerminalID
		pane.TerminalID = &terminalID
	}
	return pane
}

func cloneLayout(node LayoutNode) LayoutNode {
	node.Children = append([]LayoutNode(nil), node.Children...)
	node.Weights = append([]uint32(nil), node.Weights...)
	for index := range node.Children {
		node.Children[index] = cloneLayout(node.Children[index])
	}
	return node
}

func validDirection(direction SplitDirection) bool {
	return direction == SplitHorizontal || direction == SplitVertical
}

func validPane(pane Pane) bool {
	switch pane.Kind {
	case PaneTerminal:
		return pane.TerminalID == nil || *pane.TerminalID != 0
	case PaneFixed:
		return pane.TerminalID == nil
	default:
		return false
	}
}

func paneLeaf(id PaneID) LayoutNode {
	return LayoutNode{Kind: LayoutPane, PaneID: id}
}

func insertSplit(node *LayoutNode, target PaneID, inserted PaneID, direction SplitDirection) bool {
	if node.Kind == LayoutPane {
		if node.PaneID != target {
			return false
		}
		*node = LayoutNode{
			Kind:      LayoutSplit,
			Direction: direction,
			Children:  []LayoutNode{paneLeaf(target), paneLeaf(inserted)},
			Weights:   []uint32{1, 1},
		}
		return true
	}
	for index := range node.Children {
		child := &node.Children[index]
		if child.Kind == LayoutPane && child.PaneID == target && node.Direction == direction {
			node.Children = append(node.Children, LayoutNode{})
			copy(node.Children[index+2:], node.Children[index+1:])
			node.Children[index+1] = paneLeaf(inserted)
			node.Weights = make([]uint32, len(node.Children))
			for weightIndex := range node.Weights {
				node.Weights[weightIndex] = 1
			}
			return true
		}
		if insertSplit(child, target, inserted, direction) {
			return true
		}
	}
	return false
}

func removePaneFromLayout(node *LayoutNode, paneID PaneID) (*LayoutNode, bool) {
	if node.Kind == LayoutPane {
		if node.PaneID == paneID {
			return nil, true
		}
		cloned := cloneLayout(*node)
		return &cloned, false
	}

	children := make([]LayoutNode, 0, len(node.Children))
	removed := false
	for index := range node.Children {
		child, childRemoved := removePaneFromLayout(&node.Children[index], paneID)
		if childRemoved {
			removed = true
		}
		if child != nil {
			children = append(children, *child)
		}
	}
	if !removed {
		cloned := cloneLayout(*node)
		return &cloned, false
	}
	if len(children) == 0 {
		return nil, true
	}
	if len(children) == 1 {
		child := cloneLayout(children[0])
		return &child, true
	}
	result := LayoutNode{Kind: LayoutSplit, Direction: node.Direction, Children: children, Weights: make([]uint32, len(children))}
	for index := range result.Weights {
		result.Weights[index] = 1
	}
	return &result, true
}
