package external

import (
	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/aruzen/ariadne/internal/core"
)

func (s *session) subscribe(token string, cap v1.Capability) bool {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	if _, err := s.manager.contextFor(s, token); err != nil {
		return false
	}
	if s.subscriptions == nil {
		s.subscriptions = map[string]map[v1.Capability]bool{}
	}
	if s.subscriptions[token] == nil {
		s.subscriptions[token] = map[v1.Capability]bool{}
	}
	s.subscriptions[token][cap] = true
	return true
}
func (s *session) unsubscribe(token string) {
	s.subMu.Lock()
	delete(s.subscriptions, token)
	s.subMu.Unlock()
}
func (s *session) subscriptionContexts(cap v1.Capability) []v1.Context {
	s.subMu.Lock()
	tokens := []string{}
	for token, caps := range s.subscriptions {
		if caps[cap] {
			tokens = append(tokens, token)
		}
	}
	s.subMu.Unlock()
	var contexts []v1.Context
	for _, token := range tokens {
		if c, err := s.manager.contextFor(s, token); err == nil {
			contexts = append(contexts, c)
		}
	}
	return contexts
}
func (s *session) queueEvent(snapshot core.Snapshot, kind string, c v1.Context) error {
	projection := v1.Event{Context: c.Token, Kind: kind, Snapshot: s.filterSnapshot(snapshot, v1.CoreEvents, c)}
	size := int64(len(asJSON(projection)) + 128)
	if s.eventBytes.Add(size) > int64(s.manager.config.ControlBytes) {
		s.peer.Fail(ErrOverflow)
		return ErrOverflow
	}
	select {
	case s.events <- observedEvent{projection, size}:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	default:
		s.peer.Fail(ErrOverflow)
		return ErrOverflow
	}
}
