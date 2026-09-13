package statefile

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aruzen/ariadne/internal/core"
	"github.com/aruzen/streammux"
)

func snapshotWithTerminals(t *testing.T) core.Snapshot {
	t.Helper()
	engine, err := core.New(core.Config{EventQueueCapacity: 8})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	defer engine.Close()

	runningID := streammux.StreamID(41)
	runningValue, err := engine.Execute(context.Background(), core.CreatePaneCommand{
		WindowID: 1,
		Pane: core.PaneSpec{Kind: core.PaneTerminal, Title: "running", Terminal: &core.TerminalInstance{
			ID: &runningID, State: core.TerminalRunning,
			Launch:           core.LaunchSpec{Argv: []string{"/bin/sh", "-c", ""}, CWD: "/work"},
			HistoryAvailable: true,
		}},
	})
	if err != nil {
		t.Fatalf("create running Pane: %v", err)
	}
	runningPane := runningValue.(core.CreatePaneResult).Pane

	failedID := streammux.StreamID(42)
	_, err = engine.Execute(context.Background(), core.SplitPaneCommand{
		TargetPaneID: runningPane.ID,
		Direction:    core.SplitHorizontal,
		Pane: core.PaneSpec{Kind: core.PaneTerminal, Title: "failed", Terminal: &core.TerminalInstance{
			ID: &failedID, State: core.TerminalFailed,
			Launch:           core.LaunchSpec{Argv: []string{"agent", "--mode", "test"}, CWD: "/agent"},
			Exit:             &core.TerminalExit{Kind: core.TerminalExitPTYError, Message: "read failed"},
			HistoryAvailable: true,
		}},
	})
	if err != nil {
		t.Fatalf("create failed Pane: %v", err)
	}
	exitedID := streammux.StreamID(43)
	_, err = engine.Execute(context.Background(), core.SplitPaneCommand{
		TargetPaneID: runningPane.ID,
		Direction:    core.SplitVertical,
		Pane: core.PaneSpec{Kind: core.PaneTerminal, Title: "exited", Terminal: &core.TerminalInstance{
			ID: &exitedID, State: core.TerminalExited,
			Launch:           core.LaunchSpec{Argv: []string{"test-command"}, CWD: "/tests"},
			Exit:             &core.TerminalExit{Kind: core.TerminalExitProcess, Code: 7},
			HistoryAvailable: true,
		}},
	})
	if err != nil {
		t.Fatalf("create exited Pane: %v", err)
	}
	signaledID := streammux.StreamID(44)
	_, err = engine.Execute(context.Background(), core.SplitPaneCommand{
		TargetPaneID: runningPane.ID,
		Direction:    core.SplitVertical,
		Pane: core.PaneSpec{Kind: core.PaneTerminal, Title: "signaled", Terminal: &core.TerminalInstance{
			ID: &signaledID, State: core.TerminalExited,
			Launch:           core.LaunchSpec{Argv: []string{"server"}, CWD: "/server"},
			Exit:             &core.TerminalExit{Kind: core.TerminalExitSignal, Signal: "SIGTERM"},
			HistoryAvailable: true,
		}},
	})
	if err != nil {
		t.Fatalf("create signaled Pane: %v", err)
	}
	if _, err := engine.Execute(context.Background(), core.SetLabelCommand{Label: core.Label{
		TargetKind: core.LabelPane, TargetID: uint64(runningPane.ID), Source: "runtime-plugin", Name: "status", Value: "busy",
	}}); err != nil {
		t.Fatalf("set runtime Label: %v", err)
	}
	snapshot, err := engine.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	return snapshot
}

func TestEncodeDecodeStripsRuntimeStateAndRestoresPlaceholder(t *testing.T) {
	original := snapshotWithTerminals(t)
	data, err := Encode(original)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if strings.Contains(string(data), "history_available") || strings.Contains(string(data), "\"revision\"") || strings.Contains(string(data), "runtime-plugin") {
		t.Fatalf("runtime fields leaked into state file:\n%s", data)
	}

	var raw struct {
		State struct {
			Panes []struct {
				Terminal map[string]any `json:"terminal"`
			} `json:"panes"`
		} `json:"state"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	for _, pane := range raw.State.Panes {
		if _, exists := pane.Terminal["id"]; exists {
			t.Fatalf("runtime TerminalID persisted: %v", pane.Terminal)
		}
	}

	restored, err := Decode(data, DefaultMaxBytes)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if restored.Revision != 0 {
		t.Fatalf("restored Revision = %d, want 0", restored.Revision)
	}
	if len(restored.Labels) != 0 {
		t.Fatalf("runtime Labels persisted: %+v", restored.Labels)
	}
	if restored.NextPaneID != original.NextPaneID || len(restored.Panes) != 4 {
		t.Fatalf("restored counters or Panes differ: %+v", restored)
	}
	running := restored.Panes[0].Terminal
	if running == nil || running.ID != nil || running.State != core.TerminalPlaceholder || running.HistoryAvailable {
		t.Fatalf("running Terminal did not become placeholder: %+v", running)
	}
	if !slices.Equal(running.Launch.Argv, []string{"/bin/sh", "-c", ""}) || running.Launch.CWD != "/work" {
		t.Fatalf("placeholder launch data lost: %+v", running.Launch)
	}
	failed := restored.Panes[1].Terminal
	if failed == nil || failed.ID != nil || failed.State != core.TerminalFailed || failed.HistoryAvailable || failed.Exit == nil {
		t.Fatalf("failed Terminal restoration mismatch: %+v", failed)
	}
	if failed.Exit.Kind != core.TerminalExitPTYError || failed.Exit.Message != "read failed" {
		t.Fatalf("failed Terminal exit data lost: %+v", failed.Exit)
	}
	exited := restored.Panes[2].Terminal
	if exited == nil || exited.ID != nil || exited.State != core.TerminalExited || exited.Exit == nil || exited.Exit.Kind != core.TerminalExitProcess || exited.Exit.Code != 7 {
		t.Fatalf("nonzero exit data lost: %+v", exited)
	}
	signaled := restored.Panes[3].Terminal
	if signaled == nil || signaled.ID != nil || signaled.State != core.TerminalExited || signaled.Exit == nil || signaled.Exit.Kind != core.TerminalExitSignal || signaled.Exit.Signal != "SIGTERM" {
		t.Fatalf("signal exit data lost: %+v", signaled)
	}
	if original.Panes[0].Terminal.ID == nil || !original.Panes[0].Terminal.HistoryAvailable {
		t.Fatalf("Encode mutated source Snapshot: %+v", original.Panes[0].Terminal)
	}
}

func TestDecodeRejectsUnknownFieldsTrailingDataAndOversize(t *testing.T) {
	valid, err := Encode(core.DefaultSnapshot())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	unknown := strings.Replace(string(valid), "\"version\": 1", "\"version\": 1, \"unknown\": true", 1)
	if _, err := Decode([]byte(unknown), DefaultMaxBytes); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("unknown field error = %v", err)
	}
	if _, err := Decode(append(valid, []byte("{}")...), DefaultMaxBytes); !errors.Is(err, ErrInvalidData) {
		t.Fatalf("trailing value error = %v", err)
	}
	if _, err := Decode(valid, int64(len(valid)-1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize error = %v", err)
	}
}

func TestLoadCreatesMissingStateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	result, err := Load(path, DefaultOptions())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result.Status != LoadCreated || result.QuarantinedPath != "" || result.RecoveryCause != nil {
		t.Fatalf("unexpected Load result: %+v", result)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %#o, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if _, err := Decode(data, DefaultMaxBytes); err != nil {
		t.Fatalf("created state is invalid: %v", err)
	}
}

func TestLoadExistingState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	want := snapshotWithTerminals(t)
	data, err := Encode(want)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	result, err := Load(path, DefaultOptions())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result.Status != LoadExisting || len(result.Snapshot.Panes) != len(want.Panes) {
		t.Fatalf("unexpected existing state: %+v", result)
	}
	if result.Snapshot.Panes[0].Terminal.State != core.TerminalPlaceholder {
		t.Fatalf("running Terminal not restored as placeholder: %+v", result.Snapshot.Panes[0].Terminal)
	}
}

func TestRestoredCoreContinuesPaneIDSequence(t *testing.T) {
	original := snapshotWithTerminals(t)
	data, err := Encode(original)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	restored, err := Decode(data, DefaultMaxBytes)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	engine, err := core.NewFromSnapshot(core.Config{EventQueueCapacity: 8}, restored)
	if err != nil {
		t.Fatalf("core.NewFromSnapshot: %v", err)
	}
	defer engine.Close()
	value, err := engine.Execute(context.Background(), core.SplitPaneCommand{
		TargetPaneID: restored.Panes[0].ID,
		Direction:    core.SplitHorizontal,
		Pane:         core.PaneSpec{Kind: core.PaneTool, Title: "new"},
	})
	if err != nil {
		t.Fatalf("SplitPane after restore: %v", err)
	}
	if got := value.(core.CreatePaneResult).Pane.ID; got != original.NextPaneID {
		t.Fatalf("new Pane ID = %d, want saved next ID %d", got, original.NextPaneID)
	}
}

func TestLoadQuarantinesCorruptAndUnknownVersionFiles(t *testing.T) {
	for _, test := range []struct {
		name      string
		content   string
		wantCause error
	}{
		{name: "corrupt", content: "{not-json", wantCause: ErrInvalidData},
		{name: "unknown-version", content: `{"version":99,"state":{}}`, wantCause: ErrUnsupportedVersion},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "state.json")
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			now := time.Date(2026, 9, 7, 12, 34, 56, 123, time.FixedZone("test", 9*60*60))
			options := DefaultOptions()
			options.Now = func() time.Time { return now }
			result, err := Load(path, options)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if result.Status != LoadRecovered || !errors.Is(result.RecoveryCause, test.wantCause) {
				t.Fatalf("unexpected recovery: %+v", result)
			}
			wantQuarantine := path + ".invalid-20260907T033456.000000123Z"
			if result.QuarantinedPath != wantQuarantine {
				t.Fatalf("quarantine path = %q, want %q", result.QuarantinedPath, wantQuarantine)
			}
			quarantined, err := os.ReadFile(wantQuarantine)
			if err != nil {
				t.Fatalf("read quarantine: %v", err)
			}
			if string(quarantined) != test.content {
				t.Fatalf("quarantine content = %q, want %q", quarantined, test.content)
			}
			newData, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read recovered state: %v", err)
			}
			if _, err := Decode(newData, DefaultMaxBytes); err != nil {
				t.Fatalf("recovered state invalid: %v", err)
			}
		})
	}
}

func TestLoadQuarantineNamesDoNotOverwrite(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "state.json")
	if err := os.WriteFile(path, []byte("bad"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	base := path + ".invalid-20260102T030405.000000006Z"
	if err := os.WriteFile(base, []byte("existing"), 0o600); err != nil {
		t.Fatalf("write existing quarantine: %v", err)
	}
	options := DefaultOptions()
	options.Now = func() time.Time { return now }
	result, err := Load(path, options)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result.QuarantinedPath != base+"-1" {
		t.Fatalf("quarantine path = %q, want suffix", result.QuarantinedPath)
	}
	existing, err := os.ReadFile(base)
	if err != nil || string(existing) != "existing" {
		t.Fatalf("existing quarantine overwritten: data=%q err=%v", existing, err)
	}
}

func TestLoadQuarantinesOversizeState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	options := DefaultOptions()
	options.MaxBytes = 1024
	options.Now = func() time.Time { return time.Unix(0, 0) }
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 1025)), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	result, err := Load(path, options)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if result.Status != LoadRecovered || !errors.Is(result.RecoveryCause, ErrTooLarge) {
		t.Fatalf("unexpected oversize recovery: %+v", result)
	}
}

func TestStoreDebouncesAndPersistsNewestSnapshot(t *testing.T) {
	options := DefaultOptions()
	options.Debounce = 50 * time.Millisecond
	store, err := NewStore("unused", options)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close(context.Background())

	writes := make(chan []byte, 4)
	store.write = func(_ string, data []byte) error {
		writes <- append([]byte(nil), data...)
		return nil
	}
	first := core.DefaultSnapshot()
	second := snapshotWithTerminals(t)
	if err := store.Schedule(first); err != nil {
		t.Fatalf("Schedule first: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := store.Schedule(second); err != nil {
		t.Fatalf("Schedule second: %v", err)
	}
	select {
	case <-writes:
		t.Fatal("state was written before reset debounce elapsed")
	case <-time.After(25 * time.Millisecond):
	}
	var data []byte
	select {
	case data = <-writes:
	case <-time.After(time.Second):
		t.Fatal("debounced state was not written")
	}
	restored, err := Decode(data, DefaultMaxBytes)
	if err != nil {
		t.Fatalf("Decode written state: %v", err)
	}
	if len(restored.Panes) != len(second.Panes) {
		t.Fatalf("Store wrote stale Snapshot: Panes=%d want=%d", len(restored.Panes), len(second.Panes))
	}
	select {
	case <-writes:
		t.Fatal("coalesced schedules produced multiple writes")
	case <-time.After(75 * time.Millisecond):
	}
}

func TestStoreFlushAndCloseAreImmediate(t *testing.T) {
	options := DefaultOptions()
	options.Debounce = time.Hour
	store, err := NewStore("unused", options)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	var writes atomic.Int64
	store.write = func(_ string, _ []byte) error {
		writes.Add(1)
		return nil
	}
	if err := store.Schedule(core.DefaultSnapshot()); err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if err := store.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := writes.Load(); got != 1 {
		t.Fatalf("writes after Flush = %d, want 1", got)
	}
	if err := store.Schedule(snapshotWithTerminals(t)); err != nil {
		t.Fatalf("Schedule before Close: %v", err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := writes.Load(); got != 2 {
		t.Fatalf("writes after Close = %d, want 2", got)
	}
	if err := store.Schedule(core.DefaultSnapshot()); !errors.Is(err, ErrClosed) {
		t.Fatalf("Schedule after Close error = %v", err)
	}
}

func TestStoreReportsAsyncFailureAndFlushRetries(t *testing.T) {
	options := DefaultOptions()
	options.Debounce = time.Millisecond
	store, err := NewStore("unused", options)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close(context.Background())
	writeError := errors.New("write failed")
	var calls atomic.Int64
	store.write = func(_ string, _ []byte) error {
		if calls.Add(1) == 1 {
			return writeError
		}
		return nil
	}
	if err := store.Schedule(core.DefaultSnapshot()); err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	select {
	case err := <-store.Errors():
		if !errors.Is(err, writeError) {
			t.Fatalf("async error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("async write error not reported")
	}
	if err := store.Flush(context.Background()); err != nil {
		t.Fatalf("retry Flush: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("write calls = %d, want 2", got)
	}
}

func TestConcurrentScheduleAndFlush(t *testing.T) {
	options := DefaultOptions()
	options.Debounce = time.Hour
	store, err := NewStore(filepath.Join(t.TempDir(), "state.json"), options)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close(context.Background())
	snapshot := snapshotWithTerminals(t)
	var wait sync.WaitGroup
	for index := 0; index < 32; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := store.Schedule(snapshot); err != nil {
				t.Errorf("Schedule: %v", err)
			}
			if err := store.Flush(context.Background()); err != nil {
				t.Errorf("Flush: %v", err)
			}
		}()
	}
	wait.Wait()
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if _, err := Decode(data, DefaultMaxBytes); err != nil {
		t.Fatalf("concurrently saved state is invalid: %v", err)
	}
	temporaryFiles, err := filepath.Glob(filepath.Join(filepath.Dir(store.path), ".ariadne-state-*"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(temporaryFiles) != 0 {
		t.Fatalf("temporary files remain after atomic save: %v", temporaryFiles)
	}
}

func TestLoadCoreStoreReloadIntegration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	loaded, err := Load(path, DefaultOptions())
	if err != nil {
		t.Fatalf("initial Load: %v", err)
	}
	engine, err := core.NewFromSnapshot(core.Config{EventQueueCapacity: 8}, loaded.Snapshot)
	if err != nil {
		t.Fatalf("core.NewFromSnapshot: %v", err)
	}
	value, err := engine.Execute(context.Background(), core.CreatePaneCommand{
		WindowID: 1,
		Pane: core.PaneSpec{Kind: core.PaneTerminal, Terminal: &core.TerminalInstance{
			State: core.TerminalStarting, Launch: core.LaunchSpec{Argv: []string{"agent"}, CWD: "/work"},
		}},
	})
	if err != nil {
		t.Fatalf("CreatePane: %v", err)
	}
	createdPane := value.(core.CreatePaneResult).Pane
	current, err := engine.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("Core Close: %v", err)
	}

	options := DefaultOptions()
	options.Debounce = time.Hour
	store, err := NewStore(path, options)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.Schedule(current); err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if err := store.Close(context.Background()); err != nil {
		t.Fatalf("Store Close: %v", err)
	}

	reloaded, err := Load(path, DefaultOptions())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Status != LoadExisting || len(reloaded.Snapshot.Panes) != 1 {
		t.Fatalf("unexpected reloaded state: %+v", reloaded)
	}
	terminal := reloaded.Snapshot.Panes[0].Terminal
	if reloaded.Snapshot.Panes[0].ID != createdPane.ID || terminal == nil || terminal.State != core.TerminalPlaceholder {
		t.Fatalf("placeholder restoration failed: %+v", reloaded.Snapshot.Panes[0])
	}
}

func FuzzDecode(f *testing.F) {
	valid, err := Encode(core.DefaultSnapshot())
	if err != nil {
		f.Fatalf("Encode seed: %v", err)
	}
	f.Add(valid)
	f.Add([]byte("{}"))
	f.Add([]byte("{not-json"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Decode(data, 1<<20)
	})
}
