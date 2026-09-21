// Package version exposes build metadata injected into release binaries.
package version

import "fmt"

var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

func String() string {
	if Version == "dev" && Commit == "unknown" && Date == "unknown" {
		return "ariadne dev"
	}
	return fmt.Sprintf("ariadne %s (commit %s, built %s)", Version, Commit, Date)
}
