package auth

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// HeadlessReason follows basecamp-cli's hostutil.HeadlessReason (8655e70,
// internal/hostutil/hostutil.go:125-178): the environment and the OS only.
func TestHeadlessReason(t *testing.T) {
	tests := []struct {
		name string
		goos string
		env  map[string]string
		want string
	}{
		{"a Mac", "darwin", nil, ""},
		{"Windows", "windows", nil, ""},
		{"Linux with X11", "linux", map[string]string{"DISPLAY": ":0"}, ""},
		{"Linux with Wayland", "linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, ""},
		{"SSH_CONNECTION", "darwin", map[string]string{"SSH_CONNECTION": "10.0.0.2 52000 10.0.0.1 22"}, "SSH session"},
		{"SSH_CLIENT", "linux", map[string]string{"SSH_CLIENT": "10.0.0.2 52000 22", "DISPLAY": ":0"}, "SSH session"},
		{"SSH_TTY", "darwin", map[string]string{"SSH_TTY": "/dev/ttys003"}, "SSH session"},
		{"CI=true", "darwin", map[string]string{"CI": "true"}, "CI environment"},
		{"CI=1", "darwin", map[string]string{"CI": "1"}, "CI environment"},
		{"CI=Yes", "darwin", map[string]string{"CI": " Yes "}, "CI environment"},
		{"CI=on", "darwin", map[string]string{"CI": "on"}, "CI environment"},
		{"CI=false isn't CI", "darwin", map[string]string{"CI": "false"}, ""},
		{"CI=0 isn't CI", "darwin", map[string]string{"CI": "0"}, ""},
		{"Linux without a display", "linux", nil, "no display"},
		{"FreeBSD without a display", "freebsd", nil, "no display"},
		{"illumos without a display", "illumos", nil, "no display"},
		// macOS and Windows have no display variable to check.
		{"a Mac without DISPLAY", "darwin", map[string]string{"TERM": "xterm"}, ""},
		{"SSH comes first", "linux", map[string]string{"SSH_TTY": "/dev/pts/0", "CI": "true"}, "SSH session"},
		{"then CI", "linux", map[string]string{"CI": "true"}, "CI environment"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, HeadlessReason(env(tt.env), tt.goos))
		})
	}
}

// As basecamp-cli's hostutil.OpenBrowser (hostutil.go:181-199).
func TestBrowserCommand(t *testing.T) {
	const target = "https://calendar.promisechurch.io/cli/authorization/new?port=51234&state=s"
	tests := []struct {
		goos     string
		wantName string
		wantArgs []string
	}{
		{"darwin", "open", []string{target}},
		{"linux", "xdg-open", []string{target}},
		{"freebsd", "xdg-open", []string{target}},
		{"windows", "rundll32", []string{"url.dll,FileProtocolHandler", target}},
	}

	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			name, args, err := browserCommand(tt.goos, target)

			require.NoError(t, err)
			require.Equal(t, tt.wantName, name)
			require.Equal(t, tt.wantArgs, args)
		})
	}

	_, _, err := browserCommand("plan9", target)
	require.EqualError(t, err, "pcal doesn't know how to open a browser on plan9")
}
