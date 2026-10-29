package auth

import (
	"errors"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"
)

// errHang makes a fakeKeyring call block until the test ends, like a
// locked macOS keychain with no one to answer its prompt, or a stalled D-Bus.
var errHang = errors.New("hang")

// fakeKeyring is an in-memory keyring whose calls can fail or hang, call
// by call, so the store's keyring handling is tested without the real one.
type fakeKeyring struct {
	mu      sync.Mutex
	secrets map[string]string
	calls   int
	// outcome, when set, picks the nth call's (1-based) result: nil to
	// behave normally, errHang to block, or any other error to return.
	outcome func(n int) error
	// released unblocks hanging calls when the test ends.
	released chan struct{}
}

// newFakeKeyring returns an empty fakeKeyring whose hanging calls end with
// the test.
func newFakeKeyring(t *testing.T, outcome func(n int) error) *fakeKeyring {
	t.Helper()
	f := &fakeKeyring{secrets: map[string]string{}, outcome: outcome, released: make(chan struct{})}
	t.Cleanup(func() { close(f.released) })
	return f
}

// failFrom returns an outcome that fails every call from the nth on with err.
func failFrom(n int, err error) func(int) error {
	return func(call int) error {
		if call >= n {
			return err
		}
		return nil
	}
}

// begin counts a call and returns its scripted error, blocking first if
// the call should hang.
func (f *fakeKeyring) begin() error {
	f.mu.Lock()
	f.calls++
	n := f.calls
	f.mu.Unlock()

	if f.outcome == nil {
		return nil
	}
	err := f.outcome(n)
	if errors.Is(err, errHang) {
		<-f.released
	}
	return err
}

func (f *fakeKeyring) Get(service, user string) (string, error) {
	if err := f.begin(); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	secret, ok := f.secrets[service+"\x00"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return secret, nil
}

func (f *fakeKeyring) Set(service, user, password string) error {
	if err := f.begin(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.secrets[service+"\x00"+user] = password
	return nil
}

func (f *fakeKeyring) Delete(service, user string) error {
	if err := f.begin(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := service + "\x00" + user
	if _, ok := f.secrets[key]; !ok {
		return keyring.ErrNotFound
	}
	delete(f.secrets, key)
	return nil
}

// secret returns what the fake holds for the pcal entry of baseURL, and
// whether it holds one.
func (f *fakeKeyring) secret(baseURL string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	secret, ok := f.secrets["pcal\x00"+baseURL]
	return secret, ok
}

// callCount returns how many calls the fake has had.
func (f *fakeKeyring) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}
