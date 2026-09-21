package external

import (
	"context"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/aruzen/ariadne/internal/core"
)

type observedEvent struct {
	event v1.Event
	bytes int64
}
type observationSet struct {
	panes    map[uint64]bool
	revision uint64
}

func (s *session) updateObservation(snapshot core.Snapshot) {
	set := &observationSet{panes: map[uint64]bool{}, revision: snapshot.Revision}
	for _, p := range snapshot.Panes {
		if !p.Transient && s.allowed(snapshot, v1.PTYObserve, v1.Resource{Kind: "pane", ID: uint64(p.ID)}, v1.Context{}) {
			set.panes[uint64(p.ID)] = true
		}
	}
	for _, c := range s.subscriptionContexts(v1.PTYObserve) {
		for _, p := range snapshot.Panes {
			if !p.Transient && s.allowed(snapshot, v1.PTYObserve, v1.Resource{Kind: "pane", ID: uint64(p.ID)}, c) {
				set.panes[uint64(p.ID)] = true
			}
		}
	}
	for {
		previous := s.observation.Load()
		if previous != nil && previous.revision > set.revision {
			return
		}
		if s.observation.CompareAndSwap(previous, set) {
			return
		}
	}
}
func (s *session) eventVisible(event core.Event, before, after core.Snapshot) bool {
	return s.eventVisibleContext(event, before, after, v1.Context{})
}
func (s *session) eventVisibleContext(event core.Event, before, after core.Snapshot, c v1.Context) bool {
	var resources []v1.Resource
	pane := func(id core.PaneID) { resources = append(resources, v1.Resource{Kind: "pane", ID: uint64(id)}) }
	window := func(id core.WindowID) { resources = append(resources, v1.Resource{Kind: "window", ID: uint64(id)}) }
	workspace := func(id core.WorkspaceID) {
		resources = append(resources, v1.Resource{Kind: "workspace", ID: uint64(id)})
	}
	label := func(l core.Label) {
		resources = append(resources, v1.Resource{Kind: string(l.TargetKind), ID: l.TargetID})
	}
	switch p := event.Payload.(type) {
	case core.WorkspaceCreatedEvent:
		workspace(p.Workspace.ID)
	case core.WorkspaceEvent:
		workspace(p.Workspace.ID)
	case core.WorkspaceDeletedEvent:
		workspace(p.Workspace.ID)
	case core.WindowCreatedEvent:
		window(p.Window.ID)
	case core.WindowEvent:
		window(p.Window.ID)
	case core.WindowDeletedEvent:
		window(p.Window.ID)
	case core.PaneCreatedEvent:
		pane(p.Pane.ID)
	case core.PaneMovedEvent:
		pane(p.Pane.ID)
		window(p.SourceWindow.ID)
		window(p.DestinationWindow.ID)
	case core.PaneClosedEvent:
		pane(p.Pane.ID)
	case core.PaneStashEvent:
		pane(p.Pane.ID)
	case core.WindowStashEvent:
		window(p.Window.ID)
	case core.TerminalEvent:
		pane(p.Pane.ID)
	case core.LabelEvent:
		label(p.Label)
	case core.LabelsEvent:
		for _, l := range p.Labels {
			label(l)
		}
	case core.AttentionEvent:
		pane(p.Attention.PaneID)
		for _, removed := range p.Removed {
			pane(removed.PaneID)
		}
	case core.AttentionsEvent:
		for _, a := range p.Attentions {
			pane(a.PaneID)
		}
	case core.ToolEvent:
		if p.Tool.Descriptor.Provider != s.id {
			return false
		}
		for _, target := range after.Panes {
			if target.Tool != nil && *target.Tool == p.Tool.Descriptor {
				pane(target.ID)
			}
		}
	}
	for _, r := range resources {
		if s.allowed(before, v1.CoreEvents, r, c) || s.allowed(after, v1.CoreEvents, r, c) {
			return true
		}
	}
	return false
}
func (s *session) relayEvents(initial core.Snapshot) {
	before := initial
	for {
		select {
		case event, ok := <-s.subscription.Events():
			if !ok {
				if err := s.subscription.Err(); err != nil && err != context.Canceled {
					s.peer.Fail(err)
				}
				return
			}
			after, err := core.ApplyEvent(before, event)
			if err != nil {
				s.peer.Fail(err)
				return
			}
			s.updateObservation(after)
			if s.hasCapability(v1.CoreEvents) {
				if s.eventVisible(event, before, after) {
					if err := s.queueEvent(after, string(event.Kind), v1.Context{}); err != nil {
						return
					}
				}
				for _, c := range s.subscriptionContexts(v1.CoreEvents) {
					if s.eventVisibleContext(event, before, after, c) {
						if err := s.queueEvent(after, string(event.Kind), c); err != nil {
							return
						}
					}
				}
			}
			before = after
		case <-s.ctx.Done():
			return
		}
	}
}

// RefreshObservation publishes a newly-created Pane before the PTY observer
// drains its first output. The relay cannot overwrite it with an older revision.
func (m *Manager) RefreshObservation(ctx context.Context) {
	snapshot, err := m.engine.Snapshot(ctx)
	if err != nil {
		return
	}
	m.mu.Lock()
	sessions := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.mu.Unlock()
	for _, s := range sessions {
		if s.active.Load() {
			s.updateObservation(snapshot)
		}
	}
}
