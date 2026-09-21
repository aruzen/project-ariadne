package version

import "testing"

func TestString(t *testing.T) {
	originalVersion, originalCommit, originalDate := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = originalVersion, originalCommit, originalDate })

	Version, Commit, Date = "dev", "unknown", "unknown"
	if got := String(); got != "ariadne dev" {
		t.Fatal(got)
	}
	Version, Commit, Date = "v1.0.0", "0123456789ab", "2026-09-22T00:00:00Z"
	if got := String(); got != "ariadne v1.0.0 (commit 0123456789ab, built 2026-09-22T00:00:00Z)" {
		t.Fatal(got)
	}
}
