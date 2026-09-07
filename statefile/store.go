package statefile

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aruzen/ariadne/core"
)

type flushRequest struct {
	response chan error
}

// Store coalesces state updates and performs all filesystem I/O on its worker
// goroutine. Schedule never performs filesystem I/O.
type Store struct {
	path     string
	options  Options
	write    func(string, []byte) error
	wake     chan struct{}
	flush    chan flushRequest
	stop     chan struct{}
	done     chan struct{}
	errors   chan error
	stopOnce sync.Once

	mu         sync.Mutex
	closing    bool
	dirty      bool
	generation uint64
	latest     []byte
	closeErr   error
}

func NewStore(path string, options Options) (*Store, error) {
	options, err := options.normalized()
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, fmt.Errorf("%w: empty path", ErrInvalidOptions)
	}
	store := &Store{
		path: path, options: options, write: writeAtomic,
		wake: make(chan struct{}, 1), flush: make(chan flushRequest),
		stop: make(chan struct{}), done: make(chan struct{}), errors: make(chan error, 1),
	}
	go store.run()
	return store, nil
}

func (store *Store) Errors() <-chan error {
	return store.errors
}

// Schedule replaces the pending state and restarts the debounce interval.
func (store *Store) Schedule(snapshot core.Snapshot) error {
	store.mu.Lock()
	closing := store.closing
	store.mu.Unlock()
	if closing {
		return ErrClosed
	}
	data, err := Encode(snapshot)
	if err != nil {
		return err
	}
	if int64(len(data)) > store.options.MaxBytes {
		return fmt.Errorf("%w: encoded state is %d bytes", ErrTooLarge, len(data))
	}
	store.mu.Lock()
	if store.closing {
		store.mu.Unlock()
		return ErrClosed
	}
	store.latest = data
	store.dirty = true
	store.generation++
	store.mu.Unlock()
	select {
	case store.wake <- struct{}{}:
	default:
	}
	return nil
}

// Flush immediately writes the newest Snapshot scheduled before the request.
func (store *Store) Flush(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", ErrInvalidOptions)
	}
	request := flushRequest{response: make(chan error, 1)}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-store.stop:
		return ErrClosed
	case <-store.done:
		return ErrClosed
	case store.flush <- request:
	}
	select {
	case err := <-request.response:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-store.done:
		store.mu.Lock()
		err := store.closeErr
		store.mu.Unlock()
		if err != nil {
			return err
		}
		return ErrClosed
	}
}

// Close rejects new schedules and performs an immediate final flush.
func (store *Store) Close(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", ErrInvalidOptions)
	}
	store.stopOnce.Do(func() {
		store.mu.Lock()
		store.closing = true
		store.mu.Unlock()
		close(store.stop)
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-store.done:
		store.mu.Lock()
		defer store.mu.Unlock()
		return store.closeErr
	}
}

func (store *Store) run() {
	defer close(store.done)
	defer close(store.errors)
	var timer *time.Timer
	var timerChannel <-chan time.Time
	stopTimer := func() {
		if timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timerChannel = nil
	}
	resetTimer := func() {
		if timer == nil {
			timer = time.NewTimer(store.options.Debounce)
		} else {
			stopTimer()
			timer.Reset(store.options.Debounce)
		}
		timerChannel = timer.C
	}
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		select {
		case <-store.stop:
			stopTimer()
			err := store.writeLatest()
			store.mu.Lock()
			store.closeErr = err
			store.mu.Unlock()
			return
		case <-store.wake:
			resetTimer()
		case request := <-store.flush:
			stopTimer()
			request.response <- store.writeLatest()
		case <-timerChannel:
			timerChannel = nil
			if err := store.writeLatest(); err != nil {
				store.report(err)
			}
		}
	}
}

func (store *Store) writeLatest() error {
	store.mu.Lock()
	if !store.dirty {
		store.mu.Unlock()
		return nil
	}
	data := store.latest
	generation := store.generation
	store.dirty = false
	store.mu.Unlock()

	err := store.write(store.path, data)
	if err == nil {
		return nil
	}
	store.mu.Lock()
	if store.generation == generation {
		store.dirty = true
	}
	store.mu.Unlock()
	return err
}

func (store *Store) report(err error) {
	if err == nil || errors.Is(err, ErrClosed) {
		return
	}
	select {
	case store.errors <- err:
	default:
	}
}
