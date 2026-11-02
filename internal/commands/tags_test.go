package commands

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rorJeremy/promise-calendar-cli/internal/client"
)

// installTags makes the fake calendar serve body from GET /tags (the golden
// list, or a test's own) and answer PATCH and DELETE for each tag in it.
// POST /tags returns tag.json, the app's recorded tag.
func installTags(t *testing.T, cal *fakeCalendar, body []byte) []client.Tag {
	t.Helper()
	var tags []client.Tag
	require.NoError(t, json.Unmarshal(body, &tags))
	cal.route("GET /tags", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	for _, tag := range tags {
		payload, err := json.Marshal(tag)
		require.NoError(t, err)
		cal.route("PATCH /tags/"+tag.ID, cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload)
		}))
		cal.route("DELETE /tags/"+tag.ID, cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
	}
	cal.route("POST /tags", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		cal.serve(w, http.StatusCreated, "tag.json")
	}))
	return tags
}

func tagsHarness(t *testing.T, cal *fakeCalendar) *harness {
	t.Helper()
	h := newHarness(t, cal.URL())
	h.env["PCAL_TOKEN"] = goodToken
	return h
}

func findRequest(t *testing.T, cal *fakeCalendar, method, path string) request {
	t.Helper()
	for _, r := range cal.requests() {
		if r.Method == method && r.Path == path {
			return r
		}
	}
	t.Fatalf("no %s %s; got %#v", method, path, cal.requests())
	return request{}
}

func tagByName(t *testing.T, tags []client.Tag, name string) client.Tag {
	t.Helper()
	for _, tag := range tags {
		if tag.Name == name {
			return tag
		}
	}
	t.Fatalf("no tag %q", name)
	return client.Tag{}
}

// --- group ---

func TestTagsGroup(t *testing.T) {
	r := runPcal(t, "tags")

	require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
	require.Contains(t, r.stdout, "list")
	require.Contains(t, r.stdout, "create")
	require.Contains(t, r.stdout, "update")
	require.Contains(t, r.stdout, "delete")
}

func TestTagsRejectsUnknownSubcommands(t *testing.T) {
	r := runPcal(t, "tags", "frobnicate")

	require.Equal(t, 1, r.code)
	env := decodeError(t, r.stdout)
	require.Equal(t, "usage", env.Error.Code)
	require.Contains(t, env.Error.Message, `unknown command "frobnicate" for "pcal tags"`)
}

func TestTagsListRejectsArguments(t *testing.T) {
	r := runPcal(t, "tags", "list", "Worship")

	require.Equal(t, 1, r.code)
	require.Contains(t, decodeError(t, r.stdout).Error.Message, `unknown command "Worship" for "pcal tags list"`)
}

func TestTagsListDoesntRepeatATokenArgument(t *testing.T) {
	r := runPcal(t, "tags", "list", "pcal_secretvalue")

	require.Equal(t, 1, r.code)
	require.NotContains(t, r.stdout+r.stderr, "pcal_secretvalue")
	require.Contains(t, decodeError(t, r.stdout).Error.Message, "not shown: it looks like a token")
}

// --- list ---

func TestTagsList(t *testing.T) {
	cal := newFakeCalendar(t)
	installTags(t, cal, golden(t, "tags.json"))
	h := tagsHarness(t, cal)

	r := h.run("tags", "list")

	require.Equal(t, 0, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	env := decodeEnvelope(t, r.stdout)
	require.JSONEq(t, string(golden(t, "tags.json")), string(env.Data))
	require.Equal(t, "3 tags", env.Summary)
	require.Equal(t, "GET", findRequest(t, cal, "GET", "/tags").Method)
}

func TestTagsListKeepsTheServersOrder(t *testing.T) {
	cal := newFakeCalendar(t)
	body := []byte(`[
		{"id":"y","name":"Youth","color":"#33FF57","position":2},
		{"id":"w","name":"Worship","color":"#FF5733","position":1}
	]`)
	installTags(t, cal, body)
	h := tagsHarness(t, cal)

	r := h.run("tags", "list")

	require.Equal(t, 0, r.code, r.stdout)
	data := string(decodeEnvelope(t, r.stdout).Data)
	require.Less(t, strings.Index(data, "Youth"), strings.Index(data, "Worship"))
}

func TestTagsListEmpty(t *testing.T) {
	cal := newFakeCalendar(t)
	installTags(t, cal, []byte("[]"))
	h := tagsHarness(t, cal)

	r := h.run("tags", "list")

	require.Equal(t, 0, r.code, r.stdout)
	env := decodeEnvelope(t, r.stdout)
	require.JSONEq(t, "[]", string(env.Data))
	require.Equal(t, "0 tags", env.Summary)
}

func TestTagsListOne(t *testing.T) {
	cal := newFakeCalendar(t)
	installTags(t, cal, []byte(`[{"id":"w","name":"Worship","color":"#FF5733","position":1}]`))
	h := tagsHarness(t, cal)

	r := h.run("tags", "list", "--quiet")

	require.Equal(t, 0, r.code, r.stdout)
	require.Contains(t, r.stdout, `"name": "Worship"`)
	require.NotContains(t, r.stdout, `"ok"`)
}

func TestTagsListHuman(t *testing.T) {
	cal := newFakeCalendar(t)
	installTags(t, cal, golden(t, "tags.json"))
	h := tagsHarness(t, cal)
	h.outTTY = true
	h.env["NO_COLOR"] = "1"

	r := h.run("tags", "list")

	require.Equal(t, 0, r.code, r.stderr)
	require.Contains(t, r.stdout, "3 tags")
	require.Less(t, strings.Index(r.stdout, "Worship"), strings.Index(r.stdout, "Youth"))
	require.Less(t, strings.Index(r.stdout, "Youth"), strings.Index(r.stdout, "Outreach"))
	require.NotContains(t, r.stdout, "\x1b")
}

func TestTagsListCSV(t *testing.T) {
	cal := newFakeCalendar(t)
	installTags(t, cal, golden(t, "tags.json"))
	h := tagsHarness(t, cal)

	r := h.run("tags", "list", "--csv")

	require.Equal(t, 0, r.code, r.stdout)
	require.Equal(t, ""+
		"Name,Color,Position,ID\n"+
		"Worship,#FF5733,1,b04decba-b16e-53f6-bfe4-5c510c838caa\n"+
		"Youth,#33FF57,2,e10e7794-c9b4-511d-8a8e-114dec5f16c0\n"+
		"Outreach,#3357FF,3,a98b80ac-3e85-5e62-9124-8c5d6dcaf214\n", r.stdout)
}

func TestTagsListCSVGuardsFormulasAndEscapesControls(t *testing.T) {
	cal := newFakeCalendar(t)
	installTags(t, cal, []byte(`[{"id":"e","name":"=Kids\u001b","color":null,"position":null}]`))
	h := tagsHarness(t, cal)
	h.outTTY = true
	h.env["NO_COLOR"] = "1"

	csvRun := h.run("tags", "list", "--csv")
	h2 := tagsHarness(t, cal)
	h2.outTTY = true
	h2.env["NO_COLOR"] = "1"
	human := h2.run("tags", "list")

	require.Equal(t, 0, csvRun.code, csvRun.stdout)
	require.Contains(t, csvRun.stdout, `'=Kids`)
	require.Equal(t, 0, human.code, human.stdout)
	require.Contains(t, human.stdout, `\x1b`)
	require.NotContains(t, human.stdout, "\x1b")
}

func TestTagsListJQ(t *testing.T) {
	cal := newFakeCalendar(t)
	installTags(t, cal, golden(t, "tags.json"))
	h := tagsHarness(t, cal)

	r := h.run("tags", "list", "--jq", ".data[0].name")

	require.Equal(t, 0, r.code, r.stdout)
	require.Equal(t, "Worship\n", r.stdout)
}

func TestTagsListNotLoggedIn(t *testing.T) {
	cal := newFakeCalendar(t)
	installTags(t, cal, golden(t, "tags.json"))
	h := newHarness(t, cal.URL())

	r := h.run("tags", "list")

	require.Equal(t, 3, r.code)
	require.Equal(t, "auth_required", decodeError(t, r.stdout).Error.Code)
	require.Empty(t, cal.requests())
}

// --- create ---

func TestTagsCreate(t *testing.T) {
	cal := newFakeCalendar(t)
	installTags(t, cal, golden(t, "tags.json"))
	h := tagsHarness(t, cal)

	r := h.run("tags", "create", "Worship")

	require.Equal(t, 0, r.code, r.stdout)
	env := decodeEnvelope(t, r.stdout)
	require.JSONEq(t, string(golden(t, "tag.json")), string(env.Data))
	require.Equal(t, `Created tag "Worship".`, env.Summary)
	require.JSONEq(t, `{"tag":{"name":"Worship"}}`, findRequest(t, cal, "POST", "/tags").Body)
}

func TestTagsCreateWithColor(t *testing.T) {
	cal := newFakeCalendar(t)
	installTags(t, cal, golden(t, "tags.json"))
	h := tagsHarness(t, cal)

	r := h.run("tags", "create", "Missions", "--color", "#abcdef")

	require.Equal(t, 0, r.code, r.stdout)
	require.JSONEq(t, `{"tag":{"name":"Missions","color":"#abcdef"}}`, findRequest(t, cal, "POST", "/tags").Body)
}

func TestTagsCreateCard(t *testing.T) {
	cal := newFakeCalendar(t)
	installTags(t, cal, golden(t, "tags.json"))
	h := tagsHarness(t, cal)
	h.outTTY = true
	h.env["NO_COLOR"] = "1"

	r := h.run("tags", "create", "Worship")

	require.Equal(t, 0, r.code, r.stderr)
	require.Equal(t, ""+
		"Name      Worship\n"+
		"Color     #FF5733\n"+
		"Position  1\n"+
		"ID        b04decba-b16e-53f6-bfe4-5c510c838caa\n", r.stdout)
}

func TestTagsCreateValidation(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		message string
	}{
		{
			"name first, as the app validates it",
			`{"errors":{"name":["has already been taken"],"color":["is invalid"]}}`,
			"Name has already been taken",
		},
		{
			"color first, so the message follows the body",
			`{"errors":{"color":["is invalid"],"name":["has already been taken"]}}`,
			"Color is invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			installTags(t, cal, golden(t, "tags.json"))
			cal.route("POST /tags", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnprocessableEntity)
				_, _ = w.Write([]byte(tt.body))
			}))
			h := tagsHarness(t, cal)

			r := h.run("tags", "create", "Worship", "--color", "red")

			require.Equal(t, 1, r.code, r.stdout)
			env := decodeError(t, r.stdout)
			require.Equal(t, "validation", env.Error.Code)
			require.Equal(t, tt.message, env.Error.Message)
			require.Equal(t, []string{"has already been taken"}, env.Error.Fields["name"])
			require.Equal(t, []string{"is invalid"}, env.Error.Fields["color"])
		})
	}
}

func TestTagsCreateForbidden(t *testing.T) {
	cal := newFakeCalendar(t)
	installTags(t, cal, golden(t, "tags.json"))
	cal.route("POST /tags", cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		cal.serve(w, http.StatusForbidden, "error_read_only_token.json")
	}))
	h := tagsHarness(t, cal)

	r := h.run("tags", "create", "Missions")

	require.Equal(t, 4, r.code)
	env := decodeError(t, r.stdout)
	require.Equal(t, "forbidden", env.Error.Code)
	require.Equal(t, "token is read-only", env.Error.Message)
}

func TestTagsCreateNeedsAName(t *testing.T) {
	r := runPcal(t, "tags", "create")

	require.Equal(t, 1, r.code)
	require.Equal(t, "usage", decodeError(t, r.stdout).Error.Code)
}

// --- update ---

func TestTagsUpdateByNameAndID(t *testing.T) {
	cal := newFakeCalendar(t)
	tags := installTags(t, cal, golden(t, "tags.json"))
	worship := tagByName(t, tags, "Worship")
	h := tagsHarness(t, cal)

	tests := []struct {
		name string
		arg  string
	}{
		{"exact name", "Worship"},
		{"unique case-insensitive name", "worship"},
		{"id", worship.ID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal.log = nil
			r := h.run("tags", "update", tt.arg, "--color", "#000000")

			require.Equal(t, 0, r.code, r.stdout)
			require.JSONEq(t, `{"tag":{"color":"#000000"}}`, findRequest(t, cal, "PATCH", "/tags/"+worship.ID).Body)
			require.Equal(t, `Updated tag "Worship".`, decodeEnvelope(t, r.stdout).Summary)
		})
	}
}

func TestTagsUpdateSendsOnlyTheFlagsGiven(t *testing.T) {
	cal := newFakeCalendar(t)
	tags := installTags(t, cal, golden(t, "tags.json"))
	worship := tagByName(t, tags, "Worship")
	h := tagsHarness(t, cal)

	r := h.run("tags", "update", "Worship", "--name", "Gathering", "--color", "", "--position", "0")

	require.Equal(t, 0, r.code, r.stdout)
	require.JSONEq(t, `{"tag":{"name":"Gathering","color":"","position":0}}`, findRequest(t, cal, "PATCH", "/tags/"+worship.ID).Body)
}

func TestTagsUpdateWithNoFlagsTouchesNothingButTheEmptyObject(t *testing.T) {
	cal := newFakeCalendar(t)
	tags := installTags(t, cal, golden(t, "tags.json"))
	worship := tagByName(t, tags, "Worship")
	h := tagsHarness(t, cal)

	r := h.run("tags", "update", "Worship")

	require.Equal(t, 0, r.code, r.stdout)
	require.JSONEq(t, `{"tag":{}}`, findRequest(t, cal, "PATCH", "/tags/"+worship.ID).Body)
}

func TestTagsUpdateExactCaseWins(t *testing.T) {
	cal := newFakeCalendar(t)
	body := []byte(`[
		{"id":"upper","name":"Worship","color":"#FF5733","position":1},
		{"id":"lower","name":"worship","color":"#000000","position":2}
	]`)
	installTags(t, cal, body)
	h := tagsHarness(t, cal)

	r := h.run("tags", "update", "Worship", "--position", "4")

	require.Equal(t, 0, r.code, r.stdout)
	require.JSONEq(t, `{"tag":{"position":4}}`, findRequest(t, cal, "PATCH", "/tags/upper").Body)
}

func TestTagsUpdateAmbiguous(t *testing.T) {
	cal := newFakeCalendar(t)
	installTags(t, cal, []byte(`[
		{"id":"upper","name":"Worship","color":"#FF5733","position":1},
		{"id":"lower","name":"worship","color":"#000000","position":2}
	]`))
	h := tagsHarness(t, cal)

	r := h.run("tags", "update", "WORSHIP", "--name", "Nope")

	require.Equal(t, 8, r.code, r.stdout)
	env := decodeError(t, r.stdout)
	require.Equal(t, "ambiguous", env.Error.Code)
	require.Contains(t, env.Error.Message, "WORSHIP")
	require.Contains(t, env.Error.Hint, "Worship")
	require.Contains(t, env.Error.Hint, "worship")
	require.Contains(t, env.Error.Hint, "upper")
	require.Contains(t, env.Error.Hint, "lower")
	require.Contains(t, env.Error.Hint, "pcal tags list")
	for _, req := range cal.requests() {
		require.NotEqual(t, "PATCH", req.Method)
	}
}

func TestTagsUpdateNotFound(t *testing.T) {
	cal := newFakeCalendar(t)
	installTags(t, cal, golden(t, "tags.json"))
	h := tagsHarness(t, cal)

	r := h.run("tags", "update", "Kids")

	require.Equal(t, 2, r.code)
	env := decodeError(t, r.stdout)
	require.Equal(t, "not_found", env.Error.Code)
	require.Equal(t, `no tag "Kids"`, env.Error.Message)
	require.Equal(t, "Run `pcal tags list`.", env.Error.Hint)
}

func TestTagsUpdateNotFoundDoesNotRepeatAToken(t *testing.T) {
	cal := newFakeCalendar(t)
	installTags(t, cal, golden(t, "tags.json"))
	h := tagsHarness(t, cal)

	r := h.run("tags", "update", "pcal_secretvalue", "--name", "Nope")

	require.Equal(t, 2, r.code)
	require.NotContains(t, r.stdout+r.stderr, "pcal_secretvalue")
	require.Contains(t, decodeError(t, r.stdout).Error.Message, "not shown: it looks like a token")
}

func TestTagsUpdateValidationAndNotFoundOnTheServer(t *testing.T) {
	cal := newFakeCalendar(t)
	tags := installTags(t, cal, golden(t, "tags.json"))
	worship := tagByName(t, tags, "Worship")
	cal.route("PATCH /tags/"+worship.ID, cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"errors":{"name":["can't be blank"]}}`))
	}))
	h := tagsHarness(t, cal)

	blank := h.run("tags", "update", "Worship", "--name", "")
	require.Equal(t, 1, blank.code, blank.stdout)
	require.Equal(t, "Name can't be blank", decodeError(t, blank.stdout).Error.Message)

	cal.route("PATCH /tags/"+worship.ID, cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		cal.serve(w, http.StatusNotFound, "error_not_found.json")
	}))
	missing := h.run("tags", "update", "Worship", "--name", "Gathering")
	require.Equal(t, 2, missing.code)
	require.Equal(t, "not_found", decodeError(t, missing.stdout).Error.Code)
}

// --- delete ---

func TestTagsDeleteYes(t *testing.T) {
	cal := newFakeCalendar(t)
	tags := installTags(t, cal, golden(t, "tags.json"))
	worship := tagByName(t, tags, "Worship")
	h := tagsHarness(t, cal)

	r := h.run("tags", "delete", "worship", "--yes")

	require.Equal(t, 0, r.code, r.stdout)
	env := decodeEnvelope(t, r.stdout)
	require.Equal(t, `Deleted tag "Worship".`, env.Summary)
	require.JSONEq(t, `{"deleted":true}`, string(env.Data))
	require.Equal(t, "DELETE", findRequest(t, cal, "DELETE", "/tags/"+worship.ID).Method)
	require.Empty(t, r.stderr)
}

func TestTagsDeleteRefusesWithoutATerminalOrYes(t *testing.T) {
	tests := []struct {
		name   string
		inTTY  bool
		errTTY bool
	}{
		{"no terminal", false, false},
		{"stderr isn't a terminal", true, false},
		{"stdin isn't a terminal", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			installTags(t, cal, golden(t, "tags.json"))
			h := tagsHarness(t, cal)
			h.inTTY = tt.inTTY
			h.errTTY = tt.errTTY

			r := h.run("tags", "delete", "Worship")

			require.Equal(t, 1, r.code, r.stdout)
			env := decodeError(t, r.stdout)
			require.Equal(t, "usage", env.Error.Code)
			require.Contains(t, env.Error.Message, "--yes")
			require.Contains(t, env.Error.Hint, "--yes")
			require.Empty(t, cal.requests())
		})
	}
}

func TestTagsDeleteNoIsAResult(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"n", "n\n"},
		{"no", "no\n"},
		{"Enter", "\n"},
		{"Ctrl-D", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			installTags(t, cal, golden(t, "tags.json"))
			h := tagsHarness(t, cal)
			h.inTTY = true
			h.errTTY = true
			h.env["NO_COLOR"] = "1"
			h.stdin = strings.NewReader(tt.input)

			r := h.run("tags", "delete", "Worship")

			require.Equal(t, 0, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
			env := decodeEnvelope(t, r.stdout)
			require.Equal(t, "Nothing was deleted.", env.Summary)
			require.JSONEq(t, `{"deleted":false}`, string(env.Data))
			require.Contains(t, r.stderr, `Delete tag "Worship"? [y/N]`)
			require.NotContains(t, r.stderr, "\x1b")
			for _, req := range cal.requests() {
				require.NotEqual(t, "DELETE", req.Method)
			}
		})
	}
}

func TestTagsDeleteNoHuman(t *testing.T) {
	cal := newFakeCalendar(t)
	installTags(t, cal, golden(t, "tags.json"))
	h := tagsHarness(t, cal)
	h.inTTY = true
	h.errTTY = true
	h.outTTY = true
	h.env["NO_COLOR"] = "1"
	h.stdin = strings.NewReader("n\n")

	r := h.run("tags", "delete", "Youth")

	require.Equal(t, 0, r.code, r.stderr)
	require.Equal(t, "Nothing was deleted.\n", r.stdout)
}

func TestTagsDeleteYesAtThePrompt(t *testing.T) {
	cal := newFakeCalendar(t)
	tags := installTags(t, cal, golden(t, "tags.json"))
	youth := tagByName(t, tags, "Youth")
	h := tagsHarness(t, cal)
	h.inTTY = true
	h.errTTY = true
	h.env["TERM"] = "dumb"
	h.stdin = strings.NewReader("y\n")

	r := h.run("tags", "delete", "Youth")

	require.Equal(t, 0, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
	require.Equal(t, `Deleted tag "Youth".`, decodeEnvelope(t, r.stdout).Summary)
	require.Equal(t, "DELETE", findRequest(t, cal, "DELETE", "/tags/"+youth.ID).Method)
	require.Contains(t, r.stderr, `Delete tag "Youth"?`)
	require.NotContains(t, r.stderr, "\x1b")
}

func TestTagsDeleteDrawn(t *testing.T) {
	tests := []struct {
		name   string
		keys   string
		want   int
		delete bool
	}{
		{"y", "y", 0, true},
		{"Enter is No", "\r", 0, false},
		{"n", "n", 0, false},
		{"Esc", "\x1b", 130, false},
		{"Ctrl-C", "\x03", 130, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cal := newFakeCalendar(t)
			tags := installTags(t, cal, golden(t, "tags.json"))
			worship := tagByName(t, tags, "Worship")
			h := tagsHarness(t, cal)
			h.inTTY = true
			h.errTTY = true
			h.outTTY = true
			h.promptOptions = drawnPrompt
			h.stdin = strings.NewReader(tt.keys)

			r := h.run("tags", "delete", worship.ID)

			require.Equal(t, tt.want, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
			deleted := false
			for _, req := range cal.requests() {
				if req.Method == "DELETE" {
					deleted = true
					require.Equal(t, "/tags/"+worship.ID, req.Path)
				}
			}
			require.Equal(t, tt.delete, deleted)
			if tt.want == 130 {
				require.Empty(t, r.stdout)
				require.Contains(t, r.stderr, "Canceled. Nothing was deleted.")
				require.NotContains(t, r.stderr, "Login canceled")
			}
			if tt.want == 0 && !tt.delete {
				require.Contains(t, r.stdout, "Nothing was deleted.")
			}
			if tt.delete {
				require.Contains(t, r.stdout, `Deleted tag "Worship".`)
			}
		})
	}
}

// Esc on the accessible line cancels the delete: exit 130, the
// confirmation's own sentence, no envelope, and no DELETE. Enter, Ctrl-D,
// y, and n are covered above and stay as they are (Jeremy's Phase 9 review).
func TestTagsDeleteAccessibleEscCancels(t *testing.T) {
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
			installTags(t, cal, golden(t, "tags.json"))
			h := tagsHarness(t, cal)
			h.inTTY = true
			h.errTTY = true
			h.env[tt.key] = tt.val
			h.stdin = strings.NewReader("\x1b")

			r := h.run("tags", "delete", "Worship")

			require.Equal(t, 130, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
			require.Empty(t, r.stdout)
			require.Contains(t, r.stderr, "Canceled. Nothing was deleted.")
			require.NotContains(t, r.stderr, "invalid")
			require.NotContains(t, r.stdout+r.stderr, "Login canceled")
			require.NotContains(t, r.stdout+r.stderr, `"ok"`)
			for _, req := range cal.requests() {
				require.NotEqual(t, "DELETE", req.Method)
			}
		})
	}
}

func TestTagsDeleteCanceled(t *testing.T) {
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
			installTags(t, cal, golden(t, "tags.json"))
			h := tagsHarness(t, cal)
			h.inTTY = true
			h.errTTY = true
			h.env["NO_COLOR"] = "1"
			h.stdin = strings.NewReader("y\n")
			tt.stop(h)

			r := h.run("tags", "delete", "Worship")

			require.Equal(t, tt.code, r.code, "stdout: %s\nstderr: %s", r.stdout, r.stderr)
			require.Empty(t, r.stdout)
			require.Contains(t, r.stderr, "Canceled. Nothing was deleted.")
			require.NotContains(t, r.stdout+r.stderr, "Login canceled")
			require.NotContains(t, r.stdout+r.stderr, `"ok"`)
			for _, req := range cal.requests() {
				require.NotEqual(t, "DELETE", req.Method)
			}
		})
	}
}

func TestTagsDeleteForbidden(t *testing.T) {
	cal := newFakeCalendar(t)
	tags := installTags(t, cal, golden(t, "tags.json"))
	worship := tagByName(t, tags, "Worship")
	cal.route("DELETE /tags/"+worship.ID, cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		cal.serve(w, http.StatusForbidden, "error_read_only_token.json")
	}))
	h := tagsHarness(t, cal)

	r := h.run("tags", "delete", "Worship", "--yes")

	require.Equal(t, 4, r.code)
	require.Equal(t, "token is read-only", decodeError(t, r.stdout).Error.Message)
}

func TestTagsDeleteNotFoundOnTheServer(t *testing.T) {
	cal := newFakeCalendar(t)
	tags := installTags(t, cal, golden(t, "tags.json"))
	worship := tagByName(t, tags, "Worship")
	cal.route("DELETE /tags/"+worship.ID, cal.withToken([]string{goodToken}, func(w http.ResponseWriter, _ *http.Request) {
		cal.serve(w, http.StatusNotFound, "error_not_found.json")
	}))
	h := tagsHarness(t, cal)

	r := h.run("tags", "delete", worship.ID, "--yes")

	require.Equal(t, 2, r.code)
	require.Equal(t, "not_found", decodeError(t, r.stdout).Error.Code)
}
