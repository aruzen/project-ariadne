package tui

import "github.com/aruzen/ariadne/internal/core"

func (session *session) contentHeight() int {
	if session.height <= 1 {
		if session.inputMode != inputModeNormal {
			return 0
		}
		return max(0, session.height)
	}
	return session.height - 1
}

func (session *session) minimumContentSize() (int, int) {
	w, h := session.presentation.MinPaneWidth, session.presentation.MinPaneHeight
	if w == 0 {
		w = 2
	}
	if h == 0 {
		h = 1
	}
	return w, h
}

func (session *session) nodeMinimum(node core.LayoutNode) (int, int) {
	if node.Kind == core.LayoutPane {
		pane, _ := session.pane(node.PaneID)
		return session.paneMinimum(pane)
	}
	w, h := 0, 0
	for _, child := range node.Children {
		cw, ch := session.nodeMinimum(child)
		if node.Direction == core.SplitHorizontal {
			w += cw
			h = max(h, ch)
		} else {
			w = max(w, cw)
			h += ch
		}
	}
	if session.paneFrame == PaneFrameSplit {
		if node.Direction == core.SplitHorizontal {
			w += max(0, len(node.Children)-1)
		} else {
			h += max(0, len(node.Children)-1)
		}
	}
	return w, h
}

func (session *session) placementFits(placement Placement) bool {
	pane, ok := session.pane(placement.PaneID)
	if !ok {
		return false
	}
	renderer, err := session.paneRenderer(pane)
	if err != nil {
		return true
	}
	rect := chromeForMode(pane, renderer, session.paneFrame).ContentRect(placement.Rect)
	w, h := session.minimumContentSize()
	return rect.W >= w && rect.H >= h
}

func (session *session) paneMinimum(pane core.Pane) (int, int) {
	w, h := session.minimumContentSize()
	if renderer, err := session.paneRenderer(pane); err == nil {
		if _, border := chromeForMode(pane, renderer, session.paneFrame).(borderChrome); border {
			w += 2
			h += 2
		}
	}
	return w, h
}

func (session *session) canSplit(direction core.SplitDirection, newPane core.Pane) bool {
	if session.focus == 0 {
		return true
	}
	placement, ok := placementFor(session.allPlacements, session.focus)
	if !ok || session.compact {
		session.setMessage("not enough space to split at the minimum pane size")
		return false
	}
	old, _ := session.pane(session.focus)
	w, h := session.paneMinimum(old)
	nw, nh := session.paneMinimum(newPane)
	separator := 0
	if session.paneFrame == PaneFrameSplit {
		separator = 1
	}
	// Weight halving must leave enough space in each half, not just in total.
	valid := true
	if direction == core.SplitHorizontal {
		valid = (placement.Rect.W-separator)/2 >= max(w, nw) && placement.Rect.H >= max(h, nh)
	} else {
		valid = (placement.Rect.H-separator)/2 >= max(h, nh) && placement.Rect.W >= max(w, nw)
	}
	if !valid {
		session.setMessage("not enough space to split at the minimum pane size")
	}
	return valid
}

func (session *session) chrome(pane core.Pane, renderer paneRenderer, rect Rect) paneChrome {
	chrome := chromeForMode(pane, renderer, session.paneFrame)
	if session.compact && (chrome.ContentRect(rect).W < 1 || chrome.ContentRect(rect).H < 1) {
		return noChrome{}
	}
	return chrome
}
