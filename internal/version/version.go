// Package version exposes build information injected via -ldflags.
package version

import "fmt"

// Set at build time: -ldflags "-X github.com/ape-dev-de/acat/internal/version.Version=v0.1.0 ..."
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a human-readable version line.
func String(name string) string {
	return fmt.Sprintf("%s %s (commit %s, built %s)", name, Version, Commit, Date)
}
