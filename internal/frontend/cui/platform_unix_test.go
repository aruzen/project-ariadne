//go:build darwin || linux

package cui

import "testing"

func TestUnixPlatformCommandDefaults(t *testing.T) {
	t.Setenv("SHELL", "/bin/test-shell")
	t.Setenv("VISUAL", "test-visual")
	t.Setenv("EDITOR", "test-editor")
	if got := defaultShell(); len(got) != 1 || got[0] != "/bin/test-shell" {
		t.Fatalf("defaultShell = %q", got)
	}
	if got := defaultEditor(); len(got) != 1 || got[0] != "test-visual" {
		t.Fatalf("defaultEditor = %q", got)
	}
}
