package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestExitCodes pins pcal's exit codes, which scripts and agents branch on.
// The numbers are fizzy-cli's (internal/errors/errors_test.go#TestExitCodes).
func TestExitCodes(t *testing.T) {
	tests := []struct {
		name string
		got  int
		want int
	}{
		{"ExitOK", ExitOK, 0},
		{"ExitUsage", ExitUsage, 1},
		{"ExitNotFound", ExitNotFound, 2},
		{"ExitAuth", ExitAuth, 3},
		{"ExitForbidden", ExitForbidden, 4},
		{"ExitRateLimit", ExitRateLimit, 5},
		{"ExitNetwork", ExitNetwork, 6},
		{"ExitAPI", ExitAPI, 7},
		{"ExitAmbiguous", ExitAmbiguous, 8},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.got)
		})
	}
}

// TestErrorExitCode pins the exit code each envelope error code maps to.
//
// Deviations from fizzy-cli, where the design wins: fizzy maps a 422 to exit
// 7 (errors.go#FromHTTPStatus → NewValidationError → CodeAPI), and its
// default branch maps a 400, like any status it doesn't name, to 7 as well.
// pcal treats both as invalid input: CodeInvalidInput (400) and
// CodeValidation (422) exit 1.
func TestErrorExitCode(t *testing.T) {
	tests := []struct {
		code string
		want int
	}{
		{CodeUsage, 1},
		{CodeInvalidInput, 1},
		{CodeValidation, 1},
		{CodeNotFound, 2},
		{CodeAuth, 3},
		{CodeForbidden, 4},
		{CodeRateLimit, 5},
		{CodeNetwork, 6},
		{CodeAPI, 7},
		{CodeAmbiguous, 8},
		{"a_code_from_the_future", 7},
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			require.Equal(t, tt.want, (&Error{Code: tt.code}).ExitCode())
		})
	}
}

func TestAsError(t *testing.T) {
	notFound := &Error{Code: CodeNotFound, Message: "not_found"}

	tests := []struct {
		name     string
		err      error
		wantCode string
		wantMsg  string
	}{
		{"an *Error passes through", notFound, CodeNotFound, "not_found"},
		{"a wrapped *Error is unwrapped", fmt.Errorf("loading event: %w", notFound), CodeNotFound, "not_found"},
		{"any other error is an API error", errors.New("boom"), CodeAPI, "boom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := AsError(tt.err)
			require.Equal(t, tt.wantCode, e.Code)
			require.Equal(t, tt.wantMsg, e.Message)
		})
	}
}

func TestUsagef(t *testing.T) {
	e := Usagef("--to %s is before --from %s", "2026-10-01", "2026-10-31")

	require.Equal(t, CodeUsage, e.Code)
	require.Equal(t, "--to 2026-10-01 is before --from 2026-10-31", e.Message)
	require.Equal(t, ExitUsage, e.ExitCode())
	require.Equal(t, e.Message, e.Error())
}

func TestNewSelectsFormat(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want Format
	}{
		{"a terminal gets human output", Options{TTY: true}, FormatHuman},
		{"a pipe gets JSON", Options{}, FormatJSON},
		{"--json in a terminal", Options{TTY: true, JSON: true}, FormatJSON},
		{"--quiet", Options{TTY: true, Quiet: true}, FormatQuiet},
		{"--jq implies JSON, even in a terminal", Options{TTY: true, JQ: ".data"}, FormatJSON},
		{"--jq with --quiet filters the bare data", Options{Quiet: true, JQ: "."}, FormatQuiet},
		{"--csv in a terminal", Options{TTY: true, CSV: true}, FormatCSV},
		{"--csv in a pipe", Options{CSV: true}, FormatCSV},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := New(io.Discard, io.Discard, tt.opts)
			require.NoError(t, err)
			require.Equal(t, tt.want, p.Format())
		})
	}
}

func TestNewRejectsBadOutputFlags(t *testing.T) {
	tests := []struct {
		name    string
		opts    Options
		wantMsg string
	}{
		{"--json with --quiet", Options{JSON: true, Quiet: true}, "--json and --quiet can't be used together"},
		{"--csv with --json", Options{CSV: true, JSON: true}, "--csv can't be used with --json, --quiet, or --jq"},
		{"--csv with --quiet", Options{CSV: true, Quiet: true}, "--csv can't be used with --json, --quiet, or --jq"},
		{"--csv with --jq", Options{CSV: true, JQ: "."}, "--csv can't be used with --json, --quiet, or --jq"},
		{"--jq that doesn't parse", Options{JQ: ".data["}, "invalid --jq expression"},
		{"--jq that doesn't compile", Options{JQ: "no_such_function"}, "invalid --jq expression"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(io.Discard, io.Discard, tt.opts)

			var e *Error
			require.ErrorAs(t, err, &e)
			require.Equal(t, CodeUsage, e.Code)
			require.Equal(t, ExitUsage, e.ExitCode())
			require.Contains(t, e.Message, tt.wantMsg)
		})
	}
}

// tag stands in for a wire type: a struct with a nullable field.
type tag struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Color *string `json:"color"`
}

func tagList() Result {
	return Result{
		Data:    []tag{{ID: "t1", Name: "Worship"}, {ID: "t2", Name: "Kids, Youth & Family"}},
		Summary: "2 tags",
		Table: &Table{
			Headers: []string{"ID", "Name"},
			Rows:    [][]string{{"t1", "Worship"}, {"t2", "Kids, Youth & Family"}},
		},
	}
}

const tagListJSON = `[{"id":"t1","name":"Worship","color":null},{"id":"t2","name":"Kids, Youth & Family","color":null}]`

// newTestPrinter returns a Printer writing to buffers it also returns.
func newTestPrinter(t *testing.T, opts Options) (*Printer, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, errOut bytes.Buffer
	p, err := New(&out, &errOut, opts)
	require.NoError(t, err)
	return p, &out, &errOut
}

func TestPrintJSON(t *testing.T) {
	tests := []struct {
		name   string
		result Result
		want   string
	}{
		{"envelope with a summary", tagList(), `{"ok":true,"data":` + tagListJSON + `,"summary":"2 tags"}`},
		{"no summary, no summary key", Result{Data: tag{ID: "t1", Name: "Worship"}}, `{"ok":true,"data":{"id":"t1","name":"Worship","color":null}}`},
		// fizzy-cli's Response has `data,omitempty` too.
		{"no data, no data key", Result{Summary: "Deleted tag Worship"}, `{"ok":true,"summary":"Deleted tag Worship"}`},
		{"an empty list is still data", Result{Data: []tag{}, Summary: "0 tags"}, `{"ok":true,"data":[],"summary":"0 tags"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, out, errOut := newTestPrinter(t, Options{JSON: true})

			require.NoError(t, p.Print(tt.result))
			require.JSONEq(t, tt.want, out.String())
			require.Empty(t, errOut.String())
		})
	}
}

func TestPrintJSONIsIndentedAndUnescaped(t *testing.T) {
	p, out, _ := newTestPrinter(t, Options{})

	require.NoError(t, p.Print(tagList()))
	require.Contains(t, out.String(), "\n  \"ok\": true,\n")
	require.Contains(t, out.String(), "Kids, Youth & Family", "& must not be escaped as \\u0026")
}

func TestPrintQuiet(t *testing.T) {
	p, out, _ := newTestPrinter(t, Options{Quiet: true})

	require.NoError(t, p.Print(tagList()))
	require.JSONEq(t, tagListJSON, out.String())
}

func TestPrintJQ(t *testing.T) {
	tests := []struct {
		name  string
		quiet bool
		expr  string
		want  string
	}{
		{"a string prints raw", false, ".data[1].name", "Kids, Youth & Family\n"},
		{"a number prints as JSON", false, ".data | length", "2\n"},
		{"an object prints as compact JSON", false, ".data[0]", `{"color":null,"id":"t1","name":"Worship"}` + "\n"},
		{"each result prints on its own line", false, ".data[].id", "t1\nt2\n"},
		{"the summary is in the envelope", false, ".summary", "2 tags\n"},
		{"with --quiet the input is the bare data", true, ".[0].id", "t1\n"},
		{"no results print nothing", false, "empty", ""},
		{"halt stops quietly", false, "halt", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, out, _ := newTestPrinter(t, Options{JQ: tt.expr, Quiet: tt.quiet})

			require.NoError(t, p.Print(tagList()))
			require.Equal(t, tt.want, out.String())
		})
	}
}

func TestPrintJQRuntimeErrorIsUsageError(t *testing.T) {
	p, _, _ := newTestPrinter(t, Options{JQ: ".data.name"})

	err := p.Print(tagList())

	var e *Error
	require.ErrorAs(t, err, &e)
	require.Equal(t, CodeUsage, e.Code)
	require.Contains(t, e.Message, "jq filter error")
}

func TestPrintCSV(t *testing.T) {
	p, out, _ := newTestPrinter(t, Options{CSV: true, TTY: true})

	require.NoError(t, p.Print(tagList()))
	require.Equal(t, "ID,Name\nt1,Worship\nt2,\"Kids, Youth & Family\"\n", out.String())
}

func TestPrintCSVNeedsATable(t *testing.T) {
	p, _, _ := newTestPrinter(t, Options{CSV: true})

	err := p.Print(Result{Data: tag{ID: "t1"}, Card: &Card{Fields: []Field{{"ID", "t1"}}}})

	var e *Error
	require.ErrorAs(t, err, &e)
	require.Equal(t, CodeUsage, e.Code)
	require.Contains(t, e.Message, "--csv works only with lists")
}

func TestPrintHuman(t *testing.T) {
	tests := []struct {
		name   string
		result Result
		want   string
	}{
		{
			name:   "a table under its summary",
			result: tagList(),
			want:   "2 tags\n\nID  Name\nt1  Worship\nt2  Kids, Youth & Family\n",
		},
		{
			name:   "an empty table prints only its summary",
			result: Result{Data: []tag{}, Summary: "0 tags", Table: &Table{Headers: []string{"ID", "Name"}}},
			want:   "0 tags\n",
		},
		{
			name:   "an empty table with no summary",
			result: Result{Data: []tag{}, Table: &Table{Headers: []string{"ID", "Name"}}},
			want:   "No results.\n",
		},
		{
			name: "a card aligns its labels and indents continuation lines",
			result: Result{Data: tag{ID: "t1"}, Summary: "Tag Worship", Card: &Card{Fields: []Field{
				{"Name", "Worship"},
				{"Color", ""},
				{"Description", "First line\nSecond line"},
			}}},
			want: "Name         Worship\nColor\nDescription  First line\n             Second line\n",
		},
		{
			name:   "a summary alone",
			result: Result{Summary: "Deleted tag Worship"},
			want:   "Deleted tag Worship\n",
		},
		{
			name:   "data with nothing to render it falls back to indented JSON",
			result: Result{Data: tag{ID: "t1", Name: "Worship"}},
			want:   "{\n  \"id\": \"t1\",\n  \"name\": \"Worship\",\n  \"color\": null\n}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, out, errOut := newTestPrinter(t, Options{TTY: true, NoColor: true})

			require.NoError(t, p.Print(tt.result))
			require.Equal(t, tt.want, out.String())
			require.Empty(t, errOut.String())
		})
	}
}

func TestPrintHumanStyling(t *testing.T) {
	tests := []struct {
		name     string
		noColor  bool
		wantBold bool
	}{
		{"bold headers in a terminal", false, true},
		{"NO_COLOR turns styling off", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, out, _ := newTestPrinter(t, Options{TTY: true, NoColor: tt.noColor})

			require.NoError(t, p.Print(tagList()))
			require.NoError(t, p.Print(Result{Card: &Card{Fields: []Field{{"Name", "Worship"}}}}))
			if tt.wantBold {
				require.Contains(t, out.String(), "\x1b[1mID\x1b[0m")
				require.Contains(t, out.String(), "\x1b[1mName\x1b[0m")
			} else {
				require.NotContains(t, out.String(), "\x1b[")
			}
		})
	}
}

func validationError() *Error {
	return &Error{
		Code:    CodeValidation,
		Message: "Title can't be blank",
		Fields:  map[string][]string{"title": {"can't be blank"}, "occurs_on": {"can't be blank", "is not a date"}},
		Hint:    "Pass --title and --date.",
	}
}

const validationEnvelope = `{"ok":false,"error":{"code":"validation","message":"Title can't be blank",` +
	`"fields":{"title":["can't be blank"],"occurs_on":["can't be blank","is not a date"]},"hint":"Pass --title and --date."}}`

func TestPrintErrorMachine(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		err  error
		want string
		exit int
	}{
		{"JSON carries the fields and the hint", Options{}, validationError(), validationEnvelope, 1},
		{"no fields or hint, no keys for them", Options{JSON: true}, &Error{Code: CodeNotFound, Message: "not_found"}, `{"ok":false,"error":{"code":"not_found","message":"not_found"}}`, 2},
		{"--quiet still prints the error envelope", Options{Quiet: true}, validationError(), validationEnvelope, 1},
		{"--jq never filters an error", Options{JQ: ".data"}, validationError(), validationEnvelope, 1},
		{"a plain error is an API error", Options{}, errors.New("boom"), `{"ok":false,"error":{"code":"api_error","message":"boom"}}`, 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, out, errOut := newTestPrinter(t, tt.opts)

			require.Equal(t, tt.exit, p.PrintError(tt.err))
			require.JSONEq(t, tt.want, out.String())
			require.Empty(t, errOut.String())
		})
	}
}

func TestPrintErrorHuman(t *testing.T) {
	want := "Error: Title can't be blank\n" +
		"  occurs_on: can't be blank\n" +
		"  occurs_on: is not a date\n" +
		"  title: can't be blank\n" +
		"Pass --title and --date.\n"

	tests := []struct {
		name string
		opts Options
	}{
		{"in a terminal", Options{TTY: true}},
		{"with --csv the error goes to stderr, not into the CSV", Options{CSV: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, out, errOut := newTestPrinter(t, tt.opts)

			require.Equal(t, 1, p.PrintError(validationError()))
			require.Equal(t, want, errOut.String())
			require.Empty(t, out.String())
		})
	}
}

func TestNewForErrors(t *testing.T) {
	tests := []struct {
		name      string
		opts      Options
		wantHuman bool
	}{
		{"conflicting --json and --quiet still get the envelope", Options{JSON: true, Quiet: true, TTY: true}, false},
		{"an invalid --jq still gets the envelope", Options{JQ: ".data[", TTY: true}, false},
		{"--csv with --json gets the envelope", Options{CSV: true, JSON: true}, false},
		{"--csv alone gets stderr", Options{CSV: true}, true},
		{"a terminal gets stderr", Options{TTY: true}, true},
		{"a pipe gets the envelope", Options{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			p := NewForErrors(&out, &errOut, tt.opts)

			require.Equal(t, 1, p.PrintError(validationError()))
			if tt.wantHuman {
				require.Empty(t, out.String())
				require.Contains(t, errOut.String(), "Error: Title can't be blank\n")
			} else {
				require.JSONEq(t, validationEnvelope, out.String())
				require.Empty(t, errOut.String())
			}
		})
	}
}

func TestErrorJSONShape(t *testing.T) {
	b, err := json.Marshal(validationError())

	require.NoError(t, err)
	require.JSONEq(t, `{"code":"validation","message":"Title can't be blank","fields":{"title":["can't be blank"],"occurs_on":["can't be blank","is not a date"]},"hint":"Pass --title and --date."}`, string(b))
}
