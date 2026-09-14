package core

import (
	"context"
	"testing"
	"time"
)

func TestToolInstanceIsSharedAndRemovedWithLastPane(t *testing.T) {
	engine, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	tool := ToolInstance{Descriptor: ToolDescriptor{Provider: "ariadne", Type: "help", Instance: "default"}, StateVersion: 1, Generation: 1, State: []byte(`{"page":1}`)}
	value, err := engine.Execute(ctx, CreatePaneCommand{WindowID: 1, Pane: PaneSpec{Kind: PaneTool, Tool: &tool}})
	if err != nil {
		t.Fatal(err)
	}
	first := value.(CreatePaneResult).Pane
	value, err = engine.Execute(ctx, SplitPaneCommand{TargetPaneID: first.ID, Direction: SplitHorizontal, Pane: PaneSpec{Kind: PaneTool, Tool: &tool}})
	if err != nil {
		t.Fatal(err)
	}
	second := value.(CreatePaneResult).Pane
	value, err = engine.Execute(ctx, UpdateToolStateCommand{Descriptor: tool.Descriptor, ExpectedGeneration: 1, StateVersion: 2, State: []byte(`{"page":2}`)})
	if err != nil {
		t.Fatal(err)
	}
	if value.(ToolStateResult).Tool.Generation != 2 {
		t.Fatalf("generation = %d", value.(ToolStateResult).Tool.Generation)
	}
	if _, err := engine.Execute(ctx, ClosePaneCommand{PaneID: first.ID}); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := engine.Snapshot(ctx)
	if len(snapshot.ToolInstances) != 1 {
		t.Fatalf("tools after first close = %d", len(snapshot.ToolInstances))
	}
	if _, err := engine.Execute(ctx, ClosePaneCommand{PaneID: second.ID}); err != nil {
		t.Fatal(err)
	}
	snapshot, _ = engine.Snapshot(ctx)
	if len(snapshot.ToolInstances) != 0 {
		t.Fatalf("tools after last close = %d", len(snapshot.ToolInstances))
	}
}

func TestAttentionDedupAcknowledgeAndSourceCleanup(t *testing.T) {
	engine, err := New(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	value, err := engine.Execute(ctx, CreatePaneCommand{WindowID: 1, Pane: PaneSpec{Kind: PaneTool}})
	if err != nil {
		t.Fatal(err)
	}
	pane := value.(CreatePaneResult).Pane
	now := time.Unix(100, 0).UTC()
	command := RaiseAttentionCommand{PaneID: pane.ID, Source: "plugin:test", Key: "job", Class: AttentionWaiting, Severity: SeverityInfo, Message: "waiting", OccurredAt: now}
	value, err = engine.Execute(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	attention := value.(AttentionResult).Attention
	value, err = engine.Execute(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if value.(AttentionResult).Changed {
		t.Fatal("duplicate attention changed state")
	}
	if _, err := engine.Execute(ctx, AcknowledgeAttentionCommand{ID: attention.ID, At: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	command.Class, command.Message, command.OccurredAt = AttentionCompleted, "done", now.Add(2*time.Second)
	value, err = engine.Execute(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	updated := value.(AttentionResult).Attention
	if updated.ID != attention.ID || updated.AcknowledgedAt != nil {
		t.Fatalf("updated attention = %+v", updated)
	}
	if _, err := engine.Execute(ctx, RemoveAttentionsBySourceCommand{Source: "plugin:test"}); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := engine.Snapshot(ctx)
	if len(snapshot.Attentions) != 0 {
		t.Fatalf("attentions = %+v", snapshot.Attentions)
	}
}

func TestAttentionLimitEvictsAcknowledgedBeforeUnread(t *testing.T) {
	configuration := DefaultConfig()
	configuration.MaxAttentionEntries = 2
	engine, err := New(configuration)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	value, err := engine.Execute(ctx, CreatePaneCommand{WindowID: 1, Pane: PaneSpec{Kind: PaneTool}})
	if err != nil {
		t.Fatal(err)
	}
	pane := value.(CreatePaneResult).Pane
	base := time.Unix(200, 0).UTC()
	var ids []uint64
	for index, key := range []string{"first", "second"} {
		value, err = engine.Execute(ctx, RaiseAttentionCommand{
			PaneID: pane.ID, Source: "plugin:test", Key: key,
			Class: AttentionWaiting, Severity: SeverityInfo,
			OccurredAt: base.Add(time.Duration(index) * time.Second),
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, value.(AttentionResult).Attention.ID)
	}
	if _, err := engine.Execute(ctx, AcknowledgeAttentionCommand{ID: ids[1], At: base.Add(2 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, RaiseAttentionCommand{
		PaneID: pane.ID, Source: "plugin:test", Key: "third",
		Class: AttentionWaiting, Severity: SeverityInfo, OccurredAt: base.Add(3 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := engine.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Attentions) != 2 {
		t.Fatalf("attentions = %+v", snapshot.Attentions)
	}
	for _, attention := range snapshot.Attentions {
		if attention.ID == ids[1] {
			t.Fatalf("acknowledged attention was not evicted: %+v", snapshot.Attentions)
		}
	}
}
