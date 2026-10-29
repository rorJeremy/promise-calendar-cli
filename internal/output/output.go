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
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

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
	NoColor bool   // the environment turns styling off (NO_COLOR, CLICOLOR=0, TERM=dumb), even in a terminal
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
	// terminal, or when the environment turns styling off (NoColor), the
	// Ascii profile drops every escape code.
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

// Print writes a command's result to stdout. A nil slice in Data prints
// as an empty list, [], not null.
func (p *Printer) Print(r Result) error {
	r.Data = emptyIfNil(r.Data)
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

// emptyIfNil returns an empty slice of v's type when v is a nil slice, and
// v otherwise, so a list command that found nothing prints [] rather than
// null whether or not it made its slice.
func emptyIfNil(v any) any {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Slice && rv.IsNil() {
		return reflect.MakeSlice(rv.Type(), 0, 0).Interface()
	}
	return v
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

// printCSV writes a table as CSV, headers first, with every cell guarded
// against spreadsheet formulas (see csvCell).
func (p *Printer) printCSV(t *Table) error {
	w := csv.NewWriter(p.out)
	if err := w.Write(csvRow(t.Headers)); err != nil {
		return err
	}
	for _, row := range t.Rows {
		if err := w.Write(csvRow(row)); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// csvRow returns row with each cell made safe by csvCell.
func csvRow(row []string) []string {
	safe := make([]string, len(row))
	for i, cell := range row {
		safe[i] = csvCell(cell)
	}
	return safe
}

// csvCell prefixes a cell that a spreadsheet would run as a formula (one
// starting with =, +, -, @, a tab, or a carriage return) with ', which
// makes it text, as OWASP's CSV injection guidance says. RSVP names come
// from the public, so `rsvps list --csv` could otherwise carry a formula
// into a staff member's spreadsheet.
func csvCell(cell string) string {
	if cell != "" && strings.ContainsRune("=+-@\t\r", rune(cell[0])) {
		return "'" + cell
	}
	return cell
}

// printHuman writes a table under its summary, a card, the summary alone,
// or, when nothing else renders the data, the data as indented JSON. Every
// string goes through visible first, so no control character reaches the
// terminal.
func (p *Printer) printHuman(r Result) error {
	var b strings.Builder
	summary := visible(r.Summary, false)
	switch {
	case r.Table != nil && len(r.Table.Rows) == 0:
		if summary == "" {
			b.WriteString("No results.\n")
		} else {
			b.WriteString(summary + "\n")
		}
	case r.Table != nil:
		if summary != "" {
			b.WriteString(summary + "\n\n")
		}
		b.WriteString(p.renderTable(r.Table))
	case r.Card != nil:
		b.WriteString(p.renderCard(r.Card))
	case summary != "":
		b.WriteString(summary + "\n")
	case r.Data != nil:
		// The JSON encoder escapes C0 controls, but not DEL or C1.
		var data strings.Builder
		if err := writeJSON(&data, r.Data); err != nil {
			return err
		}
		b.WriteString(visible(data.String(), true))
	}
	_, err := io.WriteString(p.out, b.String())
	return err
}

// visible returns s with every control character (C0, DEL, and C1) and
// every byte that isn't UTF-8 replaced by its Go escape, such as \x1b,
// \t, or \u009b, so text from the server can't move the cursor, retitle
// the window, or clear a staff member's terminal. RSVP names, for one,
// come from a public form. With keepNewlines, line breaks (\n and \r\n)
// stay, as a card value's do; a lone \r is still shown as \r.
func visible(s string, keepNewlines bool) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, "\\x%02x", s[i])
		case keepNewlines && r == '\n':
			b.WriteByte('\n')
		case keepNewlines && r == '\r' && strings.HasPrefix(s[i+size:], "\n"):
			// The \n that follows is kept on the next pass.
		case unicode.IsControl(r):
			quoted := strconv.QuoteRune(r)
			b.WriteString(quoted[1 : len(quoted)-1])
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

// Visible returns s, one line of text for a terminal, with every control
// character shown as its escape (see visible): for warnings and notes
// that may quote the server.
func Visible(s string) string {
	return visible(s, false)
}

// visibleAll returns cells, each passed through visible without line breaks.
func visibleAll(cells []string) []string {
	shown := make([]string, len(cells))
	for i, cell := range cells {
		shown[i] = visible(cell, false)
	}
	return shown
}

// renderTable lays out columns separated by two spaces, with bold headers
// and no box-drawing borders, which screen readers would read out.
func (p *Printer) renderTable(t *Table) string {
	header := p.bold.PaddingRight(2)
	cell := p.plain.PaddingRight(2)
	rows := make([][]string, len(t.Rows))
	for i, row := range t.Rows {
		rows[i] = visibleAll(row)
	}
	rendered := table.New().
		Headers(visibleAll(t.Headers)...).
		Rows(rows...).
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
	labels := make([]string, len(c.Fields))
	width := 0
	for i, f := range c.Fields {
		labels[i] = visible(f.Label, false)
		width = max(width, lipgloss.Width(labels[i]))
	}
	indent := strings.Repeat(" ", width+2)

	var b strings.Builder
	for i, f := range c.Fields {
		lines := strings.Split(visible(f.Value, true), "\n")
		pad := strings.Repeat(" ", width-lipgloss.Width(labels[i])+2)
		b.WriteString(p.bold.Render(labels[i]) + pad + lines[0] + "\n")
		for _, line := range lines[1:] {
			b.WriteString(indent + line + "\n")
		}
	}
	return trimLines(strings.TrimSuffix(b.String(), "\n")) + "\n"
}

// printHumanError writes "Error: <message>", then each field message, then
// the hint, to stderr, each through visible: a message or field can quote
// the server.
func (p *Printer) printHumanError(e *Error) {
	var b strings.Builder
	b.WriteString("Error: " + visible(e.Message, false) + "\n")

	names := make([]string, 0, len(e.Fields))
	for name := range e.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, msg := range e.Fields[name] {
			b.WriteString("  " + visible(name, false) + ": " + visible(msg, false) + "\n")
		}
	}

	if e.Hint != "" {
		b.WriteString(visible(e.Hint, false) + "\n")
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
