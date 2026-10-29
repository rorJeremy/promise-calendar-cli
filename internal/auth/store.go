// Package auth keeps pcal's credentials: in the OS keyring when there is
// one, otherwise in a 0600 credentials.json beside config.json.
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/gofrs/flock"
	"github.com/zalando/go-keyring"

	"github.com/rorJeremy/promise-calendar-cli/internal/config"
	"github.com/rorJeremy/promise-calendar-cli/internal/output"
)

// keyringService is the keyring service name; entries are keyed by the
// calendar's base URL.
const keyringService = "pcal"

// credentialsFile is the fallback file's name inside the config directory.
const credentialsFile = "credentials.json"

// lockFile is the lock taken, beside credentials.json, around every
// read-modify-write of it, so two pcal processes writing at once can't lose
// each other's entries (as basecamp-cli's lock.go does with gofrs/flock).
const lockFile = "credentials.lock"

// keyringTimeout bounds every keyring call. go-keyring has none of its
// own: on macOS it runs /usr/bin/security, which waits forever on a locked
// keychain with no one to answer the prompt, and on Linux a stalled D-Bus
// never answers. basecamp-cli uses the same bound.
const keyringTimeout = 10 * time.Second

// lockTimeout bounds the wait for another pcal process to release
// credentials.lock. The work done under it is local file I/O.
const lockTimeout = 10 * time.Second

// errKeyringTimeout means a keyring call didn't return within the bound.
var errKeyringTimeout = errors.New("no answer")

// keyringBackend is the part of go-keyring the Store uses, so tests can
// pass a fake and never touch the real keychain.
type keyringBackend interface {
	Get(service, user string) (string, error)
	Set(service, user, password string) error
	Delete(service, user string) error
}

// systemKeyring is the OS keyring, through go-keyring.
type systemKeyring struct{}

func (systemKeyring) Get(service, user string) (string, error) { return keyring.Get(service, user) }
func (systemKeyring) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}
func (systemKeyring) Delete(service, user string) error { return keyring.Delete(service, user) }

// backend is where a Store keeps credentials for the rest of the process.
type backend int

const (
	// backendUndecided: no keyring call has returned yet.
	backendUndecided backend = iota
	// backendKeyring: the first keyring call worked.
	backendKeyring
	// backendFile: the first keyring call failed or hung.
	backendFile
)

// Source says how pcal got a token. Logout depends on it: a token pcal
// login minted is revoked on the server, an imported one is only forgotten.
type Source string

const (
	// SourceBrowser is a token `pcal login` minted.
	SourceBrowser Source = "browser"
	// SourceImported is a token given to `pcal auth login --with-token`.
	SourceImported Source = "imported"
	// SourceEnv is a token from PCAL_TOKEN. It's never stored.
	SourceEnv Source = "env"
)

// Where a credential was loaded from, for `pcal auth status`.
const (
	StoreKeyring = "keyring"
	StoreFile    = "file"
	StoreEnv     = "env"
)

// ErrNoCredential means no token is stored for the calendar and PCAL_TOKEN
// isn't set. Get and Resolve return it inside an *output.Error
// (auth_required, exit 3, with a hint to log in), so errors.Is still finds
// it.
var ErrNoCredential = errors.New("not logged in")

// notLoggedIn returns the error for "nothing stored": ErrNoCredential as an
// auth error.
func notLoggedIn() error {
	return &output.Error{
		Code:    output.CodeAuth,
		Message: ErrNoCredential.Error(),
		Hint:    "Run `pcal login`.",
		Cause:   ErrNoCredential,
	}
}

// Credential is a token for one calendar, and how pcal got it.
type Credential struct {
	Token  string `json:"token"`
	Source Source `json:"source"`
	// Store is where the credential was loaded from: StoreKeyring,
	// StoreFile, or StoreEnv. It isn't saved.
	Store string `json:"-"`
}

// Store reads and writes credentials, one per calendar base URL.
//
// It uses the OS keyring unless PCAL_NO_KEYRING is set (to any value). The
// first keyring call in a process settles where credentials live for the
// rest of it: if the call works (a "not found" counts as working), the
// keyring, and any later keyring failure is an error, never a silent switch
// to plaintext; if it fails or hangs, credentials.json, with one warning
// (none with PCAL_NO_KEYRING). Every keyring call gives up after
// keyringTimeout. The keyring isn't touched until a credential is read or
// written.
type Store struct {
	dir      string
	getenv   func(string) string
	warnings io.Writer
	warnOnce sync.Once
	// permOnce limits the loose-permissions warning to one per process.
	permOnce sync.Once
	// unreadableOnce limits the unreadable-leftover warning to one per
	// process.
	unreadableOnce sync.Once
	keyring        keyringBackend
	// timeout bounds each keyring call; keyringTimeout outside tests.
	timeout time.Duration
	// lockWait bounds the wait for credentials.lock; lockTimeout outside
	// tests.
	lockWait time.Duration

	mu      sync.Mutex
	backend backend
}

// NewStore returns a Store keeping its fallback file in dir (the config
// directory), reading PCAL_TOKEN and PCAL_NO_KEYRING through getenv, and
// writing its warnings to warnings (stderr).
func NewStore(dir string, getenv func(string) string, warnings io.Writer) *Store {
	return &Store{
		dir:      dir,
		getenv:   getenv,
		warnings: warnings,
		keyring:  systemKeyring{},
		timeout:  keyringTimeout,
		lockWait: lockTimeout,
	}
}

// Resolve returns the credential to call baseURL with: PCAL_TOKEN when it's
// set (source and store "env"), otherwise the stored one. It returns
// ErrNoCredential when there's neither, and a usage error when PCAL_TOKEN
// holds a control character.
func (s *Store) Resolve(baseURL string) (Credential, error) {
	if token := strings.TrimSpace(s.getenv("PCAL_TOKEN")); token != "" {
		if hasControl(token) {
			return Credential{}, &output.Error{
				Code:    output.CodeUsage,
				Message: "PCAL_TOKEN contains a control character, such as a line break inside the token",
				Hint:    "Set PCAL_TOKEN to the token alone.",
			}
		}
		return Credential{Token: token, Source: SourceEnv, Store: StoreEnv}, nil
	}
	return s.Get(baseURL)
}

// hasControl reports whether token holds a control character, such as a
// line break pasted inside it. Go refuses to send one in a header, so the
// request would fail as a network error. Errors about it never quote the
// token.
func hasControl(token string) bool {
	return strings.IndexFunc(token, unicode.IsControl) >= 0
}

// Get returns the stored credential for baseURL, or ErrNoCredential. While
// the keyring is in use, a keyring miss still checks credentials.json: an
// entry saved there while the keyring was failing moves into the keyring.
func (s *Store) Get(baseURL string) (Credential, error) {
	raw, inKeyring, err := s.keyringCall(func(k keyringBackend) (string, error) {
		return k.Get(keyringService, baseURL)
	})
	if inKeyring {
		switch {
		case err == nil:
			var c Credential
			if err := json.Unmarshal([]byte(raw), &c); err != nil {
				return Credential{}, fmt.Errorf("reading the credential for %s from the keyring: %w", baseURL, err)
			}
			c.Store = StoreKeyring
			return c, nil
		case errors.Is(err, keyring.ErrNotFound):
			return s.moveToKeyring(baseURL)
		default:
			return Credential{}, keyringError("reading the credential for "+baseURL+" from", err)
		}
	}

	all, err := s.readFile()
	if err != nil {
		return Credential{}, err
	}
	c, ok := all[baseURL]
	if !ok {
		return Credential{}, notLoggedIn()
	}
	c.Store = StoreFile
	return c, nil
}

// moveToKeyring moves baseURL's entry from credentials.json into the
// keyring, after a keyring miss (as basecamp/cli's MigrateToKeyring does
// for a whole file). A token saved while the keyring was failing would
// otherwise stay invisible, and in plaintext. If the keyring refuses it, the
// error is returned and the file is left alone.
func (s *Store) moveToKeyring(baseURL string) (Credential, error) {
	all, err := s.readFile()
	if err != nil {
		s.warnUnreadable(err)
		return Credential{}, notLoggedIn()
	}
	c, ok := all[baseURL]
	if !ok {
		return Credential{}, notLoggedIn()
	}
	if err := s.setKeyring(baseURL, c); err != nil {
		return Credential{}, err
	}
	if err := s.removeFromFile(baseURL, &c); err != nil {
		return Credential{}, err
	}
	c.Store = StoreKeyring
	return c, nil
}

// Set stores c for baseURL, replacing any earlier credential. Only browser
// and imported tokens are stored, and never one with a control character
// in it.
func (s *Store) Set(baseURL string, c Credential) error {
	if c.Token == "" {
		return errors.New("can't store an empty token")
	}
	if c.Source != SourceBrowser && c.Source != SourceImported {
		return fmt.Errorf("can't store a credential from %q", c.Source)
	}
	if hasControl(c.Token) {
		return output.Usagef("the token contains a control character, such as a line break inside it")
	}
	stored := Credential{Token: c.Token, Source: c.Source}

	err := s.setKeyring(baseURL, stored)
	if !errors.Is(err, errUseFile) {
		if err != nil {
			return err
		}
		// Don't let a plaintext copy from an earlier fallback outlive it.
		return s.removeFromFile(baseURL, nil)
	}

	return s.updateFile(func(all map[string]Credential) bool {
		all[baseURL] = stored
		return true
	})
}

// errUseFile is setKeyring's answer when the store isn't using the keyring.
var errUseFile = errors.New("the store uses credentials.json")

// setKeyring saves c for baseURL in the keyring. It returns errUseFile when
// the store uses the file instead.
func (s *Store) setKeyring(baseURL string, c Credential) error {
	data, err := json.Marshal(Credential{Token: c.Token, Source: c.Source})
	if err != nil {
		return err
	}
	_, inKeyring, err := s.keyringCall(func(k keyringBackend) (string, error) {
		return "", k.Set(keyringService, baseURL, string(data))
	})
	switch {
	case !inKeyring:
		return errUseFile
	case err != nil:
		return keyringError("saving the credential for "+baseURL+" in", err)
	default:
		return nil
	}
}

// Delete forgets the credential for baseURL in both the keyring and the
// file. Nothing stored is not an error. When the keyring delete fails, the
// file copy is still removed, and the error says the token may still be in
// the keyring.
func (s *Store) Delete(baseURL string) error {
	_, inKeyring, err := s.keyringCall(func(k keyringBackend) (string, error) {
		return "", k.Delete(keyringService, baseURL)
	})
	if errors.Is(err, keyring.ErrNotFound) {
		err = nil
	}
	fileErr := s.removeFromFile(baseURL, nil)
	if inKeyring && err != nil {
		return keyringError("forgetting the credential for "+baseURL+" in", err, "it may still be stored there")
	}
	return fileErr
}

// keyringCall runs op on the keyring, giving up after s.timeout. The first
// call in a process settles the backend: a result or keyring.ErrNotFound
// means the keyring for the rest of the run; any other error, or no answer,
// means the file, with one warning. inKeyring is false when the store uses
// the file (PCAL_NO_KEYRING, or that first failure); then the caller ignores
// value and err and uses the file.
func (s *Store) keyringCall(op func(keyringBackend) (string, error)) (value string, inKeyring bool, err error) {
	if s.getenv("PCAL_NO_KEYRING") != "" {
		return "", false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.backend == backendFile {
		return "", false, nil
	}

	value, err = s.bounded(op)
	if s.backend == backendUndecided {
		if err != nil && !errors.Is(err, keyring.ErrNotFound) {
			s.backend = backendFile
			s.warnFallback(err)
			return "", false, nil
		}
		s.backend = backendKeyring
	}
	return value, true, err
}

// bounded runs op and waits at most s.timeout for it. go-keyring can't be
// cancelled, so a call that never returns is left running; the process
// ends soon after.
func (s *Store) bounded(op func(keyringBackend) (string, error)) (string, error) {
	type result struct {
		value string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, err := op(s.keyring)
		done <- result{value, err}
	}()

	timer := time.NewTimer(s.timeout)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.value, r.err
	case <-timer.C:
		return "", fmt.Errorf("%w within %s", errKeyringTimeout, s.timeout)
	}
}

// keyringInUse reports whether the first keyring call settled on the
// keyring.
func (s *Store) keyringInUse() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.backend == backendKeyring
}

// keyringError reports err from a keyring call once the keyring is in use.
// doing reads "<verb> the credential for <url> in" (or "from"), and notes
// are appended after a semicolon. When the keyring didn't answer, the hint
// says how to stop using it.
func keyringError(doing string, err error, notes ...string) error {
	message := doing + " the system keyring: " + err.Error()
	for _, note := range notes {
		message += "; " + note
	}
	e := &output.Error{Code: output.CodeAPI, Message: message, Cause: err}
	if errors.Is(err, errKeyringTimeout) {
		e.Hint = "If the keyring keeps hanging, set PCAL_NO_KEYRING=1 to keep credentials in a 0600 file instead."
	}
	return e
}

// path returns the fallback file's path.
func (s *Store) path() string {
	return filepath.Join(s.dir, credentialsFile)
}

// warnFallback prints, once per process, that credentials are in the
// plaintext file because the keyring failed with err.
func (s *Store) warnFallback(err error) {
	s.warnOnce.Do(func() {
		_, _ = fmt.Fprintf(s.warnings,
			"warning: the system keyring is unavailable (%v), so pcal keeps credentials in plaintext in %s\n",
			err, s.path())
	})
}

// warnUnreadable prints, once per process, that credentials.json, a
// leftover while the keyring is in use, couldn't be read, so any plaintext
// copy in it stays. err comes from readFile and already names the file.
func (s *Store) warnUnreadable(err error) {
	s.unreadableOnce.Do(func() {
		_, _ = fmt.Fprintf(s.warnings,
			"warning: couldn't read the leftover credentials file (%v); pcal uses the system keyring, "+
				"but a plaintext copy of a credential may still be in that file\n", err)
	})
}

// readFile reads every credential in the fallback file, keyed by base URL.
// A missing file holds none.
func (s *Store) readFile() (map[string]Credential, error) {
	all := map[string]Credential{}
	data, err := os.ReadFile(s.path())
	if errors.Is(err, fs.ErrNotExist) {
		return all, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.path(), err)
	}
	s.makePrivate()
	return all, nil
}

// makePrivate makes the fallback file 0600 again if other users can read or
// write it, and says so once per process. Windows has no Unix permission
// bits to check.
func (s *Store) makePrivate() {
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(s.path())
	if err != nil || info.Mode().Perm()&0o077 == 0 {
		return
	}
	mode := info.Mode().Perm()
	err = os.Chmod(s.path(), 0o600)
	s.permOnce.Do(func() {
		if err != nil {
			_, _ = fmt.Fprintf(s.warnings, "warning: %s is readable by other users (mode %04o), and pcal couldn't make it 0600: %v\n", s.path(), mode, err)
			return
		}
		_, _ = fmt.Fprintf(s.warnings, "warning: %s was readable by other users (mode %04o), so pcal made it 0600\n", s.path(), mode)
	})
}

// writeFile replaces the fallback file with all, mode 0600.
func (s *Store) writeFile(all map[string]Credential) error {
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFile(s.path(), append(data, '\n'))
}

// updateFile reads the fallback file, lets change edit its credentials,
// and writes them back if change reports a change, all while holding
// credentials.lock.
func (s *Store) updateFile(change func(all map[string]Credential) bool) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()

	all, err := s.readFile()
	if err != nil {
		return err
	}
	if !change(all) {
		return nil
	}
	return s.writeFile(all)
}

// lock takes credentials.lock, waiting up to s.lockWait for another pcal
// process to release it, and returns the function that releases it. The
// kernel drops the lock if the process dies, so a crash can't leave it
// held.
func (s *Store) lock() (func(), error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(s.dir, lockFile)
	fl := flock.New(path)

	ctx, cancel := context.WithTimeout(context.Background(), s.lockWait)
	defer cancel()
	locked, err := fl.TryLockContext(ctx, 10*time.Millisecond)
	switch {
	case locked:
		return func() { _ = fl.Unlock() }, nil
	case errors.Is(err, context.DeadlineExceeded):
		return nil, fmt.Errorf("another pcal process held %s for %s; wait for it to finish, then try again", path, s.lockWait)
	default:
		return nil, fmt.Errorf("locking %s: %w", path, err)
	}
}

// removeFromFile drops baseURL's entry from the fallback file, if it has
// one, and, when want isn't nil, only if the entry is still want (another
// process may have replaced it). While the keyring is in use the file is
// only a leftover, so an unreadable one is a warning, not an error.
func (s *Store) removeFromFile(baseURL string, want *Credential) error {
	// Look before locking, so a run that never had a fallback file never
	// takes the lock.
	all, err := s.readFile()
	if err != nil {
		if s.keyringInUse() {
			s.warnUnreadable(err)
			return nil
		}
		return err
	}
	if _, ok := all[baseURL]; !ok {
		return nil
	}

	return s.updateFile(func(all map[string]Credential) bool {
		c, ok := all[baseURL]
		if !ok || (want != nil && c != *want) {
			return false
		}
		delete(all, baseURL)
		return true
	})
}
