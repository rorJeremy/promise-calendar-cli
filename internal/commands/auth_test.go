package commands

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/rorJeremy/promise-calendar-cli/internal/auth"
	"github.com/rorJeremy/promise-calendar-cli/internal/output"
)

// --- the auth group ---

// A group command isn't runnable in cobra, which then shows help (exit 0)
// for any word after it before checking arguments. pcal's groups reject an
// unknown subcommand as the root does, with suggestions.
func TestAuthGroupRejectsUnknownSubcommands(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantMsg  string
		wantHint string
	}{
		{"unknown", []string{"auth", "frobnicate"}, `unknown command "frobnicate" for "pcal auth"`, "Run `pcal auth --help` for usage."},
		{"close to one", []string{"auth", "statsu"}, `unknown command "statsu" for "pcal auth"`, "Did you mean `pcal auth status`? Run `pcal auth --help` for usage."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runPcal(t, tt.args...)

			require.Equal(t, 1, r.code, "stdout: %s", r.stdout)
			env := decodeError(t, r.stdout)
			require.Equal(t, "usage", env.Error.Code)
			require.Equal(t, tt.wantMsg, env.Error.Message)
			require.Equal(t, tt.wantHint, env.Error.Hint)
		})
	}
}

func TestAuthGroupAloneShowsHelp(t *testing.T) {
	r := runPcal(t, "auth")

	require.Equal(t, 0, r.code)
	require.Contains(t, r.stdout, "Usage:")
	require.Contains(t, r.stdout, "status")
}

// --- auth login --with-token ---

func TestLoginWithTokenFromAPipe(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	h.stdin = strings.NewReader(goodToken + "\n")

	r := h.run("auth", "login", "--with-token")

	require.Equal(t, 0, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	env := decodeEnvelope(t, r.stdout)
	require.Equal(t, "Logged in as Admin User (write)", env.Summary)
	require.JSONEq(t, string(golden(t, "identity.json")), string(env.Data))
	require.Empty(t, r.stderr)

	// Checked with the server before it was stored.
	require.Equal(t, []request{{Method: "GET", Path: "/my/identity", Auth: "Bearer " + goodToken}}, cal.requests())
	require.Equal(t, auth.Credential{Token: goodToken, Source: auth.SourceImported, Store: auth.StoreKeyring}, h.stored(cal.URL()))
	// Every /my/identity call caches the calendar's time zone.
	require.Equal(t, "America/Chicago", h.savedConfig().TimeZone)
}

func TestLoginWithTokenInATerminalPrintsALine(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	h.stdin = strings.NewReader(goodToken)
	h.outTTY = true

	r := h.run("auth", "login", "--with-token")

	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Equal(t, "Logged in as Admin User (write)\n", r.stdout)
}

func TestLoginWithTokenTrimsWhitespace(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	h.stdin = strings.NewReader("  " + goodToken + " \r\n\n")

	r := h.run("auth", "login", "--with-token")

	require.Equal(t, 0, r.code, "stdout: %s", r.stdout)
	require.Equal(t, goodToken, h.stored(cal.URL()).Token)
}

// Masked: huh draws the field with * for each character. Without
// ACCESSIBLE the input is keystrokes, so Enter is "\r". The keys come one
// read at a time, as typed, and the prompt has a window size, so the form
// is drawn while they arrive; given everything at once, huh handles the
// keys before it draws anything, and the check below would prove nothing.
func TestLoginWithTokenPromptsInATerminal(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	h.stdin = &typing{chunks: []string{goodToken, "\r"}, pause: 300 * time.Millisecond}
	h.inTTY, h.errTTY = true, true
	h.promptOptions = drawnPrompt

	r := h.run("auth", "login", "--with-token")

	require.Equal(t, 0, r.code, "stdout: %s\nstderr: %q", r.stdout, r.stderr)
	require.Equal(t, goodToken, h.stored(cal.URL()).Token)
	require.Contains(t, r.stderr, "Paste your pcal token (it won't be shown)")
	require.Contains(t, r.stderr, "*")
	require.NotContains(t, r.stderr, goodToken)
}

// The masked prompt is drawn on stderr, so with stderr redirected the
// person would see nothing while pcal waited for the token. pcal refuses
// instead, before reading anything (Gate 3b F9; basecamp-cli's
// tui.ErrNotInteractive).
func TestLoginWithTokenRefusesAPromptItCantShow(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	stdin := strings.NewReader(goodToken + "\r")
	h.stdin = stdin
	h.inTTY = true

	r := h.run("login", "--with-token")

	require.Equal(t, 1, r.code, "stdout: %s", r.stdout)
	env := decodeError(t, r.stdout)
	require.Equal(t, "usage", env.Error.Code)
	require.Equal(t, "stdin is a terminal but stderr isn't, so pcal can't show the prompt", env.Error.Message)
	require.Equal(t, "Pipe the token in instead (`pcal login --with-token < token.txt`), or keep stderr on the terminal, where the prompt is drawn.", env.Error.Hint)
	require.Equal(t, len(goodToken)+1, stdin.Len(), "stdin was read")
	require.Empty(t, cal.requests())
	h.requireNothingStored(cal.URL())
}

// Prompts follow pcal's own styling switches (colorDisabled): unstyled
// output means plain prompts, and a dumb terminal means accessible ones too
// (Gate 3c).
func TestPrompterFollowsTheOutputsStyling(t *testing.T) {
	tests := []struct {
		name           string
		env            map[string]string
		wantPlain      bool
		wantAccessible bool
	}{
		{"nothing set", nil, false, false},
		{"NO_COLOR", map[string]string{"NO_COLOR": "1"}, true, false},
		{"CLICOLOR=0", map[string]string{"CLICOLOR": "0"}, true, false},
		{"TERM=dumb", map[string]string{"TERM": "dumb"}, true, true},
		{"TERM=dumb, CLICOLOR_FORCE", map[string]string{"TERM": "dumb", "CLICOLOR_FORCE": "1"}, false, true},
		{"ACCESSIBLE", map[string]string{"ACCESSIBLE": "1"}, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &app{getenv: env(tt.env)}

			p := a.prompter()

			require.Equal(t, tt.wantPlain, p.Plain)
			require.Equal(t, tt.wantAccessible, p.Accessible)
		})
	}
}

// Ctrl-C or Esc at the prompt cancels the login: a line on stderr, exit
// 130, no envelope, nothing sent or stored (basecamp-cli's login exits 130
// the same way, internal/commands/auth.go#loginOutcome, but prints its line
// on stdout).
func TestLoginWithTokenCanceledAtThePrompt(t *testing.T) {
	tests := []struct {
		name      string
		keys      string
		interrupt func(*harness)
	}{
		{"Ctrl-C", "pcal_go\x03", nil},
		{"Esc", "pcal_go\x1b", nil},
		// How Ctrl-C reaches an accessible prompt: as SIGINT, which ends the
		// login's context.
		{"SIGINT", "", func(h *harness) { h.interrupt = canceledContext }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			h := newHarness(t, cal.URL())
			h.stdin = strings.NewReader(tt.keys)
			h.inTTY, h.errTTY = true, true
			if tt.interrupt != nil {
				tt.interrupt(h)
			}

			r := h.run("auth", "login", "--with-token")

			require.Equal(t, 130, r.code, "stdout: %s\nstderr: %q", r.stdout, r.stderr)
			require.Empty(t, r.stdout)
			require.True(t, strings.HasSuffix(r.stderr, "\nLogin canceled. Nothing was stored.\n"), "stderr: %q", r.stderr)
			require.Empty(t, cal.requests())
			h.requireNothingStored(cal.URL())
		})
	}
}

// Enter at the masked prompt with nothing typed (or only spaces) is no
// token: exit 1, nothing sent.
func TestLoginWithTokenEmptyAtThePrompt(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	h.stdin = strings.NewReader("   \r")
	h.inTTY, h.errTTY = true, true

	r := h.run("login", "--with-token")

	require.Equal(t, 1, r.code, "stdout: %s", r.stdout)
	env := decodeError(t, r.stdout)
	require.Equal(t, "usage", env.Error.Code)
	require.Equal(t, "no token entered", env.Error.Message)
	require.Equal(t, pipeHint, env.Error.Hint)
	require.Empty(t, cal.requests())
}

// Ctrl-C while a pipe is still open (`cat | pcal login --with-token`,
// waiting for EOF) cancels the login like Ctrl-C at the prompt.
func TestLoginWithTokenCanceledWhileReadingAPipe(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	stdin, typed := io.Pipe()
	t.Cleanup(func() { _ = typed.Close() })
	h.stdin = stdin
	h.interrupt = canceledContext

	r := h.run("login", "--with-token")

	require.Equal(t, 130, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	require.True(t, strings.HasSuffix(r.stderr, "\nLogin canceled. Nothing was stored.\n"), "stderr: %q", r.stderr)
	require.Empty(t, cal.requests())
}

// Ctrl-C while the token is checked with /my/identity cancels the login:
// nothing is stored, even if the server's answer arrives anyway.
func TestLoginWithTokenCanceledWhileTheTokenIsChecked(t *testing.T) {
	cal := newFakeCalendar(t)
	kb := newKeyboard()
	cal.route("GET /my/identity", func(w http.ResponseWriter, _ *http.Request) {
		kb.press()
		cal.serve(w, http.StatusOK, "identity.json")
	})
	h := newHarness(t, cal.URL())
	h.stdin = strings.NewReader(goodToken)
	h.interrupt = kb.interrupt

	r := h.run("login", "--with-token")

	require.Equal(t, 130, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	require.True(t, strings.HasSuffix(r.stderr, "\nLogin canceled. Nothing was stored.\n"), "stderr: %q", r.stderr)
	h.requireNothingStored(cal.URL())
}

func TestLoginWithTokenRefusals(t *testing.T) {
	// Never `echo … |`, which puts the token in shell history too (Gate 3b
	// F10).
	const pipeHint = "Pipe the token in from a file or the clipboard (`pcal login --with-token < token.txt` or " +
		"`pbpaste | pcal login --with-token`), or run `pcal login --with-token` in a terminal to paste it at a hidden prompt."
	tests := []struct {
		name     string
		stdin    string
		args     []string
		env      map[string]string
		wantMsg  string
		wantHint string
	}{
		{
			name: "nothing on stdin", stdin: " \n",
			wantMsg: "no token on stdin", wantHint: pipeHint,
		},
		{
			// It can't be sent in a header, and the error doesn't quote it.
			name: "a control character inside", stdin: "pcal_go\x1bod\n",
			wantMsg:  "the token contains a control character, such as a line break inside it",
			wantHint: "Paste the token on one line: it's pcal_ and 64 hex characters.",
		},
		{
			// An argument lands in shell history and the process list. The
			// error must not repeat it (basecamp-cli's cobra.NoArgs does:
			// unknown command "<TOKEN>").
			name: "a token as an argument", args: []string{goodToken},
			wantMsg:  "pcal auth login doesn't take a token as an argument, where it would land in your shell history",
			wantHint: pipeHint,
		},
		{
			// Every later command would use PCAL_TOKEN instead (Jeremy's
			// Gate 3 decision; basecamp-cli refuses the same way,
			// internal/commands/auth.go#runLoginWithToken).
			name: "PCAL_TOKEN is set", stdin: goodToken, env: map[string]string{"PCAL_TOKEN": "pcal_other"},
			wantMsg:  "PCAL_TOKEN is set, so pcal would keep using it instead of this login",
			wantHint: "Unset PCAL_TOKEN, then log in again.",
		},
		{
			// A token is 69 characters, so 4 KB is plenty; past that it's
			// the wrong thing on stdin (Gate 3b F7; basecamp-cli's
			// maxTokenBytes).
			name: "more than 4096 bytes", stdin: goodToken + strings.Repeat(" ", 4097-len(goodToken)),
			wantMsg:  "stdin holds more than 4096 bytes; a pcal token is 69 characters",
			wantHint: "Pipe in only the token.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			h := newHarness(t, cal.URL())
			h.stdin = strings.NewReader(tt.stdin)
			for k, v := range tt.env {
				h.env[k] = v
			}

			r := h.run(append([]string{"auth", "login", "--with-token"}, tt.args...)...)

			require.Equal(t, 1, r.code, "stdout: %s", r.stdout)
			env := decodeError(t, r.stdout)
			require.Equal(t, "usage", env.Error.Code)
			require.Equal(t, tt.wantMsg, env.Error.Message)
			require.Equal(t, tt.wantHint, env.Error.Hint)
			require.NotContains(t, r.stdout+r.stderr, goodToken)
			require.Empty(t, cal.requests())
			delete(h.env, "PCAL_TOKEN")
			h.requireNothingStored(cal.URL())
		})
	}
}

// stdin is read only as far as the limit: something that never ends, like
// `yes | pcal login --with-token`, is refused at once instead of filling
// memory (Gate 3b F7). Up to 4096 bytes is fine.
func TestLoginWithTokenReadsAtMost4096Bytes(t *testing.T) {
	t.Run("endless stdin", func(t *testing.T) {
		cal := newFakeCalendar(t)
		h := newHarness(t, cal.URL())
		stdin := &endless{}
		h.stdin = stdin

		r := h.run("login", "--with-token")

		require.Equal(t, 1, r.code, "stdout: %s", r.stdout)
		require.Equal(t, "stdin holds more than 4096 bytes; a pcal token is 69 characters", decodeError(t, r.stdout).Error.Message)
		require.LessOrEqual(t, stdin.read, 4097)
		require.Empty(t, cal.requests())
	})
	t.Run("4096 bytes", func(t *testing.T) {
		cal := newFakeCalendar(t)
		h := newHarness(t, cal.URL())
		h.stdin = strings.NewReader(goodToken + strings.Repeat(" ", 4096-len(goodToken)))

		r := h.run("login", "--with-token")

		require.Equal(t, 0, r.code, "stdout: %s", r.stdout)
		require.Equal(t, goodToken, h.stored(cal.URL()).Token)
	})
}

// endless is a stdin that never ends, like `yes`. So that a pcal reading
// it without a limit fails the test instead of filling memory, it gives up
// with an error after 1 MiB, far past what pcal may read.
type endless struct {
	read int
}

func (e *endless) Read(b []byte) (int, error) {
	if e.read >= 1<<20 {
		return 0, errors.New("read 1 MiB of endless stdin")
	}
	for i := range b {
		b[i] = 'y'
	}
	e.read += len(b)
	return len(b), nil
}

func TestLoginWithARejectedToken(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	h.stdin = strings.NewReader("pcal_revoked\n")

	r := h.run("auth", "login", "--with-token")

	require.Equal(t, 3, r.code, "stdout: %s", r.stdout)
	env := decodeError(t, r.stdout)
	require.Equal(t, "auth_required", env.Error.Code)
	require.Equal(t, "the server rejected that token", env.Error.Message)
	require.Equal(t, "Check the token, or create one at "+cal.URL()+"/my/access_tokens.", env.Error.Hint)
	h.requireNothingStored(cal.URL())
}

func TestLoginWithTokenWhenTheCalendarIsDown(t *testing.T) {
	cal := newFakeCalendar(t)
	cal.server.Close()
	h := newHarness(t, cal.URL())
	h.stdin = strings.NewReader(goodToken)

	r := h.run("auth", "login", "--with-token")

	require.Equal(t, 6, r.code, "stdout: %s", r.stdout)
	require.Equal(t, "network", decodeError(t, r.stdout).Error.Code)
	h.requireNothingStored(cal.URL())
}

// pcal never saves --url (Jeremy's Gate 3 decision), so after a login with
// it, a note says the other commands need it too.
func TestLoginWithURLSaysItIsntSaved(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, "")
	delete(h.env, "PCAL_URL")
	h.stdin = strings.NewReader(goodToken)

	r := h.run("auth", "login", "--with-token", "--url", cal.URL())

	require.Equal(t, 0, r.code, "stdout: %s", r.stdout)
	require.Equal(t, "note: pcal doesn't save --url, so pass --url "+cal.URL()+" (or set PCAL_URL) on other commands too\n", r.stderr)
	require.Empty(t, h.savedConfig().URL)
}

// A login over a token an earlier `pcal login` minted revokes that one, so
// it doesn't stay live on the server (Jeremy's Gate 3 decision). An
// imported token belongs to whoever made it, so it's only replaced.
func TestLoginReplacingAStoredToken(t *testing.T) {
	tests := []struct {
		name       string
		old        auth.Credential
		wantRevoke bool
	}{
		{"an earlier pcal login's token is revoked", auth.Credential{Token: "pcal_old", Source: auth.SourceBrowser}, true},
		{"an imported token is only replaced", auth.Credential{Token: "pcal_old", Source: auth.SourceImported}, false},
		{"the same token isn't revoked", auth.Credential{Token: goodToken, Source: auth.SourceBrowser}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			cal.route("DELETE /my/current_access_token", cal.withToken([]string{"pcal_old"}, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			h := newHarness(t, cal.URL())
			require.NoError(t, h.credentials().Set(cal.URL(), tt.old))
			h.stdin = strings.NewReader(goodToken)

			r := h.run("auth", "login", "--with-token")

			require.Equal(t, 0, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
			require.Empty(t, r.stderr)
			want := []request{{Method: "GET", Path: "/my/identity", Auth: "Bearer " + goodToken}}
			if tt.wantRevoke {
				want = append(want, request{Method: "DELETE", Path: "/my/current_access_token", Auth: "Bearer pcal_old"})
			}
			require.Equal(t, want, cal.requests())
			require.Equal(t, goodToken, h.stored(cal.URL()).Token)
		})
	}
}

func TestLoginWarnsWhenTheOldTokenCantBeRevoked(t *testing.T) {
	cal := newFakeCalendar(t)
	cal.route("DELETE /my/current_access_token", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	h := newHarness(t, cal.URL())
	require.NoError(t, h.credentials().Set(cal.URL(), auth.Credential{Token: "pcal_old", Source: auth.SourceBrowser}))
	h.stdin = strings.NewReader(goodToken)

	r := h.run("auth", "login", "--with-token")

	require.Equal(t, 0, r.code, "stdout: %s", r.stdout)
	require.Equal(t, "warning: couldn't revoke the token from your previous `pcal login` (Internal Server Error); "+
		"revoke it at "+cal.URL()+"/my/access_tokens\n", r.stderr)
	require.Equal(t, goodToken, h.stored(cal.URL()).Token)
}

// Revoking the token a login replaced is cleanup after the login has
// worked, so it's bounded (10 seconds) and Ctrl-C stops it. Either way the
// new login stands, and the warning says the old token may still be live
// (Gate 3c).
func TestLoginGivesUpOnRevokingTheOldToken(t *testing.T) {
	tests := []struct {
		name    string
		wait    time.Duration
		signal  func(*keyboard) bool
		wantWhy string
	}{
		{"no answer", 200 * time.Millisecond, nil, "no answer within 200ms"},
		{"Ctrl-C", 5 * time.Second, (*keyboard).press, "canceled with Ctrl-C"},
		{"SIGTERM", 5 * time.Second, (*keyboard).terminate, "stopped by SIGTERM"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			kb := newKeyboard()
			cal.route("DELETE /my/current_access_token", func(_ http.ResponseWriter, r *http.Request) {
				if tt.signal != nil {
					require.True(t, tt.signal(kb), "nothing listened for the signal during the revoke")
				}
				<-r.Context().Done()
			})
			h := newHarness(t, cal.URL())
			h.interrupt = kb.interrupt
			h.revokeWait = tt.wait
			require.NoError(t, h.credentials().Set(cal.URL(), auth.Credential{Token: "pcal_old", Source: auth.SourceBrowser}))
			h.stdin = strings.NewReader(goodToken)

			start := time.Now()
			r := h.run("auth", "login", "--with-token")

			require.Equal(t, 0, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
			require.Less(t, time.Since(start), 3*time.Second)
			require.Equal(t, "warning: couldn't revoke the token from your previous `pcal login` ("+tt.wantWhy+"); "+
				"revoke it at "+cal.URL()+"/my/access_tokens\n", r.stderr)
			require.Equal(t, "Logged in as Admin User (write)", decodeEnvelope(t, r.stdout).Summary)
			require.Equal(t, goodToken, h.stored(cal.URL()).Token)
		})
	}
}

// A token imported with --with-token that can't be stored isn't revoked:
// whoever created it manages it.
func TestLoginWithTokenWhenStoringFails(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	h.stdin = strings.NewReader(goodToken)
	h.store = failingSet{h.credentials()}

	r := h.run("auth", "login", "--with-token")

	require.Equal(t, 7, r.code, "stdout: %s", r.stdout)
	require.Equal(t, "writing credentials.json: permission denied", decodeError(t, r.stdout).Error.Message)
	require.Equal(t, []request{{Method: "GET", Path: "/my/identity", Auth: "Bearer " + goodToken}}, cal.requests())
	h.requireNothingStored(cal.URL())
}

// A warning can quote the server, so its control characters are shown as
// escapes, like the rest of pcal's human output (Gate 2b F5).
func TestWarningsShowControlCharactersAsEscapes(t *testing.T) {
	cal := newFakeCalendar(t)
	cal.route("DELETE /my/current_access_token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"\u001b]0;pwned\u0007"}`))
	})
	h := newHarness(t, cal.URL())
	require.NoError(t, h.credentials().Set(cal.URL(), auth.Credential{Token: "pcal_old", Source: auth.SourceBrowser}))
	h.stdin = strings.NewReader(goodToken)

	r := h.run("auth", "login", "--with-token")

	require.Equal(t, 0, r.code, "stdout: %s", r.stdout)
	require.Contains(t, r.stderr, `(\x1b]0;pwned\a)`)
	require.NotContains(t, r.stderr, "\x1b")
}

// A note can quote what it's given, so its control characters are shown
// as escapes too, like a warning's (Gate 3b F12, U4).
func TestNotesShowControlCharactersAsEscapes(t *testing.T) {
	var stderr strings.Builder
	a := &app{streams: Streams{Err: &stderr}}

	a.notef("the calendar said %s", "\x1b]0;pwned\a")

	require.Equal(t, "note: the calendar said \\x1b]0;pwned\\a\n", stderr.String())
}

// --- auth status ---

func TestAuthStatus(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*harness, string)
		wantStore string
		wantSrc   string
	}{
		{
			name: "a stored login",
			setup: func(h *harness, url string) {
				require.NoError(h.t, h.credentials().Set(url, auth.Credential{Token: goodToken, Source: auth.SourceBrowser}))
			},
			wantStore: "keyring", wantSrc: "browser",
		},
		{
			name: "the file, with PCAL_NO_KEYRING",
			setup: func(h *harness, url string) {
				h.env["PCAL_NO_KEYRING"] = "1"
				require.NoError(h.t, h.credentials().Set(url, auth.Credential{Token: goodToken, Source: auth.SourceImported}))
			},
			wantStore: "file", wantSrc: "imported",
		},
		{
			name:      "PCAL_TOKEN",
			setup:     func(h *harness, _ string) { h.env["PCAL_TOKEN"] = goodToken },
			wantStore: "env", wantSrc: "env",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			h := newHarness(t, cal.URL())
			tt.setup(h, cal.URL())

			r := h.run("auth", "status")

			require.Equal(t, 0, r.code, "stdout: %s", r.stdout)
			var got authStatus
			require.NoError(t, json.Unmarshal(decodeEnvelope(t, r.stdout).Data, &got))
			require.Equal(t, authStatus{
				URL: cal.URL(), Name: "Admin User", EmailAddress: "admin@example.com",
				Permission: "write", Store: tt.wantStore, Source: tt.wantSrc,
			}, got)
			require.Equal(t, "America/Chicago", h.savedConfig().TimeZone)
		})
	}
}

func TestAuthStatusCard(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	h.outTTY = true
	h.env["NO_COLOR"] = "1"
	require.NoError(t, h.credentials().Set(cal.URL(), auth.Credential{Token: goodToken, Source: auth.SourceBrowser}))

	r := h.run("auth", "status")

	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Equal(t, "URL         "+cal.URL()+"\n"+
		"User        Admin User <admin@example.com>\n"+
		"Permission  write\n"+
		"Token       in the system keyring, from `pcal login`\n", r.stdout)
}

func TestAuthStatusTokenLine(t *testing.T) {
	require.Equal(t, "in the system keyring, from `pcal login`", tokenLine(auth.Credential{Store: auth.StoreKeyring, Source: auth.SourceBrowser}, "/cfg"))
	require.Equal(t, "in /cfg/credentials.json, from `pcal auth login --with-token`", tokenLine(auth.Credential{Store: auth.StoreFile, Source: auth.SourceImported}, "/cfg"))
	require.Equal(t, "from PCAL_TOKEN", tokenLine(auth.Credential{Store: auth.StoreEnv, Source: auth.SourceEnv}, "/cfg"))
}

func TestAuthStatusNotLoggedIn(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())

	r := h.run("auth", "status")

	require.Equal(t, 3, r.code)
	env := decodeError(t, r.stdout)
	require.Equal(t, "auth_required", env.Error.Code)
	require.Equal(t, "not logged in", env.Error.Message)
	require.Equal(t, "Run `pcal login`.", env.Error.Hint)
	require.Empty(t, cal.requests())
}

func TestAuthStatusRejectedToken(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*harness, string)
		wantHint string
	}{
		{
			name: "stored",
			setup: func(h *harness, url string) {
				require.NoError(h.t, h.credentials().Set(url, auth.Credential{Token: "pcal_revoked", Source: auth.SourceBrowser}))
			},
			wantHint: "Run `pcal login`.",
		},
		{
			// `pcal login` can't fix this one.
			name:     "PCAL_TOKEN",
			setup:    func(h *harness, _ string) { h.env["PCAL_TOKEN"] = "pcal_revoked" },
			wantHint: "The server rejected PCAL_TOKEN. Unset it, or set it to a valid token.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			h := newHarness(t, cal.URL())
			tt.setup(h, cal.URL())

			r := h.run("auth", "status")

			require.Equal(t, 3, r.code)
			env := decodeError(t, r.stdout)
			require.Equal(t, "unauthorized", env.Error.Message)
			require.Equal(t, tt.wantHint, env.Error.Hint)
		})
	}
}

// --- whoami ---

func TestWhoami(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	require.NoError(t, h.credentials().Set(cal.URL(), auth.Credential{Token: goodToken, Source: auth.SourceBrowser}))

	r := h.run("whoami")

	require.Equal(t, 0, r.code, "stdout: %s", r.stdout)
	require.JSONEq(t, string(golden(t, "identity.json")), string(decodeEnvelope(t, r.stdout).Data))
	require.Equal(t, "America/Chicago", h.savedConfig().TimeZone)
}

func TestWhoamiCard(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	h.outTTY = true
	h.env["NO_COLOR"] = "1"
	h.env["PCAL_TOKEN"] = goodToken

	r := h.run("whoami")

	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Equal(t, "Name        Admin User\n"+
		"Email       admin@example.com\n"+
		"Permission  write\n"+
		"Time zone   America/Chicago\n"+
		"ID          ae6c10d0-0b13-554c-b976-a05d8a18f0cc\n", r.stdout)
}

func TestWhoamiNotLoggedIn(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())

	r := h.run("whoami")

	require.Equal(t, 3, r.code)
	require.Equal(t, "auth_required", decodeError(t, r.stdout).Error.Code)
}

// --verbose logs each request line and response status (never headers)
// to stderr.
func TestVerboseLogsRequests(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	h.env["PCAL_TOKEN"] = goodToken

	r := h.run("whoami", "--verbose")

	require.Equal(t, 0, r.code)
	require.Equal(t, "> GET "+cal.URL()+"/my/identity\n< 200 OK\n", r.stderr)
}

// --- logout ---

// logoutResult decodes a logout envelope's data.
func logoutData(t *testing.T, stdout string) (logoutResult, string) {
	t.Helper()
	env := decodeEnvelope(t, stdout)
	var got logoutResult
	require.NoError(t, json.Unmarshal(env.Data, &got))
	return got, env.Summary
}

// A token `pcal login` minted is revoked on the server, then forgotten
// (basecamp-cli's revoke.go#LogoutCredential). 401 means it was already
// gone. `pcal auth logout` is the same command.
func TestLogoutRevokesALoginToken(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		status int
	}{
		{"204", []string{"logout"}, http.StatusNoContent},
		{"401: already revoked", []string{"logout"}, http.StatusUnauthorized},
		{"auth logout", []string{"auth", "logout"}, http.StatusNoContent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			cal.route("DELETE /my/current_access_token", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
			})
			h := newHarness(t, cal.URL())
			require.NoError(t, h.credentials().Set(cal.URL(), auth.Credential{Token: goodToken, Source: auth.SourceBrowser}))

			r := h.run(tt.args...)

			require.Equal(t, 0, r.code, "stdout: %s", r.stdout)
			require.Empty(t, r.stderr)
			got, summary := logoutData(t, r.stdout)
			require.Equal(t, logoutResult{URL: cal.URL(), Source: "browser", LoggedOut: true, Revoked: true}, got)
			require.Equal(t, "Logged out; token revoked", summary)
			require.Equal(t, []request{{Method: "DELETE", Path: "/my/current_access_token", Auth: "Bearer " + goodToken}}, cal.requests())
			h.requireNothingStored(cal.URL())
		})
	}
}

// A failed revoke still forgets the token, and says it may still be live.
func TestLogoutWhenTheRevokeFails(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*fakeCalendar)
		wantWhy string
	}{
		{
			name: "5xx",
			setup: func(cal *fakeCalendar) {
				cal.route("DELETE /my/current_access_token", func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusBadGateway)
				})
			},
			wantWhy: "Bad Gateway",
		},
		{
			name:    "unreachable",
			setup:   func(cal *fakeCalendar) { cal.server.Close() },
			wantWhy: "couldn't reach",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			h := newHarness(t, cal.URL())
			require.NoError(t, h.credentials().Set(cal.URL(), auth.Credential{Token: goodToken, Source: auth.SourceBrowser}))
			tt.setup(cal)

			r := h.run("logout")

			require.Equal(t, 0, r.code, "stdout: %s", r.stdout)
			require.True(t, strings.HasPrefix(r.stderr, "warning: couldn't revoke the token on the server ("), r.stderr)
			require.Contains(t, r.stderr, tt.wantWhy)
			require.True(t, strings.HasSuffix(r.stderr, "); it may still be live, so revoke it at "+cal.URL()+"/my/access_tokens\n"), r.stderr)
			got, summary := logoutData(t, r.stdout)
			require.Equal(t, logoutResult{URL: cal.URL(), Source: "browser", LoggedOut: true, Revoked: false}, got)
			require.Equal(t, "Logged out here, but the token may still be live on the server", summary)
			h.requireNothingStored(cal.URL())
		})
	}
}

// An imported token belongs to whoever created it: forgotten, not revoked.
func TestLogoutForgetsAnImportedToken(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	require.NoError(t, h.credentials().Set(cal.URL(), auth.Credential{Token: goodToken, Source: auth.SourceImported}))

	r := h.run("logout")

	require.Equal(t, 0, r.code, "stdout: %s", r.stdout)
	got, summary := logoutData(t, r.stdout)
	require.Equal(t, logoutResult{URL: cal.URL(), Source: "imported", LoggedOut: true, Revoked: false}, got)
	require.Equal(t, "Logged out. That token is still valid; revoke it at "+cal.URL()+
		"/my/access_tokens or with `pcal tokens revoke ID`.", summary)
	require.Empty(t, cal.requests())
	h.requireNothingStored(cal.URL())
}

func TestLogoutNotLoggedIn(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())

	r := h.run("logout")

	require.Equal(t, 0, r.code, "stdout: %s", r.stdout)
	got, summary := logoutData(t, r.stdout)
	require.Equal(t, logoutResult{URL: cal.URL()}, got)
	require.Equal(t, "Not logged in", summary)
	require.Empty(t, cal.requests())
}

// pcal can't log out of a token from the environment, and the stored one
// isn't the one in use (as basecamp-cli's refuseEnvTokenLogout).
func TestLogoutWithPCALTokenSet(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())
	require.NoError(t, h.credentials().Set(cal.URL(), auth.Credential{Token: goodToken, Source: auth.SourceBrowser}))
	h.env["PCAL_TOKEN"] = "pcal_env"

	r := h.run("logout")

	require.Equal(t, 1, r.code, "stdout: %s", r.stdout)
	env := decodeError(t, r.stdout)
	require.Equal(t, "usage", env.Error.Code)
	require.Equal(t, "logged in via PCAL_TOKEN, which pcal can't log out of", env.Error.Message)
	require.Equal(t, "Unset PCAL_TOKEN. To end that token, revoke it at "+cal.URL()+"/my/access_tokens.", env.Error.Hint)
	require.Empty(t, cal.requests())
	delete(h.env, "PCAL_TOKEN")
	require.Equal(t, goodToken, h.stored(cal.URL()).Token)
}

// failingDelete is a credential store whose Delete fails as the keyring's
// does once it stops answering, after the rest worked.
type failingDelete struct {
	credentialStore
}

func (failingDelete) Delete(baseURL string) error {
	return &output.Error{
		Code:    output.CodeAPI,
		Message: "forgetting the credential for " + baseURL + " in the system keyring: no answer within 10s; it may still be stored there",
		Hint:    "If the keyring keeps hanging, set PCAL_NO_KEYRING=1 to keep credentials in a 0600 file instead.",
		Cause:   errors.New("no answer"),
	}
}

// When the local forget fails, logout fails (exit 7) and says what
// happened on the server too (Jeremy's Gate 3 decision; basecamp-cli
// returns the store's error but drops the revoke result).
func TestLogoutWhenForgettingFails(t *testing.T) {
	tests := []struct {
		name    string
		source  auth.Source
		revoke  int
		wantMsg string
	}{
		{"revoked", auth.SourceBrowser, http.StatusNoContent, "revoked the token on the server, but couldn't forget it here: "},
		{"not revoked", auth.SourceBrowser, http.StatusBadGateway, "couldn't revoke the token on the server (Bad Gateway), and couldn't forget it here: "},
		{"imported", auth.SourceImported, 0, "couldn't forget the token here: "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			cal.route("DELETE /my/current_access_token", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.revoke)
			})
			h := newHarness(t, cal.URL())
			store := h.credentials()
			require.NoError(t, store.Set(cal.URL(), auth.Credential{Token: goodToken, Source: tt.source}))
			h.store = failingDelete{store}

			r := h.run("logout")

			require.Equal(t, 7, r.code, "stdout: %s", r.stdout)
			env := decodeError(t, r.stdout)
			require.Equal(t, "api_error", env.Error.Code)
			require.Equal(t, tt.wantMsg+"forgetting the credential for "+cal.URL()+
				" in the system keyring: no answer within 10s; it may still be stored there", env.Error.Message)
			require.Equal(t, "If the keyring keeps hanging, set PCAL_NO_KEYRING=1 to keep credentials in a 0600 file instead.", env.Error.Hint)
		})
	}
}

// --- canceling ---

// SIGTERM, from whatever supervises pcal, stops a login as Ctrl-C does,
// but exits 143 (128 + SIGTERM) and says "terminated" (Jeremy's Gate 3c
// decision; basecamp-cli's loginContext and ExitTerminated).
func TestLoginTerminated(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		setup func(*testing.T, *harness)
	}{
		{"waiting for the browser", []string{"login"}, func(_ *testing.T, h *harness) { h.browser.visit = false }},
		{"at the paste prompt", []string{"login", "--remote"}, func(t *testing.T, h *harness) {
			stdin, typed := io.Pipe()
			t.Cleanup(func() { _ = typed.Close() })
			h.stdin = stdin
		}},
		{"at the masked prompt", []string{"login", "--with-token"}, func(_ *testing.T, h *harness) {
			h.inTTY, h.errTTY = true, true
		}},
		{"reading a pipe", []string{"login", "--with-token"}, func(t *testing.T, h *harness) {
			stdin, typed := io.Pipe()
			t.Cleanup(func() { _ = typed.Close() })
			h.stdin = stdin
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			h := newHarness(t, cal.URL())
			tt.setup(t, h)
			h.interrupt = terminatedContext

			r := h.run(tt.args...)

			require.Equal(t, 143, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
			require.Empty(t, r.stdout)
			require.True(t, strings.HasSuffix(r.stderr, "\nLogin terminated. Nothing was stored.\n"), "stderr: %q", r.stderr)
			requireNoRedeem(t, cal)
			h.requireNothingStored(cal.URL())
		})
	}
}

func TestTerminatedRunPrintsALineAndExits143(t *testing.T) {
	r := runWithSubcommand(t, &cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error {
		return errLoginTerminated
	}}, "probe", "--json")

	require.Equal(t, 143, r.code)
	require.Empty(t, r.stdout)
	require.Equal(t, "\nLogin terminated. Nothing was stored.\n", r.stderr)
}

// A canceled run prints its line alone on stderr, never the envelope, and
// exits 130, even when stdout isn't a terminal.
func TestCanceledRunPrintsALineAndExits130(t *testing.T) {
	r := runWithSubcommand(t, &cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error {
		return errLoginCanceled
	}}, "probe", "--json")

	require.Equal(t, 130, r.code)
	require.Empty(t, r.stdout)
	require.Equal(t, "\nLogin canceled. Nothing was stored.\n", r.stderr)
}
