package commands

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rorJeremy/promise-calendar-cli/internal/auth"
)

const pcalTokenID = "757d81e9-9bb3-50c6-99d9-5b659f726b0d"

func installTokens(t *testing.T, cal *fakeCalendar) {
	t.Helper()
	cal.route("GET /my/access_tokens", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		cal.serve(w, http.StatusOK, "access_tokens.json")
	}))
	cal.route("DELETE /my/access_tokens/"+pcalTokenID, cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
}

func TestTokensList(t *testing.T) {
	cal := newFakeCalendar(t)
	installTokens(t, cal)
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")

	r := h.run("tokens", "list")
	require.Equal(t, 0, r.code, r.stdout)
	require.JSONEq(t, string(golden(t, "access_tokens.json")), string(decodeEnvelope(t, r.stdout).Data))
	require.Equal(t, "2 tokens", decodeEnvelope(t, r.stdout).Summary)

	h.outTTY = true
	h.env["NO_COLOR"] = "1"
	human := h.run("tokens", "list")
	require.Equal(t, 0, human.code, human.stderr)
	require.Contains(t, human.stdout, "pcal on test-mac")
	require.Contains(t, human.stdout, "Tue Oct 20, 2099 2:30pm")
}

type scriptedStore struct {
	cred    auth.Credential
	deleted bool
}

func (s *scriptedStore) Resolve(string) (auth.Credential, error) { return s.cred, nil }
func (s *scriptedStore) Get(string) (auth.Credential, error)     { return s.cred, nil }
func (s *scriptedStore) Set(string, auth.Credential) error       { return nil }
func (s *scriptedStore) Delete(string) error {
	s.deleted = true
	return nil
}

func TestTokensRevoke(t *testing.T) {
	cal := newFakeCalendar(t)
	installTokens(t, cal)
	h := eventsHarness(t, cal)
	cacheZone(t, h, "America/Chicago")
	// TERM=dumb makes a prompt that this guard must not reach answer from
	// the empty reader. A drawn form would wait for a window instead.
	h.env["TERM"] = "dumb"

	skipped := h.run("tokens", "revoke", pcalTokenID)
	require.Equal(t, 1, skipped.code)
	require.Contains(t, decodeError(t, skipped.stdout).Error.Message, "--yes wasn't set")
	require.Empty(t, cal.requests())

	byName := h.run("--yes", "tokens", "revoke", "pcal on test-mac")
	require.Equal(t, 2, byName.code)
	require.Zero(t, countMethod(cal, "DELETE", "/my/access_tokens/"))

	yes := h.run("--yes", "tokens", "revoke", pcalTokenID)
	require.Equal(t, 0, yes.code, yes.stdout)
	require.Contains(t, yes.stdout, `"deleted": true`)
	require.Equal(t, `Revoked "pcal on test-mac".`, decodeEnvelope(t, yes.stdout).Summary)
	require.NotContains(t, yes.stdout, "Unset PCAL_TOKEN")
	require.NotContains(t, yes.stdout, "logged out")
	require.Equal(t, "DELETE", findRequest(t, cal, "DELETE", "/my/access_tokens/"+pcalTokenID).Method)
	require.Equal(t, "GET", findRequest(t, cal, "GET", "/my/identity").Method)

	h.inTTY = true
	h.errTTY = true
	h.env["ACCESSIBLE"] = "1"
	h.env["NO_COLOR"] = "1"
	h.stdin = strings.NewReader("\x1b")
	esc := h.run("tokens", "revoke", pcalTokenID)
	require.Equal(t, 130, esc.code, esc.stderr)
	require.Empty(t, esc.stdout)
	require.Contains(t, esc.stderr, "Canceled. Nothing was deleted.")
	require.Equal(t, 1, countMethod(cal, "DELETE", "/my/access_tokens/"+pcalTokenID))
}

func TestTokensRevokeForgetsItsOwnStoredToken(t *testing.T) {
	cal := newFakeCalendar(t)
	installTokens(t, cal)
	cal.route("GET /my/identity", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		cal.serve(w, http.StatusUnauthorized, "error_unauthorized.json")
	}))
	h := eventsHarness(t, cal)
	cacheZone(t, h, "America/Chicago")
	store := &scriptedStore{cred: auth.Credential{Token: goodToken, Source: auth.SourceImported}}
	h.store = store

	r := h.run("--yes", "tokens", "revoke", pcalTokenID)

	require.Equal(t, 0, r.code, r.stdout)
	require.True(t, store.deleted)
	require.Contains(t, r.stdout, "That was the token pcal was using, so you're logged out here as well.")
}

func TestTokensRevokeOfPCALTokenSaysToUnsetIt(t *testing.T) {
	cal := newFakeCalendar(t)
	installTokens(t, cal)
	cal.route("GET /my/identity", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		cal.serve(w, http.StatusUnauthorized, "error_unauthorized.json")
	}))
	h := eventsHarness(t, cal)
	cacheZone(t, h, "America/Chicago")
	store := &scriptedStore{cred: auth.Credential{Token: goodToken, Source: auth.SourceEnv}}
	h.store = store

	r := h.run("--yes", "tokens", "revoke", pcalTokenID)

	require.Equal(t, 0, r.code, r.stdout)
	require.False(t, store.deleted)
	require.Contains(t, r.stdout, "That was the token pcal was using. Unset PCAL_TOKEN.")
}

func TestTokensRevokeIgnoresAFailedIdentityCheck(t *testing.T) {
	cal := newFakeCalendar(t)
	installTokens(t, cal)
	cal.route("GET /my/identity", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	h := eventsHarness(t, cal)
	cacheZone(t, h, "America/Chicago")
	store := &scriptedStore{cred: auth.Credential{Token: goodToken, Source: auth.SourceImported}}
	h.store = store

	r := h.run("--yes", "tokens", "revoke", pcalTokenID)

	require.Equal(t, 0, r.code, r.stdout)
	require.False(t, store.deleted)
	require.NotContains(t, r.stdout, "logged out")
	require.NotContains(t, r.stdout, "Unset PCAL_TOKEN")
}

func TestTokensRevokeAnswersAndNotFound(t *testing.T) {
	cal := newFakeCalendar(t)
	installTokens(t, cal)
	h := eventsHarness(t, cal)
	cacheZone(t, h, "America/Chicago")
	h.inTTY = true
	h.errTTY = true
	h.env["TERM"] = "dumb"
	h.stdin = strings.NewReader("\n")

	enter := h.run("tokens", "revoke", pcalTokenID)
	require.Equal(t, 0, enter.code, enter.stderr)
	require.Contains(t, enter.stdout, `"deleted": false`)
	require.Equal(t, "Nothing was deleted.", decodeEnvelope(t, enter.stdout).Summary)
	require.Zero(t, countMethod(cal, "DELETE", "/my/access_tokens/"))

	cal.route("DELETE /my/access_tokens/"+pcalTokenID, cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		cal.serve(w, http.StatusNotFound, "error_not_found.json")
	}))
	missing := h.run("--yes", "tokens", "revoke", pcalTokenID)
	require.Equal(t, 2, missing.code, missing.stdout)
	require.Equal(t, "not_found", decodeError(t, missing.stdout).Error.Message)
}

type forgetfulStore struct {
	scriptedStore
}

func (s *forgetfulStore) Delete(string) error { return errForget }

var errForget = errors.New("keyring down")

func TestTokensRevokeDoesNotClaimLogoutWhenForgettingFails(t *testing.T) {
	cal := newFakeCalendar(t)
	installTokens(t, cal)
	cal.route("GET /my/identity", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		cal.serve(w, http.StatusUnauthorized, "error_unauthorized.json")
	}))
	h := eventsHarness(t, cal)
	cacheZone(t, h, "America/Chicago")
	store := &forgetfulStore{scriptedStore: scriptedStore{cred: auth.Credential{Token: goodToken, Source: auth.SourceImported}}}
	h.store = store

	r := h.run("--yes", "tokens", "revoke", pcalTokenID)

	require.Equal(t, 0, r.code, r.stdout)
	require.False(t, store.deleted)
	require.Contains(t, r.stderr, "couldn't forget the stored token")
	require.NotContains(t, r.stdout, "logged out")
	require.NotContains(t, r.stdout+r.stderr, goodToken)
}
