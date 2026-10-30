package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rorJeremy/promise-calendar-cli/internal/client"
	"github.com/rorJeremy/promise-calendar-cli/internal/output"
)

// The server's formats (app 85a00a0, app/models/cli_authorization.rb and
// app/controllers/cli/authorizations_controller.rb). A request outside them
// gets a 400 page, never a redirect, so the CLI would only time out.
var (
	serverStateFormat     = regexp.MustCompile(`\A[A-Za-z0-9_-]{1,128}\z`)
	serverChallengeFormat = regexp.MustCompile(`\A[A-Za-z0-9_-]{43}\z`)
	serverVerifierFormat  = regexp.MustCompile(`\A[A-Za-z0-9\-._~]{43,128}\z`)
)

func TestNewPKCE(t *testing.T) {
	p, err := NewPKCE()
	require.NoError(t, err)

	require.Regexp(t, serverVerifierFormat, p.Verifier)
	require.Regexp(t, serverChallengeFormat, p.Challenge)
	require.Regexp(t, serverStateFormat, p.State)
	// 32 random bytes each, unpadded base64url.
	require.Len(t, p.Verifier, 43)
	require.Len(t, p.State, 43)
	sum := sha256.Sum256([]byte(p.Verifier))
	require.Equal(t, base64.RawURLEncoding.EncodeToString(sum[:]), p.Challenge)

	again, err := NewPKCE()
	require.NoError(t, err)
	require.NotEqual(t, p.Verifier, again.Verifier)
	require.NotEqual(t, p.State, again.State)
	require.NotEqual(t, p.Verifier, p.State)
}

// RFC 7636 Appendix B's example.
func TestChallengeRFC7636Example(t *testing.T) {
	require.Equal(t, "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM", Challenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"))
}

func TestAuthorizationURL(t *testing.T) {
	p := PKCE{Verifier: "v", Challenge: "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM", State: "s-1_x"}

	got := AuthorizationURL("https://calendar.promisechurch.io", 51234, p, "Jeremys-MBP", "read")

	u, err := url.Parse(got)
	require.NoError(t, err)
	require.Equal(t, "https://calendar.promisechurch.io/cli/authorization/new", u.Scheme+"://"+u.Host+u.Path)
	require.Equal(t, url.Values{
		"port":           {"51234"},
		"state":          {"s-1_x"},
		"code_challenge": {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"},
		"device":         {"Jeremys-MBP"},
		"permission":     {"read"},
	}, u.Query())
	// The verifier never leaves this host.
	require.NotContains(t, got, "code_verifier")
}

// The server accepts ports 1024–65535 with 4 or 5 digits; every default
// ephemeral range is inside that, but the range is configurable.
func TestCheckPort(t *testing.T) {
	require.NoError(t, CheckPort(1024))
	require.NoError(t, CheckPort(49152))
	require.NoError(t, CheckPort(65535))

	err := CheckPort(1023)
	var e *output.Error
	require.ErrorAs(t, err, &e)
	require.Equal(t, output.CodeUsage, e.Code)
	require.Equal(t, "pcal's login listener got port 1023, and the calendar only sends the browser back to ports 1024 to 65535", e.Message)
	// --remote picks its port the same way, so it can't help (Gate 3b
	// F14); a personal access token needs no port.
	require.Equal(t, "Check this computer's ephemeral port range, or log in with a personal access token instead: `pcal login --with-token`.", e.Hint)
}

// requireAuthError asserts err is an auth error (exit 3) with message.
func requireAuthError(t *testing.T, err error, message string) {
	t.Helper()
	var e *output.Error
	require.ErrorAs(t, err, &e)
	require.Equal(t, output.CodeAuth, e.Code, "%v", err)
	require.Equal(t, message, e.Message)
}

func TestParseCallback(t *testing.T) {
	code, err := ParseCallback(url.Values{"code": {"c0de"}, "state": {"st"}}, "st")
	require.NoError(t, err)
	require.Equal(t, "c0de", code)

	_, err = ParseCallback(url.Values{"code": {"c0de"}, "state": {"other"}}, "st")
	requireAuthError(t, err, "the browser's answer didn't match this login")

	_, err = ParseCallback(url.Values{"error": {"access_denied"}, "state": {"st"}}, "st")
	requireAuthError(t, err, "authorization denied in the browser")
	require.ErrorIs(t, err, ErrDenied)

	// The state is checked first: a denial for another login is a mismatch.
	_, err = ParseCallback(url.Values{"error": {"access_denied"}, "state": {"other"}}, "st")
	requireAuthError(t, err, "the browser's answer didn't match this login")

	_, err = ParseCallback(url.Values{"error": {"server_error"}, "state": {"st"}}, "st")
	requireAuthError(t, err, `the calendar refused the login: "server_error"`)

	_, err = ParseCallback(url.Values{"state": {"st"}}, "st")
	requireAuthError(t, err, "the browser's answer had no login code")
}

func TestParsePastedCallback(t *testing.T) {
	const state = "st"
	tests := []struct {
		name     string
		text     string
		wantCode string
		wantErr  string
		garbage  bool
	}{
		{name: "the address", text: "http://127.0.0.1:51234/callback?code=c0de&state=st", wantCode: "c0de"},
		{name: "with spaces and a line break", text: "  http://127.0.0.1:51234/callback?code=c0de&state=st \n", wantCode: "c0de"},
		{name: "in quotes", text: `"http://127.0.0.1:51234/callback?code=c0de&state=st"`, wantCode: "c0de"},
		{name: "just the query", text: "?code=c0de&state=st", wantCode: "c0de"},
		{name: "denied", text: "http://127.0.0.1:51234/callback?error=access_denied&state=st", wantErr: "authorization denied in the browser"},
		{name: "another login's", text: "http://127.0.0.1:51234/callback?code=c0de&state=old", wantErr: "the browser's answer didn't match this login"},
		{name: "nothing", text: "\n", garbage: true},
		{name: "words", text: "I approved it", garbage: true},
		{name: "the calendar's page", text: "https://calendar.promisechurch.io/cli/authorization/new?port=51234&state=st", garbage: true},
		{name: "not a URL", text: "http://%zz", garbage: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, err := ParsePastedCallback(tt.text, state)

			switch {
			case tt.garbage:
				require.ErrorIs(t, err, ErrNotACallback)
			case tt.wantErr != "":
				requireAuthError(t, err, tt.wantErr)
			default:
				require.NoError(t, err)
				require.Equal(t, tt.wantCode, code)
			}
		})
	}
}

// startCallbackServer starts a CallbackServer for state and closes it when
// the test ends.
func startCallbackServer(t *testing.T, state string) *CallbackServer {
	t.Helper()
	s, err := ListenForCallback(state)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// visit makes the request the browser makes after the calendar redirects
// it, and returns the status and page.
func visit(t *testing.T, s *CallbackServer, pathAndQuery string) (int, string) {
	t.Helper()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%s", s.Port(), pathAndQuery))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}

// waitFor runs Wait with a deadline, so a test that never gets its
// callback fails instead of hanging.
func waitFor(t *testing.T, s *CallbackServer) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.Wait(ctx)
}

func TestCallbackServerListensOnLoopback(t *testing.T) {
	s := startCallbackServer(t, "st")

	require.NoError(t, CheckPort(s.Port()))
	require.Equal(t, "127.0.0.1:"+strconv.Itoa(s.Port()), s.listener.Addr().String())
}

func TestCallbackServerSuccess(t *testing.T) {
	s := startCallbackServer(t, "st")

	status, page := visit(t, s, "/callback?code=c0de&state=st")
	code, err := waitFor(t, s)

	require.NoError(t, err)
	require.Equal(t, "c0de", code)
	require.Equal(t, http.StatusOK, status)
	require.Contains(t, page, "You can close this tab")
}

func TestCallbackServerFailures(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantStatus int
		wantPage   string
		wantErr    string
	}{
		{"denied", "/callback?error=access_denied&state=st", http.StatusOK, "You canceled the login", "authorization denied in the browser"},
		{"no code", "/callback?state=st", http.StatusBadRequest, "no login code", "the browser's answer had no login code"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := startCallbackServer(t, "st")

			status, page := visit(t, s, tt.query)
			_, err := waitFor(t, s)

			requireAuthError(t, err, tt.wantErr)
			require.Equal(t, tt.wantStatus, status)
			require.Contains(t, page, tt.wantPage)
		})
	}
}

// A callback without this login's state gets a 400 page and changes
// nothing, so a stray request from another process on this computer can't
// end the login: only the calendar's redirect carries the state, so only
// it decides (Jeremy's Gate 3c decision; a deliberate deviation from
// basecamp-cli, whose first callback decides,
// internal/auth/callback.go:98-104).
func TestCallbackServerIgnoresAnotherLoginsState(t *testing.T) {
	s := startCallbackServer(t, "st")

	for _, query := range []string{"/callback", "/callback?code=c0de&state=other", "/callback?error=access_denied&state=other"} {
		status, page := visit(t, s, query)
		require.Equal(t, http.StatusBadRequest, status, query)
		require.Contains(t, page, "pcal ignored it", query)
	}
	visit(t, s, "/callback?code=c0de&state=st")
	code, err := waitFor(t, s)

	require.NoError(t, err)
	require.Equal(t, "c0de", code)
}

// Only the first callback counts: a second one, even a valid one, gets a
// page saying the login already finished and changes nothing.
func TestCallbackServerTakesOnlyTheFirstCallback(t *testing.T) {
	s := startCallbackServer(t, "st")

	visit(t, s, "/callback?code=first&state=st")
	status, page := visit(t, s, "/callback?code=second&state=st")
	code, err := waitFor(t, s)

	require.NoError(t, err)
	require.Equal(t, "first", code)
	require.Equal(t, http.StatusBadRequest, status)
	require.Contains(t, page, "already finished")
}

// Any other path (a browser asks for /favicon.ico) is a 404 and doesn't
// end the wait.
func TestCallbackServerIgnoresOtherPaths(t *testing.T) {
	s := startCallbackServer(t, "st")

	status, _ := visit(t, s, "/favicon.ico")
	require.Equal(t, http.StatusNotFound, status)
	visit(t, s, "/callback?code=c0de&state=st")
	code, err := waitFor(t, s)

	require.NoError(t, err)
	require.Equal(t, "c0de", code)
}

// pcal closes the server as soon as Wait has the answer, which can be
// before the page is sent. Close lets that page finish, so the browser
// always gets it (Gate 3b F12, P7-j2): here the handler is held after
// handing over the answer until Close is under way.
func TestCallbackServerCloseLetsAPageFinish(t *testing.T) {
	s := startCallbackServer(t, "st")
	release := make(chan struct{})
	s.answered = func() { <-release }
	type visited struct {
		status int
		page   string
		err    error
	}
	got := make(chan visited, 1)
	go func() {
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?code=c0de&state=st", s.Port())) //nolint:noctx // a test's browser
		if err != nil {
			got <- visited{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		page, err := io.ReadAll(resp.Body)
		got <- visited{resp.StatusCode, string(page), err}
	}()

	code, err := waitFor(t, s)
	require.NoError(t, err)
	require.Equal(t, "c0de", code)
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	time.Sleep(100 * time.Millisecond)
	close(release)

	v := <-got
	require.NoError(t, v.err)
	require.Equal(t, http.StatusOK, v.status)
	require.Contains(t, v.page, "You can close this tab")
	require.NoError(t, <-closed)
}

func TestCallbackServerWaitEndsWithTheContext(t *testing.T) {
	s := startCallbackServer(t, "st")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := s.Wait(ctx)

	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// tokenServer is the calendar's POST /cli/token on httptest. It records the
// body and answers status with a golden file or body.
func tokenServer(t *testing.T, status int, body string) (*client.Client, *string) {
	t.Helper()
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/cli/token", r.URL.Path)
		require.Empty(t, r.Header.Get("Authorization"))
		data, _ := io.ReadAll(r.Body)
		got = string(data)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return client.New(srv.URL, "", client.Options{}), &got
}

func goldenFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "api", name))
	require.NoError(t, err)
	return string(data)
}

// The body is flat, unlike every other write: {"code","code_verifier"}.
func TestExchange(t *testing.T) {
	c, body := tokenServer(t, http.StatusCreated, goldenFile(t, "cli_token.json"))

	tok, err := Exchange(context.Background(), c, "c0de", "the-verifier")

	require.NoError(t, err)
	require.JSONEq(t, `{"code":"c0de","code_verifier":"the-verifier"}`, *body)
	require.Equal(t, client.CLIToken{
		Token:      "pcal_0000000000000000000000000000000000000000000000000000000000000000",
		Permission: "write",
		User:       client.User{ID: "ae6c10d0-0b13-554c-b976-a05d8a18f0cc", Name: "Admin User"},
	}, tok)
}

func TestExchangeErrors(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantCode string
		wantMsg  string
		wantHint string
	}{
		{
			// Unknown, expired, reused, or the wrong verifier: every attempt
			// uses the code up, so it's start again.
			name: "invalid_grant", status: http.StatusBadRequest, body: goldenFile(t, "error_invalid_grant.json"),
			wantCode: output.CodeAuth, wantMsg: "that login code expired or was already used", wantHint: "Run `pcal login` again.",
		},
		{
			// More than 10 a minute from one address. No automatic retry.
			name: "rate_limited", status: http.StatusTooManyRequests, body: `{"error":"rate_limited"}`,
			wantCode: output.CodeRateLimit, wantMsg: "too many login attempts", wantHint: "Wait a minute, then run `pcal login` again.",
		},
		{
			name: "a server error", status: http.StatusInternalServerError, body: "",
			wantCode: output.CodeAPI, wantMsg: "Internal Server Error", wantHint: "The calendar had a problem. Try again later, or report it.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := tokenServer(t, tt.status, tt.body)

			_, err := Exchange(context.Background(), c, "c0de", "v")

			var e *output.Error
			require.ErrorAs(t, err, &e)
			require.Equal(t, tt.wantCode, e.Code)
			require.Equal(t, tt.wantMsg, e.Message)
			require.Equal(t, tt.wantHint, e.Hint)
		})
	}
}
