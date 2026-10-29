package client

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/rorJeremy/promise-calendar-cli/internal/output"
	"github.com/rorJeremy/promise-calendar-cli/internal/version"
)

const testToken = "test-token-0000"

// request is what the fake server saw.
type request struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   []byte
}

// fakeServer answers every request with one canned response and records
// the last request it got.
type fakeServer struct {
	*httptest.Server
	mu   sync.Mutex
	last request
}

// newFakeServer starts a server answering with status, body, and headers.
func newFakeServer(t *testing.T, status int, body string, headers map[string]string) *fakeServer {
	t.Helper()
	f := &fakeServer{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.last = request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header.Clone(), Body: data}
		f.mu.Unlock()
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(f.Close)
	return f
}

// lastRequest returns the last request the server got.
func (f *fakeServer) lastRequest() request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.last
}

// requireError asserts err is an *output.Error with code and message.
func requireError(t *testing.T, err error, code, message string) *output.Error {
	t.Helper()
	var e *output.Error
	require.ErrorAs(t, err, &e)
	require.Equal(t, code, e.Code, "message: %s", e.Message)
	if message != "" {
		require.Equal(t, message, e.Message)
	}
	return e
}

type tag struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func TestRequestHeaders(t *testing.T) {
	tests := []struct {
		name            string
		call            func(c *Client) error
		wantMethod      string
		wantQuery       string
		wantContentType string
	}{
		{
			name: "GET with a query",
			call: func(c *Client) error {
				return c.Get(t.Context(), "/events", url.Values{"start": {"2026-10-01"}, "tag_ids[]": {"a", "b"}}, nil)
			},
			wantMethod: "GET",
			wantQuery:  "start=2026-10-01&tag_ids%5B%5D=a&tag_ids%5B%5D=b",
		},
		{
			name: "POST with a JSON body",
			call: func(c *Client) error {
				return c.Post(t.Context(), "/events", Body("event", map[string]any{"title": "x"}), nil)
			},
			wantMethod:      "POST",
			wantContentType: "application/json",
		},
		{
			name: "PATCH with a JSON body",
			call: func(c *Client) error {
				return c.Patch(t.Context(), "/events", Body("event", map[string]any{"title": "x"}), nil)
			},
			wantMethod:      "PATCH",
			wantContentType: "application/json",
		},
		{
			name:       "DELETE without a body",
			call:       func(c *Client) error { return c.Delete(t.Context(), "/events", nil) },
			wantMethod: "DELETE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFakeServer(t, http.StatusOK, `{}`, nil)
			c := New(srv.URL, testToken, Options{})

			require.NoError(t, tt.call(c))

			got := srv.lastRequest()
			require.Equal(t, tt.wantMethod, got.Method)
			require.Equal(t, "/events", got.Path)
			require.Equal(t, tt.wantQuery, got.Query)
			require.Equal(t, "Bearer "+testToken, got.Header.Get("Authorization"))
			// Always: a bearer token on a non-JSON request gets a plain-text 401.
			require.Equal(t, "application/json", got.Header.Get("Accept"))
			require.Equal(t, version.UserAgent(), got.Header.Get("User-Agent"))
			require.Equal(t, tt.wantContentType, got.Header.Get("Content-Type"))
		})
	}
}

func TestNoTokenSendsNoAuthorization(t *testing.T) {
	srv := newFakeServer(t, http.StatusCreated, `{}`, nil)

	require.NoError(t, New(srv.URL, "", Options{}).Post(t.Context(), "/cli/token", map[string]string{"code": "c"}, nil))
	require.Empty(t, srv.lastRequest().Header.Values("Authorization"))
}

func TestBodiesAreSentAsGiven(t *testing.T) {
	tests := []struct {
		name string
		body any
		want string
	}{
		// docs/api.md → Requests: a flat body silently drops non-column
		// keys such as tag_ids.
		{"Body nests attributes under the resource", Body("event", map[string]any{"title": "Worship", "ends_on": nil, "tag_ids": []string{}}), `{"event":{"title":"Worship","ends_on":null,"tag_ids":[]}}`},
		// POST /cli/token is the one flat body.
		{"a flat body stays flat", map[string]string{"code": "abc", "code_verifier": "xyz"}, `{"code":"abc","code_verifier":"xyz"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFakeServer(t, http.StatusCreated, `{}`, nil)

			require.NoError(t, New(srv.URL, testToken, Options{}).Post(t.Context(), "/x", tt.body, nil))
			require.JSONEq(t, tt.want, string(srv.lastRequest().Body))
		})
	}
}

func TestDecodesResponsesLeniently(t *testing.T) {
	// A field added on the server must never break a released binary.
	srv := newFakeServer(t, http.StatusOK, `{"id":"t1","name":"Worship","added_later":true}`, nil)
	var got tag

	require.NoError(t, New(srv.URL, testToken, Options{}).Get(t.Context(), "/tags/t1", nil, &got))
	require.Equal(t, tag{ID: "t1", Name: "Worship"}, got)
}

func TestNoContent(t *testing.T) {
	srv := newFakeServer(t, http.StatusNoContent, "", nil)
	got := tag{ID: "unchanged"}

	require.NoError(t, New(srv.URL, testToken, Options{}).Delete(t.Context(), "/tags/t1", &got))
	require.Equal(t, tag{ID: "unchanged"}, got)
}

func TestSuccessBodyThatIsNotJSON(t *testing.T) {
	srv := newFakeServer(t, http.StatusOK, "<html>hello</html>", nil)
	var got tag

	err := New(srv.URL, testToken, Options{}).Get(t.Context(), "/tags", nil, &got)

	e := requireError(t, err, output.CodeAPI, "")
	require.Contains(t, e.Message, "unexpected response")
	require.Equal(t, output.ExitAPI, e.ExitCode())
}

// TestStatusErrors pins the exit code and hint for every status, decided
// by status alone. The body's error string is shown, and only words the
// message for read_only_token.
func TestStatusErrors(t *testing.T) {
	const serverHint = "The calendar had a problem. Try again later, or report it."

	tests := []struct {
		name     string
		status   int
		body     string
		headers  map[string]string
		wantCode string
		wantExit int
		wantMsg  string
		wantHint string
	}{
		{"400 from the app", 400, `{"error":"invalid_date"}`, nil, output.CodeInvalidInput, 1, "invalid_date", ""},
		{"400 from Rails", 400, `{"status":400,"error":"Bad Request"}`, nil, output.CodeInvalidInput, 1, "Bad Request", ""},
		{"401", 401, `{"error":"unauthorized"}`, map[string]string{"WWW-Authenticate": `Token realm="Application"`}, output.CodeAuth, 3, "unauthorized", "Run `pcal login`."},
		{"403 read-only token", 403, `{"error":"read_only_token"}`, nil, output.CodeForbidden, 4, "token is read-only", "Run `pcal login` (without --read-only) for a token that can make changes."},
		{"403 for another reason", 403, `{"error":"forbidden"}`, nil, output.CodeForbidden, 4, "forbidden", ""},
		{"404 from the app", 404, `{"error":"not_found"}`, nil, output.CodeNotFound, 2, "not_found", ""},
		{"404 from Rails (no such route)", 404, `{"status":404,"error":"Not Found"}`, nil, output.CodeNotFound, 2, "Not Found", ""},
		{"406 from Rails", 406, `{"status":406,"error":"Not Acceptable"}`, nil, output.CodeAPI, 7, "Not Acceptable", ""},
		{"406 with an empty body", 406, "", nil, output.CodeAPI, 7, "Not Acceptable", ""},
		{"422", 422, `{"errors":{"title":["can't be blank"],"occurs_on":["can't be blank"]}}`, nil, output.CodeValidation, 1, "Title can't be blank", ""},
		{"429", 429, `{"error":"rate_limited"}`, nil, output.CodeRateLimit, 5, "rate_limited", "Wait a minute, then try again."},
		{"500 from Rails", 500, `{"status":500,"error":"Internal Server Error"}`, nil, output.CodeAPI, 7, "Internal Server Error", serverHint},
		{"502 from a proxy, not JSON", 502, "<html><body>Bad Gateway</body></html>", nil, output.CodeAPI, 7, "Bad Gateway", serverHint},
		{"503 with an empty body", 503, "", nil, output.CodeAPI, 7, "Service Unavailable", serverHint},
		// Redirects aren't followed: a 301 or 302 would turn a POST into a
		// GET, and the app never redirects a JSON request it accepts.
		{"302", 302, "", map[string]string{"Location": "/session/new"}, output.CodeAPI, 7, "the server redirected to /session/new", "Check the calendar URL (--url or PCAL_URL)."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFakeServer(t, tt.status, tt.body, tt.headers)

			err := New(srv.URL, testToken, Options{}).Post(t.Context(), "/events", Body("event", map[string]any{}), nil)

			e := requireError(t, err, tt.wantCode, tt.wantMsg)
			require.Equal(t, tt.wantExit, e.ExitCode())
			require.Equal(t, tt.wantHint, e.Hint)
		})
	}
}

func TestValidationErrors(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantMsg    string
		wantFields map[string][]string
	}{
		{
			name:       "the golden error_validation.json",
			body:       `{"errors":{"title":["can't be blank"],"occurs_on":["can't be blank"]}}`,
			wantMsg:    "Title can't be blank",
			wantFields: map[string][]string{"title": {"can't be blank"}, "occurs_on": {"can't be blank"}},
		},
		{
			// One check of twelve fields over HTTP. Map order could still
			// pass it by chance; TestValidationKeepsBodyOrder can't be.
			name:    "the message comes from the first field in body order",
			body:    twelveFields,
			wantMsg: "Url a",
			wantFields: map[string][]string{
				"url": {"a"}, "title": {"b"}, "tag_ids": {"c"}, "starts_at": {"d"}, "recurrence_rule": {"e"}, "phone": {"f"},
				"occurs_on": {"g"}, "name": {"h"}, "location_name": {"i"}, "image_url": {"j"}, "ends_on": {"k"}, "color": {"l"},
			},
		},
		{
			name:       "a field with no messages is skipped",
			body:       `{"errors":{"title":[],"occurs_on":["can't be blank","is not a date"]}}`,
			wantMsg:    "Occurs on can't be blank",
			wantFields: map[string][]string{"title": {}, "occurs_on": {"can't be blank", "is not a date"}},
		},
		{
			name:       "base is the bare message",
			body:       `{"errors":{"base":["Something is wrong"]}}`,
			wantMsg:    "Something is wrong",
			wantFields: map[string][]string{"base": {"Something is wrong"}},
		},
		{
			name:       "an RSVP to an event without RSVPs",
			body:       `{"errors":{"event":["does not accept RSVPs"]}}`,
			wantMsg:    "Event does not accept RSVPs",
			wantFields: map[string][]string{"event": {"does not accept RSVPs"}},
		},
		{
			name:       "no fields at all",
			body:       `{"errors":{}}`,
			wantMsg:    "the server rejected the request",
			wantFields: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFakeServer(t, 422, tt.body, nil)

			err := New(srv.URL, testToken, Options{}).Patch(t.Context(), "/events/1", Body("event", map[string]any{}), nil)

			e := requireError(t, err, output.CodeValidation, tt.wantMsg)
			require.Equal(t, tt.wantFields, e.Fields)
			require.Equal(t, output.ExitUsage, e.ExitCode())
		})
	}
}

// twelveFields is a 422 body whose fields are in reverse alphabetical
// order, so the first in body order ("url") is last alphabetically.
const twelveFields = `{"errors":{"url":["a"],"title":["b"],"tag_ids":["c"],"starts_at":["d"],"recurrence_rule":["e"],"phone":["f"],` +
	`"occurs_on":["g"],"name":["h"],"location_name":["i"],"image_url":["j"],"ends_on":["k"],"color":["l"]}}`

// TestValidationKeepsBodyOrder pins that the message comes from the first
// field in body order. A Go map's range order is random, and a pick in map
// order still names "url" first in about one run in seven with twelve
// fields (measured: a mutant ranging over the map passed 28 of 200 runs).
// Each validationError call decodes a new map, so 100 calls leave no room
// for luck.
func TestValidationKeepsBodyOrder(t *testing.T) {
	order, _, err := decodeValidation([]byte(twelveFields))

	require.NoError(t, err)
	require.Equal(t, []string{"url", "title", "tag_ids", "starts_at", "recurrence_rule", "phone",
		"occurs_on", "name", "location_name", "image_url", "ends_on", "color"}, order)
	for range 100 {
		require.Equal(t, "Url a", validationError([]byte(twelveFields)).Message)
	}
}

func TestDecodeValidationRejectsOtherShapes(t *testing.T) {
	tests := []struct{ name, body string }{
		{"not JSON", "<html>"},
		{"no errors key", `{"error":"unprocessable"}`},
		{"errors is a list", `{"errors":["Title can't be blank"]}`},
		{"messages aren't a list", `{"errors":{"title":"can't be blank"}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := decodeValidation([]byte(tt.body))
			require.Error(t, err)
		})
	}
}

func TestValidationErrorWithAnUnexpectedBody(t *testing.T) {
	tests := []struct{ name, body, want string }{
		{"an error string is the message", `{"error":"unprocessable"}`, "unprocessable"},
		{"anything else gets a generic message", "<html>", "the server rejected the request"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := validationError([]byte(tt.body))

			require.Equal(t, output.CodeValidation, e.Code)
			require.Equal(t, tt.want, e.Message)
			require.Nil(t, e.Fields)
		})
	}
}

// TestHumanize pins attribute names to Rails' full_messages: each want is
// what activemodel 8.1.4's full_messages printed for errors.add(attr,
// "is bad") (the app has no locale overrides or inflection acronyms).
func TestHumanize(t *testing.T) {
	tests := []struct{ attr, want string }{
		{"title", "Title is bad"},
		{"occurs_on", "Occurs on is bad"},
		{"tag_ids", "Tag ids is bad"},
		{"image_url", "Image url is bad"},
		{"featured_image", "Featured image is bad"},
		{"event", "Event is bad"},
		{"occurrence_date", "Occurrence date is bad"},
		{"recurrence_rule", "Recurrence rule is bad"},
		{"ends_on", "Ends on is bad"},
		{"location.name", "Location name is bad"},
		{"event_id", "Event is bad"},
		{"location_map_url", "Location map url is bad"},
		{"base", "is bad"},
		{"location.base", "Location is bad"},
		{"a.b.base", "A b is bad"},
		{"items[0].name", "Items[0] name is bad"},
		{"_id", "Id is bad"},
		{"id", "Id is bad"},
		{"event_ID", "Event id is bad"},
		{"url", "Url is bad"},
		{"starts_at", "Starts at is bad"},
		{"ends_at", "Ends at is bad"},
		{"color", "Color is bad"},
		{"name", "Name is bad"},
		{"email", "Email is bad"},
		{"phone", "Phone is bad"},
		{"description", "Description is bad"},
	}

	for _, tt := range tests {
		t.Run(tt.attr, func(t *testing.T) {
			require.Equal(t, tt.want, fullMessage(tt.attr, "is bad"))
		})
	}
}

func TestNetworkError(t *testing.T) {
	srv := newFakeServer(t, http.StatusOK, `{}`, nil)
	srv.Close() // nothing listens there now

	err := New(srv.URL, testToken, Options{}).Get(t.Context(), "/events", nil, nil)

	e := requireError(t, err, output.CodeNetwork, "")
	require.Equal(t, output.ExitNetwork, e.ExitCode())
	require.Contains(t, e.Message, "couldn't reach "+srv.URL)
}

func TestTimeouts(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(slow.Close)
	c := New(slow.URL, testToken, Options{Timeout: 20 * time.Millisecond, UploadTimeout: 5 * time.Second})

	err := c.Get(t.Context(), "/events", nil, nil)
	e := requireError(t, err, output.CodeNetwork, "")
	require.Contains(t, e.Message, "timed out")

	// The same server is fast enough for an upload's longer timeout.
	require.NoError(t, c.Upload(t.Context(), "PATCH", "/events/1", strings.NewReader("x"), "text/plain", nil))
}

func TestDefaultTimeouts(t *testing.T) {
	c := New("https://calendar.promisechurch.io", testToken, Options{})

	require.Equal(t, 30*time.Second, c.jsonHTTP.Timeout)
	// http.Client.Timeout covers sending the body, and 10 MB over a slow
	// uplink takes longer than 30 seconds.
	require.Equal(t, 5*time.Minute, c.uploadHTTP.Timeout)
}

func TestUploadSendsMultipart(t *testing.T) {
	var got struct {
		contentType, accept, auth, alt, filename string
		file                                     []byte
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.contentType = r.Header.Get("Content-Type")
		got.accept = r.Header.Get("Accept")
		got.auth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err == nil {
			got.alt = r.FormValue("event[image_alt]")
			if f, h, err := r.FormFile("event[featured_image]"); err == nil {
				got.filename = h.Filename
				got.file, _ = io.ReadAll(f)
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"e1","name":"ok"}`)
	}))
	t.Cleanup(srv.Close)

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	require.NoError(t, form.WriteField("event[image_alt]", "A sunrise"))
	part, err := form.CreateFormFile("event[featured_image]", "sunrise.png")
	require.NoError(t, err)
	_, err = part.Write([]byte("PNG bytes"))
	require.NoError(t, err)
	require.NoError(t, form.Close())

	var out tag
	require.NoError(t, New(srv.URL, testToken, Options{}).Upload(t.Context(), "PATCH", "/events/e1", &body, form.FormDataContentType(), &out))

	require.True(t, strings.HasPrefix(got.contentType, "multipart/form-data; boundary="), got.contentType)
	require.Equal(t, "application/json", got.accept)
	require.Equal(t, "Bearer "+testToken, got.auth)
	require.Equal(t, "A sunrise", got.alt)
	require.Equal(t, "sunrise.png", got.filename)
	require.Equal(t, "PNG bytes", string(got.file))
	require.Equal(t, tag{ID: "e1", Name: "ok"}, out)
}

func TestVerbose(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   string
	}{
		{"success", 200, "< 200 OK\n"},
		{"error", 404, "< 404 Not Found\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newFakeServer(t, tt.status, `{"error":"not_found"}`, nil)
			var log bytes.Buffer

			_ = New(srv.URL, testToken, Options{Verbose: &log}).Get(t.Context(), "/events", url.Values{"q": {"Worship"}}, nil)

			require.Equal(t, "> GET "+srv.URL+"/events?q=Worship\n"+tt.want, log.String())
			require.NotContains(t, log.String(), testToken)
		})
	}
}

func TestBody(t *testing.T) {
	attrs := map[string]any{"name": "Worship"}

	require.Equal(t, map[string]any{"tag": attrs}, Body("tag", attrs))
}
