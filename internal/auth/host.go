package auth

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// HeadlessReason says why a browser opened here wouldn't reach the person
// at the terminal, for the line "Not opening a browser here (<reason>)", or
// "" when opening one is worth trying. As basecamp-cli's
// hostutil.HeadlessReason, it reads only the environment (through getenv)
// and goos: an SSH session, a CI runner (CI set to 1, true, yes, or on), or
// a Unix with neither an X11 nor a Wayland display. macOS and Windows have
// no display variable to check.
func HeadlessReason(getenv func(string) string, goos string) string {
	switch {
	case getenv("SSH_CONNECTION") != "" || getenv("SSH_CLIENT") != "" || getenv("SSH_TTY") != "":
		return "SSH session"
	case truthy(getenv("CI")):
		return "CI environment"
	case needsDisplay(goos) && getenv("DISPLAY") == "" && getenv("WAYLAND_DISPLAY") == "":
		return "no display"
	default:
		return ""
	}
}

// truthy reports whether an environment value means yes.
func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// needsDisplay reports whether goos's browsers need an X11 or Wayland
// display.
func needsDisplay(goos string) bool {
	switch goos {
	case "linux", "freebsd", "openbsd", "netbsd", "dragonfly", "solaris", "illumos":
		return true
	default:
		return false
	}
}

// OpenBrowser opens target in the default browser and returns without
// waiting for it, so only a failure to start the opener is an error.
func OpenBrowser(target string) error {
	name, args, err := browserCommand(runtime.GOOS, target)
	if err != nil {
		return err
	}
	return exec.Command(name, args...).Start() //nolint:gosec // G204: a fixed opener, and target is pcal's own URL
}

// browserCommand returns the command that opens target on goos, as
// basecamp-cli's hostutil.OpenBrowser does.
func browserCommand(goos, target string) (string, []string, error) {
	switch goos {
	case "darwin":
		return "open", []string{target}, nil
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", target}, nil
	case "linux", "freebsd", "openbsd", "netbsd", "dragonfly", "solaris", "illumos":
		return "xdg-open", []string{target}, nil
	default:
		return "", nil, fmt.Errorf("pcal doesn't know how to open a browser on %s", goos)
	}
}
