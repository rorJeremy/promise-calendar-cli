package output

import (
	"errors"
	"fmt"
)

// Exit codes. They're part of pcal's public contract: scripts and agents
// branch on them. The numbers are fizzy-cli's, except that pcal also exits 1
// for a 400 or a 422 ("invalid input"), where fizzy-cli exits 7.
const (
	ExitOK        = 0 // success
	ExitUsage     = 1 // bad flags or arguments, or input the server rejected (400, 422)
	ExitNotFound  = 2 // 404, or no event matches a title
	ExitAuth      = 3 // not logged in, or the token was rejected (401)
	ExitForbidden = 4 // the token is read-only (403)
	ExitRateLimit = 5 // too many requests (429)
	ExitNetwork   = 6 // the server couldn't be reached, or timed out
	ExitAPI       = 7 // a server error (5xx) or any other unexpected failure
	ExitAmbiguous = 8 // a title matches more than one event
)

// Error codes, the "code" of the JSON error envelope. Each maps to one exit
// code (see ExitCodeFor). Where fizzy-cli has the same kind of error, the
// code is fizzy-cli's.
const (
	CodeUsage        = "usage"         // pcal rejected the flags or arguments
	CodeInvalidInput = "invalid_input" // the server answered 400
	CodeValidation   = "validation"    // the server answered 422; see Error.Fields
	CodeNotFound     = "not_found"
	CodeAuth         = "auth_required"
	CodeForbidden    = "forbidden"
	CodeRateLimit    = "rate_limit"
	CodeNetwork      = "network"
	CodeAPI          = "api_error"
	CodeAmbiguous    = "ambiguous"
)

// ExitCodeFor returns the exit code for an error code. An unknown code is
// an API error.
func ExitCodeFor(code string) int {
	switch code {
	case CodeUsage, CodeInvalidInput, CodeValidation:
		return ExitUsage
	case CodeNotFound:
		return ExitNotFound
	case CodeAuth:
		return ExitAuth
	case CodeForbidden:
		return ExitForbidden
	case CodeRateLimit:
		return ExitRateLimit
	case CodeNetwork:
		return ExitNetwork
	case CodeAmbiguous:
		return ExitAmbiguous
	default:
		return ExitAPI
	}
}

// Error is a failure pcal reports to the user: as the JSON error envelope
// {"ok":false,"error":{"code","message","fields"}} for scripts, or as text on
// stderr for people. Its JSON form is the envelope's "error" object.
type Error struct {
	// Code says what kind of failure this is, and picks the exit code.
	Code string `json:"code"`
	// Message is one line saying what went wrong.
	Message string `json:"message"`
	// Fields holds a 422's messages per attribute, e.g.
	// {"title": ["can't be blank"]}.
	Fields map[string][]string `json:"fields,omitempty"`
	// Hint tells a person what to do next. It's printed in human output
	// only, so the envelope keeps the design's code/message/fields shape.
	Hint string `json:"-"`
	// Cause is the underlying error, if any.
	Cause error `json:"-"`
}

// Error returns the message.
func (e *Error) Error() string {
	return e.Message
}

// Unwrap returns the underlying error, so errors.Is and errors.As see it.
func (e *Error) Unwrap() error {
	return e.Cause
}

// ExitCode returns the process exit code for this error.
func (e *Error) ExitCode() int {
	return ExitCodeFor(e.Code)
}

// Usagef returns a usage error (exit 1) with a formatted message.
func Usagef(format string, args ...any) *Error {
	return &Error{Code: CodeUsage, Message: fmt.Sprintf(format, args...)}
}

// AsError returns err as an *Error: itself if it is one (or wraps one),
// otherwise an API error (exit 7) carrying its message.
func AsError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: CodeAPI, Message: err.Error(), Cause: err}
}
