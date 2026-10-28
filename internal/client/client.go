// Package client talks to the calendar's Staff API (docs/api.md in the
// calendar app): JSON requests with a bearer token, multipart image
// uploads, and HTTP statuses mapped to *output.Error, so every failure
// carries its exit code.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rorJeremy/promise-calendar-cli/internal/output"
	"github.com/rorJeremy/promise-calendar-cli/internal/version"
)

const (
	// Timeout bounds a JSON request, from connecting to reading the response.
	Timeout = 30 * time.Second
	// UploadTimeout bounds an image upload. http.Client.Timeout covers
	// sending the body, and 10 MB over a slow uplink takes longer than 30
	// seconds.
	UploadTimeout = 5 * time.Minute
)

// Options configure a Client. The zero value is the production setup.
type Options struct {
	// Verbose, when set, gets "> METHOD URL" before each request and
	// "< STATUS text" after it (as in fizzy-cli). Headers are never logged.
	Verbose io.Writer
	// Timeout replaces the default Timeout, for tests.
	Timeout time.Duration
	// UploadTimeout replaces the default UploadTimeout, for tests.
	UploadTimeout time.Duration
}

// Client calls one calendar with one token.
type Client struct {
	baseURL    string
	token      string
	verbose    io.Writer
	jsonHTTP   *http.Client
	uploadHTTP *http.Client
}

// New returns a Client for baseURL (no trailing slash, as config.ResolveURL
// returns it). An empty token sends no Authorization header, as for
// POST /cli/token.
func New(baseURL, token string, opts Options) *Client {
	if opts.Timeout == 0 {
		opts.Timeout = Timeout
	}
	if opts.UploadTimeout == 0 {
		opts.UploadTimeout = UploadTimeout
	}
	return &Client{
		baseURL:    baseURL,
		token:      token,
		verbose:    opts.Verbose,
		jsonHTTP:   &http.Client{Timeout: opts.Timeout, CheckRedirect: noRedirects},
		uploadHTTP: &http.Client{Timeout: opts.UploadTimeout, CheckRedirect: noRedirects},
	}
}

// noRedirects stops the client from following redirects, so a 3xx comes
// back as an error. Following a 301 or 302 would turn a POST into a GET,
// and the app never redirects a JSON request it accepts.
func noRedirects(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// Body nests attrs under resource: {"event": {...}}. Every write endpoint
// takes this shape (docs/api.md → Requests); a flat body silently drops
// keys that aren't columns, such as tag_ids. POST /cli/token is the one
// exception: its body is flat.
func Body(resource string, attrs map[string]any) map[string]any {
	return map[string]any{resource: attrs}
}

// Get sends a GET with query and decodes the response into out (if not nil).
func (c *Client) Get(ctx context.Context, path string, query url.Values, out any) error {
	return c.sendJSON(ctx, http.MethodGet, path, query, nil, out)
}

// Post sends body as JSON and decodes the response into out (if not nil).
func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.sendJSON(ctx, http.MethodPost, path, nil, body, out)
}

// Patch sends body as JSON and decodes the response into out (if not nil).
func (c *Client) Patch(ctx context.Context, path string, body, out any) error {
	return c.sendJSON(ctx, http.MethodPatch, path, nil, body, out)
}

// Delete sends a DELETE and decodes any response into out (if not nil).
func (c *Client) Delete(ctx context.Context, path string, out any) error {
	return c.sendJSON(ctx, http.MethodDelete, path, nil, nil, out)
}

// Upload sends a body that's already encoded, such as a multipart form
// with an image, with the longer UploadTimeout.
func (c *Client) Upload(ctx context.Context, method, path string, body io.Reader, contentType string, out any) error {
	return c.send(ctx, c.uploadHTTP, method, path, nil, body, contentType, out)
}

// sendJSON encodes body (if not nil) as JSON and sends it.
func (c *Client) sendJSON(ctx context.Context, method, path string, query url.Values, body, out any) error {
	var reader io.Reader
	contentType := ""
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return &output.Error{Code: output.CodeAPI, Message: "encoding the request: " + err.Error(), Cause: err}
		}
		reader = bytes.NewReader(data)
		contentType = "application/json"
	}
	return c.send(ctx, c.jsonHTTP, method, path, query, reader, contentType, out)
}

// send makes one request and turns anything but a 2xx into an *output.Error.
func (c *Client) send(ctx context.Context, hc *http.Client, method, path string, query url.Values, body io.Reader, contentType string, out any) error {
	target := c.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return &output.Error{Code: output.CodeAPI, Message: "building the request: " + err.Error(), Cause: err}
	}
	// Always ask for JSON: a bearer token on a non-JSON request gets a
	// plain-text 401.
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", version.UserAgent())
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	c.logf("> %s %s\n", method, target)
	resp, err := hc.Do(req)
	if err != nil {
		return c.networkError(err, method, target, hc.Timeout)
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return c.networkError(err, method, target, hc.Timeout)
	}
	c.logf("< %d %s\n", resp.StatusCode, http.StatusText(resp.StatusCode))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return errorFor(resp.StatusCode, resp.Header, data)
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	// Lenient: unknown fields are ignored, so a server addition never breaks
	// a released binary. The golden tests are the strict ones.
	if err := json.Unmarshal(data, out); err != nil {
		return &output.Error{
			Code:    output.CodeAPI,
			Message: fmt.Sprintf("unexpected response from the server to %s %s: %v", method, path, err),
			Cause:   err,
		}
	}
	return nil
}

// logf writes to the verbose log, if there is one.
func (c *Client) logf(format string, args ...any) {
	if c.verbose != nil {
		_, _ = fmt.Fprintf(c.verbose, format, args...)
	}
}

// networkError reports a request that got no response: exit 6.
func (c *Client) networkError(err error, method, target string, timeout time.Duration) *output.Error {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &output.Error{
			Code:    output.CodeNetwork,
			Message: fmt.Sprintf("%s %s timed out after %s", method, target, timeout),
			Hint:    "Check your connection, then try again.",
			Cause:   err,
		}
	}
	cause := err
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		cause = urlErr.Err // its Error() repeats the method and URL
	}
	return &output.Error{
		Code:    output.CodeNetwork,
		Message: fmt.Sprintf("couldn't reach %s: %v", c.baseURL, cause),
		Hint:    "Check your connection and the calendar URL (--url or PCAL_URL).",
		Cause:   err,
	}
}

// errorFor maps a non-2xx response to an error. The exit code depends on
// the status alone (docs/api.md → Errors: "Clients decide by HTTP status").
// The body's error string is the message, worded only for read_only_token.
func errorFor(status int, header http.Header, body []byte) *output.Error {
	if status == http.StatusUnprocessableEntity {
		return validationError(body)
	}

	message := errorString(body)
	if message == "" {
		message = http.StatusText(status)
	}
	if message == "" {
		message = fmt.Sprintf("HTTP %d", status)
	}

	switch {
	case status >= 300 && status < 400:
		return &output.Error{
			Code:    output.CodeAPI,
			Message: "the server redirected to " + header.Get("Location"),
			Hint:    "Check the calendar URL (--url or PCAL_URL).",
		}
	case status == http.StatusBadRequest:
		return &output.Error{Code: output.CodeInvalidInput, Message: message}
	case status == http.StatusUnauthorized:
		return &output.Error{Code: output.CodeAuth, Message: message, Hint: "Run `pcal login`."}
	case status == http.StatusForbidden:
		e := &output.Error{Code: output.CodeForbidden, Message: message}
		if message == "read_only_token" {
			e.Message = "token is read-only"
			e.Hint = "Run `pcal login` (without --read-only) for a token that can make changes."
		}
		return e
	case status == http.StatusNotFound:
		return &output.Error{Code: output.CodeNotFound, Message: message}
	case status == http.StatusTooManyRequests:
		return &output.Error{Code: output.CodeRateLimit, Message: message, Hint: "Wait a minute, then try again."}
	case status >= 500:
		return &output.Error{Code: output.CodeAPI, Message: message, Hint: "The calendar had a problem. Try again later, or report it."}
	default:
		return &output.Error{Code: output.CodeAPI, Message: message}
	}
}

// errorString returns the "error" string of an error body, or "" when the
// body isn't JSON (a proxy's HTML page) or has none.
func errorString(body []byte) string {
	var e APIError
	if json.Unmarshal(body, &e) != nil {
		return ""
	}
	return e.Error
}

// validationError maps a 422 body to an error whose fields are the body's
// errors object and whose message is its first message, as Rails'
// full_messages words it ("Title can't be blank").
func validationError(body []byte) *output.Error {
	e := &output.Error{Code: output.CodeValidation, Message: "the server rejected the request"}

	order, fields, err := decodeValidation(body)
	if err != nil {
		if message := errorString(body); message != "" {
			e.Message = message
		}
		return e
	}
	if len(fields) > 0 {
		e.Fields = fields
	}
	for _, name := range order {
		if messages := fields[name]; len(messages) > 0 {
			e.Message = fullMessage(name, messages[0])
			break
		}
	}
	return e
}

// decodeValidation reads a 422 body's errors object, returning its
// attribute names in body order as well as the messages.
func decodeValidation(body []byte) ([]string, map[string][]string, error) {
	var top struct {
		Errors json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, nil, err
	}

	dec := json.NewDecoder(bytes.NewReader(top.Errors))
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, nil, errors.New("errors is not an object")
	}

	var order []string
	fields := map[string][]string{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		name, ok := tok.(string)
		if !ok {
			return nil, nil, errors.New("errors has a non-string key")
		}
		var messages []string
		if err := dec.Decode(&messages); err != nil {
			return nil, nil, err
		}
		order = append(order, name)
		fields[name] = messages
	}
	return order, fields, nil
}

// fullMessage words an attribute's message as ActiveModel's
// Error.full_message does (activemodel 8.1.4): "base" is the bare message,
// anything else is the humanized attribute, a space, and the message.
func fullMessage(attribute, message string) string {
	if attribute == "base" {
		return message
	}
	name := strings.ReplaceAll(strings.TrimSuffix(attribute, ".base"), ".", "_")
	return humanize(name) + " " + message
}

// humanize ports ActiveSupport's String#humanize (activesupport 8.1.4) for
// an app with no inflection rules or acronyms: underscores become spaces, a
// trailing "_id" is dropped, everything is lowercased, and the first letter
// is capitalized. "occurs_on" → "Occurs on", "event_id" → "Event".
func humanize(word string) string {
	result := strings.ReplaceAll(word, "_", " ")
	result = strings.TrimLeftFunc(result, unicode.IsSpace)
	if strings.HasSuffix(word, "_id") {
		result = strings.TrimSuffix(result, " id")
	}
	result = strings.ToLower(result)
	if first, size := utf8.DecodeRuneInString(result); unicode.IsLetter(first) {
		result = string(unicode.ToUpper(first)) + result[size:]
	}
	return result
}
