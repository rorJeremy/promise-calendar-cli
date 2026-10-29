package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/rorJeremy/promise-calendar-cli/internal/output"
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

// newFakeStore returns a Store over a fresh config dir whose keyring is
// fake, with every keyring call bounded at 250ms.
func newFakeStore(t *testing.T, fake *fakeKeyring, vars map[string]string) (*Store, string, *bytes.Buffer) {
	t.Helper()
	s, dir, warnings := newStore(t, vars)
	s.keyring = fake
	s.timeout = 250 * time.Millisecond
	return s, dir, warnings
}

// returnsPromptly runs fn and fails the test if it hasn't returned within
// five seconds, far beyond the 250ms bound newFakeStore sets.
func returnsPromptly(t *testing.T, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the store call didn't return: keyring calls aren't bounded")
	}
}

// writeCredentialsFile writes a credentials.json holding content into dir.
func writeCredentialsFile(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "credentials.json")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestNotLoggedInIsAnAuthError(t *testing.T) {
	s, _, _ := newFakeStore(t, newFakeKeyring(t, nil), nil)

	_, err := s.Resolve(testURL)

	require.ErrorIs(t, err, ErrNoCredential)
	var e *output.Error
	require.ErrorAs(t, err, &e)
	require.Equal(t, output.CodeAuth, e.Code)
	require.Equal(t, output.ExitAuth, e.ExitCode())
	require.Equal(t, "not logged in", e.Message)
	require.Equal(t, "Run `pcal login`.", e.Hint)
}

// The first keyring call settles the backend for the rest of the run. When
// it fails, the run uses the file, warns once, and never asks the keyring
// again.
func TestKeyringFailingOnTheFirstCallMeansTheFileForTheRun(t *testing.T) {
	fake := newFakeKeyring(t, func(n int) error {
		if n == 1 {
			return errors.New("dbus: no session bus")
		}
		return nil
	})
	s, dir, warnings := newFakeStore(t, fake, nil)
	path := filepath.Join(dir, "credentials.json")

	require.NoError(t, s.Set(testURL, Credential{Token: "tok", Source: SourceImported}))
	got, err := s.Get(testURL)
	require.NoError(t, err)
	require.Equal(t, Credential{Token: "tok", Source: SourceImported, Store: StoreFile}, got)
	require.NoError(t, s.Delete(testURL))

	requireFile(t, path, `{}`)
	require.Equal(t, 1, fake.callCount(), "the keyring is asked once")
	require.Equal(t, 1, strings.Count(warnings.String(), "warning:"), warnings.String())
	require.Contains(t, warnings.String(), "dbus: no session bus")
}

// Once the keyring has answered, a failure is an error: the token is never
// silently written in plaintext instead.
func TestKeyringFailingAfterAWorkingFirstCallIsAnError(t *testing.T) {
	fake := newFakeKeyring(t, failFrom(2, errors.New("dbus: connection reset")))
	s, dir, warnings := newFakeStore(t, fake, nil)

	_, err := s.Get(testURL) // the first call: a miss, so the keyring works
	require.ErrorIs(t, err, ErrNoCredential)

	err = s.Set(testURL, Credential{Token: "tok", Source: SourceBrowser})

	require.ErrorContains(t, err, "dbus: connection reset")
	require.NoFileExists(t, filepath.Join(dir, "credentials.json"))
	require.Empty(t, warnings.String())
}

func TestKeyringHangingOnTheFirstCallMeansTheFile(t *testing.T) {
	fake := newFakeKeyring(t, func(n int) error {
		if n == 1 {
			return errHang
		}
		return nil
	})
	s, dir, warnings := newFakeStore(t, fake, nil)

	var err error
	returnsPromptly(t, func() { err = s.Set(testURL, Credential{Token: "tok", Source: SourceBrowser}) })

	require.NoError(t, err)
	requireFile(t, filepath.Join(dir, "credentials.json"), `{"https://calendar.promisechurch.io":{"token":"tok","source":"browser"}}`)
	require.Equal(t, 1, strings.Count(warnings.String(), "warning:"), warnings.String())
	require.Contains(t, warnings.String(), "no answer within 250ms")
}

// Every keyring call is bounded. A hang after the keyring has worked is an
// error that says how to opt out, not a fallback.
func TestKeyringHangingLaterIsAnErrorSuggestingPCALNoKeyring(t *testing.T) {
	cred := Credential{Token: "tok", Source: SourceBrowser}
	tests := []struct {
		name string
		call func(s *Store) error
		want string
	}{
		{"Get", func(s *Store) error { _, err := s.Get(testURL); return err }, "reading the credential for https://calendar.promisechurch.io from the system keyring: no answer within 250ms"},
		{"Set", func(s *Store) error { return s.Set(testURL, cred) }, "saving the credential for https://calendar.promisechurch.io in the system keyring: no answer within 250ms"},
		{"Delete", func(s *Store) error { return s.Delete(testURL) }, "forgetting the credential for https://calendar.promisechurch.io in the system keyring: no answer within 250ms"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeKeyring(t, func(n int) error {
				if n >= 2 {
					return errHang
				}
				return nil
			})
			s, dir, _ := newFakeStore(t, fake, nil)
			_, err := s.Get("http://localhost:3000") // the first call: a miss
			require.ErrorIs(t, err, ErrNoCredential)

			returnsPromptly(t, func() { err = tt.call(s) })

			var e *output.Error
			require.ErrorAs(t, err, &e)
			require.Equal(t, output.CodeAPI, e.Code)
			require.Contains(t, e.Message, tt.want)
			require.Equal(t, "If the keyring keeps hanging, set PCAL_NO_KEYRING=1 to keep credentials in a 0600 file instead.", e.Hint)
			require.NoFileExists(t, filepath.Join(dir, "credentials.json"))
		})
	}
}

// pcal logout must not report success while the keychain still holds the
// token. The file copy goes either way.
func TestDeleteReportsAKeyringFailure(t *testing.T) {
	fake := newFakeKeyring(t, failFrom(2, errors.New("user interaction is not allowed")))
	fake.secrets["pcal\x00"+testURL] = `{"token":"k","source":"browser"}`
	s, dir, _ := newFakeStore(t, fake, nil)
	path := writeCredentialsFile(t, dir, `{"https://calendar.promisechurch.io":{"token":"f","source":"browser"}}`)

	_, err := s.Get(testURL) // the first call works
	require.NoError(t, err)
	err = s.Delete(testURL)

	require.ErrorContains(t, err, "forgetting the credential for https://calendar.promisechurch.io in the system keyring: user interaction is not allowed")
	require.ErrorContains(t, err, "it may still be stored there")
	requireFile(t, path, `{}`)
}

// While the keyring holds the credentials, credentials.json is only a
// leftover from an earlier fallback: an unreadable one is a warning, so it
// can't fail a login or logout whose keyring step already worked.
func TestUnreadableFileIsAWarningWhileTheKeyringWorks(t *testing.T) {
	fake := newFakeKeyring(t, nil)
	s, dir, warnings := newFakeStore(t, fake, nil)
	path := writeCredentialsFile(t, dir, "{not json")

	require.NoError(t, s.Set(testURL, Credential{Token: "tok", Source: SourceBrowser}))
	_, stored := fake.secret(testURL)
	require.True(t, stored)

	require.NoError(t, s.Delete(testURL))
	_, stored = fake.secret(testURL)
	require.False(t, stored)

	_, err := s.Get(testURL)
	require.ErrorIs(t, err, ErrNoCredential)

	require.Contains(t, warnings.String(), "warning: couldn't read "+path)
	requireFileContent(t, path, "{not json")
}

// A token saved while the keyring was failing moves into the keyring the
// first time a run with a working keyring looks for it, instead of staying
// invisible, in plaintext.
func TestGetMovesAFallbackCopyIntoTheKeyring(t *testing.T) {
	fake := newFakeKeyring(t, nil)
	s, dir, warnings := newFakeStore(t, fake, nil)
	path := writeCredentialsFile(t, dir, `{"https://calendar.promisechurch.io":{"token":"old","source":"browser"},"http://localhost:3000":{"token":"dev","source":"imported"}}`)

	got, err := s.Get(testURL)

	require.NoError(t, err)
	require.Equal(t, Credential{Token: "old", Source: SourceBrowser, Store: StoreKeyring}, got)
	secret, ok := fake.secret(testURL)
	require.True(t, ok)
	require.JSONEq(t, `{"token":"old","source":"browser"}`, secret)
	requireFile(t, path, `{"http://localhost:3000":{"token":"dev","source":"imported"}}`)
	require.Empty(t, warnings.String())
}

func TestGetKeepsTheFileCopyWhenTheMoveFails(t *testing.T) {
	fake := newFakeKeyring(t, failFrom(2, errors.New("keychain locked")))
	s, dir, _ := newFakeStore(t, fake, nil)
	const content = `{"https://calendar.promisechurch.io":{"token":"old","source":"browser"}}`
	path := writeCredentialsFile(t, dir, content)

	_, err := s.Get(testURL)

	require.ErrorContains(t, err, "saving the credential for https://calendar.promisechurch.io in the system keyring: keychain locked")
	requireFile(t, path, content)
}

// Two pcal processes writing credentials.json at once must not lose each
// other's entries.
func TestConcurrentWritersKeepEveryEntry(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pcal")
	const writers = 16
	start := make(chan struct{})
	errs := make([]error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		s := NewStore(dir, env(map[string]string{"PCAL_NO_KEYRING": "1"}), io.Discard)
		wg.Go(func() {
			<-start
			errs[i] = s.Set(fmt.Sprintf("https://%d.example.com", i), Credential{Token: fmt.Sprint(i), Source: SourceBrowser})
		})
	}
	close(start)
	wg.Wait()

	for _, err := range errs {
		require.NoError(t, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "credentials.json"))
	require.NoError(t, err)
	var all map[string]Credential
	require.NoError(t, json.Unmarshal(data, &all))
	require.Len(t, all, writers)
	requireMode(t, filepath.Join(dir, "credentials.lock"), 0o600)
}

// requireFileContent asserts path holds exactly want.
func requireFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, want, string(data))
}

// Writers wait for credentials.lock, then give up rather than hang.
func TestFileWriteGivesUpWhenAnotherProcessHoldsTheLock(t *testing.T) {
	s, dir, _ := newStore(t, map[string]string{"PCAL_NO_KEYRING": "1"})
	s.lockWait = 50 * time.Millisecond
	lockPath := filepath.Join(dir, "credentials.lock")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	held := flock.New(lockPath)
	locked, err := held.TryLock()
	require.NoError(t, err)
	require.True(t, locked)
	t.Cleanup(func() { _ = held.Unlock() })

	err = s.Set(testURL, Credential{Token: "tok", Source: SourceBrowser})

	require.EqualError(t, err, "another pcal process held "+lockPath+" for 50ms; wait for it to finish, then try again")
	require.NoFileExists(t, filepath.Join(dir, "credentials.json"))
}
