// Package output renders what a command produced, in the mode the global
// flags and the terminal pick: a table or a key/value card for people, the
// JSON envelope {"ok","data","summary"} for scripts (as in fizzy-cli), bare
// data (--quiet), a jq filter over the envelope (--jq), or CSV (--csv). It
// also renders errors and owns pcal's exit codes.
package output

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/itchyny/gojq"
	"github.com/muesli/termenv"
)

// Format is how a Printer renders results.
type Format int

const (
	// FormatHuman is a table or card, for a terminal.
	FormatHuman Format = iota
	// FormatJSON is the JSON envelope (--json, --jq, or stdout not a terminal).
	FormatJSON
	// FormatQuiet is the bare data, without the envelope (--quiet).
	FormatQuiet
	// FormatCSV is a list as CSV (--csv).
	FormatCSV
)

// Options are the inputs that choose a Printer's format.
type Options struct {
	JSON    bool   // --json
	Quiet   bool   // --quiet
	JQ      string // --jq expression; implies JSON, or filters the bare data with --quiet
	CSV     bool   // --csv; only list commands define it
	TTY     bool   // stdout is a terminal
	NoColor bool   // NO_COLOR is set: no styling even in a terminal
}

// Result is what a command produced. Data is what scripts see; Table and
// Card are how people see it.
type Result struct {
	// Data is the envelope's "data": the API object or list, or nil.
	Data any
	// Summary is one line describing the result, e.g. "12 events Oct 1–31".
	// Human output prints it above a table, or alone when there's no table
	// or card.
	Summary string
	// Table renders a list, for people and for --csv.
	Table *Table
	// Card renders one record as labeled fields.
	Card *Card
}

// Table is a list: one row per item, cells in header order.
type Table struct {
	Headers []string
	Rows    [][]string
}

// Card is one record shown as labeled fields, in order.
type Card struct {
	Fields []Field
}

// Field is one labeled value on a Card. A value may span several lines.
type Field struct {
	Label string
	Value string
}

// envelope is the JSON shape of a success, as in fizzy-cli's Response.
type envelope struct {
	OK      bool   `json:"ok"`
	Data    any    `json:"data,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// errorEnvelope is the JSON shape of a failure. Unlike fizzy-cli's flat
// {ok,error,code,hint}, the error is an object, so a 422 can carry fields
// beside the code, message, and hint.
type errorEnvelope struct {
	OK    bool   `json:"ok"`
	Error *Error `json:"error"`
}

// Printer writes results to stdout and human-readable errors to stderr.
type Printer struct {
	out    io.Writer
	errOut io.Writer
	format Format
	jq     *gojq.Code
	bold   lipgloss.Style
	plain  lipgloss.Style
}

// New returns a Printer for opts. Conflicting flags or a --jq expression that
// doesn't compile are usage errors (exit 1).
func New(out, errOut io.Writer, opts Options) (*Printer, error) {
	if opts.JSON && opts.Quiet {
		return nil, Usagef("--json and --quiet can't be used together")
	}
	if opts.CSV && (opts.JSON || opts.Quiet || opts.JQ != "") {
		return nil, Usagef("--csv can't be used with --json, --quiet, or --jq")
	}

	p := &Printer{out: out, errOut: errOut, format: selectFormat(opts)}

	if opts.JQ != "" {
		code, err := compileJQ(opts.JQ)
		if err != nil {
			return nil, err
		}
		p.jq = code
	}

	// Styling is bold text only, so color never carries meaning. Without a
	// terminal, or with NO_COLOR, the Ascii profile drops every escape code.
	renderer := lipgloss.NewRenderer(out)
	if opts.TTY && !opts.NoColor {
		renderer.SetColorProfile(termenv.ANSI)
	} else {
		renderer.SetColorProfile(termenv.Ascii)
	}
	p.bold = renderer.NewStyle().Bold(true)
	p.plain = renderer.NewStyle()

	return p, nil
}

// NewForErrors returns a Printer that can report an error whatever the flags
// were, including flags New rejects (a conflict, an invalid --jq): any of
// --json, --quiet, or --jq means the error envelope; otherwise --csv or a
// terminal means text on stderr. Use it only for PrintError.
func NewForErrors(out, errOut io.Writer, opts Options) *Printer {
	format := FormatJSON
	switch {
	case opts.JSON, opts.Quiet, opts.JQ != "":
		format = FormatJSON
	case opts.CSV:
		format = FormatCSV
	case opts.TTY:
		format = FormatHuman
	}
	return &Printer{out: out, errOut: errOut, format: format}
}

// selectFormat applies the precedence: --csv, --quiet, --json or --jq, then
// a terminal gets human output and anything else gets JSON.
func selectFormat(opts Options) Format {
	switch {
	case opts.CSV:
		return FormatCSV
	case opts.Quiet:
		return FormatQuiet
	case opts.JSON, opts.JQ != "":
		return FormatJSON
	case opts.TTY:
		return FormatHuman
	default:
		return FormatJSON
	}
}

// Format returns the format the Printer renders in.
func (p *Printer) Format() Format {
	return p.format
}

// Print writes a command's result to stdout.
func (p *Printer) Print(r Result) error {
	switch p.format {
	case FormatHuman:
		return p.printHuman(r)
	case FormatCSV:
		if r.Table == nil {
			return Usagef("--csv works only with lists")
		}
		return p.printCSV(r.Table)
	case FormatQuiet:
		return p.emit(r.Data)
	default:
		return p.emit(envelope{OK: true, Data: r.Data, Summary: r.Summary})
	}
}

// PrintError reports err and returns the exit code for it. People get text
// on stderr; scripts get the error envelope on stdout, never filtered by
// --jq (as in fizzy-cli, a filter must not hide an error). With --csv the
// error goes to stderr, so it doesn't end up inside the CSV.
func (p *Printer) PrintError(err error) int {
	e := AsError(err)
	if p.format == FormatHuman || p.format == FormatCSV {
		p.printHumanError(e)
	} else {
		_ = writeJSON(p.out, errorEnvelope{OK: false, Error: e})
	}
	return e.ExitCode()
}

// emit writes v as indented JSON, or through the --jq filter.
func (p *Printer) emit(v any) error {
	if p.jq != nil {
		return p.runJQ(v)
	}
	return writeJSON(p.out, v)
}

// writeJSON writes v as indented JSON, without escaping &, <, and >.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// compileJQ compiles a --jq expression. $ENV works as in jq.
func compileJQ(expr string) (*gojq.Code, error) {
	query, err := gojq.Parse(expr)
	if err != nil {
		return nil, &Error{Code: CodeUsage, Message: "invalid --jq expression: " + err.Error(), Cause: err}
	}
	code, err := gojq.Compile(query, gojq.WithEnvironLoader(os.Environ))
	if err != nil {
		return nil, &Error{Code: CodeUsage, Message: "invalid --jq expression: " + err.Error(), Cause: err}
	}
	return code, nil
}

// runJQ runs the --jq filter over v and prints each result on its own line:
// strings raw (like jq -r), anything else as compact JSON. A runtime error is
// a usage error (exit 1), as in fizzy-cli.
func (p *Printer) runJQ(v any) error {
	// gojq takes only the types encoding/json decodes into.
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var input any
	if err := json.Unmarshal(raw, &input); err != nil {
		return err
	}

	iter := p.jq.Run(input)
	for {
		result, ok := iter.Next()
		if !ok {
			return nil
		}
		if err, isErr := result.(error); isErr {
			var halt *gojq.HaltError
			if errors.As(err, &halt) && halt.Value() == nil {
				return nil
			}
			return &Error{Code: CodeUsage, Message: "jq filter error: " + err.Error(), Cause: err}
		}

		line, isString := result.(string)
		if !isString {
			encoded, err := gojq.Marshal(result)
			if err != nil {
				return err
			}
			line = string(encoded)
		}
		if _, err := fmt.Fprintln(p.out, line); err != nil {
			return err
		}
	}
}

// printCSV writes a table as CSV, headers first.
func (p *Printer) printCSV(t *Table) error {
	w := csv.NewWriter(p.out)
	if err := w.Write(t.Headers); err != nil {
		return err
	}
	return w.WriteAll(t.Rows)
}

// printHuman writes a table under its summary, a card, the summary alone,
// or, when nothing else renders the data, the data as indented JSON.
func (p *Printer) printHuman(r Result) error {
	var b strings.Builder
	switch {
	case r.Table != nil && len(r.Table.Rows) == 0:
		if r.Summary == "" {
			b.WriteString("No results.\n")
		} else {
			b.WriteString(r.Summary + "\n")
		}
	case r.Table != nil:
		if r.Summary != "" {
			b.WriteString(r.Summary + "\n\n")
		}
		b.WriteString(p.renderTable(r.Table))
	case r.Card != nil:
		b.WriteString(p.renderCard(r.Card))
	case r.Summary != "":
		b.WriteString(r.Summary + "\n")
	case r.Data != nil:
		return writeJSON(p.out, r.Data)
	}
	_, err := io.WriteString(p.out, b.String())
	return err
}

// renderTable lays out columns separated by two spaces, with bold headers
// and no box-drawing borders, which screen readers would read out.
func (p *Printer) renderTable(t *Table) string {
	header := p.bold.PaddingRight(2)
	cell := p.plain.PaddingRight(2)
	rendered := table.New().
		Headers(t.Headers...).
		Rows(t.Rows...).
		BorderTop(false).
		BorderBottom(false).
		BorderLeft(false).
		BorderRight(false).
		BorderColumn(false).
		BorderHeader(false).
		BorderRow(false).
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == table.HeaderRow {
				return header
			}
			return cell
		}).
		Render()
	return trimLines(rendered) + "\n"
}

// renderCard lays out labels in a bold column with each value beside its
// label; a value's later lines line up under its first.
func (p *Printer) renderCard(c *Card) string {
	width := 0
	for _, f := range c.Fields {
		width = max(width, lipgloss.Width(f.Label))
	}
	indent := strings.Repeat(" ", width+2)

	var b strings.Builder
	for _, f := range c.Fields {
		lines := strings.Split(f.Value, "\n")
		pad := strings.Repeat(" ", width-lipgloss.Width(f.Label)+2)
		b.WriteString(p.bold.Render(f.Label) + pad + lines[0] + "\n")
		for _, line := range lines[1:] {
			b.WriteString(indent + line + "\n")
		}
	}
	return trimLines(strings.TrimSuffix(b.String(), "\n")) + "\n"
}

// printHumanError writes "Error: <message>", then each field message, then
// the hint, to stderr.
func (p *Printer) printHumanError(e *Error) {
	var b strings.Builder
	b.WriteString("Error: " + e.Message + "\n")

	names := make([]string, 0, len(e.Fields))
	for name := range e.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, msg := range e.Fields[name] {
			b.WriteString("  " + name + ": " + msg + "\n")
		}
	}

	if e.Hint != "" {
		b.WriteString(e.Hint + "\n")
	}
	_, _ = io.WriteString(p.errOut, b.String())
}

// trimLines removes trailing spaces from every line.
func trimLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}
