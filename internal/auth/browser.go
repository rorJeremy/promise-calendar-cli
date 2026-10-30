package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rorJeremy/promise-calendar-cli/internal/client"
	"github.com/rorJeremy/promise-calendar-cli/internal/output"
)

// The browser login is OAuth 2.0's authorization code flow with PKCE (RFC
// 7636, S256) and a loopback redirect, as the calendar app implements it
// (docs/api.md → "Browser login (for clients)"; app 85a00a0,
// Cli::AuthorizationsController, Cli::TokensController, CliAuthorization).

// PKCE is one browser login's secrets: the code verifier and its S256
// challenge (RFC 7636), and the state that ties the callback to this login.
// The verifier never leaves this host, so a code is useless to anyone who
// sees it on the way.
type PKCE struct {
	Verifier  string
	Challenge string
	State     string
}

// NewPKCE returns fresh login secrets: a verifier and a state of 32 random
// bytes each, as unpadded base64url (43 characters, inside the server's
// VERIFIER_FORMAT and STATE_FORMAT), and the verifier's challenge.
func NewPKCE() (PKCE, error) {
	verifier, err := randomString()
	if err != nil {
		return PKCE{}, err
	}
	state, err := randomString()
	if err != nil {
		return PKCE{}, err
	}
	return PKCE{Verifier: verifier, Challenge: Challenge(verifier), State: state}, nil
}

// randomString returns 32 random bytes as unpadded base64url.
func randomString() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating login secrets: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Challenge returns the S256 challenge for verifier:
// base64url(SHA-256(verifier)) without padding, 43 characters.
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// AuthorizationURL returns the calendar page that asks the person to
// approve pcal on device with permission ("read" or "write"), and sends the
// browser back to 127.0.0.1:port. The server answers bad parameters with a
// 400 page, never a redirect, so only valid ones are sent.
func AuthorizationURL(baseURL string, port int, p PKCE, device, permission string) string {
	query := url.Values{
		"port":           {strconv.Itoa(port)},
		"state":          {p.State},
		"code_challenge": {p.Challenge},
		"device":         {device},
		"permission":     {permission},
	}
	return baseURL + "/cli/authorization/new?" + query.Encode()
}

// CheckPort returns a usage error for a port the calendar won't send the
// browser back to (it accepts 1024 to 65535). Every OS's default ephemeral
// range is inside that, but the range can be configured. The paste flow
// picks its port from the same range, so the hint offers a token login,
// which needs no port.
func CheckPort(port int) error {
	if port >= 1024 && port <= 65535 {
		return nil
	}
	return &output.Error{
		Code:    output.CodeUsage,
		Message: fmt.Sprintf("pcal's login listener got port %d, and the calendar only sends the browser back to ports 1024 to 65535", port),
		Hint:    "Check this computer's ephemeral port range, or log in with a personal access token instead: `pcal login --with-token`.",
	}
}

// ErrDenied is in the error for a login the person canceled in the
// browser (error=access_denied).
var ErrDenied = errors.New("access_denied")

// ErrNotACallback means pasted text isn't a callback address: it has
// neither a code nor an error.
var ErrNotACallback = errors.New("not a callback address")

// loginAgain is the hint for a login that has to start over.
const loginAgain = "Run `pcal login` again."

// loginError returns an auth error (exit 3) for a login that failed.
func loginError(message string, cause error) error {
	return &output.Error{Code: output.CodeAuth, Message: message, Hint: loginAgain, Cause: cause}
}

// ParseCallback returns the code from the query of a callback to this
// login, whose state is state. The state is checked first, so an answer
// meant for another login is a mismatch, whatever it says. Errors are auth
// errors (exit 3); a denial wraps ErrDenied.
func ParseCallback(query url.Values, state string) (string, error) {
	if query.Get("state") != state {
		// The expected state is never printed: it's half of what a forged
		// callback would need.
		return "", loginError("the browser's answer didn't match this login", nil)
	}
	switch errParam := query.Get("error"); {
	case errParam == "access_denied":
		return "", loginError("authorization denied in the browser", ErrDenied)
	case errParam != "":
		return "", loginError(fmt.Sprintf("the calendar refused the login: %q", errParam), nil)
	}
	code := query.Get("code")
	if code == "" {
		return "", loginError("the browser's answer had no login code", nil)
	}
	return code, nil
}

// ParsePastedCallback returns the code from the address a person pasted
// in the paste-the-callback flow: the whole address, or just its query,
// with any spaces or quotes around it. Text with neither a code nor an
// error is ErrNotACallback; otherwise it's ParseCallback's answer.
func ParsePastedCallback(text, state string) (string, error) {
	text = strings.Trim(strings.TrimSpace(text), "\"'`")
	u, err := url.Parse(strings.TrimSpace(text))
	if err != nil {
		return "", ErrNotACallback
	}
	query := u.Query()
	if !query.Has("code") && !query.Has("error") {
		return "", ErrNotACallback
	}
	return ParseCallback(query, state)
}

// page returns the small HTML page the browser shows after the calendar
// sends it back to pcal: a heading and a paragraph, self-contained, since
// it's served from 127.0.0.1.
func page(heading, text string) string {
	return "<!doctype html>\n<html lang=\"en\">\n<meta charset=\"utf-8\">\n<title>pcal login</title>\n" +
		"<h1>" + html.EscapeString(heading) + "</h1>\n<p>" + html.EscapeString(text) + "</p>\n</html>\n"
}

// sentence capitalizes message, one of ParseCallback's, and ends it with a
// period.
func sentence(message string) string {
	return strings.ToUpper(message[:1]) + message[1:] + "."
}

// callbackResult is what the first callback said.
type callbackResult struct {
	code string
	err  error
}

// CallbackServer is the loopback listener the calendar sends the browser
// back to, at http://127.0.0.1:<port>/callback. Only the first callback
// with this login's state counts; later ones get a page saying the login
// already finished, and one with any other state is answered and ignored.
type CallbackServer struct {
	listener net.Listener
	server   *http.Server
	state    string
	once     sync.Once
	result   chan callbackResult
}

// ListenForCallback starts a CallbackServer on 127.0.0.1 and a port the OS
// picks, for the login whose state is state.
func ListenForCallback(state string) (*CallbackServer, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("starting the login listener on 127.0.0.1: %w", err)
	}
	s := &CallbackServer{listener: l, state: state, result: make(chan callbackResult, 1)}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", s.handleCallback)
	s.server = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = s.server.Serve(l) }()
	return s, nil
}

// handleCallback takes the first callback's answer and shows the browser a
// page about it. The page is written before Wait hears the answer, and
// Close lets it finish, so the browser always gets it.
func (s *CallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	if r.URL.Query().Get("state") != s.state {
		// Only the calendar's redirect carries this login's state, so a
		// callback without it, such as a stray request from another process
		// on this computer, doesn't end the login (Jeremy's Gate 3c
		// decision; a deliberate deviation from basecamp-cli, whose first
		// callback decides, internal/auth/callback.go:98-104). The paste
		// flow still fails at once on a wrong state: the person pasted it.
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, page("pcal is waiting for a different login", "This answer is for another login, so pcal ignored it. Check your terminal."))
		return
	}

	first := false
	var res callbackResult
	s.once.Do(func() {
		first = true
		res.code, res.err = ParseCallback(r.URL.Query(), s.state)
	})

	switch {
	case !first:
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, page("This login already finished", "You can close this tab. Check your terminal."))
		return
	case res.err == nil:
		_, _ = fmt.Fprint(w, page("pcal got your approval", "You can close this tab and go back to your terminal."))
	case errors.Is(res.err, ErrDenied):
		_, _ = fmt.Fprint(w, page("You canceled the login", "pcal wasn't given access. You can close this tab."))
	default:
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, page("pcal couldn't finish logging in", sentence(res.err.Error())+" Check your terminal."))
	}
	s.result <- res
}

// Port returns the port the server listens on.
func (s *CallbackServer) Port() int {
	return s.listener.Addr().(*net.TCPAddr).Port
}

// Wait returns the code from the first callback, or its error, or ctx's
// error if ctx ends first.
func (s *CallbackServer) Wait(ctx context.Context) (string, error) {
	select {
	case res := <-s.result:
		return res.code, res.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Close stops the server, giving a page still being written a moment to
// finish.
func (s *CallbackServer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.server.Shutdown(ctx)
}

// Exchange redeems code and verifier for a token with POST /cli/token, a
// flat body and no authentication. Every attempt uses the code up, so a 400
// (invalid_grant: unknown, expired, reused, or the wrong verifier) means
// starting over, exit 3. A 429 (more than 10 a minute from one address) is
// exit 5, with no automatic retry. Anything else is the client's error.
func Exchange(ctx context.Context, c *client.Client, code, verifier string) (client.CLIToken, error) {
	var tok client.CLIToken
	err := c.Post(ctx, "/cli/token", map[string]string{"code": code, "code_verifier": verifier}, &tok)
	var e *output.Error
	if errors.As(err, &e) {
		switch e.Code {
		case output.CodeInvalidInput:
			return client.CLIToken{}, &output.Error{Code: output.CodeAuth, Message: "that login code expired or was already used", Hint: loginAgain, Cause: err}
		case output.CodeRateLimit:
			return client.CLIToken{}, &output.Error{Code: output.CodeRateLimit, Message: "too many login attempts", Hint: "Wait a minute, then run `pcal login` again.", Cause: err}
		}
	}
	if err != nil {
		return client.CLIToken{}, err
	}
	return tok, nil
}
