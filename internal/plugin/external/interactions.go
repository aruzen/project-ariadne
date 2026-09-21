package external

import (
	"context"
	"encoding/json"
	"errors"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
)

func (m *Manager) beginInteraction(s *session, c v1.Context, view string, parent context.Context) (string, context.Context, error) {
	id := randomID()
	ctx, cancel := context.WithTimeout(parent, milliseconds(m.config.InteractionMS))
	m.mu.Lock()
	if !s.active.Load() || m.sessions[s.id] != s {
		m.mu.Unlock()
		cancel()
		return "", nil, ErrUnavailable
	}
	if len(m.interactions) >= m.config.MaxInteractions {
		m.mu.Unlock()
		cancel()
		return "", nil, ErrOverflow
	}
	m.interactions[id] = interactionSession{plugin: s.id, id: id, generation: s.generation, frontend: c.FrontendID, view: view, cancel: cancel}
	m.mu.Unlock()
	return id, ctx, nil
}

func (m *Manager) finishInteraction(s *session, id string) bool {
	m.mu.Lock()
	record, current := m.interactions[id]
	if current && record.plugin == s.id && record.generation == s.generation {
		delete(m.interactions, id)
		record.cancel()
	} else {
		current = false
	}
	m.mu.Unlock()
	return current
}

func (m *Manager) startInteraction(s *session, c v1.Context, view string, params json.RawMessage) (any, error) {
	id, ctx, err := m.beginInteraction(s, c, view, s.apiCtx)
	if err != nil {
		return v1.InteractionStarted{}, err
	}
	start := func() {
		go func() {
			value, err := s.io(ctx, "frontend.interact", params, c)
			current := m.finishInteraction(s, id)
			if !current || !s.active.Load() {
				return
			}
			completed := v1.InteractionCompleted{ID: id}
			if err != nil {
				completed.Error = err.Error()
			} else if result, ok := value.(v1.InteractionResult); ok {
				completed.Result = &result
			} else {
				completed.Error = ErrProtocol.Error()
			}
			if notifyErr := s.peer.Notify("interaction.result", completed); notifyErr != nil && !errors.Is(notifyErr, context.Canceled) {
				s.peer.Fail(notifyErr)
			}
		}()
	}
	abort := func() { m.finishInteraction(s, id) }
	return deferredInteractionStart{InteractionStarted: v1.InteractionStarted{ID: id}, start: start, abort: abort}, nil
}

type deferredInteractionStart struct {
	v1.InteractionStarted
	start func()
	abort func()
}

func (start deferredInteractionStart) rpcResult() any { return start.InteractionStarted }
func (start deferredInteractionStart) rpcAfterResponse(err error) {
	if err != nil {
		start.abort()
		return
	}
	start.start()
}

func (m *Manager) cancelInteractionsLocked(match func(interactionSession) bool) {
	for id, interaction := range m.interactions {
		if match(interaction) {
			interaction.cancel()
			delete(m.interactions, id)
		}
	}
}
