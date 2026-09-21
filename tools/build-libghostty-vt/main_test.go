package main

import "testing"

func TestResolveTarget(t *testing.T) {
	tests := []struct {
		name    string
		zig     string
		archive string
	}{
		{name: "darwin-arm64", zig: "aarch64-macos", archive: "libghostty-vt.a"},
		{name: "darwin-amd64", zig: "x86_64-macos", archive: "libghostty-vt.a"},
		{name: "linux-amd64", zig: "x86_64-linux-gnu", archive: "libghostty-vt.a"},
		{name: "linux-arm64", zig: "aarch64-linux-gnu", archive: "libghostty-vt.a"},
		{name: "windows-amd64", zig: "x86_64-windows-gnu", archive: "ghostty-vt-static.lib"},
		{name: "windows-arm64", zig: "aarch64-windows-gnu", archive: "ghostty-vt-static.lib"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual, err := resolveTarget(test.name)
			if err != nil {
				t.Fatal(err)
			}
			if actual.name != test.name || actual.zig != test.zig || actual.archive != test.archive {
				t.Fatalf("resolveTarget(%q) = %#v", test.name, actual)
			}
		})
	}
}

func TestResolveTargetRejectsUnsupportedTarget(t *testing.T) {
	if _, err := resolveTarget("plan9-amd64"); err == nil {
		t.Fatal("resolveTarget accepted an unsupported target")
	}
}
