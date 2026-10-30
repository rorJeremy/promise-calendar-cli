package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/rorJeremy/promise-calendar-cli/internal/auth"
	"github.com/rorJeremy/promise-calendar-cli/internal/client"
	"github.com/rorJeremy/promise-calendar-cli/internal/config"
)

// The fake calendar's tokens: goodToken is valid, and so is cliToken, the
// token POST /cli/token mints (cli_token.json). Anything else is rejected
// with 401, as the app does.
const (
	goodToken = "pcal_good"
	cliToken  = "pcal_0000000000000000000000000000000000000000000000000000000000000000"
)

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
	// decision is what the person clicks on the authorization page:
	// "allow" (the default), "deny", or "mismatch" (a callback carrying
	// another login's state).
	decision string
	// grants are the codes the authorization page issued: code → the
	// challenge it was issued for. Redeeming one removes it.
	grants map[string]string
}

// newFakeCalendar starts a fake calendar that knows goodToken and
// cliToken: GET /my/identity answers identity.json, and DELETE
// /my/current_access_token answers 204. Any other token gets the app's
// 401. The browser login's two halves work as the app's do (see
// authorizationPage and redeem).
func newFakeCalendar(t *testing.T) *fakeCalendar {
	t.Helper()
	f := &fakeCalendar{t: t, routes: map[string]http.HandlerFunc{}, decision: "allow", grants: map[string]string{}}
	f.routes["GET /my/identity"] = f.withToken([]string{goodToken, cliToken}, func(w http.ResponseWriter, _ *http.Request) {
		f.serve(w, http.StatusOK, "identity.json")
	})
	f.routes["DELETE /my/current_access_token"] = f.withToken([]string{goodToken, cliToken}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	f.routes["GET /cli/authorization/new"] = f.authorizationPage
	f.routes["POST /cli/token"] = f.redeem
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	return f
}

// The server's checks on the authorization page's parameters (app
// 85a00a0, Cli::AuthorizationsController#valid_request? and
// CliAuthorization::CHALLENGE_FORMAT). Anything else gets a 400 page.
var (
	serverPortFormat      = regexp.MustCompile(`\A\d{4,5}\z`)
	serverStateFormat     = regexp.MustCompile(`\A[A-Za-z0-9_-]{1,128}\z`)
	serverChallengeFormat = regexp.MustCompile(`\A[A-Za-z0-9_-]{43}\z`)
)

// authorizationPage is GET /cli/authorization/new with the person already
// signed in and clicking f.decision at once: it checks the parameters as
// the app does, issues the code "c0de" for the challenge, and redirects
// the browser to pcal's callback on 127.0.0.1.
func (f *fakeCalendar) authorizationPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	port, _ := strconv.Atoi(q.Get("port"))
	if !serverPortFormat.MatchString(q.Get("port")) || port < 1024 || port > 65535 ||
		!serverStateFormat.MatchString(q.Get("state")) || !serverChallengeFormat.MatchString(q.Get("code_challenge")) ||
		strings.TrimSpace(q.Get("device")) == "" || (q.Get("permission") != "read" && q.Get("permission") != "write") {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "<h1>Your authorization link is invalid</h1>")
		return
	}
	f.mu.Lock()
	decision := f.decision
	f.mu.Unlock()

	callback := url.Values{"state": {q.Get("state")}}
	switch decision {
	case "deny":
		callback.Set("error", "access_denied")
	case "mismatch":
		callback.Set("code", "c0de")
		callback.Set("state", "another-login")
	default:
		f.grant("c0de", q.Get("code_challenge"))
		callback.Set("code", "c0de")
	}
	http.Redirect(w, r, "http://127.0.0.1:"+q.Get("port")+"/callback?"+callback.Encode(), http.StatusFound)
}

// grant records that code was issued for challenge.
func (f *fakeCalendar) grant(code, challenge string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grants[code] = challenge
}

// redeem is POST /cli/token: a flat {"code","code_verifier"} body, no
// authentication. The code is used up by any attempt, and the verifier
// must match the challenge it was issued for (as CliAuthorization.redeem).
func (f *fakeCalendar) redeem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code     string `json:"code"`
		Verifier string `json:"code_verifier"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	challenge, ok := f.grants[body.Code]
	delete(f.grants, body.Code)
	f.mu.Unlock()
	if !ok || r.Header.Get("Authorization") != "" || auth.Challenge(body.Verifier) != challenge {
		f.serve(w, http.StatusBadRequest, "error_invalid_grant.json")
		return
	}
	f.serve(w, http.StatusCreated, "cli_token.json")
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
func (f *fakeCalendar) withToken(tokens []string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for _, token := range tokens {
			if r.Header.Get("Authorization") == "Bearer "+token {
				h(w, r)
				return
			}
		}
		w.Header().Set("WWW-Authenticate", `Token realm="Application"`)
		f.serve(w, http.StatusUnauthorized, "error_unauthorized.json")
	}
}

// fakeBrowser stands in for the person's browser.
type fakeBrowser struct {
	mu     sync.Mutex
	opened []string
	// fail makes opening it fail, as when there's no opener installed.
	fail bool
	// visit makes it load the page it's opened on, and follow the
	// calendar's redirect back to pcal's callback.
	visit bool
}

// open is the browser opener pcal is given.
func (b *fakeBrowser) open(target string) error {
	b.mu.Lock()
	b.opened = append(b.opened, target)
	b.mu.Unlock()
	if b.fail {
		return errors.New(`exec: "xdg-open": executable file not found in $PATH`)
	}
	if b.visit {
		go visitPage(target)
	}
	return nil
}

// openedURLs returns what pcal opened.
func (b *fakeBrowser) openedURLs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.opened...)
}

// visitPage loads target as a browser does, following redirects.
func visitPage(target string) {
	resp, err := http.Get(target) //nolint:gosec,noctx // a test's fake browser
	if err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// printedLink returns the authorization link in a line pcal printed, or "".
func printedLink(line string) string {
	if link := strings.TrimSpace(line); strings.HasPrefix(link, "http") && strings.Contains(link, "/cli/authorization/new?") {
		return link
	}
	return ""
}

// personOpensTheLink is an onStderrLine hook: the person opens the link pcal
// printed in a browser on this computer.
func personOpensTheLink(line string) {
	if link := printedLink(line); link != "" {
		go visitPage(link)
	}
}

// personOnAnotherDevice returns an onStderrLine hook for the paste flow:
// the person opens the printed link on another device, where the calendar's
// redirect to 127.0.0.1 doesn't load, and pastes into stdin first each of
// before, then the address the browser ended up on. It returns pcal's
// stdin.
func personOnAnotherDevice(t *testing.T, before ...string) (io.Reader, func(string)) {
	t.Helper()
	stdin, typed := io.Pipe()
	t.Cleanup(func() { _ = typed.Close() })
	noRedirects := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return stdin, func(line string) {
		link := printedLink(line)
		if link == "" {
			return
		}
		go func() {
			resp, err := noRedirects.Get(link) //nolint:noctx // a test's fake browser
			if err != nil {
				return
			}
			_ = resp.Body.Close()
			lines := strings.Join(append(before, resp.Header.Get("Location")), "\n") + "\n"
			_, _ = io.WriteString(typed, lines)
		}()
	}
}

// lineWatcher is a stderr that also hands each complete line to onLine,
// so a test can act on what pcal prints while it waits.
type lineWatcher struct {
	mu      sync.Mutex
	all     bytes.Buffer
	partial string
	onLine  func(string)
}

func (w *lineWatcher) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.all.Write(p)
	w.partial += string(p)
	var lines []string
	for {
		i := strings.IndexByte(w.partial, '\n')
		if i < 0 {
			break
		}
		lines = append(lines, w.partial[:i])
		w.partial = w.partial[i+1:]
	}
	w.mu.Unlock()
	if w.onLine != nil {
		for _, line := range lines {
			w.onLine(line)
		}
	}
	return len(p), nil
}

func (w *lineWatcher) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.all.String()
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
	errTTY bool
	outTTY bool
	// promptOptions are extra bubbletea options for the prompts.
	promptOptions []tea.ProgramOption
	// store, when set, replaces the credential store.
	store credentialStore
	// interrupt, when set, replaces catching Ctrl-C.
	interrupt func(context.Context) (context.Context, context.CancelFunc)
	// browser is the browser pcal opens.
	browser *fakeBrowser
	// goos is the OS pcal believes it's on ("darwin" unless set).
	goos string
	// hostname is this computer's name, as os.Hostname returns it.
	hostname func() (string, error)
	// loginWait bounds a browser login's wait (5 minutes unless set).
	loginWait time.Duration
	// revokeWait bounds revoking a token after a login (10 seconds unless
	// set).
	revokeWait time.Duration
	// onStderrLine, when set, sees each line pcal prints on stderr as it's
	// printed.
	onStderrLine func(string)
}

// newHarness returns a harness whose calendar is baseURL (through
// PCAL_URL), with a fresh config directory and an empty keyring.
func newHarness(t *testing.T, baseURL string) *harness {
	t.Helper()
	keyring.MockInit()
	return &harness{
		t:        t,
		env:      map[string]string{"XDG_CONFIG_HOME": t.TempDir(), "PCAL_URL": baseURL},
		stdin:    strings.NewReader(""),
		browser:  &fakeBrowser{visit: true},
		goos:     "darwin",
		hostname: func() (string, error) { return "test-host", nil },
	}
}

// run runs pcal with args and returns what it produced.
func (h *harness) run(args ...string) result {
	h.t.Helper()
	var stdout bytes.Buffer
	stderr := &lineWatcher{onLine: h.onStderrLine}
	a := &app{
		streams:       Streams{In: h.stdin, Out: &stdout, Err: stderr, OutTTY: h.outTTY, InTTY: h.inTTY, ErrTTY: h.errTTY},
		getenv:        env(h.env),
		store:         h.store,
		interrupt:     h.interrupt,
		clientOptions: client.Options{Timeout: 5 * time.Second},
		openBrowser:   h.browser.open,
		goos:          h.goos,
		hostname:      h.hostname,
		loginWait:     h.loginWait,
		revokeWait:    h.revokeWait,
		promptOptions: h.promptOptions,
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

// terminatedContext stands in for a SIGTERM: the context a login waits on
// is already over, and says so.
func terminatedContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(ctx)
	cancel(interruption{syscall.SIGTERM})
	return ctx, func() { cancel(nil) }
}

// keyboard stands in for the person pressing Ctrl-C, or a supervisor's
// SIGTERM, at a moment a test picks. Its interrupt is the app's interrupt
// seam: each context it hands out listens until its stop function runs, as
// a signal.Notify channel stays registered until signal.Stop, and a signal
// ends every context still listening, as it reaches every registered
// channel.
type keyboard struct {
	mu        sync.Mutex
	listening map[int]context.CancelCauseFunc
	next      int
}

func newKeyboard() *keyboard {
	return &keyboard{listening: map[int]context.CancelCauseFunc{}}
}

// interrupt is the app's interrupt seam.
func (k *keyboard) interrupt(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(parent)
	k.mu.Lock()
	id := k.next
	k.next++
	k.listening[id] = cancel
	k.mu.Unlock()
	return ctx, func() {
		k.mu.Lock()
		delete(k.listening, id)
		k.mu.Unlock()
		cancel(nil)
	}
}

// press presses Ctrl-C and reports whether anything was listening for it.
// With nothing listening, a real Ctrl-C ends pcal the usual way.
func (k *keyboard) press() bool {
	return k.send(os.Interrupt)
}

// terminate sends SIGTERM, as press sends Ctrl-C.
func (k *keyboard) terminate() bool {
	return k.send(syscall.SIGTERM)
}

func (k *keyboard) send(sig os.Signal) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, cancel := range k.listening {
		cancel(interruption{sig})
	}
	return len(k.listening) > 0
}

// typing is a stdin that returns each chunk in its own read, pausing before
// every chunk after the first, as a person typing at a terminal does.
type typing struct {
	chunks []string
	pause  time.Duration
	reads  int
}

func (r *typing) Read(b []byte) (int, error) {
	if len(r.chunks) == 0 {
		return 0, io.EOF
	}
	if r.reads > 0 {
		time.Sleep(r.pause)
	}
	r.reads++
	n := copy(b, r.chunks[0])
	r.chunks = r.chunks[1:]
	return n, nil
}

// drawnPrompt is what a terminal gives a drawn prompt and a buffer
// doesn't: a window size, without which bubbletea draws nothing.
var drawnPrompt = []tea.ProgramOption{tea.WithWindowSize(80, 24)}

// failingSet is a credential store whose Set fails, as when
// credentials.json can't be written.
type failingSet struct {
	credentialStore
}

func (failingSet) Set(string, auth.Credential) error {
	return errors.New("writing credentials.json: permission denied")
}

// onSet is a credential store that calls before at the start of every Set.
type onSet struct {
	credentialStore
	before func()
}

func (s onSet) Set(baseURL string, c auth.Credential) error {
	s.before()
	return s.credentialStore.Set(baseURL, c)
}
