//go:build cgo && (darwin || (linux && amd64) || (windows && amd64))

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
