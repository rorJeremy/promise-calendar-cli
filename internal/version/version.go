// Package version reports which build of pcal is running. Release builds
// will set Version, Commit, and Date with -ldflags "-X …" (the goreleaser
// config the plan adds in Phase 14); a plain `go build` keeps the defaults.
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
)

// Version is the release version without a leading "v", e.g. "0.1.0".
var Version = "dev"

// Commit is the git commit the binary was built from.
var Commit = "none"

// Date is when the binary was built, in RFC 3339.
var Date = "unknown"

// ModuleVersion is debug.ReadBuildInfo().Main.Version. A `go install` build
// has no ldflags, so Version stays "dev" and this carries the module
// version (v0.1.0, or a pseudo-version). "(devel)" means there isn't one.
// Tests replace it.
var ModuleVersion = moduleVersion()

func moduleVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	return info.Main.Version
}

// Shown is the version a person sees. ldflags win. When Version is still
// "dev", a module version is used with one leading "v" removed, so a
// `go install …@v0.1.0` build reports 0.1.0 (Jeremy's Gate 2 decision,
// Phase 13).
func Shown() string {
	if Version != "dev" {
		return Version
	}
	v := strings.TrimSpace(ModuleVersion)
	if v == "" || v == "(devel)" {
		return "dev"
	}
	return strings.TrimPrefix(v, "v")
}

// UserAgent returns the User-Agent header pcal sends with every request,
// e.g. "pcal/0.1.0 (darwin/arm64)". It uses Shown, so a go install build
// names the module version it was installed at.
func UserAgent() string {
	return fmt.Sprintf("pcal/%s (%s/%s)", Shown(), runtime.GOOS, runtime.GOARCH)
}
