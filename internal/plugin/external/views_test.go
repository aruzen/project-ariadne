package external

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/aruzen/ariadne/internal/core"
)

func TestClosedViewsReleaseCapacityAndRejectLateRequests(t *testing.T) {
	s, engine, frontend := brokerSession(t)
	m := s.manager
	m.sessions[s.id] = s
	// Synthetic sessions have no process supervisor.
	defer delete(m.sessions, s.id)
	tool := core.ToolInstance{Descriptor: core.ToolDescriptor{Provider: s.id, Type: "demo", Instance: "churn"}, StateVersion: 1, Generation: 1, State: asJSON(map[string]any{})}
	created, err := engine.Execute(context.Background(), core.CreatePaneCommand{WindowID: 1, Pane: core.PaneSpec{Kind: core.PaneTool, Tool: &tool}})
	if err != nil {
		t.Fatal(err)
	}
	view := v1.View{Generation: 1, RuntimeGeneration: s.generation, PaneID: uint64(created.(core.CreatePaneResult).Pane.ID), Width: 4, Height: 1}
	for i := 0; i < m.config.MaxViews+1; i++ {
		view.ID = fmt.Sprintf("view-%d", i)
		manage(t, m, frontend, v1.ManageRequest{Action: "view.open", ID: s.id, View: &view})
		// Skip the process notification; registry lifetime is independent of it.
		s.active.Store(false)
		manage(t, m, frontend, v1.ManageRequest{Action: "view.close", ID: s.id, View: &view})
		s.active.Store(true)
		if len(m.views) != 0 {
			t.Fatal("closed view retained")
		}
	}
	for _, action := range []string{"render", "input"} {
		_, err := m.Manage(context.Background(), frontend, v1.ManageRequest{Action: action, ID: s.id, View: &view, Input: &v1.Input{View: view}})
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("late %s: %v", action, err)
		}
	}
	if len(m.views) != 0 {
		t.Fatal("late request recreated view")
	}
	m.config.MaxViews = 2
	for _, id := range []string{"active-1", "active-2"} {
		view.ID = id
		manage(t, m, frontend, v1.ManageRequest{Action: "view.open", ID: s.id, View: &view})
	}
	view.ID = "active-3"
	if _, err := m.Manage(context.Background(), frontend, v1.ManageRequest{Action: "view.open", ID: s.id, View: &view}); !errors.Is(err, ErrOverflow) {
		t.Fatalf("configured concurrent limit: %v", err)
	}
	view.ID = "active-1"
	s.active.Store(false)
	manage(t, m, frontend, v1.ManageRequest{Action: "view.close", ID: s.id, View: &view})
	s.active.Store(true)
	view.ID = "active-3"
	manage(t, m, frontend, v1.ManageRequest{Action: "view.open", ID: s.id, View: &view})
}

func TestFailurePublishesInactiveAndReasonUnderOneLock(t *testing.T) {
	s, _, _ := brokerSession(t)
	m := s.manager
	m.sessions[s.id] = s
	defer delete(m.sessions, s.id)
	done := make(chan struct{})
	m.mu.Lock()
	go func() {
		m.failed(s, errors.New("test failure"))
		close(done)
	}()
	// While List's lock is occupied, failure must not publish inactive alone.
	for i := 0; i < 1000; i++ {
		runtime.Gosched()
		if !s.active.Load() {
			m.mu.Unlock()
			<-done
			t.Fatal("inactive published before failure acquired status lock")
		}
	}
	m.mu.Unlock()
	<-done
	m.mu.Lock()
	inactive, reason := !s.active.Load(), m.failures[s.id]
	m.mu.Unlock()
	if !inactive || reason != "test failure" {
		t.Fatalf("inactive=%v reason=%q", inactive, reason)
	}
}
