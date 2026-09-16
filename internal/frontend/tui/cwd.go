package tui

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aruzen/ariadne/internal/core"
)

// Tool content may report a frontend-local cwd without exposing VT or core state.
type paneWorkingDirectory interface{ WorkingDirectory() string }

func validDirectory(path string) string {
	if runtime.GOOS == "windows" && strings.HasPrefix(filepath.VolumeName(path), "\\\\") {
		return ""
	}
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return ""
	}
	return filepath.Clean(path)
}

func localOSC7Directory(value string) string {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "file" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	hostname, _ := os.Hostname()
	if u.Host != "" && !strings.EqualFold(u.Host, "localhost") && !strings.EqualFold(u.Host, hostname) {
		return ""
	}
	path := u.Path
	if runtime.GOOS == "windows" && len(path) > 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return validDirectory(filepath.FromSlash(path))
}

func (session *session) paneCWD(pane core.Pane) string {
	if pane.Terminal != nil {
		if view := session.views[pane.ID]; view != nil {
			if content, ok := view.content.(*terminalPaneContent); ok && content.terminal != nil {
				if cwd := localOSC7Directory(content.terminal.WorkingDirectory()); cwd != "" {
					return cwd
				}
			}
		}
		if cwd := validDirectory(pane.Terminal.Launch.CWD); cwd != "" {
			return cwd
		}
	}
	if view := session.views[pane.ID]; view != nil {
		if provider, ok := view.content.(paneWorkingDirectory); ok {
			if cwd := validDirectory(safeWorkingDirectory(provider)); cwd != "" {
				return cwd
			}
		}
	}
	return session.cwd
}

func safeWorkingDirectory(provider paneWorkingDirectory) (cwd string) {
	defer func() {
		if recover() != nil {
			cwd = ""
		}
	}()
	return provider.WorkingDirectory()
}

func (session *session) widgetCWD(explicit string) string {
	pane, _ := session.pane(session.focus)
	source := session.presentation.CWD.Tool
	if pane.Kind == core.PaneTerminal {
		source = session.presentation.CWD.Terminal
	}
	if value, ok := session.presentation.CWD.Kinds[string(pane.Kind)]; ok {
		source = value
	}
	if pane.Tool != nil {
		if value, ok := session.presentation.CWD.Tools[pane.Tool.Provider+"/"+pane.Tool.Type]; ok {
			source = value
		}
	}
	if explicit != "" {
		source = explicit
	}
	switch source {
	case "pane":
		return session.paneCWD(pane)
	case "", "startup":
		return session.cwd
	default:
		return source
	}
}
