package tui

import (
	"testing"
	"time"

	"github.com/aruzen/ariadne/internal/core"
)

func TestSortedAttentionsPrioritizesSeverityThenRecency(t *testing.T) {
	base := time.Unix(500, 0).UTC()
	acknowledged := base.Add(time.Second)
	values := []core.Attention{
		{ID: 1, Severity: core.SeverityInfo, UpdatedAt: base.Add(3 * time.Second)},
		{ID: 2, Severity: core.SeverityError, UpdatedAt: base},
		{ID: 3, Severity: core.SeverityError, UpdatedAt: base.Add(time.Second)},
		{ID: 4, Severity: core.SeverityCritical, UpdatedAt: base.Add(2 * time.Second), AcknowledgedAt: &acknowledged},
	}
	all := sortedAttentions(values, false)
	if len(all) != 4 || all[0].ID != 4 || all[1].ID != 3 || all[2].ID != 2 || all[3].ID != 1 {
		t.Fatalf("sorted = %+v", all)
	}
	unread := sortedAttentions(values, true)
	if len(unread) != 3 || unread[0].ID != 3 || unread[1].ID != 2 || unread[2].ID != 1 {
		t.Fatalf("unread = %+v", unread)
	}
}

func TestStashedPreviewUsesFullscreenWithoutChangingCoreLayout(t *testing.T) {
	session := session{
		width: 80, height: 24, workspace: 1, window: 1, focus: 1,
		views: make(map[core.PaneID]*paneView),
		snapshot: core.Snapshot{
			Workspaces:   []core.Workspace{{ID: 1, Name: "default", WindowIDs: []core.WindowID{1}}, {ID: 2, Name: "other"}},
			Windows:      []core.Window{{ID: 1, WorkspaceID: 1, Name: "main", Layout: &core.LayoutNode{Kind: core.LayoutPane, PaneID: 1}}, {ID: 2, WorkspaceID: 2, Name: "stash"}},
			Panes:        []core.Pane{{ID: 1, WindowID: 1, Kind: core.PaneTool}, {ID: 2, WindowID: 2, Kind: core.PaneTool}},
			StashedPanes: []core.StashedPane{{PaneID: 2, OriginWorkspaceID: 2, OriginWindowID: 2}},
		},
	}
	session.previewPaneByID(2)
	if session.focus != 2 || session.previewPane != 2 || session.previewPreviousFocus != 1 || len(session.placements) != 1 {
		t.Fatalf("preview state = focus:%d preview:%d previous:%d placements:%+v", session.focus, session.previewPane, session.previewPreviousFocus, session.placements)
	}
	if session.placements[0].PaneID != 2 || session.placements[0].Rect != (Rect{W: 80, H: 23}) {
		t.Fatalf("preview placement = %+v", session.placements[0])
	}
	if session.snapshot.Windows[0].Layout == nil || session.snapshot.Windows[0].Layout.PaneID != 1 {
		t.Fatalf("Core layout was changed: %+v", session.snapshot.Windows[0].Layout)
	}
}
