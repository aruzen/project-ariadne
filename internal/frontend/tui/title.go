package tui

import (
	"github.com/aruzen/ariadne/internal/config"
	"github.com/aruzen/ariadne/internal/core"
)

func (session *session) terminalTitle(pane core.Pane) string {
	if pane.Terminal == nil {
		return ""
	}
	if view := session.views[pane.ID]; view != nil {
		if content, ok := view.content.(*terminalPaneContent); ok && content.terminal != nil {
			return sanitizeWidgetText(content.terminal.Title())
		}
	}
	return ""
}

func (session *session) displayPaneTitle(pane core.Pane) string {
	if pane.ID == 0 {
		return ""
	}
	mode := session.presentation.PaneTitle
	if mode == config.PaneTitlePane {
		return sanitizeWidgetText(paneTitle(pane))
	}
	if manual := sanitizeWidgetText(pane.Title); manual != "" && mode != config.PaneTitleTerminal {
		return manual
	}
	if title := session.terminalTitle(pane); title != "" {
		return title
	}
	return sanitizeWidgetText(paneTitle(pane))
}
