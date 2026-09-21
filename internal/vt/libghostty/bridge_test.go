//go:build cgo && (darwin || linux || windows) && (amd64 || arm64)

package libghostty

import "testing"

func TestVersion(t *testing.T) {
	version, err := Version()
	if err != nil {
		t.Fatal(err)
	}
	if version == "" {
		t.Fatal("Version returned an empty string")
	}
}
