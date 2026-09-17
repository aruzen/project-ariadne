package external

import (
	"context"
	"errors"
	"fmt"
	"unicode"
	"unicode/utf8"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/rivo/uniseg"
)

func validFrame(f v1.Frame, v v1.View) error {
	if f.ViewID != v.ID || f.Generation != v.Generation {
		return ErrUnavailable
	}
	if f.Width != v.Width || f.Height != v.Height || len(f.Cells) != v.Width*v.Height {
		return ErrProtocol
	}
	for i, c := range f.Cells {
		if c.Width == 0 {
			if i%f.Width == 0 || f.Cells[i-1].Width != 2 || c.Text != "" {
				return ErrProtocol
			}
			continue
		}
		if c.Style.UnderlineStyle > 5 {
			return ErrProtocol
		}
		if c.Width > 2 || !utf8.ValidString(c.Text) || len(c.Text) > 256 {
			return ErrProtocol
		}
		for _, r := range c.Text {
			if unicode.IsControl(r) {
				return ErrProtocol
			}
		}
		graphemes := uniseg.NewGraphemes(c.Text)
		if !graphemes.Next() || graphemes.Width() != int(c.Width) || graphemes.Next() {
			return ErrProtocol
		}
		if c.Width == 2 && (i%f.Width == f.Width-1 || i+1 >= len(f.Cells) || f.Cells[i+1].Width != 0) {
			return ErrProtocol
		}
	}
	if f.Cursor.Shape > 6 || f.Cursor.Visible && (f.Cursor.X < 0 || f.Cursor.Y < 0 || f.Cursor.X >= f.Width || f.Cursor.Y >= f.Height) {
		return ErrProtocol
	}
	return nil
}
func (m *Manager) view(ctx context.Context, frontend uint64, request v1.ManageRequest) (v1.ManageResult, error) {
	var v v1.View
	if request.View != nil {
		v = *request.View
	}
	if request.Input != nil {
		v = request.Input.View
	}
	if v.ID == "" || len(v.ID) > 128 || v.Generation == 0 {
		return v1.ManageResult{}, errors.New("plugin: invalid view identity")
	}
	key := fmt.Sprintf("%d/%s", frontend, v.ID)
	if request.Action == "view.close" {
		m.mu.Lock()
		record, ok := m.views[key]
		if ok && record.frontend == frontend && record.plugin == request.ID && !record.closed && v.Generation >= record.generation {
			record.closed = true
			m.views[key] = record
		}
		m.mu.Unlock()
		if ok && record.frontend == frontend && record.plugin == request.ID && record.closed && v.Generation >= record.generation {
			if s, err := m.get(request.ID); err == nil {
				_ = s.peer.Notify("view.close", map[string]any{"view_id": record.hostID, "generation": v.Generation})
			}
		}
		return v1.ManageResult{}, nil
	}
	if v.Width < 1 || v.Height < 1 || v.Width > 4096 || v.Height > 4096 || v.Width*v.Height > 65536 {
		return v1.ManageResult{}, errors.New("plugin: view dimensions exceed limits")
	}
	s, err := m.get(request.ID)
	if err != nil {
		return v1.ManageResult{}, err
	}
	if request.Action == "input" && (v.RuntimeGeneration == 0 || v.RuntimeGeneration != s.generation) {
		return v1.ManageResult{}, ErrUnavailable
	}
	v.RuntimeGeneration = s.generation
	snapshot, err := m.engine.Snapshot(ctx)
	if err != nil {
		return v1.ManageResult{}, err
	}
	found := false
	for _, p := range snapshot.Panes {
		if uint64(p.ID) == v.PaneID && p.Tool != nil && p.Tool.Provider == s.id && declares(s.manifest.Tools, p.Tool.Type) {
			found = true
			v.Type = p.Tool.Type
			v.Instance = p.Tool.Instance
			for _, state := range snapshot.ToolInstances {
				if state.Descriptor == *p.Tool {
					v.State = state.State
					v.StateGeneration = state.Generation
					v.StateVersion = state.StateVersion
				}
			}
		}
	}
	if !found {
		return v1.ManageResult{}, ErrPermission
	}
	m.mu.Lock()
	old, ok := m.views[key]
	if ok && (old.closed || old.plugin != s.id || old.pane != v.PaneID || v.Generation < old.generation || (old.generation == v.Generation && (old.width != v.Width || old.height != v.Height))) {
		m.mu.Unlock()
		return v1.ManageResult{}, ErrUnavailable
	}
	if len(m.views) >= 4096 && !ok {
		m.mu.Unlock()
		return v1.ManageResult{}, ErrOverflow
	}
	hostID := old.hostID
	if !ok {
		hostID = randomID()
	}
	m.views[key] = viewRecord{hostID, false, frontend, v.PaneID, v.Generation, s.id, v.Width, v.Height}
	v.ID = hostID
	m.mu.Unlock()
	c, err := m.capture(ctx, s, frontend, v.PaneID)
	if err != nil {
		return v1.ManageResult{}, err
	}
	defer m.releaseContext(c.Token)
	v.Context = c
	if request.Action == "input" {
		if request.Input == nil {
			return v1.ManageResult{}, ErrProtocol
		}
		input := *request.Input
		input.View = v
		err = s.call(ctx, "view.input", input, nil, m.config.APIMS)
		return v1.ManageResult{}, err
	}
	var frame v1.Frame
	if err := s.call(ctx, "view.render", v, &frame, m.config.RenderMS); err != nil {
		return v1.ManageResult{}, err
	}
	m.mu.Lock()
	current, ok := m.views[key]
	m.mu.Unlock()
	if !ok || current.closed || current.generation != v.Generation || current.plugin != s.id || !s.active.Load() {
		return v1.ManageResult{}, ErrUnavailable
	}
	if err := validFrame(frame, v); err != nil {
		if !errors.Is(err, ErrUnavailable) {
			s.peer.Fail(err)
		}
		return v1.ManageResult{}, err
	}
	frame.RuntimeGeneration = s.generation
	return v1.ManageResult{Frame: &frame}, nil
}
