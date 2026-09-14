package statefile

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aruzen/ariadne/internal/core"
)

func TestToolStatePersistsAndAttentionDoesNot(t *testing.T) {
	engine, err := core.New(core.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	tool := core.ToolInstance{Descriptor: core.ToolDescriptor{Provider: "ariadne", Type: "diagnostics", Instance: "default"}, StateVersion: 1, Generation: 1, State: []byte(`{"mode":"full"}`)}
	value, err := engine.Execute(ctx, core.CreatePaneCommand{WindowID: 1, Pane: core.PaneSpec{Kind: core.PaneTool, Tool: &tool}})
	if err != nil {
		t.Fatal(err)
	}
	pane := value.(core.CreatePaneResult).Pane
	if _, err := engine.Execute(ctx, core.RaiseAttentionCommand{PaneID: pane.ID, Source: "plugin:test", Key: "x", Class: core.AttentionWarning, Severity: core.SeverityWarning, OccurredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := engine.Snapshot(ctx)
	data, err := Encode(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Decode(data, DefaultMaxBytes)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]string
	if len(restored.ToolInstances) != 1 || json.Unmarshal(restored.ToolInstances[0].State, &state) != nil || state["mode"] != "full" {
		t.Fatalf("tools = %+v", restored.ToolInstances)
	}
	if len(restored.Attentions) != 0 {
		t.Fatalf("runtime attentions persisted: %+v", restored.Attentions)
	}
}
