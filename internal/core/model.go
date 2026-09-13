package core

import (
	"fmt"
	"sort"
	"strings"

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
	// PaneTool hosts frontend or plugin-provided content without a PTY.
	PaneTool PaneKind = "tool"
)

// PaneChrome is a frontend-neutral presentation hint. Auto lets each frontend
// and Pane renderer choose its default chrome.
type PaneChrome string

const (
	PaneChromeAuto   PaneChrome = ""
	PaneChromeBorder PaneChrome = "border"
	PaneChromeNone   PaneChrome = "none"
)

type PanePresentation struct {
	Chrome PaneChrome `json:"chrome,omitempty"`
}

type TerminalState string

const (
	TerminalStarting    TerminalState = "starting"
	TerminalRunning     TerminalState = "running"
	TerminalStopping    TerminalState = "stopping"
	TerminalExited      TerminalState = "exited"
	TerminalFailed      TerminalState = "failed"
	TerminalPlaceholder TerminalState = "placeholder"
)

type TerminalExitKind string

const (
	TerminalExitProcess  TerminalExitKind = "process"
	TerminalExitSignal   TerminalExitKind = "signal"
	TerminalExitPTYError TerminalExitKind = "pty_error"
)

// LaunchSpec is safe to persist. Environment variables are intentionally not
// represented here and remain runtime-only daemon input.
type LaunchSpec struct {
	Argv []string `json:"argv"`
	CWD  string   `json:"cwd"`
}

type TerminalExit struct {
	Kind    TerminalExitKind `json:"kind"`
	Code    int              `json:"code,omitempty"`
	Signal  string           `json:"signal,omitempty"`
	Message string           `json:"message,omitempty"`
}

// TerminalInstance is Pane-associated runtime state. ID is streammux's
// runtime-only StreamID; Launch and abnormal exit data survive persistence.
type TerminalInstance struct {
	ID               *TerminalID   `json:"id,omitempty"`
	State            TerminalState `json:"state"`
	Launch           LaunchSpec    `json:"launch"`
	Exit             *TerminalExit `json:"exit,omitempty"`
	HistoryAvailable bool          `json:"history_available"`
}

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
	ID           PaneID            `json:"id"`
	WindowID     WindowID          `json:"window_id"`
	Kind         PaneKind          `json:"kind"`
	Title        string            `json:"title,omitempty"`
	Presentation PanePresentation  `json:"presentation,omitempty"`
	Terminal     *TerminalInstance `json:"terminal,omitempty"`
}

type LabelTargetKind string

const (
	LabelWorkspace LabelTargetKind = "workspace"
	LabelWindow    LabelTargetKind = "window"
	LabelPane      LabelTargetKind = "pane"
)

// Label is keyed by Target, Source and Name. Source identifies the Plugin or
// built-in subsystem that owns the Label and is used for failure cleanup.
type Label struct {
	TargetKind LabelTargetKind `json:"target_kind"`
	TargetID   uint64          `json:"target_id"`
	Source     string          `json:"source"`
	Name       string          `json:"name"`
	Value      string          `json:"value"`
}

type labelKey struct {
	targetKind LabelTargetKind
	targetID   uint64
	source     string
	name       string
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
	Labels          []Label     `json:"labels,omitempty"`
}

// DefaultSnapshot returns the initial default/main hierarchy without starting
// an executor.
func DefaultSnapshot() Snapshot {
	return defaultState().snapshot()
}

// ValidateSnapshot verifies all ID, hierarchy, layout, and Terminal
// invariants without retaining or mutating snapshot.
func ValidateSnapshot(snapshot Snapshot) error {
	_, err := stateFromSnapshot(snapshot)
	return err
}

// PaneByTerminalID finds the Pane associated with one runtime StreamID.
func (snapshot Snapshot) PaneByTerminalID(id TerminalID) (Pane, bool) {
	if id == 0 {
		return Pane{}, false
	}
	for _, pane := range snapshot.Panes {
		if pane.Terminal != nil && pane.Terminal.ID != nil && *pane.Terminal.ID == id {
			return clonePane(pane), true
		}
	}
	return Pane{}, false
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
	labels          map[labelKey]Label
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
		labels:          make(map[labelKey]Label),
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
		labels:          make(map[labelKey]Label, len(snapshot.Labels)),
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
		if pane.Terminal != nil && pane.Terminal.ID != nil {
			if _, exists := terminalIDs[*pane.Terminal.ID]; exists {
				return nil, fmt.Errorf("%w: duplicate terminal ID %d", ErrInvalidState, *pane.Terminal.ID)
			}
			terminalIDs[*pane.Terminal.ID] = struct{}{}
		}
		s.panes[pane.ID] = pane
		s.paneOrder = append(s.paneOrder, pane.ID)
	}
	for _, label := range snapshot.Labels {
		if !validLabel(label) || !s.labelTargetExists(label.TargetKind, label.TargetID) {
			return nil, fmt.Errorf("%w: invalid label", ErrInvalidState)
		}
		key := keyForLabel(label)
		if _, exists := s.labels[key]; exists {
			return nil, fmt.Errorf("%w: duplicate label", ErrInvalidState)
		}
		s.labels[key] = label
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
		Labels:          make([]Label, 0, len(s.labels)),
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
	for _, label := range s.labels {
		snapshot.Labels = append(snapshot.Labels, label)
	}
	sort.Slice(snapshot.Labels, func(left, right int) bool {
		first, second := snapshot.Labels[left], snapshot.Labels[right]
		if first.TargetKind != second.TargetKind {
			return first.TargetKind < second.TargetKind
		}
		if first.TargetID != second.TargetID {
			return first.TargetID < second.TargetID
		}
		if first.Source != second.Source {
			return first.Source < second.Source
		}
		return first.Name < second.Name
	})
	return snapshot
}

func keyForLabel(label Label) labelKey {
	return labelKey{targetKind: label.TargetKind, targetID: label.TargetID, source: label.Source, name: label.Name}
}

func validLabel(label Label) bool {
	if label.TargetID == 0 || strings.TrimSpace(label.Source) == "" || strings.TrimSpace(label.Name) == "" ||
		len(label.Source) > 128 || len(label.Name) > 128 || len(label.Value) > 4096 {
		return false
	}
	return !strings.ContainsRune(label.Source, 0) && !strings.ContainsRune(label.Name, 0) && !strings.ContainsRune(label.Value, 0)
}

func (s *state) labelTargetExists(kind LabelTargetKind, id uint64) bool {
	switch kind {
	case LabelWorkspace:
		_, exists := s.workspaces[WorkspaceID(id)]
		return exists
	case LabelWindow:
		_, exists := s.windows[WindowID(id)]
		return exists
	case LabelPane:
		_, exists := s.panes[PaneID(id)]
		return exists
	default:
		return false
	}
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
	if pane.Terminal != nil {
		terminal := cloneTerminal(*pane.Terminal)
		pane.Terminal = &terminal
	}
	return pane
}

func cloneTerminal(terminal TerminalInstance) TerminalInstance {
	terminal.Launch.Argv = append([]string(nil), terminal.Launch.Argv...)
	if terminal.ID != nil {
		terminalID := *terminal.ID
		terminal.ID = &terminalID
	}
	if terminal.Exit != nil {
		exit := *terminal.Exit
		terminal.Exit = &exit
	}
	return terminal
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
	if !validPanePresentation(pane.Presentation) {
		return false
	}
	switch pane.Kind {
	case PaneTerminal:
		return pane.Terminal == nil || validTerminal(*pane.Terminal)
	case PaneTool:
		return pane.Terminal == nil
	default:
		return false
	}
}

func validPanePresentation(presentation PanePresentation) bool {
	switch presentation.Chrome {
	case PaneChromeAuto, PaneChromeBorder, PaneChromeNone:
		return true
	default:
		return false
	}
}

func validTerminal(terminal TerminalInstance) bool {
	if len(terminal.Launch.Argv) == 0 || terminal.Launch.Argv[0] == "" || terminal.Launch.CWD == "" ||
		strings.ContainsRune(terminal.Launch.CWD, 0) {
		return false
	}
	for _, argument := range terminal.Launch.Argv {
		if strings.ContainsRune(argument, 0) {
			return false
		}
	}
	if terminal.ID != nil && *terminal.ID == 0 {
		return false
	}
	if terminal.HistoryAvailable && terminal.ID == nil {
		return false
	}
	switch terminal.State {
	case TerminalStarting:
		return terminal.ID == nil && terminal.Exit == nil && !terminal.HistoryAvailable
	case TerminalRunning, TerminalStopping:
		return terminal.ID != nil && terminal.Exit == nil
	case TerminalPlaceholder:
		return terminal.ID == nil && terminal.Exit == nil && !terminal.HistoryAvailable
	case TerminalExited:
		return terminal.Exit != nil && validTerminalExit(*terminal.Exit) && terminal.Exit.Kind != TerminalExitPTYError
	case TerminalFailed:
		return terminal.Exit != nil && validTerminalExit(*terminal.Exit) && terminal.Exit.Kind == TerminalExitPTYError
	default:
		return false
	}
}

func validTerminalExit(exit TerminalExit) bool {
	switch exit.Kind {
	case TerminalExitProcess:
		return exit.Code >= 0 && exit.Signal == "" && exit.Message == ""
	case TerminalExitSignal:
		return exit.Code == 0 && exit.Signal != "" && exit.Message == ""
	case TerminalExitPTYError:
		return exit.Code == 0 && exit.Signal == "" && exit.Message != ""
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
