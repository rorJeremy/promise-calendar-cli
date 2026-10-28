package client

// APIError is the body of most error responses, e.g. {"error":"not_found"}
// (golden error_*.json). Rails' own errors add a "status" member, which
// lenient decoding ignores. Show the string; never branch on it, except to
// word read_only_token.
type APIError struct {
	Error string `json:"error"`
}

// ValidationErrors is a 422 body, e.g. {"errors":{"title":["can't be
// blank"]}} (golden error_validation.json): messages per attribute, in the
// order the server validated them. A Go map loses that order, so the client
// reads it from the raw body to pick the first message.
type ValidationErrors struct {
	Errors map[string][]string `json:"errors"`
}
