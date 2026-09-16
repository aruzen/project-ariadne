package config

import (
	"strings"
	"testing"
)

func TestFlatKeybindingsHaveMigrationError(t *testing.T) {
	_, err := Parse([]byte("[keybindings]\n\"ctrl-a h\"=\"focus right\""))
	if err == nil || !strings.Contains(err.Error(), "[keybindings.normal]") {
		t.Fatalf("error=%v", err)
	}
}

func TestPresentationOverridesAndValidation(t *testing.T) {
	configuration, err := Parse([]byte(`[tui]
mouse=false
pane_title="terminal"
min_pane_width=4
[tui.theme.focused_border]
foreground="#112233"
[tui.theme.lines]
horizontal="-"
[tui.status]
left=["cwd", "git"]
right=[]
[tui.status.widgets.git]
command=["git", "branch", "--show-current"]
cwd="pane"
format=" {output} "
style="accent"
[keybindings.copy]
"v"="select"
[keybindings.prompt]
"ctrl-g"="cancel"
`))
	if err != nil {
		t.Fatal(err)
	}
	if configuration.TUI.Mouse || configuration.TUI.PaneTitle != PaneTitleTerminal || configuration.TUI.MinPaneWidth != 4 || configuration.TUI.Theme.FocusedBorder.Foreground != "#112233" || configuration.TUI.Theme.FocusedBorder.Background != DefaultTheme().FocusedBorder.Background || configuration.Keybindings.Copy["v"] != "select" {
		t.Fatalf("config=%+v", configuration)
	}
	for _, data := range []string{`[tui]
pane_title="unknown"`, `[tui]
min_pane_height=0`, `[tui.theme.base]
foreground="red"`, `[tui.theme.lines]
horizontal="界"`, `[tui.theme.base]
typo="red"`, `[tui.status]
left=["missing"]`, `[tui.status.widgets.x]
command=["echo"]
timeout_ms=-1`, `[tui.cwd]
terminal="relative/path"`} {
		if _, err := Parse([]byte(data)); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
}
