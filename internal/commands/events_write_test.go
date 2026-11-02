package commands

import (
	"encoding/json"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func installEventWrites(t *testing.T, cal *fakeCalendar) {
	t.Helper()
	installEvents(t, cal, golden(t, "events.json"))
	installTags(t, cal, golden(t, "tags.json"))
	cal.route("POST /events", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		cal.serve(w, http.StatusCreated, "event.json")
	}))
	cal.route("PATCH /events/"+worshipNightID, cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		cal.serve(w, http.StatusOK, "event.json")
	}))
	cal.route("DELETE /events/"+worshipNightID, cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	cal.route("DELETE /events/"+worshipNightID+"/image", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
}

func bodyOf(t *testing.T, cal *fakeCalendar, method, prefix string) string {
	t.Helper()
	for _, r := range cal.requests() {
		if r.Method == method && (r.Path == prefix || strings.HasPrefix(r.Path, prefix)) {
			return r.Body
		}
	}
	t.Fatalf("no %s %s; got %#v", method, prefix, cal.requests())
	return ""
}

func eventAttrs(t *testing.T, body string) map[string]any {
	t.Helper()
	var wrap struct {
		Event map[string]any `json:"event"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &wrap), body)
	require.NotNil(t, wrap.Event, body)
	return wrap.Event
}

func TestEventsCreateSendsTheNestedBody(t *testing.T) {
	cal := newFakeCalendar(t)
	installEventWrites(t, cal)
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.October, 31)
	cacheZone(t, h, "America/Chicago")

	r := h.run("events", "create",
		"--title", "Prayer",
		"--date", "2026-10-31",
		"--end-date", "2026-11-01",
		"--start", "6pm",
		"--end", "7:30pm",
		"--repeat", "weekly on sun",
		"--tag", "Worship",
		"--description", "Hello",
		"--notes", "Band",
		"--location-name", "Hall",
		"--address", "1 Main",
		"--map-url", "https://maps.app.goo.gl/x",
		"--website", "--rsvp", "--draft",
	)

	require.Equal(t, 0, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	attrs := eventAttrs(t, bodyOf(t, cal, "POST", "/events"))
	require.Equal(t, "Prayer", attrs["title"])
	require.Equal(t, "2026-10-31", attrs["occurs_on"])
	require.Equal(t, "2026-11-01", attrs["ends_on"])
	require.Equal(t, "2026-10-31T18:00:00-05:00", attrs["starts_at"])
	require.Equal(t, "2026-11-01T19:30:00-06:00", attrs["ends_at"])
	require.Equal(t, "FREQ=WEEKLY;BYDAY=SU", attrs["recurrence_rule"])
	require.Equal(t, []any{"b04decba-b16e-53f6-bfe4-5c510c838caa"}, attrs["tag_ids"])
	require.Equal(t, "Hello", attrs["description"])
	require.Equal(t, "Band", attrs["internal_notes"])
	require.Equal(t, "Hall", attrs["location_name"])
	require.Equal(t, "1 Main", attrs["location_address"])
	require.Equal(t, "https://maps.app.goo.gl/x", attrs["location_map_url"])
	require.Equal(t, true, attrs["show_on_website"])
	require.Equal(t, true, attrs["rsvp_enabled"])
	require.Equal(t, false, attrs["published"])
	env := decodeEnvelope(t, r.stdout)
	require.JSONEq(t, string(golden(t, "event.json")), string(env.Data))
	require.Equal(t, `Created "Worship Night".`, env.Summary)
}

func multipartBody(t *testing.T, body string) *multipart.Form {
	t.Helper()
	line, _, _ := strings.Cut(body, "\r\n")
	require.True(t, strings.HasPrefix(line, "--"), body[:min(80, len(body))])
	form, err := multipart.NewReader(strings.NewReader(body), strings.TrimPrefix(line, "--")).ReadForm(20 << 20)
	require.NoError(t, err)
	return form
}

func TestEventsCreateRRuleAndConflicts(t *testing.T) {
	cal := newFakeCalendar(t)
	installEventWrites(t, cal)
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.October, 31)
	cacheZone(t, h, "America/Chicago")

	r := h.run("events", "create", "--title", "Prayer", "--date", "2026-10-31", "--rrule", "FREQ=DAILY;COUNT=3")
	require.Equal(t, 0, r.code, r.stdout)
	require.Equal(t, "FREQ=DAILY;COUNT=3", eventAttrs(t, bodyOf(t, cal, "POST", "/events"))["recurrence_rule"])

	both := h.run("events", "create", "--title", "Prayer", "--date", "2026-10-31", "--repeat", "daily", "--rrule", "FREQ=DAILY")
	require.Equal(t, 1, both.code)
	require.Contains(t, decodeError(t, both.stdout).Error.Message, "--repeat and --rrule")

	day := h.run("events", "create", "--title", "Prayer", "--date", "2026-10-31", "--all-day")
	require.Equal(t, 0, day.code, day.stdout)
	var last string
	for _, req := range cal.requests() {
		if req.Method == "POST" {
			last = req.Body
		}
	}
	attrs := eventAttrs(t, last)
	require.Contains(t, last, `"starts_at":null`)
	require.Contains(t, last, `"ends_at":null`)
	require.Nil(t, attrs["starts_at"])
	require.Nil(t, attrs["ends_at"])

	clash := h.run("events", "create", "--title", "Prayer", "--date", "2026-10-31", "--all-day", "--start", "10am")
	require.Equal(t, 1, clash.code)
	require.Contains(t, decodeError(t, clash.stdout).Error.Message, "--all-day")
	require.Equal(t, 2, countMethod(cal, "POST", "/events"))
}

func TestEventsCreateNeedsATitleWithoutATTY(t *testing.T) {
	cal := newFakeCalendar(t)
	installEventWrites(t, cal)
	h := eventsHarness(t, cal)

	r := h.run("events", "create", "--date", "2026-10-31")

	require.Equal(t, 1, r.code)
	require.Contains(t, decodeError(t, r.stdout).Error.Message, "need a title and a date")
	require.Empty(t, cal.requests())
}

func TestEventsCreateAsksForTheMissingFields(t *testing.T) {
	cal := newFakeCalendar(t)
	installEventWrites(t, cal)
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.October, 31)
	cacheZone(t, h, "America/Chicago")
	h.inTTY = true
	h.errTTY = true
	h.env["TERM"] = "dumb"
	h.stdin = strings.NewReader("Prayer\n2026-10-31\n")

	r := h.run("events", "create")

	require.Equal(t, 0, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	attrs := eventAttrs(t, bodyOf(t, cal, "POST", "/events"))
	require.Equal(t, "Prayer", attrs["title"])
	require.Equal(t, "2026-10-31", attrs["occurs_on"])
	require.Contains(t, r.stderr, "Title")
	require.Contains(t, r.stderr, "Date")
}

func TestEventsCreateDescriptionFile(t *testing.T) {
	cal := newFakeCalendar(t)
	installEventWrites(t, cal)
	h := eventsHarness(t, cal)
	cacheZone(t, h, "America/Chicago")
	path := filepath.Join(t.TempDir(), "about.txt")
	require.NoError(t, os.WriteFile(path, []byte("From a file"), 0o600))

	r := h.run("events", "create", "--title", "Prayer", "--date", "2026-10-31", "--description-file", path)
	require.Equal(t, 0, r.code, r.stdout)
	require.Equal(t, "From a file", eventAttrs(t, bodyOf(t, cal, "POST", "/events"))["description"])

	h.stdin = strings.NewReader("From stdin")
	piped := h.run("events", "create", "--title", "Prayer", "--date", "2026-10-31", "--description-file", "-")
	require.Equal(t, 0, piped.code, piped.stdout)
	var last string
	for _, req := range cal.requests() {
		if req.Method == "POST" {
			last = req.Body
		}
	}
	require.Equal(t, "From stdin", eventAttrs(t, last)["description"])

	h.stdin = strings.NewReader(strings.Repeat("a", maxDescriptionBytes+1))
	big := h.run("events", "create", "--title", "Prayer", "--date", "2026-10-31", "--description-file", "-")
	require.Equal(t, 1, big.code)
	require.Contains(t, decodeError(t, big.stdout).Error.Message, "larger than 1 MiB")
	require.Equal(t, 2, countMethod(cal, "POST", "/events"))
}

func TestEventsCreateImage(t *testing.T) {
	cal := newFakeCalendar(t)
	installEventWrites(t, cal)
	h := eventsHarness(t, cal)
	cacheZone(t, h, "America/Chicago")
	path := filepath.Join(t.TempDir(), "sunrise.png")
	require.NoError(t, os.WriteFile(path, []byte("PNG bytes"), 0o600))

	r := h.run("events", "create", "--title", "Prayer", "--date", "2026-10-31", "--image", path, "--image-alt", "A sunrise")
	require.Equal(t, 0, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	form := multipartBody(t, bodyOf(t, cal, "POST", "/events"))
	require.Equal(t, []string{"A sunrise"}, form.Value["event[image_alt]"])
	require.Equal(t, []string{"Prayer"}, form.Value["event[title]"])
	require.Equal(t, []string{"2026-10-31"}, form.Value["event[occurs_on]"])
	require.Len(t, form.File["event[featured_image]"], 1)
	require.Equal(t, "sunrise.png", form.File["event[featured_image]"][0].Filename)

	encoded := h.run("events", "create", "--title", "Prayer", "--date", "2026-10-31", "--image", path, "--draft", "--notes", "", "--tag", "")
	require.Equal(t, 0, encoded.code, encoded.stdout)
	var encodedBody string
	for _, req := range cal.requests() {
		if req.Method == "POST" && strings.HasPrefix(req.Body, "--") {
			encodedBody = req.Body
		}
	}
	encodedForm := multipartBody(t, encodedBody)
	require.Equal(t, []string{"false"}, encodedForm.Value["event[published]"])
	require.Equal(t, []string{""}, encodedForm.Value["event[internal_notes]"])
	require.Equal(t, []string{""}, encodedForm.Value["event[tag_ids][]"])

	exact := filepath.Join(t.TempDir(), "exact.png")
	require.NoError(t, os.WriteFile(exact, make([]byte, maxImageBytes), 0o600))
	fits := h.run("events", "create", "--title", "Prayer", "--date", "2026-10-31", "--image", exact)
	require.Equal(t, 0, fits.code, fits.stdout)

	asDir := h.run("events", "create", "--title", "Prayer", "--date", "2026-10-31", "--image", t.TempDir())
	require.Equal(t, 1, asDir.code)
	require.Contains(t, decodeError(t, asDir.stdout).Error.Message, "is a directory")

	posts := countMethod(cal, "POST", "/events")
	both := h.run("events", "create", "--title", "Prayer", "--date", "2026-10-31", "--image", path, "--image-url", "https://images.example.com/a.jpg")
	require.Equal(t, 1, both.code)
	require.Contains(t, decodeError(t, both.stdout).Error.Message, "--image and --image-url")
	require.Equal(t, posts, countMethod(cal, "POST", "/events"))

	big := filepath.Join(t.TempDir(), "big.png")
	require.NoError(t, os.WriteFile(big, make([]byte, maxImageBytes+1), 0o600))
	over := h.run("events", "create", "--title", "Prayer", "--date", "2026-10-31", "--image", big)
	require.Equal(t, 1, over.code)
	require.Contains(t, decodeError(t, over.stdout).Error.Message, "larger than 10 MB")
	require.Equal(t, posts, countMethod(cal, "POST", "/events"))
}

func TestEventsUpdateMovesATimedEvent(t *testing.T) {
	cal := newFakeCalendar(t)
	installEventWrites(t, cal)
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")

	r := h.run("events", "update", worshipNightID, "--date", "2026-11-02")

	require.Equal(t, 0, r.code, r.stdout)
	attrs := eventAttrs(t, bodyOf(t, cal, "PATCH", "/events/"+worshipNightID))
	require.Equal(t, "2026-11-02", attrs["occurs_on"])
	require.Equal(t, "2026-11-02T18:00:00-06:00", attrs["starts_at"])
	require.Equal(t, "2026-11-02T19:30:00-06:00", attrs["ends_at"])
	require.NotContains(t, bodyOf(t, cal, "PATCH", "/events/"+worshipNightID), "title")
	require.Equal(t, 1, countMethod(cal, "GET", "/events/"+worshipNightID))
}

func TestEventsUpdateSendsOnlyTheFlagsGiven(t *testing.T) {
	cal := newFakeCalendar(t)
	installEventWrites(t, cal)
	h := eventsHarness(t, cal)
	cacheZone(t, h, "America/Chicago")

	r := h.run("events", "update", worshipNightID, "--title", "Renamed")
	require.Equal(t, 0, r.code, r.stdout)
	require.Equal(t, map[string]any{"title": "Renamed"}, eventAttrs(t, bodyOf(t, cal, "PATCH", "/events/"+worshipNightID)))
	require.Zero(t, countMethod(cal, "GET", "/events/"+worshipNightID))

	cleared := h.run("events", "update", worshipNightID, "--tag", "", "--rrule", "")
	require.Equal(t, 0, cleared.code, cleared.stdout)
	var last string
	for _, req := range cal.requests() {
		if req.Method == "PATCH" {
			last = req.Body
		}
	}
	attrs := eventAttrs(t, last)
	require.Equal(t, []any{}, attrs["tag_ids"])
	require.Nil(t, attrs["recurrence_rule"])
}

func TestEventsUpdateRebuildsTimes(t *testing.T) {
	cal := newFakeCalendar(t)
	installEventWrites(t, cal)
	body := append([]byte(nil), golden(t, "event.json")...)
	var event map[string]any
	require.NoError(t, json.Unmarshal(body, &event))
	event["starts_at"] = nil
	event["ends_at"] = nil
	event["all_day"] = true
	raw, err := json.Marshal(event)
	require.NoError(t, err)
	cal.route("GET /events/"+worshipNightID, cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(raw)
	}))
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")

	moved := h.run("events", "update", worshipNightID, "--date", "2026-11-02")
	require.Equal(t, 0, moved.code, moved.stdout)
	attrs := eventAttrs(t, bodyOf(t, cal, "PATCH", "/events/"+worshipNightID))
	require.Equal(t, "2026-11-02", attrs["occurs_on"])
	_, hasStart := attrs["starts_at"]
	_, hasEnd := attrs["ends_at"]
	require.False(t, hasStart)
	require.False(t, hasEnd)

	day := h.run("events", "update", worshipNightID, "--all-day")
	require.Equal(t, 0, day.code, day.stdout)
	gets := countMethod(cal, "GET", "/events/"+worshipNightID)
	var last string
	for _, req := range cal.requests() {
		if req.Method == "PATCH" {
			last = req.Body
		}
	}
	require.Equal(t, 1, gets)
	require.Contains(t, last, `"starts_at":null`)
	require.Contains(t, last, `"ends_at":null`)
	require.Nil(t, eventAttrs(t, last)["starts_at"])
	require.Nil(t, eventAttrs(t, last)["ends_at"])
	require.NotContains(t, last, "occurs_on")

	full := h.run("events", "update", worshipNightID,
		"--date", "2026-11-07", "--end-date", "2026-11-08", "--start", "10am", "--end", "11am")
	require.Equal(t, 0, full.code, full.stdout)
	require.Equal(t, 1, countMethod(cal, "GET", "/events/"+worshipNightID))
	for _, req := range cal.requests() {
		if req.Method == "PATCH" {
			last = req.Body
		}
	}
	attrs = eventAttrs(t, last)
	require.Equal(t, "2026-11-07", attrs["occurs_on"])
	require.Equal(t, "2026-11-08", attrs["ends_on"])
	require.Equal(t, "2026-11-07T10:00:00-06:00", attrs["starts_at"])
	require.Equal(t, "2026-11-08T11:00:00-06:00", attrs["ends_at"])

	early := h.run("events", "create", "--title", "Prayer", "--date", "2026-11-02", "--end-date", "2026-10-31")
	require.Equal(t, 1, early.code)
	require.Contains(t, decodeError(t, early.stdout).Error.Message, "--end-date is before --date")
}

func TestEventsCreateRefusesASharedStdin(t *testing.T) {
	cal := newFakeCalendar(t)
	installEventWrites(t, cal)
	h := eventsHarness(t, cal)
	h.stdin = strings.NewReader("From stdin")

	shared := h.run("events", "create", "--description-file", "-")
	require.Equal(t, 1, shared.code)
	require.Contains(t, decodeError(t, shared.stdout).Error.Message, "pass --title and --date when --description-file is -")
	require.Empty(t, cal.requests())

	both := h.run("events", "create", "--title", "Prayer", "--date", "2026-10-31", "--description", "Hi", "--description-file", "-")
	require.Equal(t, 1, both.code)
	require.Contains(t, decodeError(t, both.stdout).Error.Message, "--description and --description-file")
	require.Empty(t, cal.requests())
}

func TestEventsCreateDrawnCancel(t *testing.T) {
	cal := newFakeCalendar(t)
	installEventWrites(t, cal)
	h := eventsHarness(t, cal)
	h.inTTY = true
	h.errTTY = true
	h.outTTY = true
	h.promptOptions = drawnPrompt
	h.stdin = strings.NewReader("\x1b")

	r := h.run("events", "create")

	require.Equal(t, 130, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	require.Empty(t, r.stdout)
	require.Contains(t, r.stderr, "Canceled. Nothing was created.")
	require.Empty(t, cal.requests())
}

func TestEventsDelete(t *testing.T) {
	cal := newFakeCalendar(t)
	installEventWrites(t, cal)
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")
	// TERM=dumb makes a prompt that this guard must not reach answer from
	// the empty reader. A drawn form would wait for a window instead, and
	// the test would only fail by timing out.
	h.env["TERM"] = "dumb"

	skipped := h.run("events", "delete", worshipNightID)
	require.Equal(t, 1, skipped.code)
	require.Contains(t, decodeError(t, skipped.stdout).Error.Message, "--yes wasn't set")
	require.Empty(t, cal.requests())

	yes := h.run("--yes", "events", "delete", worshipNightID)
	require.Equal(t, 0, yes.code, yes.stdout)
	require.Equal(t, true, decodeEnvelope(t, yes.stdout).Data != nil)
	var body struct {
		Deleted bool `json:"deleted"`
	}
	require.NoError(t, json.Unmarshal(decodeEnvelope(t, yes.stdout).Data, &body))
	require.True(t, body.Deleted)
	require.Equal(t, `Deleted "Worship Night".`, decodeEnvelope(t, yes.stdout).Summary)
	require.Equal(t, "DELETE", findRequest(t, cal, "DELETE", "/events/"+worshipNightID).Method)

	h.inTTY = true
	h.errTTY = true
	h.env["TERM"] = "dumb"
	h.env["NO_COLOR"] = "1"
	h.stdin = strings.NewReader("n\n")
	no := h.run("events", "delete", worshipNightID)
	require.Equal(t, 0, no.code, no.stderr)
	require.Equal(t, "Nothing was deleted.", decodeEnvelope(t, no.stdout).Summary)
	require.Contains(t, no.stderr, `Delete "Worship Night" on Sun Oct 25, 2099? This deletes the whole series.`)
	require.Equal(t, 1, countMethod(cal, "DELETE", "/events/"+worshipNightID))

	h.stdin = strings.NewReader("\n")
	enter := h.run("events", "delete", worshipNightID)
	require.Equal(t, 0, enter.code, enter.stderr)
	require.Equal(t, "Nothing was deleted.", decodeEnvelope(t, enter.stdout).Summary)

	h.stdin = strings.NewReader("\x04")
	ctrlD := h.run("events", "delete", worshipNightID)
	require.Equal(t, 0, ctrlD.code, ctrlD.stderr)
	require.Equal(t, "Nothing was deleted.", decodeEnvelope(t, ctrlD.stdout).Summary)
	require.Equal(t, 1, countMethod(cal, "DELETE", "/events/"+worshipNightID))
}

func TestEventsDeleteAccessibleEscCancels(t *testing.T) {
	tests := []struct {
		name string
		key  string
		val  string
	}{
		{"ACCESSIBLE", "ACCESSIBLE", "1"},
		{"TERM=dumb", "TERM", "dumb"},
		{"NO_COLOR", "NO_COLOR", "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			installEventWrites(t, cal)
			h := eventsHarness(t, cal)
			pinChicago(h, 2026, time.November, 2)
			cacheZone(t, h, "America/Chicago")
			h.inTTY = true
			h.errTTY = true
			h.env[tt.key] = tt.val
			h.stdin = strings.NewReader("\x1b")

			r := h.run("events", "delete", worshipNightID)

			require.Equal(t, 130, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
			require.Empty(t, r.stdout)
			require.Contains(t, r.stderr, "Canceled. Nothing was deleted.")
			require.NotContains(t, r.stderr, "invalid")
			require.NotContains(t, r.stdout+r.stderr, "Login canceled")
			for _, req := range cal.requests() {
				require.NotEqual(t, "DELETE", req.Method)
			}
		})
	}
}

func TestEventsTogglesAndImage(t *testing.T) {
	cal := newFakeCalendar(t)
	installEventWrites(t, cal)
	h := eventsHarness(t, cal)
	cacheZone(t, h, "America/Chicago")

	pub := h.run("events", "publish", worshipNightID)
	require.Equal(t, 0, pub.code, pub.stdout)
	require.Equal(t, true, eventAttrs(t, bodyOf(t, cal, "PATCH", "/events/"+worshipNightID))["published"])

	web := h.run("events", "website", "off", worshipNightID)
	require.Equal(t, 0, web.code, web.stdout)
	rsvp := h.run("events", "rsvp", "on", worshipNightID)
	require.Equal(t, 0, rsvp.code, rsvp.stdout)
	var sawWeb, sawRSVP bool
	for _, req := range cal.requests() {
		if req.Method != "PATCH" {
			continue
		}
		attrs := eventAttrs(t, req.Body)
		if v, ok := attrs["show_on_website"]; ok {
			require.Equal(t, false, v)
			sawWeb = true
		}
		if v, ok := attrs["rsvp_enabled"]; ok {
			require.Equal(t, true, v)
			sawRSVP = true
		}
	}
	require.True(t, sawWeb)
	require.True(t, sawRSVP)

	path := filepath.Join(t.TempDir(), "sunrise.png")
	require.NoError(t, os.WriteFile(path, []byte("PNG bytes"), 0o600))
	set := h.run("events", "image", "set", worshipNightID, path, "--alt", "A sunrise")
	require.Equal(t, 0, set.code, set.stdout)
	var imageBody string
	for _, req := range cal.requests() {
		if req.Method == "PATCH" && strings.HasPrefix(req.Body, "--") {
			imageBody = req.Body
		}
	}
	form := multipartBody(t, imageBody)
	require.Equal(t, []string{"A sunrise"}, form.Value["event[image_alt]"])
	require.Equal(t, "sunrise.png", form.File["event[featured_image]"][0].Filename)

	link := h.run("events", "image", "set", worshipNightID, "--url", "https://images.example.com/a.jpg")
	require.Equal(t, 0, link.code, link.stdout)
	var linked string
	for _, req := range cal.requests() {
		if req.Method == "PATCH" && strings.Contains(req.Body, "image_url") {
			linked = req.Body
		}
	}
	require.Equal(t, "https://images.example.com/a.jpg", eventAttrs(t, linked)["image_url"])

	both := h.run("events", "image", "set", worshipNightID, path, "--url", "https://images.example.com/a.jpg")
	require.Equal(t, 1, both.code)
	neither := h.run("events", "image", "set", worshipNightID)
	require.Equal(t, 1, neither.code)

	rm := h.run("events", "image", "rm", worshipNightID)
	require.Equal(t, 0, rm.code, rm.stdout)
	require.Contains(t, rm.stdout, `"removed": true`)
	require.Equal(t, "DELETE", findRequest(t, cal, "DELETE", "/events/"+worshipNightID+"/image").Method)
}

func TestEventsCreateValidationAndForbidden(t *testing.T) {
	cal := newFakeCalendar(t)
	installEventWrites(t, cal)
	cal.route("POST /events", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		cal.serve(w, http.StatusUnprocessableEntity, "error_validation.json")
	}))
	h := eventsHarness(t, cal)
	cacheZone(t, h, "America/Chicago")

	r := h.run("events", "create", "--title", "Prayer", "--date", "2026-10-31")
	require.Equal(t, 1, r.code)
	env := decodeError(t, r.stdout)
	require.Equal(t, "validation", env.Error.Code)
	require.Equal(t, "Title can't be blank", env.Error.Message)
	require.Equal(t, []string{"can't be blank"}, env.Error.Fields["title"])
	require.Equal(t, []string{"can't be blank"}, env.Error.Fields["occurs_on"])

	cal.route("POST /events", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		cal.serve(w, http.StatusForbidden, "error_read_only_token.json")
	}))
	denied := h.run("events", "create", "--title", "Prayer", "--date", "2026-10-31")
	require.Equal(t, 4, denied.code)
	require.Equal(t, "token is read-only", decodeError(t, denied.stdout).Error.Message)
}
