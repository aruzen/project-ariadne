package v1

import (
	"encoding/json"
	"time"
)

// Resource projections have a fixed v1 schema. They are not Core snapshots and
// are not suitable for round-tripping into Ariadne's persistent state.
type Workspace struct {
	ID        uint64   `json:"id"`
	Name      string   `json:"name"`
	WindowIDs []uint64 `json:"window_ids"`
}
type Window struct {
	ID          uint64  `json:"id"`
	WorkspaceID uint64  `json:"workspace_id"`
	Name        string  `json:"name"`
	Layout      *Layout `json:"layout,omitempty"`
	Stashed     bool    `json:"stashed,omitempty"`
}
type Layout struct {
	Kind      string   `json:"kind"`
	SplitID   uint64   `json:"split_id,omitempty"`
	PaneID    uint64   `json:"pane_id,omitempty"`
	Direction string   `json:"direction,omitempty"`
	Children  []Layout `json:"children,omitempty"`
	Weights   []uint32 `json:"weights,omitempty"`
}
type Pane struct {
	ID           uint64          `json:"id"`
	WindowID     uint64          `json:"window_id"`
	Kind         string          `json:"kind"`
	Title        string          `json:"title,omitempty"`
	Presentation Presentation    `json:"presentation,omitempty"`
	Terminal     *Terminal       `json:"terminal,omitempty"`
	Tool         *ToolDescriptor `json:"tool,omitempty"`
	Stashed      bool            `json:"stashed,omitempty"`
}
type Presentation struct {
	Chrome string `json:"chrome,omitempty"`
}
type Launch struct {
	Argv []string `json:"argv"`
	CWD  string   `json:"cwd"`
}
type Exit struct {
	Kind    string `json:"kind"`
	Code    int    `json:"code,omitempty"`
	Signal  string `json:"signal,omitempty"`
	Message string `json:"message,omitempty"`
}
type Terminal struct {
	ID               *uint64 `json:"id,omitempty"`
	State            string  `json:"state"`
	Launch           Launch  `json:"launch"`
	Exit             *Exit   `json:"exit,omitempty"`
	HistoryAvailable bool    `json:"history_available"`
}
type ToolDescriptor struct {
	Provider string `json:"provider"`
	Type     string `json:"type"`
	Instance string `json:"instance"`
}
type ToolInstance struct {
	Descriptor   ToolDescriptor  `json:"descriptor"`
	StateVersion uint32          `json:"state_version"`
	Generation   uint64          `json:"generation"`
	State        json.RawMessage `json:"state"`
}
type Label struct {
	TargetKind string `json:"target_kind"`
	TargetID   uint64 `json:"target_id"`
	Source     string `json:"source"`
	Name       string `json:"name"`
	Value      string `json:"value"`
}
type Attention struct {
	ID             uint64     `json:"id"`
	PaneID         uint64     `json:"pane_id"`
	Source         string     `json:"source"`
	Key            string     `json:"key"`
	Class          string     `json:"class"`
	Severity       string     `json:"severity"`
	Message        string     `json:"message,omitempty"`
	OccurredAt     time.Time  `json:"occurred_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	AcknowledgedAt *time.Time `json:"acknowledged_at,omitempty"`
}
