package client

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/aruzen/ariadne/internal/core"
)

type ResourceUnit string

const (
	ResourcePane      ResourceUnit = "pane"
	ResourceWindow    ResourceUnit = "window"
	ResourceWorkspace ResourceUnit = "workspace"
)

func ParseResourceUnit(value string) (ResourceUnit, error) {
	unit := ResourceUnit(value)
	switch unit {
	case ResourcePane, ResourceWindow, ResourceWorkspace:
		return unit, nil
	default:
		return "", fmt.Errorf("unknown unit %q; use pane, window, or workspace", value)
	}
}

func ParseResourceTarget(arguments []string, defaults map[ResourceUnit]uint64) (ResourceUnit, uint64, error) {
	unit := ResourceWindow
	if len(arguments) != 0 {
		if candidate, err := ParseResourceUnit(arguments[0]); err == nil {
			unit, arguments = candidate, arguments[1:]
		}
	}
	if len(arguments) == 0 && defaults != nil && defaults[unit] != 0 {
		return unit, defaults[unit], nil
	}
	if len(arguments) != 1 {
		return "", 0, fmt.Errorf("usage: delete [pane|window|workspace] ID")
	}
	id, err := strconv.ParseUint(arguments[0], 10, 64)
	if err != nil || id == 0 {
		return "", 0, fmt.Errorf("delete requires a positive ID")
	}
	return unit, id, nil
}

type ResourceEntry struct {
	Pane        *core.Pane      `json:"pane,omitempty"`
	Window      *core.Window    `json:"window,omitempty"`
	Workspace   *core.Workspace `json:"workspace,omitempty"`
	PaneCount   int             `json:"pane_count"`
	WindowCount int             `json:"window_count"`
	Stashed     bool            `json:"stashed"`
}

type ResourceList struct {
	Revision uint64          `json:"revision"`
	Unit     ResourceUnit    `json:"unit"`
	Entries  []ResourceEntry `json:"entries"`
}

// ListResources builds a coherent view from one synchronized Snapshot.
func ListResources(snapshot core.Snapshot, unit ResourceUnit) ResourceList {
	result := ResourceList{Revision: snapshot.Revision, Unit: unit, Entries: []ResourceEntry{}}
	stashedPanes := make(map[core.PaneID]bool)
	stashedWindows := make(map[core.WindowID]bool)
	for _, stash := range snapshot.StashedPanes {
		stashedPanes[stash.PaneID] = true
	}
	for _, stash := range snapshot.StashedWindows {
		stashedWindows[stash.WindowID] = true
	}
	counts := make(map[core.WindowID]int)
	for _, pane := range snapshot.Panes {
		if !stashedPanes[pane.ID] {
			counts[pane.WindowID]++
		}
	}
	switch unit {
	case ResourcePane:
		for _, pane := range snapshot.Panes {
			result.Entries = append(result.Entries, ResourceEntry{Pane: &pane, Stashed: stashedPanes[pane.ID] || stashedWindows[pane.WindowID]})
		}
	case ResourceWindow:
		for _, window := range snapshot.Windows {
			result.Entries = append(result.Entries, ResourceEntry{Window: &window, PaneCount: counts[window.ID], Stashed: stashedWindows[window.ID]})
		}
	case ResourceWorkspace:
		for _, workspace := range snapshot.Workspaces {
			entry := ResourceEntry{Workspace: &workspace, WindowCount: len(workspace.WindowIDs)}
			for _, id := range workspace.WindowIDs {
				entry.PaneCount += counts[id]
			}
			result.Entries = append(result.Entries, entry)
		}
	}
	return result
}

func (list ResourceList) Rows() [][]string {
	rows := [][]string{}
	switch list.Unit {
	case ResourcePane:
		rows = append(rows, []string{"PANE", "WINDOW", "KIND", "STATE", "TERMINAL", "STASHED", "TITLE", "CWD"})
		for _, entry := range list.Entries {
			pane := entry.Pane
			state, terminal, cwd := "-", "-", "-"
			if pane.Terminal != nil {
				state, cwd = string(pane.Terminal.State), pane.Terminal.Launch.CWD
				if pane.Terminal.ID != nil {
					terminal = fmt.Sprint(*pane.Terminal.ID)
				}
			}
			rows = append(rows, []string{fmt.Sprint(pane.ID), fmt.Sprint(pane.WindowID), string(pane.Kind), state, terminal, strconv.FormatBool(entry.Stashed), pane.Title, cwd})
		}
	case ResourceWindow:
		rows = append(rows, []string{"WINDOW", "WORKSPACE", "PANES", "STASHED", "NAME"})
		for _, entry := range list.Entries {
			rows = append(rows, []string{fmt.Sprint(entry.Window.ID), fmt.Sprint(entry.Window.WorkspaceID), fmt.Sprint(entry.PaneCount), strconv.FormatBool(entry.Stashed), entry.Window.Name})
		}
	case ResourceWorkspace:
		rows = append(rows, []string{"WORKSPACE", "WINDOWS", "PANES", "NAME"})
		for _, entry := range list.Entries {
			rows = append(rows, []string{fmt.Sprint(entry.Workspace.ID), fmt.Sprint(entry.WindowCount), fmt.Sprint(entry.PaneCount), entry.Workspace.Name})
		}
	}
	for _, row := range rows {
		for i, value := range row {
			row[i] = strings.Map(func(r rune) rune {
				if unicode.IsControl(r) {
					return ' '
				}
				return r
			}, value)
		}
	}
	return rows
}
