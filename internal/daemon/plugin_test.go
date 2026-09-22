package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/ariadne/internal/plugin/external"
	ariadneprotocol "github.com/aruzen/ariadne/internal/protocol"
	"github.com/aruzen/streammux/pty"
)

func TestPluginTerminalProcessReturnsCurrentTerminalIdentity(t *testing.T) {
	process := newTestManagedProcess()
	server, _, err := Open(context.Background(), &testFactory{processes: []*testManagedProcess{process}}, DefaultConfig(filepath.Join(t.TempDir(), "state.json")))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close(context.Background())
	result, err := server.NewTerminal(context.Background(), ariadneprotocol.NewTerminalParams{WindowID: 1, Argv: []string{"shell"}, CWD: t.TempDir(), InitialSize: pty.Size{Cols: 80, Rows: 24}})
	if err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(v1.TerminalProcessParams{PaneID: uint64(result.Pane.ID)})
	value, err := server.PluginOperation(context.Background(), "terminal.process", params, v1.Context{})
	if err != nil {
		t.Fatal(err)
	}
	identity := value.(v1.TerminalProcessResult)
	if identity.TerminalID != uint64(*result.Pane.Terminal.ID) || identity.PID != 4312 {
		t.Fatalf("identity = %+v", identity)
	}
	if _, err := server.StopTerminal(context.Background(), ariadneprotocol.PaneParams{PaneID: result.Pane.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.PluginOperation(context.Background(), "terminal.process", params, v1.Context{}); !errors.Is(err, external.ErrUnavailable) {
		t.Fatalf("stopped terminal process error = %v", err)
	}
}

func TestPluginEditorNormalExitAndCancellation(t *testing.T) {
	for _, normal := range []bool{true, false} {
		name := "cancel"
		if normal {
			name = "complete"
		}
		t.Run(name, func(t *testing.T) {
			process := newTestManagedProcess()
			factory := &testFactory{processes: []*testManagedProcess{process}}
			c := DefaultConfig(filepath.Join(t.TempDir(), "state.json"))
			c.ClosePaneOnSuccessfulExit = true
			server, _, err := Open(context.Background(), factory, c)
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close(context.Background())
			_, sub, err := server.core.Subscribe(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer sub.Close()
			frontend := uint64(sub.ID())
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			before, _ := server.core.Snapshot(ctx)
			result, err := server.managePluginEditor(ctx, frontend, v1.ManageRequest{Action: "editor.open", Editor: &v1.EditorRequest{Argv: []string{"editor", "--wait"}, CWD: t.TempDir(), Text: "initial"}})
			if err != nil {
				t.Fatal(err)
			}
			snapshot, _ := server.core.Snapshot(ctx)
			if len(snapshot.StashedPanes) != 1 || snapshot.Windows[0].Layout != nil || !snapshot.Panes[0].Transient {
				t.Fatal("editor was inserted into layout")
			}
			server.mu.Lock()
			editor := server.pluginEditors[core.PaneID(result.Editor.PaneID)]
			server.mu.Unlock()
			path := editor.path
			factory.mu.Lock()
			argv := append([]string{factory.specs[0].Command}, factory.specs[0].Args...)
			factory.mu.Unlock()
			if len(argv) != 3 || argv[1] != "--wait" || argv[2] != path {
				t.Fatal("editor argv not preserved")
			}
			if _, err := server.managePluginEditor(ctx, frontend+1, v1.ManageRequest{Action: "editor.cancel", PaneID: result.Editor.PaneID}); !errors.Is(err, external.ErrPermission) {
				t.Fatal("another frontend cancelled editor")
			}
			if normal {
				if _, err := server.managePluginEditor(ctx, frontend, v1.ManageRequest{Action: "editor.finish", PaneID: result.Editor.PaneID}); !errors.Is(err, core.ErrInvalidState) {
					t.Fatal("finish of a running editor must fail immediately")
				}
				if err := os.WriteFile(path, []byte("edited"), 0600); err != nil {
					t.Fatal(err)
				}
				process.complete(pty.ExitStatus{Reason: pty.ExitReasonExited, Code: 0})
				session, _ := server.manager.Get(editor.terminal)
				if _, err := session.Wait(ctx); err != nil {
					t.Fatal(err)
				}
				result, err = server.managePluginEditor(ctx, frontend, v1.ManageRequest{Action: "editor.finish", PaneID: result.Editor.PaneID})
				if err != nil || result.Editor.Text != "edited" {
					t.Fatalf("edit result: %+v %v", result, err)
				}
			} else {
				_, err = server.managePluginEditor(ctx, frontend, v1.ManageRequest{Action: "editor.cancel", PaneID: result.Editor.PaneID})
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("temporary file retained")
			}
			snapshot, _ = server.core.Snapshot(ctx)
			if len(snapshot.Panes) != len(before.Panes) || len(snapshot.StashedPanes) != 0 || snapshot.Windows[0].Layout != nil {
				t.Fatal("editor cleanup altered layout or retained Pane")
			}
		})
	}
}
