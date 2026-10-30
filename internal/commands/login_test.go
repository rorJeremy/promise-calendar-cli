package commands

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rorJeremy/promise-calendar-cli/internal/auth"
)

// openedQuery returns the query of the one URL pcal opened, after checking
// it's the calendar's authorization page.
func openedQuery(t *testing.T, h *harness, baseURL string) url.Values {
	t.Helper()
	opened := h.browser.openedURLs()
	require.Len(t, opened, 1)
	u, err := url.Parse(opened[0])
	require.NoError(t, err)
	require.Equal(t, baseURL+"/cli/authorization/new", u.Scheme+"://"+u.Host+u.Path)
	return u.Query()
}

// redeemed returns the body of the one POST /cli/token pcal made.
func redeemed(t *testing.T, cal *fakeCalendar) map[string]string {
	t.Helper()
	var body map[string]string
	n := 0
	for _, r := range cal.requests() {
		if r.Method == "POST" && r.Path == "/cli/token" {
			n++
			require.Empty(t, r.Auth)
			require.NoError(t, json.Unmarshal([]byte(r.Body), &body))
		}
	}
	require.Equal(t, 1, n, "POST /cli/token requests")
	return body
}

// requireNoRedeem asserts pcal never asked for a token.
func requireNoRedeem(t *testing.T, cal *fakeCalendar) {
	t.Helper()
	for _, r := range cal.requests() {
		require.False(t, r.Method == "POST" && r.Path == "/cli/token", "unexpected POST /cli/token")
	}
}

// The whole browser login, as the app does its half: pcal listens on
// 127.0.0.1, opens the authorization page, the person approves, the
// browser comes back with a code, and pcal redeems it with its verifier
// (a flat body), stores the token, and checks it with /my/identity.
func TestBrowserLogin(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())

	r := h.run("login")

	require.Equal(t, 0, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	env := decodeEnvelope(t, r.stdout)
	require.Equal(t, "Logged in as Admin User (write)", env.Summary)
	require.JSONEq(t, string(golden(t, "identity.json")), string(env.Data))

	q := openedQuery(t, h, cal.URL())
	port, err := strconv.Atoi(q.Get("port"))
	require.NoError(t, err)
	require.GreaterOrEqual(t, port, 1024)
	require.Equal(t, "test-host", q.Get("device"))
	require.Equal(t, "write", q.Get("permission"))
	require.Regexp(t, serverStateFormat, q.Get("state"))
	require.Len(t, q.Get("state"), 43)

	body := redeemed(t, cal)
	require.Equal(t, []string{"code", "code_verifier"}, sortedKeys(body))
	require.Equal(t, "c0de", body["code"])
	require.Equal(t, q.Get("code_challenge"), auth.Challenge(body["code_verifier"]))

	require.Equal(t, auth.Credential{Token: cliToken, Source: auth.SourceBrowser, Store: auth.StoreKeyring}, h.stored(cal.URL()))
	last := cal.requests()[len(cal.requests())-1]
	require.Equal(t, request{Method: "GET", Path: "/my/identity", Auth: "Bearer " + cliToken}, last)
	require.Equal(t, "America/Chicago", h.savedConfig().TimeZone)

	// The link is always printed, in case the browser doesn't open.
	require.Equal(t, "Open this link to log in:\n"+
		"  "+h.browser.openedURLs()[0]+"\n"+
		"Opening your browser… If nothing appears, open the link above.\n"+
		"Waiting for you to approve pcal in the browser (up to 5 minutes)…\n", r.stderr)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	if len(keys) == 2 && keys[0] > keys[1] {
		keys[0], keys[1] = keys[1], keys[0]
	}
	return keys
}

// `pcal auth login` is the same command as `pcal login` (Jeremy's Gate 3
// decision, as basecamp-cli's buildLoginCmd).
func TestAuthLoginIsTheBrowserLogin(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())

	r := h.run("auth", "login")

	require.Equal(t, 0, r.code, "stdout: %s", r.stdout)
	require.Equal(t, cliToken, h.stored(cal.URL()).Token)
}

// `pcal login --with-token` is the token login.
func TestLoginWithTokenOnTheTopLevelCommand(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	h.stdin = strings.NewReader(goodToken)

	r := h.run("login", "--with-token")

	require.Equal(t, 0, r.code, "stdout: %s", r.stdout)
	require.Equal(t, auth.SourceImported, h.stored(cal.URL()).Source)
	require.Empty(t, h.browser.openedURLs())
}

func TestBrowserLoginReadOnly(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())

	r := h.run("login", "--read-only")

	require.Equal(t, 0, r.code, "stdout: %s", r.stdout)
	require.Equal(t, "read", openedQuery(t, h, cal.URL()).Get("permission"))
}

// The server names the token "pcal on <device>" and needs a device.
func TestBrowserLoginDeviceName(t *testing.T) {
	tests := []struct {
		name     string
		hostname func() (string, error)
		want     string
	}{
		{"the hostname", func() (string, error) { return "Jeremys-MacBook-Pro.local", nil }, "Jeremys-MacBook-Pro.local"},
		{"no hostname", func() (string, error) { return "", io.ErrUnexpectedEOF }, "unknown host"},
		{"a blank one", func() (string, error) { return "  ", nil }, "unknown host"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			h := newHarness(t, cal.URL())
			h.hostname = tt.hostname

			r := h.run("login")

			require.Equal(t, 0, r.code, "stdout: %s", r.stdout)
			require.Equal(t, tt.want, openedQuery(t, h, cal.URL()).Get("device"))
		})
	}
}

// A browser that doesn't open isn't a failed login: the person opens the
// printed link.
func TestBrowserLoginWhenTheBrowserDoesntOpen(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	h.browser.fail = true
	h.onStderrLine = personOpensTheLink

	r := h.run("login")

	require.Equal(t, 0, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	require.Contains(t, r.stderr, "\nCouldn't open a browser. Open the link above.\n")
	require.NotContains(t, r.stderr, "Opening your browser")
	require.Equal(t, cliToken, h.stored(cal.URL()).Token)
}

// --no-browser keeps the listener but only prints the link.
func TestBrowserLoginNoBrowser(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	h.onStderrLine = personOpensTheLink

	r := h.run("login", "--no-browser")

	require.Equal(t, 0, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	require.Empty(t, h.browser.openedURLs())
	require.NotContains(t, r.stderr, "Opening your browser")
	require.Equal(t, cliToken, h.stored(cal.URL()).Token)
}

func TestBrowserLoginFailures(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*harness, *fakeCalendar)
		wantExit int
		wantCode string
		wantMsg  string
		wantHint string
		redeemed bool
	}{
		{
			name:     "denied in the browser",
			setup:    func(_ *harness, cal *fakeCalendar) { cal.decision = "deny" },
			wantExit: 3, wantCode: "auth_required", wantMsg: "authorization denied in the browser", wantHint: "Run `pcal login` again.",
		},
		{
			// A callback with another login's state is ignored, so pcal
			// keeps waiting for the right one (Jeremy's Gate 3c decision).
			name: "another login's state is ignored",
			setup: func(h *harness, cal *fakeCalendar) {
				cal.decision = "mismatch"
				h.loginWait = 200 * time.Millisecond
			},
			wantExit: 3, wantCode: "auth_required", wantMsg: "timed out waiting for you to approve pcal in the browser", wantHint: "Run `pcal login` again.",
		},
		{
			name: "no answer in time",
			setup: func(h *harness, _ *fakeCalendar) {
				h.browser.visit = false
				h.loginWait = 200 * time.Millisecond
			},
			wantExit: 3, wantCode: "auth_required", wantMsg: "timed out waiting for you to approve pcal in the browser", wantHint: "Run `pcal login` again.",
		},
		{
			name: "the code expired or was used",
			setup: func(_ *harness, cal *fakeCalendar) {
				cal.route("POST /cli/token", func(w http.ResponseWriter, _ *http.Request) {
					cal.serve(w, http.StatusBadRequest, "error_invalid_grant.json")
				})
			},
			wantExit: 3, wantCode: "auth_required", wantMsg: "that login code expired or was already used", wantHint: "Run `pcal login` again.",
			redeemed: true,
		},
		{
			name: "rate limited",
			setup: func(_ *harness, cal *fakeCalendar) {
				cal.route("POST /cli/token", func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = io.WriteString(w, `{"error":"rate_limited"}`)
				})
			},
			wantExit: 5, wantCode: "rate_limit", wantMsg: "too many login attempts", wantHint: "Wait a minute, then run `pcal login` again.",
			redeemed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			h := newHarness(t, cal.URL())
			tt.setup(h, cal)

			r := h.run("login")

			require.Equal(t, tt.wantExit, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
			env := decodeError(t, r.stdout)
			require.Equal(t, tt.wantCode, env.Error.Code)
			require.Equal(t, tt.wantMsg, env.Error.Message)
			require.Equal(t, tt.wantHint, env.Error.Hint)
			if tt.redeemed {
				redeemed(t, cal)
			} else {
				requireNoRedeem(t, cal)
			}
			h.requireNothingStored(cal.URL())
		})
	}
}

// Ctrl-C while pcal waits for the browser cancels the login (exit 130).
func TestBrowserLoginCanceled(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	h.browser.visit = false
	h.interrupt = canceledContext

	r := h.run("login")

	require.Equal(t, 130, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	require.Empty(t, r.stdout)
	require.True(t, strings.HasSuffix(r.stderr, "\nLogin canceled. Nothing was stored.\n"), r.stderr)
	requireNoRedeem(t, cal)
	h.requireNothingStored(cal.URL())
}

func TestBrowserLoginRefusals(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		wantMsg string
	}{
		{"PCAL_TOKEN is set", []string{"login"}, map[string]string{"PCAL_TOKEN": goodToken}, "PCAL_TOKEN is set, so pcal would keep using it instead of this login"},
		{"--remote with --local", []string{"login", "--remote", "--local"}, nil, "--remote and --local can't be used together"},
		{"--with-token with --remote", []string{"login", "--with-token", "--remote"}, nil, "--with-token can't be used with --read-only, --remote, --local, or --no-browser"},
		{"--with-token with --read-only", []string{"auth", "login", "--with-token", "--read-only"}, nil, "--with-token can't be used with --read-only, --remote, --local, or --no-browser"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			h := newHarness(t, cal.URL())
			for k, v := range tt.env {
				h.env[k] = v
			}

			r := h.run(tt.args...)

			require.Equal(t, 1, r.code, "stdout: %s", r.stdout)
			env := decodeError(t, r.stdout)
			require.Equal(t, "usage", env.Error.Code)
			require.Equal(t, tt.wantMsg, env.Error.Message)
			require.Empty(t, h.browser.openedURLs())
			require.Empty(t, cal.requests())
		})
	}
}

// A browser login over an earlier one revokes the old token (Gate 3).
func TestBrowserLoginRevokesTheTokenItReplaces(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	require.NoError(t, h.credentials().Set(cal.URL(), auth.Credential{Token: goodToken, Source: auth.SourceBrowser}))

	r := h.run("login")

	require.Equal(t, 0, r.code, "stdout: %s", r.stdout)
	require.Contains(t, cal.requests(), request{Method: "DELETE", Path: "/my/current_access_token", Auth: "Bearer " + goodToken})
	require.Equal(t, cliToken, h.stored(cal.URL()).Token)
}

// The new token is checked with /my/identity before it's stored, as the
// token login checks its token (Jeremy's Gate 3c decision; basecamp-cli's
// verifyAndStore). When that fails, the new token is revoked and the old
// login stays as it was, its token still live.
func TestBrowserLoginWhenTheIdentityCheckFails(t *testing.T) {
	cal := newFakeCalendar(t)
	cal.route("GET /my/identity", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	h := newHarness(t, cal.URL())
	old := auth.Credential{Token: goodToken, Source: auth.SourceBrowser, Store: auth.StoreKeyring}
	require.NoError(t, h.credentials().Set(cal.URL(), old))

	r := h.run("login")

	require.Equal(t, 7, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	env := decodeError(t, r.stdout)
	require.Equal(t, "api_error", env.Error.Code)
	require.Equal(t, "Internal Server Error", env.Error.Message)
	requests := cal.requests()
	require.Equal(t, []request{
		{Method: "GET", Path: "/my/identity", Auth: "Bearer " + cliToken},
		{Method: "DELETE", Path: "/my/current_access_token", Auth: "Bearer " + cliToken},
	}, requests[len(requests)-2:])
	require.NotContains(t, requests, request{Method: "DELETE", Path: "/my/current_access_token", Auth: "Bearer " + goodToken})
	require.Equal(t, old, h.stored(cal.URL()))
}

// Ctrl-C while POST /cli/token is in flight doesn't cut the request off,
// since the server may already have minted the token: pcal waits for the
// answer (the client's timeout bounds it) and revokes a token that came
// back, so the login leaves nothing live with no record of it. So does
// Ctrl-C while the new token is checked (Jeremy's Gate 3c decision;
// basecamp-cli's discardGrant).
func TestBrowserLoginCanceledAfterTheCodeIsSent(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(*fakeCalendar, *keyboard)
		wantExit   int
		wantStderr func(baseURL string) string
		wantRevoke bool
	}{
		{
			name: "during the exchange",
			setup: func(cal *fakeCalendar, kb *keyboard) {
				cal.route("POST /cli/token", func(w http.ResponseWriter, r *http.Request) {
					kb.press()
					cal.redeem(w, r)
				})
			},
			wantStderr: func(string) string { return "\nLogin canceled. The new token was revoked.\n" },
			wantRevoke: true,
		},
		{
			// SIGTERM stops it the same way, but says so and exits 143.
			name: "SIGTERM during the exchange",
			setup: func(cal *fakeCalendar, kb *keyboard) {
				cal.route("POST /cli/token", func(w http.ResponseWriter, r *http.Request) {
					kb.terminate()
					cal.redeem(w, r)
				})
			},
			wantExit:   143,
			wantStderr: func(string) string { return "\nLogin terminated. The new token was revoked.\n" },
			wantRevoke: true,
		},
		{
			name: "during the exchange, and the revoke fails",
			setup: func(cal *fakeCalendar, kb *keyboard) {
				cal.route("POST /cli/token", func(w http.ResponseWriter, r *http.Request) {
					kb.press()
					cal.redeem(w, r)
				})
				cal.route("DELETE /my/current_access_token", func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusBadGateway)
				})
			},
			wantStderr: func(baseURL string) string {
				return "\nLogin canceled, but pcal couldn't revoke the new token (Bad Gateway); revoke it at " + baseURL + "/my/access_tokens.\n"
			},
			wantRevoke: true,
		},
		{
			// The line quotes the server, so its control characters are
			// shown as escapes, like a warning's.
			name: "during the exchange, and the revoke fails with control characters",
			setup: func(cal *fakeCalendar, kb *keyboard) {
				cal.route("POST /cli/token", func(w http.ResponseWriter, r *http.Request) {
					kb.press()
					cal.redeem(w, r)
				})
				cal.route("DELETE /my/current_access_token", func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":"\u001b]0;pwned\u0007"}`))
				})
			},
			wantStderr: func(baseURL string) string {
				return "\nLogin canceled, but pcal couldn't revoke the new token (\\x1b]0;pwned\\a); revoke it at " + baseURL + "/my/access_tokens.\n"
			},
			wantRevoke: true,
		},
		{
			name: "during an exchange that fails",
			setup: func(cal *fakeCalendar, kb *keyboard) {
				cal.route("POST /cli/token", func(w http.ResponseWriter, _ *http.Request) {
					kb.press()
					cal.serve(w, http.StatusBadRequest, "error_invalid_grant.json")
				})
			},
			wantStderr: func(string) string { return "\nLogin canceled. Nothing was stored.\n" },
		},
		{
			name: "while the new token is checked",
			setup: func(cal *fakeCalendar, kb *keyboard) {
				cal.route("GET /my/identity", func(w http.ResponseWriter, _ *http.Request) {
					kb.press()
					cal.serve(w, http.StatusOK, "identity.json")
				})
			},
			wantStderr: func(string) string { return "\nLogin canceled. The new token was revoked.\n" },
			wantRevoke: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			kb := newKeyboard()
			tt.setup(cal, kb)
			h := newHarness(t, cal.URL())
			h.interrupt = kb.interrupt

			r := h.run("login")

			wantExit := 130
			if tt.wantExit != 0 {
				wantExit = tt.wantExit
			}
			require.Equal(t, wantExit, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
			require.Empty(t, r.stdout)
			require.True(t, strings.HasSuffix(r.stderr, tt.wantStderr(cal.URL())), "stderr: %q", r.stderr)
			revoke := request{Method: "DELETE", Path: "/my/current_access_token", Auth: "Bearer " + cliToken}
			if tt.wantRevoke {
				require.Contains(t, cal.requests(), revoke)
			} else {
				require.NotContains(t, cal.requests(), revoke)
			}
			h.requireNothingStored(cal.URL())
		})
	}
}

// A new token pcal can't store is revoked before the store's error is
// returned (Jeremy's Gate 3c decision); a failed revoke is a warning.
func TestBrowserLoginWhenStoringFails(t *testing.T) {
	tests := []struct {
		name       string
		revoke     int
		wantStderr func(baseURL string) string
	}{
		{"the new token is revoked", http.StatusNoContent, func(string) string { return "" }},
		{"the revoke fails too", http.StatusBadGateway, func(baseURL string) string {
			return "warning: couldn't revoke the new token (Bad Gateway); revoke it at " + baseURL + "/my/access_tokens\n"
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			cal.route("DELETE /my/current_access_token", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.revoke)
			})
			h := newHarness(t, cal.URL())
			h.store = failingSet{h.credentials()}

			r := h.run("login")

			require.Equal(t, 7, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
			env := decodeError(t, r.stdout)
			require.Equal(t, "api_error", env.Error.Code)
			require.Equal(t, "writing credentials.json: permission denied", env.Error.Message)
			require.Contains(t, cal.requests(), request{Method: "DELETE", Path: "/my/current_access_token", Auth: "Bearer " + cliToken})
			require.True(t, strings.HasSuffix(r.stderr, "…\n"+tt.wantStderr(cal.URL())), "stderr: %q", r.stderr)
			h.requireNothingStored(cal.URL())
		})
	}
}

// Once the token is being stored, nothing listens for Ctrl-C, so it ends
// pcal the usual way rather than claiming nothing was stored. The bounded
// revoke of a replaced token listens again (Gate 3c).
func TestLoginStopsCatchingCtrlCBeforeStoring(t *testing.T) {
	for _, args := range [][]string{{"login"}, {"login", "--with-token"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cal := newFakeCalendar(t)
			h := newHarness(t, cal.URL())
			h.stdin = strings.NewReader(goodToken)
			kb := newKeyboard()
			h.interrupt = kb.interrupt
			var heard []bool
			h.store = onSet{h.credentials(), func() { heard = append(heard, kb.press()) }}

			r := h.run(args...)

			require.Equal(t, 0, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
			require.Equal(t, []bool{false}, heard, "something listened for Ctrl-C while the token was stored")
		})
	}
}

// --- the paste-the-callback flow ---

// On a host with no browser for the person (here an SSH session), pcal
// prints the link and asks for the address the browser ended up on, which
// fails to load but holds the code.
func TestPasteLogin(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	h.env["SSH_CONNECTION"] = "10.0.0.2 52000 10.0.0.1 22"
	var paste func(string)
	h.stdin, paste = personOnAnotherDevice(t)
	h.onStderrLine = paste

	r := h.run("login")

	require.Equal(t, 0, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	require.Empty(t, h.browser.openedURLs())
	require.Equal(t, "c0de", redeemed(t, cal)["code"])
	require.Equal(t, auth.Credential{Token: cliToken, Source: auth.SourceBrowser, Store: auth.StoreKeyring}, h.stored(cal.URL()))

	lines := strings.Split(r.stderr, "\n")
	require.Equal(t, "Not opening a browser here (SSH session). Open this link on any device:", lines[0])
	require.NotEmpty(t, printedLink(lines[1]))
	require.Equal(t, "After you approve pcal, your browser goes to an address starting with "+
		"http://127.0.0.1 that won't load. That's expected.", lines[2])
	require.Equal(t, "Paste the address your browser ended up on: ", lines[3])
}

func TestPasteLoginWithRemote(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	var paste func(string)
	h.stdin, paste = personOnAnotherDevice(t)
	h.onStderrLine = paste

	r := h.run("login", "--remote")

	require.Equal(t, 0, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	require.True(t, strings.HasPrefix(r.stderr, "Open this link on any device:\n"), r.stderr)
	require.Empty(t, h.browser.openedURLs())
	require.Equal(t, cliToken, h.stored(cal.URL()).Token)
}

// --local wins over the detection.
func TestLocalOverridesTheDetection(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	h.env["SSH_TTY"] = "/dev/ttys003"

	r := h.run("login", "--local")

	require.Equal(t, 0, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	require.Len(t, h.browser.openedURLs(), 1)
}

func TestHeadlessDetectionUsesTheOS(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	h.goos = "linux"
	var paste func(string)
	h.stdin, paste = personOnAnotherDevice(t)
	h.onStderrLine = paste

	r := h.run("login")

	require.Equal(t, 0, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	require.True(t, strings.HasPrefix(r.stderr, "Not opening a browser here (no display)."), r.stderr)
}

// Text that isn't the address gets one more try (the plan's "re-prompt
// once").
func TestPasteLoginTriesTwice(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	var paste func(string)
	h.stdin, paste = personOnAnotherDevice(t, "I approved it")
	h.onStderrLine = paste

	r := h.run("login", "--remote")

	require.Equal(t, 0, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	require.Contains(t, r.stderr, "Paste the address your browser ended up on: "+
		"That isn't the address: it starts with http://127.0.0.1 and has code= in it. Try once more.\n"+
		"Paste the address your browser ended up on: ")
	require.Equal(t, cliToken, h.stored(cal.URL()).Token)
}

func TestPasteLoginFailures(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*testing.T, *harness, *fakeCalendar)
		wantExit int
		wantCode string
		wantMsg  string
		wantHint string
	}{
		{
			name: "not the address, twice",
			setup: func(_ *testing.T, h *harness, _ *fakeCalendar) {
				h.stdin = strings.NewReader("I approved it\nhttps://calendar.promisechurch.io/\n")
			},
			wantExit: 1, wantCode: "usage", wantMsg: "that isn't the address your browser ended up on",
			wantHint: "Run `pcal login` again, and paste the whole address from the browser's address bar.",
		},
		{
			name: "nothing pasted",
			setup: func(_ *testing.T, h *harness, _ *fakeCalendar) {
				h.stdin = strings.NewReader("")
			},
			wantExit: 1, wantCode: "usage", wantMsg: "no address was pasted",
			wantHint: "Run `pcal login` again, and paste the whole address from the browser's address bar.",
		},
		{
			name: "another login's address",
			setup: func(_ *testing.T, h *harness, _ *fakeCalendar) {
				h.stdin = strings.NewReader("http://127.0.0.1:51234/callback?code=c0de&state=another-login\n")
			},
			wantExit: 3, wantCode: "auth_required", wantMsg: "the browser's answer didn't match this login", wantHint: "Run `pcal login` again.",
		},
		{
			name: "denied",
			setup: func(t *testing.T, h *harness, cal *fakeCalendar) {
				cal.decision = "deny"
				var paste func(string)
				h.stdin, paste = personOnAnotherDevice(t)
				h.onStderrLine = paste
			},
			wantExit: 3, wantCode: "auth_required", wantMsg: "authorization denied in the browser", wantHint: "Run `pcal login` again.",
		},
		{
			name: "no answer in time",
			setup: func(t *testing.T, h *harness, _ *fakeCalendar) {
				stdin, typed := io.Pipe()
				t.Cleanup(func() { _ = typed.Close() })
				h.stdin = stdin
				h.loginWait = 200 * time.Millisecond
			},
			wantExit: 3, wantCode: "auth_required", wantMsg: "timed out waiting for the address your browser ended up on", wantHint: "Run `pcal login` again.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			h := newHarness(t, cal.URL())
			tt.setup(t, h, cal)

			r := h.run("login", "--remote")

			require.Equal(t, tt.wantExit, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
			env := decodeError(t, r.stdout)
			require.Equal(t, tt.wantCode, env.Error.Code)
			require.Equal(t, tt.wantMsg, env.Error.Message)
			require.Equal(t, tt.wantHint, env.Error.Hint)
			requireNoRedeem(t, cal)
			h.requireNothingStored(cal.URL())
		})
	}
}

func TestPasteLoginCanceled(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	stdin, typed := io.Pipe()
	t.Cleanup(func() { _ = typed.Close() })
	h.stdin = stdin
	h.interrupt = canceledContext

	r := h.run("login", "--remote")

	require.Equal(t, 130, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	require.True(t, strings.HasSuffix(r.stderr, "\nLogin canceled. Nothing was stored.\n"), r.stderr)
	h.requireNothingStored(cal.URL())
}

func TestDescribeWait(t *testing.T) {
	require.Equal(t, "5 minutes", describeWait(5*time.Minute))
	require.Equal(t, "1 minute", describeWait(time.Minute))
	require.Equal(t, "1m30s", describeWait(90*time.Second))
	require.Equal(t, "200ms", describeWait(200*time.Millisecond))
}
