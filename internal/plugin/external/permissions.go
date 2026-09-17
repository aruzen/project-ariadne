package external

import (
	"encoding/json"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/aruzen/ariadne/internal/core"
)

func (s *session) hasCapability(cap v1.Capability) bool {
	requested := false
	for _, c := range s.manifest.Capabilities {
		if c == cap {
			requested = true
			break
		}
	}
	if !requested {
		return false
	}
	for _, g := range s.grants {
		if g.Capability == cap {
			return true
		}
	}
	return false
}
func contains(ids []uint64, id uint64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
func workspaceOf(snapshot core.Snapshot, r v1.Resource) uint64 {
	if r.Kind == "workspace" {
		for _, w := range snapshot.Workspaces {
			if uint64(w.ID) == r.ID {
				return r.ID
			}
		}
		return 0
	}
	window := r.ID
	if r.Kind == "pane" {
		window = 0
		for _, p := range snapshot.Panes {
			if uint64(p.ID) == r.ID {
				window = uint64(p.WindowID)
			}
		}
		for _, p := range snapshot.StashedPanes {
			if uint64(p.PaneID) == r.ID {
				return 0
			}
		}
	}
	for _, w := range snapshot.StashedWindows {
		if uint64(w.WindowID) == window {
			return 0
		}
	}
	for _, w := range snapshot.Windows {
		if uint64(w.ID) == window {
			return uint64(w.WorkspaceID)
		}
	}
	return 0
}
func resourceExists(snapshot core.Snapshot, r v1.Resource) bool {
	switch r.Kind {
	case "pane":
		for _, p := range snapshot.Panes {
			if uint64(p.ID) == r.ID && !p.Transient {
				return true
			}
		}
	case "window":
		for _, w := range snapshot.Windows {
			if uint64(w.ID) == r.ID {
				return true
			}
		}
	case "workspace":
		for _, w := range snapshot.Workspaces {
			if uint64(w.ID) == r.ID {
				return true
			}
		}
	}
	return false
}
func (s *session) allowed(snapshot core.Snapshot, cap v1.Capability, r v1.Resource, c v1.Context) bool {
	if !s.hasCapability(cap) || !resourceExists(snapshot, r) {
		return false
	}
	for _, g := range s.grants {
		if g.Capability != cap {
			continue
		}
		switch g.Scope.Kind {
		case "all":
			return true
		case "pane":
			if r.Kind == "pane" && contains(g.Scope.IDs, r.ID) {
				return true
			}
		case "workspace":
			if id := workspaceOf(snapshot, r); id != 0 && contains(g.Scope.IDs, id) {
				return true
			}
		case "context":
			if c.Token == "" {
				continue
			}
			switch r.Kind {
			case "pane":
				if r.ID == c.PaneID {
					return true
				}
			case "window":
				if r.ID == c.WindowID {
					return true
				}
			case "workspace":
				if r.ID == c.WorkspaceID {
					return true
				}
			}
		}
	}
	return false
}
func asJSON(v any) json.RawMessage { data, _ := json.Marshal(v); return data }
func (s *session) filterSnapshot(snapshot core.Snapshot, cap v1.Capability, c v1.Context) v1.Snapshot {
	result := v1.Snapshot{Revision: snapshot.Revision, Workspaces: []json.RawMessage{}, Windows: []json.RawMessage{}, Panes: []json.RawMessage{}, Labels: []json.RawMessage{}, Attentions: []json.RawMessage{}, ToolInstances: []json.RawMessage{}}
	paneIDs := map[core.PaneID]bool{}
	windowIDs := map[core.WindowID]bool{}
	workspaceIDs := map[core.WorkspaceID]bool{}
	for _, p := range snapshot.Panes {
		if s.allowed(snapshot, cap, v1.Resource{Kind: "pane", ID: uint64(p.ID)}, c) {
			paneIDs[p.ID] = true
			projection := PublicPane(p)
			for _, hidden := range snapshot.StashedPanes {
				if hidden.PaneID == p.ID {
					projection.Stashed = true
					projection.WindowID = 0
				}
			}
			result.Panes = append(result.Panes, asJSON(projection))
		}
	}
	for _, w := range snapshot.Windows {
		if s.allowed(snapshot, cap, v1.Resource{Kind: "window", ID: uint64(w.ID)}, c) {
			windowIDs[w.ID] = true
			w.Layout = filterLayout(w.Layout, paneIDs)
			projection := v1.Window{ID: uint64(w.ID), WorkspaceID: uint64(w.WorkspaceID), Name: w.Name, Layout: publicLayout(w.Layout)}
			for _, hidden := range snapshot.StashedWindows {
				if hidden.WindowID == w.ID {
					projection.Stashed = true
					projection.WorkspaceID = 0
				}
			}
			result.Windows = append(result.Windows, asJSON(projection))
		}
	}
	for _, w := range snapshot.Workspaces {
		if s.allowed(snapshot, cap, v1.Resource{Kind: "workspace", ID: uint64(w.ID)}, c) {
			workspaceIDs[w.ID] = true
			ids := make([]core.WindowID, 0)
			for _, id := range w.WindowIDs {
				if windowIDs[id] {
					ids = append(ids, id)
				}
			}
			w.WindowIDs = ids
			projection := v1.Workspace{ID: uint64(w.ID), Name: w.Name, WindowIDs: []uint64{}}
			for _, id := range w.WindowIDs {
				projection.WindowIDs = append(projection.WindowIDs, uint64(id))
			}
			result.Workspaces = append(result.Workspaces, asJSON(projection))
		}
	}
	for _, l := range snapshot.Labels {
		ok := false
		switch l.TargetKind {
		case core.LabelPane:
			ok = paneIDs[core.PaneID(l.TargetID)]
		case core.LabelWindow:
			ok = windowIDs[core.WindowID(l.TargetID)]
		case core.LabelWorkspace:
			ok = workspaceIDs[core.WorkspaceID(l.TargetID)]
		}
		if ok {
			result.Labels = append(result.Labels, asJSON(v1.Label{TargetKind: string(l.TargetKind), TargetID: l.TargetID, Source: l.Source, Name: l.Name, Value: l.Value}))
		}
	}
	for _, a := range snapshot.Attentions {
		if paneIDs[a.PaneID] {
			result.Attentions = append(result.Attentions, asJSON(v1.Attention{ID: a.ID, PaneID: uint64(a.PaneID), Source: a.Source, Key: a.Key, Class: string(a.Class), Severity: string(a.Severity), Message: a.Message, OccurredAt: a.OccurredAt, UpdatedAt: a.UpdatedAt, AcknowledgedAt: a.AcknowledgedAt}))
		}
	}
	for _, t := range snapshot.ToolInstances {
		if t.Descriptor.Provider != s.id {
			continue
		}
		all := true
		found := false
		for _, p := range snapshot.Panes {
			if p.Tool != nil && *p.Tool == t.Descriptor {
				found = true
				if !paneIDs[p.ID] {
					all = false
				}
			}
		}
		if all && found {
			result.ToolInstances = append(result.ToolInstances, asJSON(publicTool(t)))
		}
	}
	return result
}
func filterLayout(n *core.LayoutNode, allowed map[core.PaneID]bool) *core.LayoutNode {
	if n == nil {
		return nil
	}
	if n.Kind == core.LayoutPane {
		if !allowed[n.PaneID] {
			return nil
		}
		copy := *n
		return &copy
	}
	copy := *n
	copy.Children = nil
	copy.Weights = nil
	for i, child := range n.Children {
		if p := filterLayout(&child, allowed); p != nil {
			copy.Children = append(copy.Children, *p)
			copy.Weights = append(copy.Weights, n.Weights[i])
		}
	}
	if len(copy.Children) == 0 {
		return nil
	}
	if len(copy.Children) == 1 {
		return &copy.Children[0]
	}
	return &copy
}

func (s *session) allCapability(cap v1.Capability) bool {
	if !s.hasCapability(cap) {
		return false
	}
	for _, g := range s.grants {
		if g.Capability == cap && g.Scope.Kind == "all" {
			return true
		}
	}
	return false
}
