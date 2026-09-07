// Package plugin runs statically linked, in-process plugins with isolated Core
// event subscriptions and source-scoped Label access.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aruzen/ariadne/core"
)

const DefaultConsecutiveErrorLimit = 3
const sourcePrefix = "plugin:"

var (
	ErrInvalidConfig   = errors.New("plugin: invalid configuration")
	ErrDuplicateName   = errors.New("plugin: duplicate name")
	ErrPanic           = errors.New("plugin: panic")
	ErrCallbackTimeout = errors.New("plugin: callback timed out")
	ErrDisabled        = errors.New("plugin: disabled")
)

type Labels interface {
	Set(context.Context, core.LabelTargetKind, uint64, string, string) error
	Remove(context.Context, core.LabelTargetKind, uint64, string) error
}

type Plugin interface {
	Name() string
	Initialize(context.Context, core.Snapshot, Labels) error
	HandleEvent(context.Context, core.Event, Labels) error
}

type Config struct {
	ConsecutiveErrorLimit int
	CallbackTimeout       time.Duration
}

func DefaultConfig() Config {
	return Config{ConsecutiveErrorLimit: DefaultConsecutiveErrorLimit, CallbackTimeout: 2 * time.Second}
}

type Status struct {
	Name    string
	Enabled bool
	Error   error
}

type runtime struct {
	plugin       Plugin
	subscription *core.Subscription
	labels       labelWriter
	mu           sync.Mutex
	status       Status
	active       atomic.Bool
	labelGate    sync.Mutex
}

type Host struct {
	core    *core.Core
	config  Config
	ctx     context.Context
	cancel  context.CancelFunc
	runtime []*runtime
	wg      sync.WaitGroup
	once    sync.Once
}

func New(parent context.Context, engine *core.Core, plugins []Plugin, configuration Config) (*Host, error) {
	if parent == nil || engine == nil {
		return nil, fmt.Errorf("%w: nil dependency", ErrInvalidConfig)
	}
	defaults := DefaultConfig()
	if configuration.ConsecutiveErrorLimit == 0 {
		configuration.ConsecutiveErrorLimit = defaults.ConsecutiveErrorLimit
	}
	if configuration.CallbackTimeout == 0 {
		configuration.CallbackTimeout = defaults.CallbackTimeout
	}
	if configuration.ConsecutiveErrorLimit < 1 || configuration.CallbackTimeout < 0 {
		return nil, fmt.Errorf("%w: error limit must be positive", ErrInvalidConfig)
	}
	ctx, cancel := context.WithCancel(parent)
	host := &Host{core: engine, config: configuration, ctx: ctx, cancel: cancel}
	names := make(map[string]struct{}, len(plugins))
	for _, implementation := range plugins {
		if implementation == nil || strings.TrimSpace(implementation.Name()) == "" || len(implementation.Name()) > 128-len(sourcePrefix) || strings.ContainsRune(implementation.Name(), 0) {
			cancel()
			host.closeSubscriptions()
			return nil, fmt.Errorf("%w: invalid plugin", ErrInvalidConfig)
		}
		name := implementation.Name()
		if _, exists := names[name]; exists {
			cancel()
			host.closeSubscriptions()
			return nil, fmt.Errorf("%w: %s", ErrDuplicateName, name)
		}
		names[name] = struct{}{}
		snapshot, subscription, err := engine.Subscribe(ctx)
		if err != nil {
			cancel()
			host.closeSubscriptions()
			return nil, err
		}
		runtime := &runtime{
			plugin: implementation, subscription: subscription,
			status: Status{Name: name, Enabled: true},
		}
		runtime.active.Store(true)
		runtime.labels = labelWriter{core: engine, source: LabelSource(name), active: &runtime.active, gate: &runtime.labelGate}
		host.runtime = append(host.runtime, runtime)
		host.wg.Add(1)
		go host.run(runtime, snapshot)
	}
	return host, nil
}

func (host *Host) Status() []Status {
	result := make([]Status, 0, len(host.runtime))
	for _, runtime := range host.runtime {
		runtime.mu.Lock()
		result = append(result, runtime.status)
		runtime.mu.Unlock()
	}
	return result
}

func (host *Host) Close(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", ErrInvalidConfig)
	}
	host.once.Do(func() {
		host.cancel()
		host.closeSubscriptions()
	})
	done := make(chan struct{})
	go func() {
		host.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (host *Host) closeSubscriptions() {
	for _, runtime := range host.runtime {
		_ = runtime.subscription.Close()
	}
}

func (host *Host) run(runtime *runtime, snapshot core.Snapshot) {
	defer host.wg.Done()
	defer func() {
		_, _ = host.core.Execute(context.Background(), core.RemoveLabelsBySourceCommand{Source: LabelSource(runtime.plugin.Name())})
	}()
	if err, _ := callInitialize(host.ctx, host.config.CallbackTimeout, runtime.plugin, snapshot, runtime.labels); err != nil {
		runtime.disable(err)
		return
	}
	consecutiveErrors := 0
	for {
		select {
		case event, ok := <-runtime.subscription.Events():
			if !ok {
				err := runtime.subscription.Err()
				if err == nil {
					err = context.Canceled
				}
				runtime.disable(err)
				return
			}
			err, panicked := callEvent(host.ctx, host.config.CallbackTimeout, runtime.plugin, event, runtime.labels)
			if panicked {
				runtime.disable(err)
				return
			}
			if err != nil {
				consecutiveErrors++
				if consecutiveErrors >= host.config.ConsecutiveErrorLimit {
					runtime.disable(err)
					return
				}
				continue
			}
			consecutiveErrors = 0
		case <-host.ctx.Done():
			runtime.disable(host.ctx.Err())
			return
		}
	}
}

func LabelSource(pluginName string) string { return sourcePrefix + pluginName }

func (runtime *runtime) disable(err error) {
	runtime.labelGate.Lock()
	runtime.active.Store(false)
	runtime.labelGate.Unlock()
	runtime.mu.Lock()
	runtime.status.Enabled = false
	runtime.status.Error = err
	runtime.mu.Unlock()
}

type labelWriter struct {
	core   *core.Core
	source string
	active *atomic.Bool
	gate   *sync.Mutex
}

func (writer labelWriter) Set(ctx context.Context, kind core.LabelTargetKind, id uint64, name, value string) error {
	writer.gate.Lock()
	defer writer.gate.Unlock()
	if writer.active == nil || !writer.active.Load() {
		return ErrDisabled
	}
	_, err := writer.core.Execute(ctx, core.SetLabelCommand{Label: core.Label{
		TargetKind: kind, TargetID: id, Source: writer.source, Name: name, Value: value,
	}})
	return err
}

func (writer labelWriter) Remove(ctx context.Context, kind core.LabelTargetKind, id uint64, name string) error {
	writer.gate.Lock()
	defer writer.gate.Unlock()
	if writer.active == nil || !writer.active.Load() {
		return ErrDisabled
	}
	_, err := writer.core.Execute(ctx, core.RemoveLabelCommand{
		TargetKind: kind, TargetID: id, Source: writer.source, Name: name,
	})
	return err
}

type callbackResult struct {
	err      error
	panicked bool
}

func callInitialize(ctx context.Context, timeout time.Duration, implementation Plugin, snapshot core.Snapshot, labels Labels) (error, bool) {
	callbackCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return awaitCallback(callbackCtx, func() error { return implementation.Initialize(callbackCtx, snapshot, labels) })
}

func callEvent(ctx context.Context, timeout time.Duration, implementation Plugin, event core.Event, labels Labels) (error, bool) {
	callbackCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return awaitCallback(callbackCtx, func() error { return implementation.HandleEvent(callbackCtx, event, labels) })
}

func awaitCallback(ctx context.Context, callback func() error) (error, bool) {
	result := make(chan callbackResult, 1)
	go func() {
		completed := callbackResult{}
		defer func() {
			if recovered := recover(); recovered != nil {
				completed.err = fmt.Errorf("%w: %v", ErrPanic, recovered)
				completed.panicked = true
			}
			result <- completed
		}()
		completed.err = callback()
	}()
	select {
	case completed := <-result:
		return completed.err, completed.panicked
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return ErrCallbackTimeout, true
		}
		return ctx.Err(), false
	}
}
