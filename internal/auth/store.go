// Package auth keeps pcal's credentials: in the OS keyring when there is
// one, otherwise in a 0600 credentials.json beside config.json.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"

	"github.com/rorJeremy/promise-calendar-cli/internal/config"
)

// keyringService is the keyring service name; entries are keyed by the
// calendar's base URL.
const keyringService = "pcal"

// credentialsFile is the fallback file's name inside the config directory.
const credentialsFile = "credentials.json"

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
// isn't set.
var ErrNoCredential = errors.New("not logged in")

// Credential is a token for one calendar, and how pcal got it.
type Credential struct {
	Token  string `json:"token"`
	Source Source `json:"source"`
	// Store is where the credential was loaded from: StoreKeyring,
	// StoreFile, or StoreEnv. It isn't saved.
	Store string `json:"-"`
}

// Store reads and writes credentials, one per calendar base URL. It uses
// the OS keyring unless PCAL_NO_KEYRING is set (to any value) or the keyring
// fails; then it uses credentials.json, and the first keyring failure in a
// process prints one warning. The keyring isn't touched until a credential
// is read or written.
type Store struct {
	dir      string
	getenv   func(string) string
	warnings io.Writer
	warnOnce sync.Once
}

// NewStore returns a Store keeping its fallback file in dir (the config
// directory), reading PCAL_TOKEN and PCAL_NO_KEYRING through getenv, and
// writing its warning to warnings (stderr).
func NewStore(dir string, getenv func(string) string, warnings io.Writer) *Store {
	return &Store{dir: dir, getenv: getenv, warnings: warnings}
}

// Resolve returns the credential to call baseURL with: PCAL_TOKEN when it's
// set (source and store "env"), otherwise the stored one. It returns
// ErrNoCredential when there's neither.
func (s *Store) Resolve(baseURL string) (Credential, error) {
	if token := strings.TrimSpace(s.getenv("PCAL_TOKEN")); token != "" {
		return Credential{Token: token, Source: SourceEnv, Store: StoreEnv}, nil
	}
	return s.Get(baseURL)
}

// Get returns the stored credential for baseURL, or ErrNoCredential. A
// keyring miss means nothing is stored; the file is read only when the
// keyring is off or fails.
func (s *Store) Get(baseURL string) (Credential, error) {
	if s.useKeyring() {
		raw, err := keyring.Get(keyringService, baseURL)
		switch {
		case err == nil:
			var c Credential
			if err := json.Unmarshal([]byte(raw), &c); err != nil {
				return Credential{}, fmt.Errorf("reading the credential for %s from the keyring: %w", baseURL, err)
			}
			c.Store = StoreKeyring
			return c, nil
		case errors.Is(err, keyring.ErrNotFound):
			return Credential{}, ErrNoCredential
		default:
			s.warnFallback(err)
		}
	}

	all, err := s.readFile()
	if err != nil {
		return Credential{}, err
	}
	c, ok := all[baseURL]
	if !ok {
		return Credential{}, ErrNoCredential
	}
	c.Store = StoreFile
	return c, nil
}

// Set stores c for baseURL, replacing any earlier credential. Only browser
// and imported tokens are stored.
func (s *Store) Set(baseURL string, c Credential) error {
	if c.Token == "" {
		return errors.New("can't store an empty token")
	}
	if c.Source != SourceBrowser && c.Source != SourceImported {
		return fmt.Errorf("can't store a credential from %q", c.Source)
	}
	stored := Credential{Token: c.Token, Source: c.Source}

	if s.useKeyring() {
		data, err := json.Marshal(stored)
		if err != nil {
			return err
		}
		err = keyring.Set(keyringService, baseURL, string(data))
		if err == nil {
			// Don't let a plaintext copy from an earlier fallback outlive it.
			return s.removeFromFile(baseURL)
		}
		s.warnFallback(err)
	}

	all, err := s.readFile()
	if err != nil {
		return err
	}
	all[baseURL] = stored
	return s.writeFile(all)
}

// Delete forgets the credential for baseURL in both the keyring and the
// file. Nothing stored is not an error; a keyring failure falls back to the
// file, as everywhere else.
func (s *Store) Delete(baseURL string) error {
	if s.useKeyring() {
		err := keyring.Delete(keyringService, baseURL)
		if err != nil && !errors.Is(err, keyring.ErrNotFound) {
			s.warnFallback(err)
		}
	}
	return s.removeFromFile(baseURL)
}

// useKeyring reports whether to try the keyring at all.
func (s *Store) useKeyring() bool {
	return s.getenv("PCAL_NO_KEYRING") == ""
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
	return all, nil
}

// writeFile replaces the fallback file with all, mode 0600.
func (s *Store) writeFile(all map[string]Credential) error {
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFile(s.path(), append(data, '\n'))
}

// removeFromFile drops baseURL's entry from the fallback file, if it has one.
func (s *Store) removeFromFile(baseURL string) error {
	all, err := s.readFile()
	if err != nil {
		return err
	}
	if _, ok := all[baseURL]; !ok {
		return nil
	}
	delete(all, baseURL)
	return s.writeFile(all)
}
