package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/rorJeremy/promise-calendar-cli/internal/auth"
	"github.com/rorJeremy/promise-calendar-cli/internal/client"
	"github.com/rorJeremy/promise-calendar-cli/internal/config"
)

// The fake calendar's tokens: goodToken is valid, anything else is
// rejected with 401 as the app does.
const goodToken = "pcal_good"

// env returns a getenv func that reads from vars.
func env(vars map[string]string) func(string) string {
	return func(key string) string { return vars[key] }
}

// golden returns a golden file from testdata/api, the calendar app's
// recorded responses.
func golden(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "api", name))
	require.NoError(t, err)
	return data
}

// request is one request the fake calendar received.
type request struct {
	Method string
	Path   string
	Auth   string
	Body   string
}

// fakeCalendar is the calendar app on httptest: it serves the golden JSON
// and records every request. Routes are "METHOD /path"; a test replaces
// one to change its answer.
type fakeCalendar struct {
	t      *testing.T
	server *httptest.Server
	mu     sync.Mutex
	routes map[string]http.HandlerFunc
	log    []request
}

// newFakeCalendar starts a fake calendar that knows goodToken:
// GET /my/identity answers identity.json, and DELETE
// /my/current_access_token answers 204. Any other token gets the app's 401.
func newFakeCalendar(t *testing.T) *fakeCalendar {
	t.Helper()
	f := &fakeCalendar{t: t, routes: map[string]http.HandlerFunc{}}
	f.routes["GET /my/identity"] = f.withToken(goodToken, func(w http.ResponseWriter, _ *http.Request) {
		f.serve(w, http.StatusOK, "identity.json")
	})
	f.routes["DELETE /my/current_access_token"] = f.withToken(goodToken, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

// URL is the fake calendar's base URL.
func (f *fakeCalendar) URL() string {
	return f.server.URL
}

func (f *fakeCalendar) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.log = append(f.log, request{Method: r.Method, Path: r.URL.RequestURI(), Auth: r.Header.Get("Authorization"), Body: string(body)})
	route := f.routes[r.Method+" "+r.URL.Path]
	f.mu.Unlock()

	if route == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"status":404,"error":"Not Found"}`))
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	route(w, r)
}

// route replaces the handler for "METHOD /path".
func (f *fakeCalendar) route(key string, h http.HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[key] = h
}

// withToken answers with h when the request's bearer token is one of
// tokens, and otherwise as the app answers a rejected token.
func (f *fakeCalendar) withToken(token string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.Header().Set("WWW-Authenticate", `Token realm="Application"`)
			f.serve(w, http.StatusUnauthorized, "error_unauthorized.json")
			return
		}
		h(w, r)
	}
}

// serve answers with status and a golden file.
func (f *fakeCalendar) serve(w http.ResponseWriter, status int, name string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(golden(f.t, name))
}

// requests returns what the fake calendar has received.
func (f *fakeCalendar) requests() []request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]request(nil), f.log...)
}

// harness runs pcal in-process against a fake calendar, with its own
// environment and config directory, and go-keyring's in-memory keyring.
type harness struct {
	t      *testing.T
	env    map[string]string
	stdin  io.Reader
	inTTY  bool
	outTTY bool
	// store, when set, replaces the credential store.
	store credentialStore
	// interrupt, when set, replaces catching Ctrl-C.
	interrupt func(context.Context) (context.Context, context.CancelFunc)
}

// newHarness returns a harness whose calendar is baseURL (through
// PCAL_URL), with a fresh config directory and an empty keyring.
func newHarness(t *testing.T, baseURL string) *harness {
	t.Helper()
	keyring.MockInit()
	return &harness{
		t:     t,
		env:   map[string]string{"XDG_CONFIG_HOME": t.TempDir(), "PCAL_URL": baseURL},
		stdin: strings.NewReader(""),
	}
}

// run runs pcal with args and returns what it produced.
func (h *harness) run(args ...string) result {
	h.t.Helper()
	var stdout, stderr bytes.Buffer
	a := &app{
		streams:       Streams{In: h.stdin, Out: &stdout, Err: &stderr, OutTTY: h.outTTY, InTTY: h.inTTY},
		getenv:        env(h.env),
		store:         h.store,
		interrupt:     h.interrupt,
		clientOptions: client.Options{Timeout: 5 * time.Second},
	}
	code := execute(a, newRootCmd(a), args)
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// configDir is pcal's config directory under the harness's
// XDG_CONFIG_HOME.
func (h *harness) configDir() string {
	return filepath.Join(h.env["XDG_CONFIG_HOME"], "pcal")
}

// credentials returns a store over the harness's config directory, to put
// credentials in before a run or look at them after.
func (h *harness) credentials() *auth.Store {
	return auth.NewStore(h.configDir(), env(h.env), io.Discard)
}

// stored returns the credential stored for baseURL, failing the test if
// there's none.
func (h *harness) stored(baseURL string) auth.Credential {
	h.t.Helper()
	c, err := h.credentials().Get(baseURL)
	require.NoError(h.t, err)
	return c
}

// requireNothingStored asserts no credential is stored for baseURL.
func (h *harness) requireNothingStored(baseURL string) {
	h.t.Helper()
	_, err := h.credentials().Get(baseURL)
	require.ErrorIs(h.t, err, auth.ErrNoCredential)
}

// savedConfig reads config.json from the harness's config directory.
func (h *harness) savedConfig() config.Config {
	h.t.Helper()
	cfg, err := config.Load(h.configDir())
	require.NoError(h.t, err)
	return cfg
}

// envelope is the JSON shape of a successful run.
type envelope struct {
	OK      bool            `json:"ok"`
	Data    json.RawMessage `json:"data"`
	Summary string          `json:"summary"`
}

// decodeEnvelope decodes stdout as a success envelope.
func decodeEnvelope(t *testing.T, stdout string) envelope {
	t.Helper()
	var env envelope
	require.NoError(t, json.Unmarshal([]byte(stdout), &env), "stdout: %s", stdout)
	require.True(t, env.OK)
	return env
}

// canceledContext stands in for Ctrl-C: the context a login waits on is
// already over.
func canceledContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	return ctx, cancel
}
