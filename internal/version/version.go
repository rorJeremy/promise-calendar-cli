// Package version reports which build of pcal is running. Release builds
// will set Version, Commit, and Date with -ldflags "-X …" (the goreleaser
// config the plan adds in Phase 14); a plain `go build` keeps the defaults.
package version

import (
	"fmt"
	"runtime"
)

// Version is the release version without a leading "v", e.g. "0.1.0".
var Version = "dev"

// Commit is the git commit the binary was built from.
var Commit = "none"

// Date is when the binary was built, in RFC 3339.
var Date = "unknown"

// UserAgent returns the User-Agent header pcal sends with every request,
// e.g. "pcal/0.1.0 (darwin/arm64)".
func UserAgent() string {
	return fmt.Sprintf("pcal/%s (%s/%s)", Version, runtime.GOOS, runtime.GOARCH)
}
