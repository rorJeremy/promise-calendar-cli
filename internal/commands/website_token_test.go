package commands

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func installWebsiteToken(t *testing.T, cal *fakeCalendar) {
	t.Helper()
	cal.route("GET /api_token", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		cal.serve(w, http.StatusOK, "api_token.json")
	}))
	cal.route("POST /api_token", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		cal.serve(w, http.StatusCreated, "api_token_rotated.json")
	}))
}

func TestWebsiteTokenShow(t *testing.T) {
	cal := newFakeCalendar(t)
	installWebsiteToken(t, cal)
	h := eventsHarness(t, cal)
	cacheZone(t, h, "America/Chicago")

	r := h.run("website-token", "show")

	require.Equal(t, 0, r.code, r.stdout)
	require.JSONEq(t, string(golden(t, "api_token.json")), string(decodeEnvelope(t, r.stdout).Data))
	require.Equal(t, "The website token exists.", decodeEnvelope(t, r.stdout).Summary)
	require.Equal(t, "GET", findRequest(t, cal, "GET", "/api_token").Method)
}

func TestWebsiteTokenRotate(t *testing.T) {
	cal := newFakeCalendar(t)
	installWebsiteToken(t, cal)
	h := eventsHarness(t, cal)
	cacheZone(t, h, "America/Chicago")
	// TERM=dumb makes a prompt that this guard must not reach answer from
	// the empty reader. A drawn form would wait for a window instead.
	h.env["TERM"] = "dumb"

	skipped := h.run("website-token", "rotate")
	require.Equal(t, 1, skipped.code)
	require.Contains(t, decodeError(t, skipped.stdout).Error.Message, "--yes wasn't set")
	require.Empty(t, cal.requests())

	yes := h.run("--yes", "website-token", "rotate")
	require.Equal(t, 0, yes.code, yes.stdout)
	require.JSONEq(t, string(golden(t, "api_token_rotated.json")), string(decodeEnvelope(t, yes.stdout).Data))
	require.Equal(t, "Rotated the website token.", decodeEnvelope(t, yes.stdout).Summary)
	require.Contains(t, yes.stderr, "Update the church website now.")
	require.Equal(t, 1, countMethod(cal, "POST", "/api_token"))
	token := "0000000000000000000000000000000000000000000000000000000000000000"
	require.Equal(t, 1, strings.Count(yes.stdout, token))
	require.Zero(t, strings.Count(yes.stderr, token))

	h.inTTY = true
	h.errTTY = true
	h.env["TERM"] = "dumb"
	h.env["NO_COLOR"] = "1"
	h.stdin = strings.NewReader("n\n")
	no := h.run("website-token", "rotate")
	require.Equal(t, 0, no.code, no.stderr)
	require.Contains(t, no.stdout, `"rotated": false`)
	require.Equal(t, "The website token wasn't changed.", decodeEnvelope(t, no.stdout).Summary)
	require.Equal(t, 1, countMethod(cal, "POST", "/api_token"))

	h.stdin = strings.NewReader("\x1b")
	h.env["ACCESSIBLE"] = "1"
	esc := h.run("website-token", "rotate")
	require.Equal(t, 130, esc.code, "stdout: %s\nstderr: %s", esc.stdout, esc.stderr)
	require.Empty(t, esc.stdout)
	require.Contains(t, esc.stderr, "Canceled. The website token wasn't changed.")
	require.Equal(t, 1, countMethod(cal, "POST", "/api_token"))
}

func TestWebsiteTokenNone(t *testing.T) {
	cal := newFakeCalendar(t)
	installWebsiteToken(t, cal)
	cal.route("GET /api_token", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"exists":false,"description":null,"last_used_at":null,"created_at":null}`))
	}))
	h := eventsHarness(t, cal)
	cacheZone(t, h, "America/Chicago")

	r := h.run("website-token", "show")

	require.Equal(t, 0, r.code, r.stdout)
	require.Equal(t, "No website token.", decodeEnvelope(t, r.stdout).Summary)
	require.Contains(t, string(decodeEnvelope(t, r.stdout).Data), `"exists": false`)
}

func TestWebsiteTokenRotateSignals(t *testing.T) {
	tests := []struct {
		name string
		stop func(*harness)
		code int
	}{
		{"Ctrl-C", func(h *harness) { h.interrupt = canceledContext }, 130},
		{"SIGTERM", func(h *harness) { h.interrupt = terminatedContext }, 143},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			installWebsiteToken(t, cal)
			h := eventsHarness(t, cal)
			cacheZone(t, h, "America/Chicago")
			h.inTTY = true
			h.errTTY = true
			h.env["NO_COLOR"] = "1"
			h.stdin = strings.NewReader("y\n")
			tt.stop(h)

			r := h.run("website-token", "rotate")

			require.Equal(t, tt.code, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
			require.Empty(t, r.stdout)
			require.Contains(t, r.stderr, "Canceled. The website token wasn't changed.")
			require.NotContains(t, r.stderr, "Nothing was deleted")
			require.Empty(t, cal.requests())
		})
	}
}
