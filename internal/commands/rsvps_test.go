package commands

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const samRSVP = "f2c7df45-caa5-5dd7-98e0-d403e01cdf6d"

func installRSVPs(t *testing.T, cal *fakeCalendar) {
	t.Helper()
	installEvents(t, cal, golden(t, "events.json"))
	cal.route("GET /events/"+worshipNightID+"/rsvps", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		cal.serve(w, http.StatusOK, "rsvps.json")
	}))
	cal.route("POST /events/"+worshipNightID+"/rsvps", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"` + samRSVP + `","name":"Ada","email":"ada@example.com","phone":"555","occurrence_date":"2099-11-01","created_at":"2099-09-03T09:00:00.000-05:00"}`))
	}))
	cal.route("DELETE /events/"+worshipNightID+"/rsvps/"+samRSVP, cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
}

func TestRSVPsList(t *testing.T) {
	cal := newFakeCalendar(t)
	installRSVPs(t, cal)
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")

	r := h.run("rsvps", "list", worshipNightID)
	require.Equal(t, 0, r.code, r.stdout)
	require.JSONEq(t, string(golden(t, "rsvps.json")), string(decodeEnvelope(t, r.stdout).Data))
	require.Equal(t, "2 RSVPs", decodeEnvelope(t, r.stdout).Summary)
	require.Equal(t, "/events/"+worshipNightID+"/rsvps", findRequest(t, cal, "GET", "/events/"+worshipNightID+"/rsvps").Path)
	require.Zero(t, countMethod(cal, "GET", "/events?"))

	dated := h.run("rsvps", "list", worshipNightID, "--date", "2099-11-01")
	require.Equal(t, 0, dated.code, dated.stdout)
	var query url.Values
	for _, req := range cal.requests() {
		if req.Method == "GET" && strings.Contains(req.Path, "occurrence=") {
			u, err := url.Parse(req.Path)
			require.NoError(t, err)
			query = u.Query()
		}
	}
	require.Equal(t, "2099-11-01", query.Get("occurrence"))

	before := countMethod(cal, "GET", "/events/"+worshipNightID+"/rsvps")
	bad := h.run("rsvps", "list", worshipNightID, "--date", "nope")
	require.Equal(t, 1, bad.code)
	require.Equal(t, before, countMethod(cal, "GET", "/events/"+worshipNightID+"/rsvps"))

	h.outTTY = true
	h.env["NO_COLOR"] = "1"
	human := h.run("rsvps", "list", worshipNightID)
	require.Equal(t, 0, human.code, human.stderr)
	require.Contains(t, human.stdout, "Sam Lee")
	require.Contains(t, human.stdout, "Sun Nov 1, 2099")
	require.Contains(t, human.stdout, "Sun Oct 25, 2099")
}

func TestRSVPsAdd(t *testing.T) {
	cal := newFakeCalendar(t)
	installRSVPs(t, cal)
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")

	r := h.run("rsvps", "add", worshipNightID, "--name", "Ada", "--email", "ada@example.com", "--phone", "555", "--date", "2099-11-01")
	require.Equal(t, 0, r.code, r.stdout)
	body := bodyOf(t, cal, "POST", "/events/"+worshipNightID+"/rsvps")
	var wrap struct {
		RSVP map[string]any `json:"rsvp"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &wrap), body)
	require.Equal(t, "Ada", wrap.RSVP["name"])
	require.Equal(t, "ada@example.com", wrap.RSVP["email"])
	require.Equal(t, "555", wrap.RSVP["phone"])
	require.Equal(t, "2099-11-01", wrap.RSVP["occurrence_date"])

	missing := h.run("rsvps", "add", worshipNightID, "--name", "Ada")
	require.Equal(t, 1, missing.code)
	require.Contains(t, decodeError(t, missing.stdout).Error.Message, "need a name and an email")
	require.Equal(t, 1, countMethod(cal, "POST", "/events/"+worshipNightID+"/rsvps"))

	h.inTTY = true
	h.errTTY = true
	h.env["TERM"] = "dumb"
	h.stdin = strings.NewReader("Ada\nada@example.com\n")
	asked := h.run("rsvps", "add", worshipNightID)
	require.Equal(t, 0, asked.code, "stdout: %s\nstderr: %s", asked.stdout, asked.stderr)
	require.Contains(t, asked.stderr, "Name")
	require.Contains(t, asked.stderr, "Email")
}

func TestRSVPsAddValidationAndForbidden(t *testing.T) {
	cal := newFakeCalendar(t)
	installRSVPs(t, cal)
	cal.route("POST /events/"+worshipNightID+"/rsvps", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"errors":{"occurrence_date":["is required for recurring events"]}}`))
	}))
	h := eventsHarness(t, cal)
	cacheZone(t, h, "America/Chicago")

	r := h.run("rsvps", "add", worshipNightID, "--name", "Ada", "--email", "ada@example.com")
	require.Equal(t, 1, r.code)
	require.Equal(t, "Occurrence date is required for recurring events", decodeError(t, r.stdout).Error.Message)

	cal.route("POST /events/"+worshipNightID+"/rsvps", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"errors":{"event":["does not accept RSVPs"]}}`))
	}))
	off := h.run("rsvps", "add", worshipNightID, "--name", "Ada", "--email", "ada@example.com")
	require.Equal(t, 1, off.code)
	require.Equal(t, "Event does not accept RSVPs", decodeError(t, off.stdout).Error.Message)

	cal.route("POST /events/"+worshipNightID+"/rsvps", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		cal.serve(w, http.StatusForbidden, "error_read_only_token.json")
	}))
	denied := h.run("rsvps", "add", worshipNightID, "--name", "Ada", "--email", "ada@example.com")
	require.Equal(t, 4, denied.code)
	require.Equal(t, "token is read-only", decodeError(t, denied.stdout).Error.Message)
}

func TestRSVPsDelete(t *testing.T) {
	cal := newFakeCalendar(t)
	installRSVPs(t, cal)
	h := eventsHarness(t, cal)
	cacheZone(t, h, "America/Chicago")
	// TERM=dumb makes a prompt that this guard must not reach answer from
	// the empty reader. A drawn form would wait for a window instead.
	h.env["TERM"] = "dumb"

	skipped := h.run("rsvps", "delete", worshipNightID, samRSVP)
	require.Equal(t, 1, skipped.code)
	require.Contains(t, decodeError(t, skipped.stdout).Error.Message, "--yes wasn't set")
	require.Empty(t, cal.requests())

	named := h.run("--yes", "rsvps", "delete", worshipNightID, "Sam Lee")
	require.Equal(t, 2, named.code)
	require.Zero(t, countMethod(cal, "DELETE", "/events/"+worshipNightID+"/rsvps/"))

	yes := h.run("--yes", "rsvps", "delete", worshipNightID, samRSVP)
	require.Equal(t, 0, yes.code, yes.stdout)
	require.Contains(t, yes.stdout, `"deleted": true`)
	require.Equal(t, "DELETE", findRequest(t, cal, "DELETE", "/events/"+worshipNightID+"/rsvps/"+samRSVP).Method)

	h.inTTY = true
	h.errTTY = true
	h.env["TERM"] = "dumb"
	h.env["NO_COLOR"] = "1"
	h.stdin = strings.NewReader("n\n")
	no := h.run("rsvps", "delete", worshipNightID, samRSVP)
	require.Equal(t, 0, no.code, no.stderr)
	require.Contains(t, no.stdout, `"deleted": false`)
	require.Equal(t, "Nothing was deleted.", decodeEnvelope(t, no.stdout).Summary)
	require.Contains(t, no.stderr, `Delete the RSVP from "Sam Lee"?`)
	require.Equal(t, 1, countMethod(cal, "DELETE", "/events/"+worshipNightID+"/rsvps/"+samRSVP))
}

func TestRSVPsDeleteAccessibleEscCancels(t *testing.T) {
	cal := newFakeCalendar(t)
	installRSVPs(t, cal)
	h := eventsHarness(t, cal)
	cacheZone(t, h, "America/Chicago")
	h.inTTY = true
	h.errTTY = true
	h.env["ACCESSIBLE"] = "1"
	h.stdin = strings.NewReader("\x1b")

	r := h.run("rsvps", "delete", worshipNightID, samRSVP)

	require.Equal(t, 130, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	require.Empty(t, r.stdout)
	require.Contains(t, r.stderr, "Canceled. Nothing was deleted.")
	require.Zero(t, countMethod(cal, "DELETE", "/events/"+worshipNightID+"/rsvps/"))
}

func TestRSVPsDeleteAnswersAndSignals(t *testing.T) {
	tests := []struct {
		name    string
		keys    string
		stop    func(*harness)
		code    int
		deleted bool
	}{
		{name: "enter", keys: "\n", code: 0},
		{name: "ctrl-d", keys: "\x04", code: 0},
		{name: "y", keys: "y\n", code: 0, deleted: true},
		{name: "Ctrl-C", keys: "y\n", stop: func(h *harness) { h.interrupt = canceledContext }, code: 130},
		{name: "SIGTERM", keys: "y\n", stop: func(h *harness) { h.interrupt = terminatedContext }, code: 143},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			installRSVPs(t, cal)
			h := eventsHarness(t, cal)
			cacheZone(t, h, "America/Chicago")
			h.inTTY = true
			h.errTTY = true
			h.env["TERM"] = "dumb"
			h.env["NO_COLOR"] = "1"
			h.stdin = strings.NewReader(tt.keys)
			if tt.stop != nil {
				tt.stop(h)
			}

			r := h.run("rsvps", "delete", worshipNightID, samRSVP)

			require.Equal(t, tt.code, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
			deleted := countMethod(cal, "DELETE", "/events/"+worshipNightID+"/rsvps/")
			if tt.deleted {
				require.Equal(t, 1, deleted)
				require.Equal(t, `Deleted the RSVP from "Sam Lee".`, decodeEnvelope(t, r.stdout).Summary)
				return
			}
			require.Zero(t, deleted)
			if tt.code == 0 {
				require.Contains(t, r.stdout, `"deleted": false`)
				require.Equal(t, "Nothing was deleted.", decodeEnvelope(t, r.stdout).Summary)
				return
			}
			require.Empty(t, r.stdout)
			require.Contains(t, r.stderr, "Canceled. Nothing was deleted.")
			require.NotContains(t, r.stdout+r.stderr, "Login canceled")
		})
	}
}

func TestRSVPsDeleteServerNotFound(t *testing.T) {
	cal := newFakeCalendar(t)
	installRSVPs(t, cal)
	cal.route("DELETE /events/"+worshipNightID+"/rsvps/"+samRSVP, cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		cal.serve(w, http.StatusNotFound, "error_not_found.json")
	}))
	h := eventsHarness(t, cal)
	cacheZone(t, h, "America/Chicago")

	r := h.run("--yes", "rsvps", "delete", worshipNightID, samRSVP)

	require.Equal(t, 2, r.code, r.stdout)
	require.Equal(t, "not_found", decodeError(t, r.stdout).Error.Message)
	require.Equal(t, "DELETE", findRequest(t, cal, "DELETE", "/events/"+worshipNightID+"/rsvps/"+samRSVP).Method)
}

func TestRSVPsListCSVAndCounts(t *testing.T) {
	cal := newFakeCalendar(t)
	installRSVPs(t, cal)
	cal.route("GET /events/"+worshipNightID+"/rsvps", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"e","name":"=cmd\u001b","email":"a@b.c","phone":null,"occurrence_date":null,"created_at":"2099-09-03T09:00:00.000-05:00"}]`))
	}))
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")

	one := h.run("rsvps", "list", worshipNightID)
	require.Equal(t, 0, one.code, one.stdout)
	require.Equal(t, "1 RSVP", decodeEnvelope(t, one.stdout).Summary)

	csvRun := h.run("rsvps", "list", worshipNightID, "--csv")
	require.Equal(t, 0, csvRun.code, csvRun.stdout)
	require.Contains(t, csvRun.stdout, `'=cmd`)
	require.Contains(t, csvRun.stdout, "Name,Email,Phone,Date,ID")

	cal.route("GET /events/"+worshipNightID+"/rsvps", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("null"))
	}))
	empty := h.run("rsvps", "list", worshipNightID)
	require.Equal(t, 0, empty.code, empty.stdout)
	require.JSONEq(t, "[]", string(decodeEnvelope(t, empty.stdout).Data))
	require.Equal(t, "0 RSVPs", decodeEnvelope(t, empty.stdout).Summary)

	cal.route("GET /events/"+worshipNightID+"/rsvps", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"e","name":"=cmd\u001b","email":"a@b.c","phone":null,"occurrence_date":null,"created_at":"2099-09-03T09:00:00.000-05:00"}]`))
	}))
	h.outTTY = true
	h.env["NO_COLOR"] = "1"
	human := h.run("rsvps", "list", worshipNightID)
	require.Equal(t, 0, human.code, human.stdout)
	require.Contains(t, human.stdout, `\x1b`)
	require.NotContains(t, human.stdout, "\x1b")
}

// rsvps list --csv must not write the raw ESC or BEL from a public name,
// on a pipe or on a terminal. The spreadsheet prefix stays.
func TestRSVPsListCSVEscapesControlsOnAPipeAndATerminal(t *testing.T) {
	body := `[{"id":"r3","name":"\u001b]0;pwned\u0007","email":"a@b.c","phone":"+15551212","occurrence_date":null,"created_at":"2099-09-03T09:00:00.000-05:00"},{"id":"r4","name":"=HYPERLINK(\"http://example.com\")","email":"b@b.c","phone":null,"occurrence_date":null,"created_at":"2099-09-03T09:00:00.000-05:00"}]`
	tests := []struct {
		name string
		tty  bool
	}{
		{name: "a pipe", tty: false},
		{name: "a terminal", tty: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			installRSVPs(t, cal)
			cal.route("GET /events/"+worshipNightID+"/rsvps", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			h := eventsHarness(t, cal)
			pinChicago(h, 2026, time.November, 2)
			cacheZone(t, h, "America/Chicago")
			h.outTTY = tt.tty
			if tt.tty {
				h.env["NO_COLOR"] = "1"
			}

			r := h.run("rsvps", "list", worshipNightID, "--csv")

			require.Equal(t, 0, r.code, r.stdout)
			require.NotContains(t, r.stdout, "\x1b")
			require.NotContains(t, r.stdout, "\x07")
			require.Contains(t, r.stdout, `\x1b]0;pwned\a`)
			require.Contains(t, r.stdout, `'+15551212`)
			require.Contains(t, r.stdout, `"'=HYPERLINK`)
		})
	}
}

func TestRSVPsListResolvesATitleInsideTheWindow(t *testing.T) {
	cal := newFakeCalendar(t)
	installRSVPs(t, cal)
	list := golden(t, "events.json")
	cal.route("GET /events", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		start, end := r.URL.Query().Get("start"), r.URL.Query().Get("end")
		if end < "2099-10-25" || start > "2099-11-01" {
			_, _ = w.Write([]byte("[]"))
			return
		}
		_, _ = w.Write(list)
	}))
	h := eventsHarness(t, cal)
	pinChicago(h, 2099, time.November, 1)
	cacheZone(t, h, "America/Chicago")

	r := h.run("rsvps", "list", "Worship Night")

	require.Equal(t, 0, r.code, r.stdout)
	require.Equal(t, "GET", findRequest(t, cal, "GET", "/events/"+worshipNightID+"/rsvps").Method)
	require.Equal(t, 1, countMethod(cal, "GET", "/events?"))

	early := eventsHarness(t, cal)
	pinChicago(early, 2026, time.November, 2)
	cacheZone(t, early, "America/Chicago")
	miss := early.run("rsvps", "list", "Worship Night")
	require.Equal(t, 2, miss.code)
	require.Contains(t, decodeError(t, miss.stdout).Error.Message, "no event")
	require.Equal(t, 1, countMethod(cal, "GET", "/events/"+worshipNightID+"/rsvps"))
}

func TestRSVPsAddPhoneAndASingleMissingField(t *testing.T) {
	cal := newFakeCalendar(t)
	installRSVPs(t, cal)
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")

	cleared := h.run("rsvps", "add", worshipNightID, "--name", "Ada", "--email", "ada@example.com", "--phone", "")
	require.Equal(t, 0, cleared.code, cleared.stdout)
	body := bodyOf(t, cal, "POST", "/events/"+worshipNightID+"/rsvps")
	require.Contains(t, strings.ReplaceAll(body, " ", ""), `"phone":null`)
	require.NotContains(t, strings.ReplaceAll(body, " ", ""), `"occurrence_date"`)

	omitted := h.run("rsvps", "add", worshipNightID, "--name", "Ada", "--email", "ada@example.com")
	require.Equal(t, 0, omitted.code, omitted.stdout)
	var last string
	for _, req := range cal.requests() {
		if req.Method == "POST" && strings.HasPrefix(req.Path, "/events/"+worshipNightID+"/rsvps") {
			last = req.Body
		}
	}
	require.NotContains(t, strings.ReplaceAll(last, " ", ""), `"phone"`)

	h.inTTY = true
	h.errTTY = true
	h.env["TERM"] = "dumb"
	h.stdin = strings.NewReader("ada@example.com\n")
	asked := h.run("rsvps", "add", worshipNightID, "--name", "Ada")
	require.Equal(t, 0, asked.code, "stdout: %s\nstderr: %s", asked.stdout, asked.stderr)
	require.Contains(t, asked.stderr, "Email")
	require.NotContains(t, asked.stderr, "Name")
}

func TestRSVPsAddFormCancel(t *testing.T) {
	cal := newFakeCalendar(t)
	installRSVPs(t, cal)
	h := eventsHarness(t, cal)
	h.inTTY = true
	h.errTTY = true
	h.promptOptions = drawnPrompt
	h.stdin = strings.NewReader("\x1b")

	r := h.run("rsvps", "add", worshipNightID)

	require.Equal(t, 130, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	require.Empty(t, r.stdout)
	require.Contains(t, r.stderr, "Canceled. Nothing was added.")
	require.Empty(t, cal.requests())
}
