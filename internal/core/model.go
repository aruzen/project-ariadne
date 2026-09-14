package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/aruzen/streammux"
)

type WorkspaceID uint64
type WindowID uint64
type PaneID uint64
type SplitID uint64
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

const (
	MaxToolStateBytes        = 64 << 10
	MaxAttentionMessageBytes = 4 << 10
)

// ToolDescriptor is a stable, frontend-neutral reference to one logical Tool.
// Multiple Panes may reference the same descriptor and therefore share state.
type ToolDescriptor struct {
	Provider string `json:"provider"`
	Type     string `json:"type"`
	Instance string `json:"instance"`
}

// ToolInstance owns the opaque persistent state shared by all views of a Tool.
type ToolInstance struct {
	Descriptor   ToolDescriptor  `json:"descriptor"`
	StateVersion uint32          `json:"state_version"`
	Generation   uint64          `json:"generation"`
	State        json.RawMessage `json:"state"`
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
	SplitID   SplitID        `json:"split_id,omitempty"`
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
	Tool         *ToolDescriptor   `json:"tool,omitempty"`
}

type AttentionClass string

const (
	AttentionWaiting   AttentionClass = "waiting"
	AttentionCompleted AttentionClass = "completed"
	AttentionWarning   AttentionClass = "warning"
	AttentionError     AttentionClass = "error"
)

type AttentionSeverity string

const (
	SeverityInfo     AttentionSeverity = "info"
	SeverityWarning  AttentionSeverity = "warning"
	SeverityError    AttentionSeverity = "error"
	SeverityCritical AttentionSeverity = "critical"
)

// Attention is runtime-only state. State files intentionally omit it.
type Attention struct {
	ID             uint64            `json:"id"`
	PaneID         PaneID            `json:"pane_id"`
	Source         string            `json:"source"`
	Key            string            `json:"key"`
	Class          AttentionClass    `json:"class"`
	Severity       AttentionSeverity `json:"severity"`
	Message        string            `json:"message,omitempty"`
	OccurredAt     time.Time         `json:"occurred_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	AcknowledgedAt *time.Time        `json:"acknowledged_at,omitempty"`
}

// StashedPane keeps a Pane alive while removing it from every Window layout.
// The remaining fields are best-effort hints for restoring its old position.
type StashedPane struct {
	PaneID            PaneID         `json:"pane_id"`
	OriginWorkspaceID WorkspaceID    `json:"origin_workspace_id"`
	OriginWindowID    WindowID       `json:"origin_window_id"`
	TargetPaneID      PaneID         `json:"target_pane_id,omitempty"`
	Direction         SplitDirection `json:"direction,omitempty"`
	Before            bool           `json:"before,omitempty"`
	Weight            uint32         `json:"weight,omitempty"`
	TargetWeight      uint32         `json:"target_weight,omitempty"`
	OriginalSplitID   SplitID        `json:"original_split_id,omitempty"`
}

// StashedWindow keeps an entire Window and its layout alive but removes it
// from normal Workspace navigation.
type StashedWindow struct {
	WindowID            WindowID    `json:"window_id"`
	OriginWorkspaceID   WorkspaceID `json:"origin_workspace_id"`
	OriginalWindowIndex int         `json:"original_window_index"`
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

// Snapshot is an immutable point-in-time copy of Core state. Statefile decides
// which fields are persistent; callers may mutate their copy without affecting Core.
type Snapshot struct {
	Revision        uint64          `json:"revision"`
	NextWorkspaceID WorkspaceID     `json:"next_workspace_id"`
	NextWindowID    WindowID        `json:"next_window_id"`
	NextPaneID      PaneID          `json:"next_pane_id"`
	NextSplitID     SplitID         `json:"next_split_id"`
	Workspaces      []Workspace     `json:"workspaces"`
	Windows         []Window        `json:"windows"`
	Panes           []Pane          `json:"panes"`
	StashedPanes    []StashedPane   `json:"stashed_panes,omitempty"`
	StashedWindows  []StashedWindow `json:"stashed_windows,omitempty"`
	Labels          []Label         `json:"labels,omitempty"`
	ToolInstances   []ToolInstance  `json:"tool_instances,omitempty"`
	Attentions      []Attention     `json:"attentions,omitempty"`
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
	nextSplitID     SplitID
	workspaces      map[WorkspaceID]Workspace
	windows         map[WindowID]Window
	panes           map[PaneID]Pane
	stashedPanes    map[PaneID]StashedPane
	stashedWindows  map[WindowID]StashedWindow
	labels          map[labelKey]Label
	toolInstances   map[toolKey]ToolInstance
	attentions      map[uint64]Attention
	attentionKeys   map[attentionKey]uint64
	nextAttentionID uint64
	workspaceOrder  []WorkspaceID
	windowOrder     []WindowID
	paneOrder       []PaneID
}

type toolKey struct{ provider, kind, instance string }
type attentionKey struct {
	source string
	paneID PaneID
	key    string
}

func defaultState() *state {
	workspace := Workspace{ID: 1, Name: "default", WindowIDs: []WindowID{1}}
	window := Window{ID: 1, WorkspaceID: workspace.ID, Name: "main"}
	return &state{
		nextWorkspaceID: 2,
		nextWindowID:    2,
		nextPaneID:      1,
		nextSplitID:     1,
		workspaces:      map[WorkspaceID]Workspace{workspace.ID: workspace},
		windows:         map[WindowID]Window{window.ID: window},
		panes:           make(map[PaneID]Pane),
		stashedPanes:    make(map[PaneID]StashedPane),
		stashedWindows:  make(map[WindowID]StashedWindow),
		labels:          make(map[labelKey]Label),
		toolInstances:   make(map[toolKey]ToolInstance),
		attentions:      make(map[uint64]Attention),
		attentionKeys:   make(map[attentionKey]uint64),
		nextAttentionID: 1,
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
		nextSplitID:     snapshot.NextSplitID,
		workspaces:      make(map[WorkspaceID]Workspace, len(snapshot.Workspaces)),
		windows:         make(map[WindowID]Window, len(snapshot.Windows)),
		panes:           make(map[PaneID]Pane, len(snapshot.Panes)),
		stashedPanes:    make(map[PaneID]StashedPane, len(snapshot.StashedPanes)),
		stashedWindows:  make(map[WindowID]StashedWindow, len(snapshot.StashedWindows)),
		labels:          make(map[labelKey]Label, len(snapshot.Labels)),
		toolInstances:   make(map[toolKey]ToolInstance, len(snapshot.ToolInstances)),
		attentions:      make(map[uint64]Attention, len(snapshot.Attentions)),
		attentionKeys:   make(map[attentionKey]uint64, len(snapshot.Attentions)),
		nextAttentionID: 1,
	}
	for _, tool := range snapshot.ToolInstances {
		tool = cloneToolInstance(tool)
		if !validToolInstance(tool) {
			return nil, fmt.Errorf("%w: invalid tool instance", ErrInvalidState)
		}
		key := keyForTool(tool.Descriptor)
		if _, exists := s.toolInstances[key]; exists {
			return nil, fmt.Errorf("%w: duplicate tool instance", ErrInvalidState)
		}
		s.toolInstances[key] = tool
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
	for _, stashed := range snapshot.StashedWindows {
		if stashed.WindowID == 0 || stashed.OriginWorkspaceID == 0 || stashed.OriginalWindowIndex < 0 {
			return nil, fmt.Errorf("%w: invalid stashed window", ErrInvalidState)
		}
		if _, exists := s.stashedWindows[stashed.WindowID]; exists {
			return nil, fmt.Errorf("%w: duplicate stashed window %d", ErrInvalidState, stashed.WindowID)
		}
		s.stashedWindows[stashed.WindowID] = stashed
	}
	for _, stashed := range snapshot.StashedPanes {
		if stashed.PaneID == 0 || stashed.TargetPaneID == stashed.PaneID || stashed.OriginWorkspaceID == 0 || stashed.OriginWindowID == 0 ||
			(stashed.TargetPaneID == 0 && (stashed.Direction != "" || stashed.Before || stashed.Weight != 0 || stashed.TargetWeight != 0 || stashed.OriginalSplitID != 0)) ||
			(stashed.TargetPaneID != 0 && (!validDirection(stashed.Direction) || stashed.Weight == 0 || stashed.TargetWeight == 0 || stashed.OriginalSplitID == 0)) {
			return nil, fmt.Errorf("%w: invalid stashed pane", ErrInvalidState)
		}
		if _, exists := s.stashedPanes[stashed.PaneID]; exists {
			return nil, fmt.Errorf("%w: duplicate stashed pane %d", ErrInvalidState, stashed.PaneID)
		}
		s.stashedPanes[stashed.PaneID] = stashed
	}
	for _, window := range snapshot.Windows {
		window = cloneWindow(window)
		if window.ID == 0 || window.Name == "" {
			return nil, fmt.Errorf("%w: invalid window", ErrInvalidState)
		}
		if _, exists := s.windows[window.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate window ID %d", ErrInvalidState, window.ID)
		}
		_, isStashed := s.stashedWindows[window.ID]
		if _, exists := s.workspaces[window.WorkspaceID]; !exists && !isStashed {
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
		_, isStashed := s.stashedPanes[pane.ID]
		if _, exists := s.windows[pane.WindowID]; !exists && !isStashed {
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
	for _, pane := range s.panes {
		if pane.Tool != nil {
			if _, exists := s.toolInstances[keyForTool(*pane.Tool)]; !exists {
				return nil, fmt.Errorf("%w: pane %d references missing tool instance", ErrInvalidState, pane.ID)
			}
		}
	}
	for paneID := range s.stashedPanes {
		if _, exists := s.panes[paneID]; !exists {
			return nil, fmt.Errorf("%w: stashed pane %d does not exist", ErrInvalidState, paneID)
		}
	}
	for windowID := range s.stashedWindows {
		if _, exists := s.windows[windowID]; !exists {
			return nil, fmt.Errorf("%w: stashed window %d does not exist", ErrInvalidState, windowID)
		}
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
	for _, attention := range snapshot.Attentions {
		attention = cloneAttention(attention)
		if !validAttention(attention) {
			return nil, fmt.Errorf("%w: invalid attention", ErrInvalidState)
		}
		if _, exists := s.panes[attention.PaneID]; !exists {
			return nil, fmt.Errorf("%w: attention references missing pane", ErrInvalidState)
		}
		key := keyForAttention(attention)
		if _, exists := s.attentionKeys[key]; exists {
			return nil, fmt.Errorf("%w: duplicate attention key", ErrInvalidState)
		}
		if _, exists := s.attentions[attention.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate attention ID", ErrInvalidState)
		}
		s.attentions[attention.ID] = attention
		s.attentionKeys[key] = attention.ID
		if attention.ID >= s.nextAttentionID {
			s.nextAttentionID = attention.ID + 1
		}
	}
	if err := s.validateHierarchy(); err != nil {
		return nil, err
	}
	if s.nextWorkspaceID == 0 || s.nextWindowID == 0 || s.nextPaneID == 0 || s.nextSplitID == 0 ||
		s.nextWorkspaceID <= WorkspaceID(maxKey(s.workspaces)) ||
		s.nextWindowID <= WindowID(maxKey(s.windows)) ||
		s.nextPaneID <= PaneID(maxKey(s.panes)) || s.nextSplitID <= maxSplitID(s.windows) || s.nextSplitID <= maxStashedSplitID(s.stashedPanes) {
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
	for windowID := range s.windows {
		_, referenced := seenWindows[windowID]
		_, stashed := s.stashedWindows[windowID]
		if referenced == stashed {
			return fmt.Errorf("%w: window %d must be either visible or stashed", ErrInvalidState, windowID)
		}
	}

	seenPanes := make(map[PaneID]struct{}, len(s.panes))
	seenSplits := make(map[SplitID]struct{})
	for _, windowID := range s.windowOrder {
		window := s.windows[windowID]
		if window.Layout == nil {
			continue
		}
		if err := validateLayout(*window.Layout, seenSplits, func(paneID PaneID) error {
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
	for paneID := range s.panes {
		_, referenced := seenPanes[paneID]
		_, stashed := s.stashedPanes[paneID]
		if referenced == stashed {
			return fmt.Errorf("%w: pane %d must be either visible or stashed", ErrInvalidState, paneID)
		}
		if stashed {
			pane := s.panes[paneID]
			if _, windowStashed := s.stashedWindows[pane.WindowID]; windowStashed {
				return fmt.Errorf("%w: pane %d and its window are both stashed", ErrInvalidState, paneID)
			}
		}
	}
	return nil
}

func validateLayout(node LayoutNode, seenSplits map[SplitID]struct{}, visit func(PaneID) error) error {
	switch node.Kind {
	case LayoutPane:
		if node.SplitID != 0 || node.PaneID == 0 || node.Direction != "" || len(node.Children) != 0 || len(node.Weights) != 0 {
			return fmt.Errorf("%w: invalid pane layout node", ErrInvalidState)
		}
		return visit(node.PaneID)
	case LayoutSplit:
		if node.SplitID == 0 || !validDirection(node.Direction) || node.PaneID != 0 || len(node.Children) < 2 || len(node.Children) != len(node.Weights) {
			return fmt.Errorf("%w: invalid split layout node", ErrInvalidState)
		}
		if _, exists := seenSplits[node.SplitID]; exists {
			return fmt.Errorf("%w: duplicate split ID %d", ErrInvalidState, node.SplitID)
		}
		seenSplits[node.SplitID] = struct{}{}
		for index, child := range node.Children {
			if node.Weights[index] == 0 {
				return fmt.Errorf("%w: zero split weight", ErrInvalidState)
			}
			if err := validateLayout(child, seenSplits, visit); err != nil {
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
		NextSplitID:     s.nextSplitID,
		Workspaces:      make([]Workspace, 0, len(s.workspaces)),
		Windows:         make([]Window, 0, len(s.windows)),
		Panes:           make([]Pane, 0, len(s.panes)),
		StashedPanes:    make([]StashedPane, 0, len(s.stashedPanes)),
		StashedWindows:  make([]StashedWindow, 0, len(s.stashedWindows)),
		Labels:          make([]Label, 0, len(s.labels)),
		ToolInstances:   make([]ToolInstance, 0, len(s.toolInstances)),
		Attentions:      make([]Attention, 0, len(s.attentions)),
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
	for _, id := range s.paneOrder {
		if stashed, exists := s.stashedPanes[id]; exists {
			snapshot.StashedPanes = append(snapshot.StashedPanes, stashed)
		}
	}
	for _, id := range s.windowOrder {
		if stashed, exists := s.stashedWindows[id]; exists {
			snapshot.StashedWindows = append(snapshot.StashedWindows, stashed)
		}
	}
	for _, label := range s.labels {
		snapshot.Labels = append(snapshot.Labels, label)
	}
	for _, tool := range s.toolInstances {
		snapshot.ToolInstances = append(snapshot.ToolInstances, cloneToolInstance(tool))
	}
	for _, attention := range s.attentions {
		snapshot.Attentions = append(snapshot.Attentions, cloneAttention(attention))
	}
	sort.Slice(snapshot.ToolInstances, func(i, j int) bool {
		left, right := snapshot.ToolInstances[i].Descriptor, snapshot.ToolInstances[j].Descriptor
		if left.Provider != right.Provider {
			return left.Provider < right.Provider
		}
		if left.Type != right.Type {
			return left.Type < right.Type
		}
		return left.Instance < right.Instance
	})
	sort.Slice(snapshot.Attentions, func(i, j int) bool { return snapshot.Attentions[i].ID < snapshot.Attentions[j].ID })
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

func maxSplitID(windows map[WindowID]Window) SplitID {
	maximum := SplitID(0)
	var visit func(LayoutNode)
	visit = func(node LayoutNode) {
		if node.SplitID > maximum {
			maximum = node.SplitID
		}
		for _, child := range node.Children {
			visit(child)
		}
	}
	for _, window := range windows {
		if window.Layout != nil {
			visit(*window.Layout)
		}
	}
	return maximum
}

func maxStashedSplitID(stashed map[PaneID]StashedPane) SplitID {
	maximum := SplitID(0)
	for _, pane := range stashed {
		if pane.OriginalSplitID > maximum {
			maximum = pane.OriginalSplitID
		}
	}
	return maximum
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
	if pane.Tool != nil {
		tool := *pane.Tool
		pane.Tool = &tool
	}
	return pane
}

func cloneToolInstance(tool ToolInstance) ToolInstance {
	tool.State = append(json.RawMessage(nil), tool.State...)
	return tool
}

func cloneAttention(attention Attention) Attention {
	if attention.AcknowledgedAt != nil {
		value := *attention.AcknowledgedAt
		attention.AcknowledgedAt = &value
	}
	return attention
}

func cloneTerminal(terminal TerminalInstance) TerminalInstance {
	terminal.Launch = cloneLaunch(terminal.Launch)
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

func cloneLaunch(launch LaunchSpec) LaunchSpec {
	launch.Argv = append([]string(nil), launch.Argv...)
	return launch
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
		return pane.Tool == nil && (pane.Terminal == nil || validTerminal(*pane.Terminal))
	case PaneTool:
		return pane.Terminal == nil && (pane.Tool == nil || validToolDescriptor(*pane.Tool))
	default:
		return false
	}
}

func keyForTool(descriptor ToolDescriptor) toolKey {
	return toolKey{provider: descriptor.Provider, kind: descriptor.Type, instance: descriptor.Instance}
}

func validToolDescriptor(descriptor ToolDescriptor) bool {
	for _, value := range []string{descriptor.Provider, descriptor.Type, descriptor.Instance} {
		if strings.TrimSpace(value) == "" || len(value) > 128 || strings.ContainsRune(value, 0) {
			return false
		}
	}
	return true
}

func validToolInstance(tool ToolInstance) bool {
	if !validToolDescriptor(tool.Descriptor) || tool.StateVersion == 0 || tool.Generation == 0 ||
		len(tool.State) > MaxToolStateBytes || !json.Valid(tool.State) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(tool.State))
	decoder.UseNumber()
	var value any
	return decoder.Decode(&value) == nil
}

func keyForAttention(attention Attention) attentionKey {
	return attentionKey{source: attention.Source, paneID: attention.PaneID, key: attention.Key}
}

func validAttention(attention Attention) bool {
	if attention.ID == 0 || attention.PaneID == 0 || strings.TrimSpace(attention.Source) == "" ||
		strings.TrimSpace(attention.Key) == "" || len(attention.Source) > 128 || len(attention.Key) > 128 ||
		len(attention.Message) > MaxAttentionMessageBytes || strings.ContainsRune(attention.Source, 0) ||
		strings.ContainsRune(attention.Key, 0) || strings.ContainsRune(attention.Message, 0) ||
		attention.OccurredAt.IsZero() || attention.UpdatedAt.IsZero() || attention.UpdatedAt.Before(attention.OccurredAt) ||
		(attention.AcknowledgedAt != nil && attention.AcknowledgedAt.Before(attention.UpdatedAt)) {
		return false
	}
	switch attention.Class {
	case AttentionWaiting, AttentionCompleted, AttentionWarning, AttentionError:
	default:
		return false
	}
	switch attention.Severity {
	case SeverityInfo, SeverityWarning, SeverityError, SeverityCritical:
		return true
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

func insertSplit(node *LayoutNode, target PaneID, inserted PaneID, direction SplitDirection, splitID SplitID) bool {
	if node.Kind == LayoutPane {
		if node.PaneID != target {
			return false
		}
		*node = LayoutNode{
			Kind:      LayoutSplit,
			SplitID:   splitID,
			Direction: direction,
			Children:  []LayoutNode{paneLeaf(target), paneLeaf(inserted)},
			Weights:   []uint32{1, 1},
		}
		return true
	}
	for index := range node.Children {
		child := &node.Children[index]
		if child.Kind == LayoutPane && child.PaneID == target && node.Direction == direction {
			weights := splitWeightsForInsertion(node.Weights, index)
			node.Children = append(node.Children, LayoutNode{})
			copy(node.Children[index+2:], node.Children[index+1:])
			node.Children[index+1] = paneLeaf(inserted)
			node.Weights = weights
			return true
		}
		if insertSplit(child, target, inserted, direction, splitID) {
			return true
		}
	}
	return false
}

func splitWeightsForInsertion(weights []uint32, index int) []uint32 {
	normalized := append([]uint32(nil), weights...)
	for {
		canDouble := true
		for _, weight := range normalized {
			if weight > math.MaxUint32/2 {
				canDouble = false
				break
			}
		}
		if canDouble {
			break
		}
		for position, weight := range normalized {
			normalized[position] = max(1, (weight+1)/2)
		}
	}
	result := make([]uint32, len(normalized)+1)
	for position, weight := range normalized {
		result[position] = weight * 2
	}
	copy(result[index+2:], result[index+1:])
	result[index], result[index+1] = normalized[index], normalized[index]
	return result
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
	weights := make([]uint32, 0, len(node.Weights))
	removed := false
	for index := range node.Children {
		child, childRemoved := removePaneFromLayout(&node.Children[index], paneID)
		if childRemoved {
			removed = true
		}
		if child != nil {
			children = append(children, *child)
			weights = append(weights, node.Weights[index])
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
	result := LayoutNode{Kind: LayoutSplit, SplitID: node.SplitID, Direction: node.Direction, Children: children, Weights: weights}
	return &result, true
}
