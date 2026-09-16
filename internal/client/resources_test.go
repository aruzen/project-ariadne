package client

import (
	"strings"
	"testing"

	"github.com/aruzen/ariadne/internal/core"
)

func TestResourceTargets(t *testing.T) {
	for _, args := range [][]string{{"12"}, {"window", "12"}} {
		unit, id, err := ParseResourceTarget(args, nil)
		if err != nil || unit != ResourceWindow || id != 12 {
			t.Fatalf("target=%s/%d %v", unit, id, err)
		}
	}
	unit, id, err := ParseResourceTarget([]string{"pane"}, map[ResourceUnit]uint64{ResourcePane: 4})
	if err != nil || unit != ResourcePane || id != 4 {
		t.Fatal(unit, id, err)
	}
	for _, args := range [][]string{nil, {"pane"}, {"0"}, {"-1"}, {"unknown", "4"}, {"pane", "4", "5"}} {
		if _, _, err := ParseResourceTarget(args, nil); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

func TestResourceListsUseSnapshotAndIncludeToolsAndStash(t *testing.T) {
	s := core.Snapshot{Revision: 42, Workspaces: []core.Workspace{{ID: 1, WindowIDs: []core.WindowID{1}}}, Windows: []core.Window{{ID: 1, WorkspaceID: 1}, {ID: 2, WorkspaceID: 1}}, Panes: []core.Pane{{ID: 1, WindowID: 1, Kind: core.PaneTool}, {ID: 2, WindowID: 2, Kind: core.PaneTerminal}}, StashedWindows: []core.StashedWindow{{WindowID: 2}}}
	panes := ListResources(s, ResourcePane)
	if len(panes.Entries) != 2 || panes.Revision != 42 || !panes.Entries[1].Stashed {
		t.Fatalf("panes=%+v", panes)
	}
	windows := ListResources(s, ResourceWindow)
	if len(windows.Entries) != 2 || windows.Entries[0].PaneCount != 1 || !windows.Entries[1].Stashed {
		t.Fatalf("windows=%+v", windows)
	}
	if len(panes.Rows()) != 3 {
		t.Fatal("missing tool row")
	}
	s.Panes = append(s.Panes, core.Pane{ID: 3, WindowID: 1, Kind: core.PaneTool, Title: "unsafe\x1b[2J\n"})
	s.StashedPanes = []core.StashedPane{{PaneID: 3}}
	if got := ListResources(s, ResourceWindow).Entries[0].PaneCount; got != 1 {
		t.Fatalf("individually stashed pane counted as attached: %d", got)
	}
	workspace := ListResources(s, ResourceWorkspace).Entries[0]
	if workspace.WindowCount != 1 || workspace.PaneCount != 1 {
		t.Fatalf("workspace membership includes stash origins: %+v", workspace)
	}
	for _, row := range ListResources(s, ResourcePane).Rows() {
		if strings.ContainsAny(strings.Join(row, " "), "\x1b\n") {
			t.Fatal("terminal controls escaped into list output")
		}
	}
}
