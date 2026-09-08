package plugin

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aruzen/ariadne/internal/core"
)

type testPlugin struct {
	name       string
	initialize func(context.Context, core.Snapshot, Labels) error
	handle     func(context.Context, core.Event, Labels) error
}

func (plugin testPlugin) Name() string { return plugin.name }
func (plugin testPlugin) Initialize(ctx context.Context, snapshot core.Snapshot, labels Labels) error {
	if plugin.initialize != nil {
		return plugin.initialize(ctx, snapshot, labels)
	}
	return nil
}
func (plugin testPlugin) HandleEvent(ctx context.Context, event core.Event, labels Labels) error {
	if plugin.handle != nil {
		return plugin.handle(ctx, event, labels)
	}
	return nil
}

func testCore(t *testing.T, capacity int) *core.Core {
	t.Helper()
	engine, err := core.New(core.Config{EventQueueCapacity: capacity})
	if err != nil {
		t.Fatalf("Core New: %v", err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine
}

func waitUntil(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPluginPanicDisablesOnlyPluginAndClearsLabels(t *testing.T) {
	engine := testCore(t, 16)
	var healthyEvents atomic.Int64
	panicking := testPlugin{
		name: "panicking",
		initialize: func(ctx context.Context, _ core.Snapshot, labels Labels) error {
			if err := labels.Set(ctx, core.LabelWorkspace, 1, "temporary", "value"); err != nil {
				return err
			}
			panic("boom")
		},
	}
	healthy := testPlugin{name: "healthy", handle: func(context.Context, core.Event, Labels) error {
		healthyEvents.Add(1)
		return nil
	}}
	host, err := New(context.Background(), engine, []Plugin{panicking, healthy}, DefaultConfig())
	if err != nil {
		t.Fatalf("Host New: %v", err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	waitUntil(t, func() bool {
		statuses := host.Status()
		return !statuses[0].Enabled && errors.Is(statuses[0].Error, ErrPanic)
	})
	waitUntil(t, func() bool {
		snapshot, _ := engine.Snapshot(context.Background())
		return len(snapshot.Labels) == 0
	})
	_, err = engine.Execute(context.Background(), core.CreateWorkspaceCommand{Name: "still-running"})
	if err != nil {
		t.Fatalf("Core Execute: %v", err)
	}
	waitUntil(t, func() bool { return healthyEvents.Load() != 0 })
	if !host.Status()[1].Enabled {
		t.Fatal("healthy Plugin was disabled")
	}
}

func TestConsecutiveErrorsDisableAndClearSourceLabels(t *testing.T) {
	engine := testCore(t, 16)
	var calls atomic.Int64
	failing := testPlugin{name: "failing", handle: func(ctx context.Context, _ core.Event, labels Labels) error {
		calls.Add(1)
		if err := labels.Set(ctx, core.LabelWorkspace, 1, "failure", "pending"); err != nil {
			return err
		}
		return errors.New("failure")
	}}
	host, err := New(context.Background(), engine, []Plugin{failing}, Config{ConsecutiveErrorLimit: 3})
	if err != nil {
		t.Fatalf("Host New: %v", err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	for index, name := range []string{"trigger-one", "trigger-two", "trigger-three"} {
		_, err = engine.Execute(context.Background(), core.CreateWorkspaceCommand{Name: name})
		if err != nil {
			t.Fatalf("Core Execute: %v", err)
		}
		want := int64(index + 1)
		waitUntil(t, func() bool { return calls.Load() >= want || !host.Status()[0].Enabled })
	}
	waitUntil(t, func() bool { return !host.Status()[0].Enabled })
	if errors.Is(host.Status()[0].Error, core.ErrEventQueueOverflow) {
		t.Fatalf("Plugin disabled by queue overflow instead of consecutive errors: %v", host.Status()[0].Error)
	}
	waitUntil(t, func() bool {
		snapshot, _ := engine.Snapshot(context.Background())
		return len(snapshot.Labels) == 0
	})
}

func TestRawQueueOverflowDoesNotBlockCoreOrOtherPlugin(t *testing.T) {
	engine := testCore(t, 1)
	entered := make(chan struct{})
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	var healthyEvents atomic.Int64
	slow := testPlugin{name: "slow", handle: func(context.Context, core.Event, Labels) error {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-release
		return nil
	}}
	healthy := testPlugin{name: "healthy", handle: func(context.Context, core.Event, Labels) error {
		healthyEvents.Add(1)
		return nil
	}}
	host, err := New(context.Background(), engine, []Plugin{slow, healthy}, DefaultConfig())
	if err != nil {
		t.Fatalf("Host New: %v", err)
	}
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	if _, err := engine.Execute(context.Background(), core.CreateWorkspaceCommand{Name: "one"}); err != nil {
		t.Fatalf("first Execute: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("slow Plugin did not enter handler")
	}
	waitUntil(t, func() bool { return healthyEvents.Load() >= 1 })
	for index, name := range []string{"two", "three"} {
		if _, err := engine.Execute(context.Background(), core.CreateWorkspaceCommand{Name: name}); err != nil {
			t.Fatalf("Execute %s: %v", name, err)
		}
		want := int64(index + 2)
		waitUntil(t, func() bool { return healthyEvents.Load() >= want })
	}
	close(release)
	released = true
	waitUntil(t, func() bool {
		status := host.Status()[0]
		return !status.Enabled && errors.Is(status.Error, core.ErrEventQueueOverflow)
	})
	waitUntil(t, func() bool { return healthyEvents.Load() >= 3 })
	if !host.Status()[1].Enabled {
		t.Fatal("healthy Plugin was disabled by another queue overflow")
	}
}

func TestHungCallbackIsDisabledAndDoesNotBlockClose(t *testing.T) {
	engine := testCore(t, 8)
	entered := make(chan struct{})
	blocked := make(chan struct{})
	defer close(blocked)
	hung := testPlugin{name: "hung", handle: func(context.Context, core.Event, Labels) error {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-blocked
		return nil
	}}
	host, err := New(context.Background(), engine, []Plugin{hung}, Config{
		ConsecutiveErrorLimit: 3, CallbackTimeout: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Host New: %v", err)
	}
	if _, err := engine.Execute(context.Background(), core.CreateWorkspaceCommand{Name: "trigger"}); err != nil {
		t.Fatalf("Core Execute: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("hung callback was not invoked")
	}
	waitUntil(t, func() bool {
		status := host.Status()[0]
		return !status.Enabled && errors.Is(status.Error, ErrCallbackTimeout)
	})
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := host.Close(closeCtx); err != nil {
		t.Fatalf("Host Close: %v", err)
	}
}
