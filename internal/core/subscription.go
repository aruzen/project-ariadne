package core

import (
	"context"
	"sync"
)

// Subscription represents one frontend's bounded Event queue.
type Subscription struct {
	id     FrontendID
	core   *Core
	events chan Event
	done   chan struct{}
	once   sync.Once
	errMu  sync.Mutex
	err    error
}

func (s *Subscription) ID() FrontendID {
	return s.id
}

func (s *Subscription) Events() <-chan Event {
	return s.events
}

func (s *Subscription) Done() <-chan struct{} {
	return s.done
}

func (s *Subscription) Err() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.err
}

func (s *Subscription) Close() error {
	if s.core == nil {
		return nil
	}
	return s.core.unsubscribe(context.Background(), s.id, nil)
}

// finish is called only by the Core executor.
func (s *Subscription) finish(err error) {
	s.once.Do(func() {
		s.errMu.Lock()
		s.err = err
		s.errMu.Unlock()
		close(s.events)
		close(s.done)
	})
}
