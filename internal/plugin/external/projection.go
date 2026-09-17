package external

import (
	v1 "github.com/aruzen/ariadne/api/plugin/v1"
	"github.com/aruzen/ariadne/internal/core"
)

func PublicExit(e *core.TerminalExit) *v1.Exit {
	if e == nil {
		return nil
	}
	return &v1.Exit{Kind: string(e.Kind), Code: e.Code, Signal: e.Signal, Message: e.Message}
}
func publicDescriptor(t core.ToolDescriptor) v1.ToolDescriptor {
	return v1.ToolDescriptor{Provider: t.Provider, Type: t.Type, Instance: t.Instance}
}
func publicTool(t core.ToolInstance) v1.ToolInstance {
	return v1.ToolInstance{Descriptor: publicDescriptor(t.Descriptor), StateVersion: t.StateVersion, Generation: t.Generation, State: t.State}
}

// PublicPane is also used by the daemon I/O broker. No internal type is serialized
// directly onto the external plugin boundary.
func PublicPane(p core.Pane) v1.Pane {
	result := v1.Pane{ID: uint64(p.ID), WindowID: uint64(p.WindowID), Kind: string(p.Kind), Title: p.Title, Presentation: v1.Presentation{Chrome: string(p.Presentation.Chrome)}}
	if t := p.Terminal; t != nil {
		result.Terminal = &v1.Terminal{State: string(t.State), Launch: v1.Launch{Argv: t.Launch.Argv, CWD: t.Launch.CWD}, Exit: PublicExit(t.Exit), HistoryAvailable: t.HistoryAvailable}
		if t.ID != nil {
			id := uint64(*t.ID)
			result.Terminal.ID = &id
		}
	}
	if p.Tool != nil {
		t := publicDescriptor(*p.Tool)
		result.Tool = &t
	}
	return result
}
func publicLayout(n *core.LayoutNode) *v1.Layout {
	if n == nil {
		return nil
	}
	r := &v1.Layout{Kind: string(n.Kind), SplitID: uint64(n.SplitID), PaneID: uint64(n.PaneID), Direction: string(n.Direction), Weights: n.Weights}
	for _, child := range n.Children {
		r.Children = append(r.Children, *publicLayout(&child))
	}
	return r
}
