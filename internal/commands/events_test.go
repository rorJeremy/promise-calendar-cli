package commands

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rorJeremy/promise-calendar-cli/internal/client"
	"github.com/rorJeremy/promise-calendar-cli/internal/config"
	"github.com/rorJeremy/promise-calendar-cli/skills"
)

// eventNotFoundHintWant is the exit 2 hint. It names the 365 cap and does
// not claim `pcal events list --search` will show an id the list can't
// return (Gate 4c, G4b-F2). The unicode minus matches the skill.
const eventNotFoundHintWant = "use the event's ID. `pcal events list` doesn't include a series outside today−1 year through today+2 years, or one past 365 occurrences"

const worshipNightID = "2c8f0476-9289-5844-9a29-c7fb20a9f3b3"

func installEvents(t *testing.T, cal *fakeCalendar, listBody []byte) {
	t.Helper()
	cal.route("GET /events", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(listBody)
	}))
	cal.route("GET /events/"+worshipNightID, cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		cal.serve(w, http.StatusOK, "event.json")
	}))
}

func eventsHarness(t *testing.T, cal *fakeCalendar) *harness {
	t.Helper()
	h := newHarness(t, cal.URL())
	h.env["PCAL_TOKEN"] = goodToken
	return h
}

func pinChicago(h *harness, year int, month time.Month, day int) {
	h.t.Helper()
	loc, err := time.LoadLocation("America/Chicago")
	require.NoError(h.t, err)
	h.clock = func() time.Time {
		return time.Date(year, month, day, 12, 0, 0, 0, loc)
	}
}

func cacheZone(t *testing.T, h *harness, zone string) {
	t.Helper()
	require.NoError(t, config.Save(h.configDir(), config.Config{TimeZone: zone}))
}

func eventsListQuery(t *testing.T, cal *fakeCalendar) url.Values {
	t.Helper()
	for _, r := range cal.requests() {
		if r.Method == "GET" && strings.HasPrefix(r.Path, "/events?") {
			u, err := url.Parse(r.Path)
			require.NoError(t, err)
			return u.Query()
		}
	}
	t.Fatalf("no GET /events list; got %#v", cal.requests())
	return nil
}

func countMethod(cal *fakeCalendar, method, prefix string) int {
	n := 0
	for _, r := range cal.requests() {
		if r.Method == method && (r.Path == prefix || strings.HasPrefix(r.Path, prefix)) {
			n++
		}
	}
	return n
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}

// occ is one list item. Times and tags are filled in by the caller.
func occ(id, title, date string) map[string]any {
	return map[string]any{
		"id": id, "title": title, "description": nil, "internal_notes": nil,
		"occurs_on": date, "ends_on": nil, "starts_at": nil, "ends_at": nil,
		"all_day": true, "multi_day": false, "recurrence_rule": nil, "recurrence_text": nil,
		"location":  map[string]any{"name": nil, "address": nil, "map_url": nil, "latitude": nil, "longitude": nil},
		"published": true, "show_on_website": false, "rsvp_enabled": false, "rsvp_count": 0,
		"tags":  []any{},
		"image": nil, "created_by": nil,
		"created_at":      "2099-09-01T09:00:00.000-05:00",
		"updated_at":      "2099-09-01T09:00:00.000-05:00",
		"url":             "http://www.example.com/events/" + id + ".json",
		"occurrence_date": date, "occurrence_starts_at": nil, "occurrence_ends_at": nil,
	}
}

func TestEventsGroup(t *testing.T) {
	r := runPcal(t, "events")

	require.Equal(t, 0, r.code, r.stderr)
	require.Contains(t, r.stdout, "list")
	require.Contains(t, r.stdout, "show")
}

func TestEventsRejectsUnknownSubcommands(t *testing.T) {
	r := runPcal(t, "events", "frobnicate")

	require.Equal(t, 1, r.code)
	require.Contains(t, decodeError(t, r.stdout).Error.Message, `unknown command "frobnicate" for "pcal events"`)
}

func TestEventsListRejectsArguments(t *testing.T) {
	r := runPcal(t, "events", "list", "Worship")

	require.Equal(t, 1, r.code)
	require.Contains(t, decodeError(t, r.stdout).Error.Message, `unknown command "Worship" for "pcal events list"`)
}

func TestEventsListDoesntRepeatATokenArgument(t *testing.T) {
	r := runPcal(t, "events", "list", "pcal_secretvalue")

	require.Equal(t, 1, r.code)
	require.NotContains(t, r.stdout+r.stderr, "pcal_secretvalue")
}

func TestEventsList(t *testing.T) {
	cal := newFakeCalendar(t)
	installEvents(t, cal, golden(t, "events.json"))
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")

	r := h.run("events", "list")

	require.Equal(t, 0, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	env := decodeEnvelope(t, r.stdout)
	require.JSONEq(t, string(golden(t, "events.json")), string(env.Data))
	require.Equal(t, "2 events Nov 2–Dec 2", env.Summary)
	q := eventsListQuery(t, cal)
	require.Equal(t, "2026-11-02", q.Get("start"))
	require.Equal(t, "2026-12-02", q.Get("end"))
	require.Empty(t, q.Get("q"))
	require.Empty(t, q.Get("published"))
	require.Empty(t, q.Get("website"))
	require.Empty(t, q["tag_ids[]"])
	require.Zero(t, countMethod(cal, "GET", "/my/identity"))
}

func TestEventsListFetchesTheTimeZoneWhenItIsntCached(t *testing.T) {
	cal := newFakeCalendar(t)
	installEvents(t, cal, golden(t, "events.json"))
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)

	r := h.run("events", "list")

	require.Equal(t, 0, r.code, r.stdout)
	require.Equal(t, "America/Chicago", h.savedConfig().TimeZone)
	require.Equal(t, 1, countMethod(cal, "GET", "/my/identity"))
	require.Equal(t, "2026-11-02", eventsListQuery(t, cal).Get("start"))
}

func TestEventsListKeepsTheServersOrder(t *testing.T) {
	cal := newFakeCalendar(t)
	later := occ("later", "Later", "2099-11-08")
	earlier := occ("earlier", "Earlier", "2099-11-01")
	installEvents(t, cal, mustJSON(t, []any{later, earlier}))
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")

	r := h.run("events", "list")

	require.Equal(t, 0, r.code, r.stdout)
	data := string(decodeEnvelope(t, r.stdout).Data)
	require.Less(t, strings.Index(data, "Later"), strings.Index(data, "Earlier"))
}

func TestEventsListKeepsAnOccurrenceThatStartedBeforeFrom(t *testing.T) {
	cal := newFakeCalendar(t)
	installEvents(t, cal, golden(t, "events.json"))
	h := eventsHarness(t, cal)
	pinChicago(h, 2099, time.November, 1)
	cacheZone(t, h, "America/Chicago")

	r := h.run("events", "list", "--from", "2099-11-01", "--to", "2099-11-01")

	require.Equal(t, 0, r.code, r.stdout)
	env := decodeEnvelope(t, r.stdout)
	require.JSONEq(t, string(golden(t, "events.json")), string(env.Data))
	require.Contains(t, string(env.Data), `"occurrence_date": "2099-10-25"`)
	require.Equal(t, "2099-11-01", eventsListQuery(t, cal).Get("start"))
}

func TestEventsListEmptyAndOne(t *testing.T) {
	cal := newFakeCalendar(t)
	installEvents(t, cal, []byte("null"))
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")

	empty := h.run("events", "list")
	require.Equal(t, 0, empty.code, empty.stdout)
	env := decodeEnvelope(t, empty.stdout)
	require.JSONEq(t, "[]", string(env.Data))
	require.Equal(t, "0 events Nov 2–Dec 2", env.Summary)

	installEvents(t, cal, mustJSON(t, []any{occ("one", "One", "2099-11-02")}))
	one := h.run("events", "list", "--quiet")
	require.Equal(t, 0, one.code, one.stdout)
	require.Contains(t, one.stdout, `"title": "One"`)
	require.NotContains(t, one.stdout, `"ok"`)
	require.NotContains(t, one.stdout, "1 event")
}

func TestEventsListHumanChicago(t *testing.T) {
	cal := newFakeCalendar(t)
	installEvents(t, cal, golden(t, "events.json"))
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")
	h.outTTY = true
	h.env["NO_COLOR"] = "1"

	r := h.run("events", "list")

	require.Equal(t, 0, r.code, r.stderr)
	require.Contains(t, r.stdout, "2 events Nov 2–Dec 2")
	require.Contains(t, r.stdout, "Sun Oct 25, 2099")
	require.Contains(t, r.stdout, "Sun Nov 1, 2099")
	require.Contains(t, r.stdout, "6:00pm–7:30pm")
	require.Contains(t, r.stdout, "Worship, Outreach")
	require.Contains(t, r.stdout, "web, rsvp")
	require.NotContains(t, r.stdout, "\x1b")
	require.Less(t, strings.Index(r.stdout, "Sun Oct 25, 2099"), strings.Index(r.stdout, "Sun Nov 1, 2099"))
}

func TestEventsListOmitsTheYearWhenItIsThisYear(t *testing.T) {
	cal := newFakeCalendar(t)
	installEvents(t, cal, golden(t, "events.json"))
	h := eventsHarness(t, cal)
	pinChicago(h, 2099, time.October, 20)
	cacheZone(t, h, "America/Chicago")
	h.outTTY = true
	h.env["NO_COLOR"] = "1"

	r := h.run("events", "list")

	require.Equal(t, 0, r.code, r.stderr)
	require.Contains(t, r.stdout, "2 events Oct 20–Nov 19")
	require.Contains(t, r.stdout, "Sun Oct 25")
	require.Contains(t, r.stdout, "Sun Nov 1")
	require.NotContains(t, r.stdout, "2099")
}

func TestEventsListUsesTheCalendarZone(t *testing.T) {
	cal := newFakeCalendar(t)
	installEvents(t, cal, golden(t, "events.json"))
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/New_York")
	h.outTTY = true
	h.env["NO_COLOR"] = "1"

	r := h.run("events", "list")

	require.Equal(t, 0, r.code, r.stderr)
	// 6:00pm Chicago is 7:00pm in New York. The digits in the timestamp stay
	// in JSON; this is the human clock.
	require.Contains(t, r.stdout, "7:00pm–8:30pm")
	require.NotContains(t, r.stdout, "6:00pm")
}

func TestEventsListAllDayAndMultiDay(t *testing.T) {
	cal := newFakeCalendar(t)
	allDay := occ("all", "Prayer", "2099-11-02")
	span := occ("span", "Retreat", "2099-11-01")
	span["occurs_on"] = "2099-10-25"
	span["ends_on"] = "2099-10-27"
	span["multi_day"] = true
	installEvents(t, cal, mustJSON(t, []any{allDay, span}))
	h := eventsHarness(t, cal)
	pinChicago(h, 2099, time.November, 2)
	cacheZone(t, h, "America/Chicago")
	h.outTTY = true
	h.env["NO_COLOR"] = "1"

	r := h.run("events", "list")

	require.Equal(t, 0, r.code, r.stderr)
	require.Contains(t, r.stdout, "all day")
	// occurrence_date Nov 1 plus the series' two days, not the series dates.
	require.Contains(t, r.stdout, "Sun Nov 1–Tue Nov 3")
	require.NotContains(t, r.stdout, "Oct 25")
}

func TestEventsListRangeWordsAndSummary(t *testing.T) {
	cal := newFakeCalendar(t)
	installEvents(t, cal, []byte("[]"))
	h := eventsHarness(t, cal)
	// 2026-11-02 is a Monday, so "sat" is Nov 7.
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")

	r := h.run("events", "list", "--from", "sat", "--to", "2099-10-31")

	require.Equal(t, 0, r.code, r.stdout)
	require.Equal(t, "0 events Nov 7, 2026–Oct 31, 2099", decodeEnvelope(t, r.stdout).Summary)
	q := eventsListQuery(t, cal)
	require.Equal(t, "2026-11-07", q.Get("start"))
	require.Equal(t, "2099-10-31", q.Get("end"))

	sameMonth := h.run("events", "list", "--from", "2099-10-01", "--to", "2099-10-31")
	require.Equal(t, 0, sameMonth.code, sameMonth.stdout)
	require.Equal(t, "0 events Oct 1, 2099–Oct 31, 2099", decodeEnvelope(t, sameMonth.stdout).Summary)

	thisMonth := h.run("events", "list", "--from", "2026-11-01", "--to", "2026-11-30")
	require.Equal(t, "0 events Nov 1–30", decodeEnvelope(t, thisMonth.stdout).Summary)
}

func TestEventsListFromOnlyAndToOnly(t *testing.T) {
	cal := newFakeCalendar(t)
	installEvents(t, cal, []byte("[]"))
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")

	fromOnly := h.run("events", "list", "--from", "2099-10-01")
	require.Equal(t, 0, fromOnly.code, fromOnly.stdout)
	q := eventsListQuery(t, cal)
	require.Equal(t, "2099-10-01", q.Get("start"))
	require.Equal(t, "2099-10-31", q.Get("end"))

	toOnly := h.run("events", "list", "--to", "2026-12-01")
	require.Equal(t, 0, toOnly.code, toOnly.stdout)
	// The second list is the latest GET /events.
	var last url.Values
	for _, r := range cal.requests() {
		if r.Method == "GET" && strings.HasPrefix(r.Path, "/events?") {
			u, err := url.Parse(r.Path)
			require.NoError(t, err)
			last = u.Query()
		}
	}
	require.Equal(t, "2026-11-02", last.Get("start"))
	require.Equal(t, "2026-12-01", last.Get("end"))
}

func TestEventsListRefusesAToBeforeFrom(t *testing.T) {
	cal := newFakeCalendar(t)
	installEvents(t, cal, golden(t, "events.json"))
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)

	r := h.run("events", "list", "--from", "2099-11-02", "--to", "2099-11-01")

	require.Equal(t, 1, r.code, r.stdout)
	env := decodeError(t, r.stdout)
	require.Equal(t, "usage", env.Error.Code)
	require.Contains(t, env.Error.Message, "--to is before --from")
	require.Empty(t, cal.requests(), "an inverted range is rejected before any request")
}

func TestEventsListRefusesARelativeToBeforeFromBeforeTheList(t *testing.T) {
	cal := newFakeCalendar(t)
	installEvents(t, cal, golden(t, "events.json"))
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")

	r := h.run("events", "list", "--from", "today", "--to", "2020-01-01")

	require.Equal(t, 1, r.code, r.stdout)
	require.Contains(t, decodeError(t, r.stdout).Error.Message, "--to is before --from")
	require.Zero(t, countMethod(cal, "GET", "/events"))
}

func TestEventsListInvalidDate(t *testing.T) {
	cal := newFakeCalendar(t)
	installEvents(t, cal, []byte("[]"))
	h := eventsHarness(t, cal)
	cacheZone(t, h, "America/Chicago")

	r := h.run("events", "list", "--from", "whenever")

	require.Equal(t, 1, r.code)
	env := decodeError(t, r.stdout)
	require.Contains(t, env.Error.Message, `invalid date "whenever"`)
	require.Contains(t, env.Error.Hint, "YYYY-MM-DD")
	require.Zero(t, countMethod(cal, "GET", "/events"))
}

func TestEventsListFilters(t *testing.T) {
	cal := newFakeCalendar(t)
	installEvents(t, cal, []byte("[]"))
	installTags(t, cal, golden(t, "tags.json"))
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")

	r := h.run("events", "list", "--search", "Night", "--unpublished", "--website", "--tag", "Worship", "--tag", "Outreach")

	require.Equal(t, 0, r.code, r.stdout)
	q := eventsListQuery(t, cal)
	require.Equal(t, "Night", q.Get("q"))
	require.Equal(t, "false", q.Get("published"))
	require.Equal(t, "true", q.Get("website"))
	require.Equal(t, []string{
		"b04decba-b16e-53f6-bfe4-5c510c838caa",
		"a98b80ac-3e85-5e62-9124-8c5d6dcaf214",
	}, q["tag_ids[]"])
}

func TestEventsListTagMatching(t *testing.T) {
	body := []byte(`[
		{"id":"exact","name":"Worship","color":"#FF5733","position":1},
		{"id":"fold","name":"worship","color":"#000000","position":2},
		{"id":"comma","name":"Kids, Youth","color":null,"position":3}
	]`)
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantIDs  []string
		wantMsg  string
	}{
		{"exact case wins", []string{"--tag", "Worship"}, 0, []string{"exact"}, ""},
		{"unique fold", []string{"--tag", "KIDS, YOUTH"}, 0, []string{"comma"}, ""},
		{"a comma stays inside one name", []string{"--tag", "Kids, Youth"}, 0, []string{"comma"}, ""},
		{"case-only duplicates", []string{"--tag", "WORSHIP"}, 8, nil, "matches more than one tag"},
		{"unknown", []string{"--tag", "Missing"}, 1, nil, `no tag "Missing"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			installEvents(t, cal, []byte("[]"))
			installTags(t, cal, body)
			h := eventsHarness(t, cal)
			pinChicago(h, 2026, time.November, 2)
			cacheZone(t, h, "America/Chicago")

			r := h.run(append([]string{"events", "list"}, tt.args...)...)

			require.Equal(t, tt.wantCode, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
			if tt.wantCode != 0 {
				require.Contains(t, decodeError(t, r.stdout).Error.Message, tt.wantMsg)
				require.Zero(t, countMethod(cal, "GET", "/events"))
				return
			}
			require.Equal(t, tt.wantIDs, eventsListQuery(t, cal)["tag_ids[]"])
		})
	}
}

func TestEventsListBlankTagIsAUsageError(t *testing.T) {
	cal := newFakeCalendar(t)
	installEvents(t, cal, []byte("[]"))
	installTags(t, cal, golden(t, "tags.json"))
	h := eventsHarness(t, cal)
	cacheZone(t, h, "America/Chicago")

	r := h.run("events", "list", "--tag", "")

	require.Equal(t, 1, r.code, r.stdout)
	env := decodeError(t, r.stdout)
	require.Equal(t, "usage", env.Error.Code)
	require.Contains(t, env.Error.Message, "need a tag name")
	require.Zero(t, countMethod(cal, "GET", "/events"))
	require.Zero(t, countMethod(cal, "GET", "/tags"))
}

func TestEventsListCSVGuardsFormulas(t *testing.T) {
	cal := newFakeCalendar(t)
	item := occ("e", "=Kids", "2099-11-02")
	installEvents(t, cal, mustJSON(t, []any{item}))
	h := eventsHarness(t, cal)
	pinChicago(h, 2099, time.November, 2)
	cacheZone(t, h, "America/Chicago")

	r := h.run("events", "list", "--csv")

	require.Equal(t, 0, r.code, r.stdout)
	require.Equal(t, ""+
		"Date,Time,Title,Tags,Flags\n"+
		"Mon Nov 2,all day,'=Kids,,\n", r.stdout)
}

func TestEventsListJQKeepsServerTimes(t *testing.T) {
	cal := newFakeCalendar(t)
	installEvents(t, cal, golden(t, "events.json"))
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")

	r := h.run("events", "list", "--jq", ".data[0].occurrence_starts_at")

	require.Equal(t, 0, r.code, r.stderr)
	require.Equal(t, "2099-10-25T18:00:00.000-05:00\n", r.stdout)
	require.NotContains(t, r.stdout, "6:00pm")
}

func TestEventsListNotLoggedIn(t *testing.T) {
	cal := newFakeCalendar(t)
	h := newHarness(t, cal.URL())

	r := h.run("events", "list")

	require.Equal(t, 3, r.code)
	require.Equal(t, "not logged in", decodeError(t, r.stdout).Error.Message)
	require.Empty(t, cal.requests())
}

func TestEventsListReadOnly(t *testing.T) {
	cal := newFakeCalendar(t)
	cal.route("GET /events", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		cal.serve(w, http.StatusForbidden, "error_read_only_token.json")
	}))
	h := eventsHarness(t, cal)
	cacheZone(t, h, "America/Chicago")

	r := h.run("events", "list")

	require.Equal(t, 4, r.code)
	require.Equal(t, "token is read-only", decodeError(t, r.stdout).Error.Message)
}

func TestEventsShow(t *testing.T) {
	cal := newFakeCalendar(t)
	installEvents(t, cal, golden(t, "events.json"))
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")

	r := h.run("events", "show", worshipNightID)

	require.Equal(t, 0, r.code, r.stdout)
	env := decodeEnvelope(t, r.stdout)
	require.JSONEq(t, string(golden(t, "event.json")), string(env.Data))
	require.Equal(t, `Event "Worship Night".`, env.Summary)
	require.Equal(t, 1, countMethod(cal, "GET", "/events/"+worshipNightID))
	require.Zero(t, countMethod(cal, "GET", "/events?"))
}

func TestEventsShowHuman(t *testing.T) {
	cal := newFakeCalendar(t)
	installEvents(t, cal, golden(t, "events.json"))
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")
	h.outTTY = true
	h.env["NO_COLOR"] = "1"

	r := h.run("events", "show", worshipNightID)

	require.Equal(t, 0, r.code, r.stderr)
	require.Equal(t, ""+
		"Title        Worship Night\n"+
		"Date         Sun Oct 25, 2099\n"+
		"Time         6:00pm–7:30pm\n"+
		"Repeats      Occurs weekly on Sunday until November 15, 2099\n"+
		"Location     Main Sanctuary\n"+
		"Address      100 Church St, Springfield, IL 62701\n"+
		"Map          https://maps.app.goo.gl/worshipnight\n"+
		"Description  An evening of worship and prayer\n"+
		"Notes        Band arrives at 5\n"+
		"Tags         Worship, Outreach\n"+
		"Flags        web, rsvp\n"+
		"RSVPs        2\n"+
		"ID           2c8f0476-9289-5844-9a29-c7fb20a9f3b3\n", r.stdout)
}

func TestEventsShowOmitsRepeatsWhenThereAreNone(t *testing.T) {
	cal := newFakeCalendar(t)
	body := golden(t, "event.json")
	var event map[string]any
	require.NoError(t, json.Unmarshal(body, &event))
	event["recurrence_text"] = nil
	event["recurrence_rule"] = nil
	encoded := mustJSON(t, event)
	const plainID = "11111111-1111-1111-1111-111111111111"
	cal.route("GET /events/"+plainID, cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(encoded)
	}))
	h := eventsHarness(t, cal)
	pinChicago(h, 2099, time.October, 25)
	cacheZone(t, h, "America/Chicago")
	h.outTTY = true
	h.env["NO_COLOR"] = "1"

	r := h.run("events", "show", plainID)

	require.Equal(t, 0, r.code, r.stderr)
	require.NotContains(t, r.stdout, "Repeats")
	require.Contains(t, r.stdout, "Sun Oct 25")
	require.NotContains(t, r.stdout, "2099")
}

func TestEventsShowNotFound(t *testing.T) {
	cal := newFakeCalendar(t)
	cal.route("GET /events/00000000-0000-0000-0000-000000000000", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		cal.serve(w, http.StatusNotFound, "error_not_found.json")
	}))
	h := eventsHarness(t, cal)
	cacheZone(t, h, "America/Chicago")

	r := h.run("events", "show", "00000000-0000-0000-0000-000000000000")

	require.Equal(t, 2, r.code)
	require.Equal(t, "not_found", decodeError(t, r.stdout).Error.Message)
	require.Zero(t, countMethod(cal, "GET", "/events?"))
}

func TestMatchEvent(t *testing.T) {
	worship := eventRef{id: "w", title: "Worship", firstDate: "2099-10-25"}
	night := eventRef{id: "n", title: "Worship Night", firstDate: "2099-11-01"}
	other := eventRef{id: "o", title: "Outreach", firstDate: "2099-10-01"}
	dupA := eventRef{id: "a", title: "Prayer", firstDate: "2099-01-01"}
	dupB := eventRef{id: "b", title: "prayer", firstDate: "2099-02-01"}

	tests := []struct {
		name    string
		arg     string
		refs    []eventRef
		wantID  string
		wantErr string
	}{
		{"exact beats a prefix", "Worship", []eventRef{night, worship}, "w", ""},
		{"exact is case-insensitive", "worship night", []eventRef{night, worship}, "n", ""},
		{"duplicate exact titles", "Prayer", []eventRef{dupA, dupB}, "", "ambiguous"},
		{"unique prefix", "Worship N", []eventRef{night, worship, other}, "n", ""},
		{"prefix is case-insensitive", "out", []eventRef{night, other}, "o", ""},
		{"several prefixes", "W", []eventRef{worship, night}, "", "ambiguous"},
		{"anywhere is not a prefix", "Night", []eventRef{night}, "", ""},
		{"none", "Missing", []eventRef{night}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found, ok, err := matchEvent(tt.arg, tt.refs)
			switch tt.wantErr {
			case "":
				require.NoError(t, err)
				if tt.wantID == "" {
					require.False(t, ok)
					return
				}
				require.True(t, ok)
				require.Equal(t, tt.wantID, found.id)
			case "ambiguous":
				require.Error(t, err)
				require.False(t, ok)
				require.Contains(t, err.Error(), "matches more than one")
			}
		})
	}
}

func TestEventsShowResolvesATitle(t *testing.T) {
	cal := newFakeCalendar(t)
	night := occ(worshipNightID, "Worship Night", "2099-11-01")
	worship := occ("worship-id", "Worship", "2099-10-01")
	installEvents(t, cal, mustJSON(t, []any{night, worship}))
	cal.route("GET /events/worship-id", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := golden(t, "event.json")
		var event map[string]any
		require.NoError(t, json.Unmarshal(body, &event))
		event["id"] = "worship-id"
		event["title"] = "Worship"
		encoded := mustJSON(t, event)
		_, _ = w.Write(encoded)
	}))
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")

	r := h.run("events", "show", "Worship")

	require.Equal(t, 0, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	require.Contains(t, decodeEnvelope(t, r.stdout).Summary, `"Worship"`)
	q := eventsListQuery(t, cal)
	require.Equal(t, "Worship", q.Get("q"))
	require.Equal(t, "2025-11-02", q.Get("start"))
	require.Equal(t, "2028-11-02", q.Get("end"))
	require.Equal(t, 1, countMethod(cal, "GET", "/events/worship-id"))
	require.Zero(t, countMethod(cal, "GET", "/events/"+worshipNightID))
}

func TestEventsShowDuplicateExactTitles(t *testing.T) {
	cal := newFakeCalendar(t)
	// The later occurrence is listed first. The hint uses the earliest date.
	first := occ("a", "Prayer", "2099-12-01")
	second := occ("b", "Prayer", "2099-03-01")
	again := occ("a", "Prayer", "2099-01-15")
	installEvents(t, cal, mustJSON(t, []any{first, second, again}))
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")

	r := h.run("events", "show", "prayer")

	require.Equal(t, 8, r.code, r.stdout)
	env := decodeError(t, r.stdout)
	require.Equal(t, "ambiguous", env.Error.Code)
	require.Contains(t, env.Error.Hint, `"Prayer" (a, 2099-01-15)`)
	require.Contains(t, env.Error.Hint, `"Prayer" (b, 2099-03-01)`)
	require.Zero(t, countMethod(cal, "GET", "/events/a"))
}

func TestEventsShowTitleNotFound(t *testing.T) {
	cal := newFakeCalendar(t)
	installEvents(t, cal, mustJSON(t, []any{occ(worshipNightID, "Worship Night", "2099-11-01")}))
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")

	r := h.run("events", "show", "Night")

	require.Equal(t, 2, r.code, r.stdout)
	env := decodeError(t, r.stdout)
	require.Equal(t, "not_found", env.Error.Code)
	require.Contains(t, env.Error.Message, `no event "Night"`)
	require.Equal(t, eventNotFoundHintWant, env.Error.Hint)
	require.NotContains(t, r.stdout+r.stderr, "--search")
	require.NotContains(t, env.Error.Hint, "shows it")
	require.Zero(t, countMethod(cal, "GET", "/events/"+worshipNightID))
}

func TestEventsShowDoesntRepeatATokenLikeTitle(t *testing.T) {
	cal := newFakeCalendar(t)
	installEvents(t, cal, []byte("[]"))
	h := eventsHarness(t, cal)
	pinChicago(h, 2026, time.November, 2)
	cacheZone(t, h, "America/Chicago")

	r := h.run("events", "show", "pcal_secretvalue")

	require.Equal(t, 2, r.code)
	require.NotContains(t, r.stdout+r.stderr, "pcal_secretvalue")
	require.Equal(t, eventNotFoundHintWant, decodeError(t, r.stdout).Error.Hint)
	require.NotContains(t, decodeError(t, r.stdout).Error.Hint, "--search")
}

// The plan says the README and the skill document the server's 365
// occurrence cap and tell the reader an old series needs its UUID.
func TestReadmeAndSkillDocumentTheOccurrenceCap(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	readme, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "README.md"))
	require.NoError(t, err)
	text := string(readme)
	require.Contains(t, text, "365")
	require.Contains(t, text, "UUID")
	require.NotContains(t, text, "events list --search")
	require.Contains(t, text, eventNotFoundHintWant)

	skill := string(skills.Skill)
	require.Contains(t, skill, "365")
	require.Contains(t, skill, "old daily series needs the UUID")
}

func TestDedupeEventsKeepsTheEarliestDate(t *testing.T) {
	refs := dedupeEvents([]client.Occurrence{
		{Event: client.Event{ID: "a", Title: "A"}, OccurrenceDate: "2099-12-01"},
		{Event: client.Event{ID: "a", Title: "A"}, OccurrenceDate: "2099-01-02"},
		{Event: client.Event{ID: "a", Title: "A"}, OccurrenceDate: ""},
	})
	require.Equal(t, []eventRef{{id: "a", title: "A", firstDate: "2099-01-02"}}, refs)
}
