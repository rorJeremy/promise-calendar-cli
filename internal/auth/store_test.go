package auth

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

const testURL = "https://calendar.promisechurch.io"

// env returns a getenv func that reads from vars.
func env(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

// newStore returns a Store over a fresh config dir, with its warnings
// captured. The caller picks the keyring with keyring.MockInit or
// keyring.MockInitWithError, so no test touches the real keychain.
func newStore(t *testing.T, vars map[string]string) (*Store, string, *bytes.Buffer) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "pcal")
	var warnings bytes.Buffer
	return NewStore(dir, env(vars), &warnings), dir, &warnings
}

func TestKeyringStore(t *testing.T) {
	keyring.MockInit()
	s, dir, warnings := newStore(t, nil)

	require.NoError(t, s.Set(testURL, Credential{Token: "tok", Source: SourceBrowser}))
	got, err := s.Get(testURL)

	require.NoError(t, err)
	require.Equal(t, Credential{Token: "tok", Source: SourceBrowser, Store: StoreKeyring}, got)

	// Service "pcal", keyed by the base URL.
	raw, err := keyring.Get("pcal", testURL)
	require.NoError(t, err)
	require.JSONEq(t, `{"token":"tok","source":"browser"}`, raw)

	require.NoFileExists(t, filepath.Join(dir, "credentials.json"))
	require.Empty(t, warnings.String())
}

func TestGetWithNothingStored(t *testing.T) {
	tests := []struct {
		name  string
		setup func()
		vars  map[string]string
	}{
		// keyring.ErrNotFound means "not stored", not a keyring failure.
		{"keyring", keyring.MockInit, nil},
		{"PCAL_NO_KEYRING", keyring.MockInit, map[string]string{"PCAL_NO_KEYRING": "1"}},
		{"keyring unavailable", func() { keyring.MockInitWithError(errors.New("no keychain")) }, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()
			s, _, _ := newStore(t, tt.vars)

			_, err := s.Get(testURL)

			require.ErrorIs(t, err, ErrNoCredential)
		})
	}
}

func TestFileFallbackWhenKeyringFails(t *testing.T) {
	keyring.MockInitWithError(errors.New("dbus: no session bus"))
	s, dir, warnings := newStore(t, nil)
	path := filepath.Join(dir, "credentials.json")

	require.NoError(t, s.Set(testURL, Credential{Token: "tok", Source: SourceImported}))
	got, err := s.Get(testURL)

	require.NoError(t, err)
	require.Equal(t, Credential{Token: "tok", Source: SourceImported, Store: StoreFile}, got)
	requireMode(t, dir, 0o700)
	requireMode(t, path, 0o600)
	requireFile(t, path, `{"https://calendar.promisechurch.io":{"token":"tok","source":"imported"}}`)

	// One warning per process, however many operations fall back.
	require.Equal(t, 1, strings.Count(warnings.String(), "warning:"), warnings.String())
	require.Contains(t, warnings.String(), "dbus: no session bus")
	require.Contains(t, warnings.String(), path)
}

func TestNoKeyringEnvUsesFileWithoutWarning(t *testing.T) {
	keyring.MockInit()
	s, dir, warnings := newStore(t, map[string]string{"PCAL_NO_KEYRING": "1"})

	require.NoError(t, s.Set(testURL, Credential{Token: "tok", Source: SourceBrowser}))
	got, err := s.Get(testURL)

	require.NoError(t, err)
	require.Equal(t, Credential{Token: "tok", Source: SourceBrowser, Store: StoreFile}, got)
	requireMode(t, filepath.Join(dir, "credentials.json"), 0o600)

	_, err = keyring.Get("pcal", testURL)
	require.ErrorIs(t, err, keyring.ErrNotFound, "the keyring is never touched")

	// Setting PCAL_NO_KEYRING is a choice, as in basecamp-cli, so no warning.
	require.Empty(t, warnings.String())
}

func TestFileHoldsOneCredentialPerURL(t *testing.T) {
	keyring.MockInit()
	s, _, _ := newStore(t, map[string]string{"PCAL_NO_KEYRING": "1"})
	const localURL = "http://localhost:3000"

	require.NoError(t, s.Set(testURL, Credential{Token: "prod", Source: SourceBrowser}))
	require.NoError(t, s.Set(localURL, Credential{Token: "dev", Source: SourceImported}))
	require.NoError(t, s.Delete(testURL))

	_, err := s.Get(testURL)
	require.ErrorIs(t, err, ErrNoCredential)
	got, err := s.Get(localURL)
	require.NoError(t, err)
	require.Equal(t, "dev", got.Token)
}

func TestDeleteRemovesBoth(t *testing.T) {
	keyring.MockInit()
	s, dir, _ := newStore(t, nil)
	path := filepath.Join(dir, "credentials.json")

	// One copy in the keyring, and one in the file from a run when the
	// keyring failed.
	require.NoError(t, keyring.Set("pcal", testURL, `{"token":"k","source":"browser"}`))
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(path, []byte(`{"https://calendar.promisechurch.io":{"token":"f","source":"browser"}}`), 0o600))

	require.NoError(t, s.Delete(testURL))

	_, err := keyring.Get("pcal", testURL)
	require.ErrorIs(t, err, keyring.ErrNotFound)
	requireFile(t, path, `{}`)
}

func TestDeleteWithNothingStored(t *testing.T) {
	keyring.MockInit()
	s, dir, _ := newStore(t, nil)

	require.NoError(t, s.Delete(testURL))
	require.NoFileExists(t, filepath.Join(dir, "credentials.json"))
}

func TestKeyringSetRemovesAStaleFileCopy(t *testing.T) {
	keyring.MockInit()
	s, dir, _ := newStore(t, nil)
	path := filepath.Join(dir, "credentials.json")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(path, []byte(`{"https://calendar.promisechurch.io":{"token":"old","source":"browser"},"http://localhost:3000":{"token":"dev","source":"imported"}}`), 0o600))

	require.NoError(t, s.Set(testURL, Credential{Token: "new", Source: SourceBrowser}))

	// The old plaintext token doesn't outlive the move to the keyring.
	requireFile(t, path, `{"http://localhost:3000":{"token":"dev","source":"imported"}}`)
}

func TestSetRejects(t *testing.T) {
	keyring.MockInit()
	s, _, _ := newStore(t, nil)

	tests := []struct {
		name    string
		cred    Credential
		wantErr string
	}{
		{"an empty token", Credential{Source: SourceBrowser}, "empty token"},
		// PCAL_TOKEN is read from the environment on every run.
		{"an env token", Credential{Token: "tok", Source: SourceEnv}, `can't store a credential from "env"`},
		{"an unknown source", Credential{Token: "tok", Source: "pasted"}, `can't store a credential from "pasted"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorContains(t, s.Set(testURL, tt.cred), tt.wantErr)
		})
	}
}

func TestGetRejectsCorruptEntries(t *testing.T) {
	t.Run("keyring", func(t *testing.T) {
		keyring.MockInit()
		s, _, _ := newStore(t, nil)
		require.NoError(t, keyring.Set("pcal", testURL, "not json"))

		_, err := s.Get(testURL)

		require.ErrorContains(t, err, "keyring")
	})

	t.Run("file", func(t *testing.T) {
		keyring.MockInit()
		s, dir, _ := newStore(t, map[string]string{"PCAL_NO_KEYRING": "1"})
		path := filepath.Join(dir, "credentials.json")
		require.NoError(t, os.MkdirAll(dir, 0o700))
		require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o600))

		_, err := s.Get(testURL)

		require.ErrorContains(t, err, path)
	})
}

func TestResolve(t *testing.T) {
	stored := Credential{Token: "stored", Source: SourceBrowser}

	tests := []struct {
		name  string
		vars  map[string]string
		store *Credential
		want  Credential
	}{
		{
			name:  "PCAL_TOKEN beats the store",
			vars:  map[string]string{"PCAL_TOKEN": "from-env"},
			store: &stored,
			want:  Credential{Token: "from-env", Source: SourceEnv, Store: StoreEnv},
		},
		{
			name: "PCAL_TOKEN with nothing stored",
			vars: map[string]string{"PCAL_TOKEN": "from-env"},
			want: Credential{Token: "from-env", Source: SourceEnv, Store: StoreEnv},
		},
		{
			name: "PCAL_TOKEN is trimmed",
			vars: map[string]string{"PCAL_TOKEN": " from-env\n"},
			want: Credential{Token: "from-env", Source: SourceEnv, Store: StoreEnv},
		},
		{
			name:  "the keyring when PCAL_TOKEN is unset",
			store: &stored,
			want:  Credential{Token: "stored", Source: SourceBrowser, Store: StoreKeyring},
		},
		{
			name:  "an empty PCAL_TOKEN counts as unset",
			vars:  map[string]string{"PCAL_TOKEN": "  "},
			store: &stored,
			want:  Credential{Token: "stored", Source: SourceBrowser, Store: StoreKeyring},
		},
		{
			name:  "the file with PCAL_NO_KEYRING",
			vars:  map[string]string{"PCAL_NO_KEYRING": "1"},
			store: &stored,
			want:  Credential{Token: "stored", Source: SourceBrowser, Store: StoreFile},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyring.MockInit()
			s, _, _ := newStore(t, tt.vars)
			if tt.store != nil {
				require.NoError(t, s.Set(testURL, *tt.store))
			}

			got, err := s.Resolve(testURL)

			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestResolveWithNothing(t *testing.T) {
	keyring.MockInit()
	s, _, _ := newStore(t, nil)

	_, err := s.Resolve(testURL)

	require.ErrorIs(t, err, ErrNoCredential)
}

// requireMode asserts path's permission bits.
func requireMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, want, info.Mode().Perm(), path)
}

// requireFile asserts path holds JSON equal to want.
func requireFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.JSONEq(t, want, string(data))
}
