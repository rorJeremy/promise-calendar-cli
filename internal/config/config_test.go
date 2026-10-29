package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rorJeremy/promise-calendar-cli/internal/output"
)

// env returns a getenv func that reads from vars.
func env(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

func TestDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	xdg := t.TempDir()

	tests := []struct {
		name string
		vars map[string]string
		want string
	}{
		{"XDG_CONFIG_HOME wins", map[string]string{"XDG_CONFIG_HOME": xdg}, filepath.Join(xdg, "pcal")},
		// The design keeps ~/.config on macOS too, so not os.UserConfigDir.
		{"otherwise ~/.config", nil, filepath.Join(home, ".config", "pcal")},
		// The XDG spec says to ignore a relative path.
		{"a relative XDG_CONFIG_HOME is ignored", map[string]string{"XDG_CONFIG_HOME": "relative/dir"}, filepath.Join(home, ".config", "pcal")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, err := Dir(env(tt.vars))

			require.NoError(t, err)
			require.Equal(t, tt.want, dir)
		})
	}
}

func TestLoadWithNoFileIsEmpty(t *testing.T) {
	c, err := Load(t.TempDir())

	require.NoError(t, err)
	require.Equal(t, Config{}, c)
}

func TestSaveThenLoad(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pcal")
	want := Config{URL: "http://localhost:3000", TimeZone: "America/Chicago"}

	require.NoError(t, Save(dir, want))
	got, err := Load(dir)

	require.NoError(t, err)
	require.Equal(t, want, got)

	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	require.NoError(t, err)
	require.JSONEq(t, `{"url":"http://localhost:3000","time_zone":"America/Chicago"}`, string(data))
}

func TestSavePermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pcal")
	path := filepath.Join(dir, "config.json")

	require.NoError(t, Save(dir, Config{URL: "http://localhost:3000"}))
	requireMode(t, dir, 0o700)
	requireMode(t, path, 0o600)

	// A file left with looser permissions is replaced with a 0600 one.
	require.NoError(t, os.Chmod(path, 0o644))
	require.NoError(t, Save(dir, Config{URL: "http://localhost:4000"}))
	requireMode(t, path, 0o600)
}

func TestWriteFileReplacesAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")

	require.NoError(t, WriteFile(path, []byte("first")))
	require.NoError(t, WriteFile(path, []byte("second")))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "second", string(data))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temporary files are left behind")
}

func TestLoadRejectsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))

	_, err := Load(dir)

	require.ErrorContains(t, err, path)
}

func TestResolveURL(t *testing.T) {
	saved := Config{URL: "https://saved.example.com"}

	tests := []struct {
		name    string
		flag    string
		vars    map[string]string
		config  Config
		want    string
		wantErr string
	}{
		{name: "the default", want: DefaultURL},
		{name: "config beats the default", config: saved, want: "https://saved.example.com"},
		{name: "PCAL_URL beats config", vars: map[string]string{"PCAL_URL": "https://env.example.com"}, config: saved, want: "https://env.example.com"},
		{name: "--url beats PCAL_URL", flag: "http://localhost:3000", vars: map[string]string{"PCAL_URL": "https://env.example.com"}, config: saved, want: "http://localhost:3000"},
		{name: "an empty PCAL_URL counts as unset", vars: map[string]string{"PCAL_URL": ""}, config: saved, want: "https://saved.example.com"},
		{name: "trailing slashes are dropped", flag: "https://calendar.promisechurch.io/", want: "https://calendar.promisechurch.io"},
		{name: "scheme and host are lowercased", flag: "HTTPS://Calendar.PromiseChurch.io", want: "https://calendar.promisechurch.io"},
		{name: "a missing scheme is rejected", flag: "calendar.promisechurch.io", wantErr: `invalid calendar URL "calendar.promisechurch.io"`},
		{name: "another scheme is rejected", flag: "ftp://calendar.promisechurch.io", wantErr: "invalid calendar URL"},
		{name: "a missing host is rejected", flag: "https://", wantErr: "invalid calendar URL"},
		{name: "a query is rejected", flag: "https://calendar.promisechurch.io/?x=1", wantErr: "invalid calendar URL"},
		{name: "an invalid PCAL_URL is rejected", vars: map[string]string{"PCAL_URL": "nope"}, wantErr: "invalid calendar URL"},
		// http would send the token unencrypted: loopback only.
		{name: "http to a public host is rejected", flag: "http://calendar.promisechurch.io", wantErr: `invalid calendar URL "http://calendar.promisechurch.io": http would send your token unencrypted, so pcal allows it only for localhost, 127.0.0.0/8, and ::1; use https`},
		{name: "http to a private address is rejected", flag: "http://10.0.0.5:3000", wantErr: "http would send your token unencrypted"},
		{name: "http to a name containing localhost is rejected", flag: "http://evil.localhost.example.com", wantErr: "http would send your token unencrypted"},
		{name: "http to localhost", flag: "http://localhost:3000", want: "http://localhost:3000"},
		{name: "http to 127.0.0.1", flag: "http://127.0.0.1:3000", want: "http://127.0.0.1:3000"},
		{name: "http to anywhere in 127.0.0.0/8", flag: "http://127.1.2.3", want: "http://127.1.2.3"},
		{name: "http to ::1", flag: "http://[::1]:3000", want: "http://[::1]:3000"},
		// One calendar, one credential key, however its URL is spelled.
		{name: "the default https port is dropped", flag: "https://calendar.promisechurch.io:443", want: "https://calendar.promisechurch.io"},
		{name: "the default http port is dropped", flag: "http://localhost:80/", want: "http://localhost"},
		{name: "another port is kept", flag: "https://calendar.promisechurch.io:8443", want: "https://calendar.promisechurch.io:8443"},
		{name: "an empty port is dropped", flag: "https://calendar.promisechurch.io:", want: "https://calendar.promisechurch.io"},
		{name: "a host that is only a dot is rejected", flag: "https://.", wantErr: `invalid calendar URL "https://."`},
		{name: "a trailing dot on the host is dropped", flag: "https://calendar.promisechurch.io.", want: "https://calendar.promisechurch.io"},
		{name: "a trailing dot and uppercase on localhost", flag: "http://LOCALHOST.:3000", want: "http://localhost:3000"},
		{name: "an IPv6 host with the default port", flag: "https://[::1]:443/", want: "https://[::1]"},
		{name: "an escaped slash in the path stays escaped", flag: "https://calendar.example.com/a%2Fb/", want: "https://calendar.example.com/a%2Fb"},
		{name: "a path is kept", flag: "https://calendar.example.com/staff/", want: "https://calendar.example.com/staff"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveURL(tt.flag, env(tt.vars), tt.config)

			if tt.wantErr != "" {
				var e *output.Error
				require.ErrorAs(t, err, &e)
				require.Equal(t, output.CodeUsage, e.Code)
				require.Contains(t, e.Message, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// requireMode asserts path's permission bits.
func requireMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, want, info.Mode().Perm(), path)
}

// WriteFile only writes into pcal's own config directory, so it makes that
// directory private even when it already existed with looser permissions.
func TestWriteFileMakesAnExistingDirectoryPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pcal")
	require.NoError(t, os.Mkdir(dir, 0o755))
	require.NoError(t, os.Chmod(dir, 0o755))

	require.NoError(t, WriteFile(filepath.Join(dir, "config.json"), []byte("{}")))

	requireMode(t, dir, 0o700)
}
