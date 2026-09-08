package tui

import (
	"reflect"
	"testing"

	"github.com/aruzen/ariadne/internal/core"
)

func TestInputDecoder(t *testing.T) {
	decoder := inputDecoder{}
	data, actions := decoder.Feed([]byte{'a', 0x01})
	if string(data) != "a" || len(actions) != 0 {
		t.Fatalf("first feed = %q, %v", data, actions)
	}
	data, actions = decoder.Feed([]byte{'j', 0x01, 0x01, 0x01, 'x'})
	if string(data) != "\x01\x01x" || !reflect.DeepEqual(actions, []inputAction{actionFocusDown}) {
		t.Fatalf("second feed = %q, %v", data, actions)
	}
}

func TestPreferredFocusChoosesRunningPane(t *testing.T) {
	runningID := core.TerminalID(3)
	session := session{
		placements: []Placement{{PaneID: 1}, {PaneID: 2}},
		snapshot: core.Snapshot{Panes: []core.Pane{
			{ID: 1, Terminal: &core.TerminalInstance{State: core.TerminalExited}},
			{ID: 2, Terminal: &core.TerminalInstance{ID: &runningID, State: core.TerminalRunning}},
		}},
	}
	if got := session.preferredFocus(); got != 2 {
		t.Fatalf("preferred focus = %d", got)
	}
}

func TestDirectionalDistance(t *testing.T) {
	primary, secondary, valid := directionalDistance(actionFocusRight, 10, 10, 20, 13)
	if !valid || primary != 10 || secondary != 3 {
		t.Fatalf("distance = %d, %d, %t", primary, secondary, valid)
	}
	if _, _, valid := directionalDistance(actionFocusLeft, 10, 10, 20, 13); valid {
		t.Fatal("accepted candidate on the wrong side")
	}
}
