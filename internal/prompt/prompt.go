// Package prompt asks questions on a terminal with huh forms (huh v2): a
// masked field for a token, and yes/no confirmations. Prompts render on
// stderr, so stdout stays clean for the result, and use no colors (see
// theme).
//
// huh v2, not v1: huh v1.0.0 pulls in bubbletea v1, whose package init()
// asks the terminal for its background color whenever stdout is a terminal
// (bubbletea v1.3.6 tea_init.go:21), and termenv then waits up to 5 seconds
// for an answer. Real terminals answer at once, but on a pty that answers
// nothing every pcal command waited 5 seconds. bubbletea v2 has no such
// init.
package prompt

import (
	"context"
	"errors"
	"io"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"golang.org/x/term"
)

// ErrCanceled means the person canceled a prompt, with Ctrl-C or Esc.
var ErrCanceled = errors.New("canceled")

// Prompter asks questions, reading answers from In and drawing prompts on
// Out.
type Prompter struct {
	// In is where answers come from: a terminal.
	In io.Reader
	// Out is where prompts are drawn: stderr.
	Out io.Writer
	// Accessible swaps huh's redrawing forms for plain prompts that a
	// screen reader can follow.
	Accessible bool
	// Plain drops even the attributes (bold, faint, reverse) from the
	// prompts, for when pcal's output is unstyled (see theme).
	Plain bool

	// options are extra bubbletea options. Tests give a window size, as a
	// terminal would: without one, bubbletea draws nothing into a buffer.
	// A color profile only keeps the styling.
	options []tea.ProgramOption
}

// New returns a Prompter for in and out, Plain when plain. It's Accessible
// when the ACCESSIBLE environment variable is set to anything, the switch
// huh's README recommends, or on a dumb terminal (TERM=dumb), where a
// redrawing form can't work: huh falls back to accessible prompts there
// itself (huh v2.0.3 form.go:129-131), which run's WithAccessible would
// otherwise undo (Gate 3c).
func New(in io.Reader, out io.Writer, getenv func(string) string, plain bool) Prompter {
	accessible := getenv("ACCESSIBLE") != "" || getenv("TERM") == "dumb"
	return Prompter{In: in, Out: out, Accessible: accessible, Plain: plain}
}

// WithProgramOptions returns p with extra bubbletea options for its drawn
// prompts. Tests give a window size, as a terminal would; without one,
// bubbletea draws nothing into a buffer.
func (p Prompter) WithProgramOptions(opts ...tea.ProgramOption) Prompter {
	p.options = opts
	return p
}

// Password asks for a secret under title without echoing it.
func (p Prompter) Password(ctx context.Context, title string) (string, error) {
	// huh's accessible password field reads the terminal through its file
	// descriptor (term.ReadPassword). Given a reader without one it prints
	// nothing and answers ""; given a file that isn't a terminal it prints
	// the prompt, then answers "" (huh v2.0.3 field_input.go:452-460,
	// internal/accessibility/accessibility.go:115-116; runAccessible drops
	// the error, form.go:731). Either way, say why instead.
	if p.Accessible && terminalFd(p.In) < 0 {
		return "", errors.New("a masked prompt needs a terminal")
	}
	var value string
	input := huh.NewInput().Title(title).EchoMode(huh.EchoModePassword).Value(&value)
	if err := p.run(ctx, huh.NewForm(huh.NewGroup(input))); err != nil {
		return "", err
	}
	return value, nil
}

// Confirm asks a yes/no question under title; def is the answer Enter
// gives.
func (p Prompter) Confirm(ctx context.Context, title string, def bool) (bool, error) {
	value := def
	confirm := huh.NewConfirm().Title(title).Value(&value)
	if err := p.run(ctx, huh.NewForm(huh.NewGroup(confirm))); err != nil {
		return def, err
	}
	return value, nil
}

// run shows form and waits for an answer. Ctrl-C or Esc in the form, or
// ctx ending (pcal catches Ctrl-C as SIGINT, which is how it arrives at an
// accessible prompt, and SIGTERM), is ErrCanceled.
func (p Prompter) run(ctx context.Context, form *huh.Form) error {
	keys := huh.NewDefaultKeyMap()
	// huh quits only on Ctrl-C; basecamp-cli adds Esc too
	// (internal/tui/forms.go#escKeyMap).
	keys.Quit.SetKeys("ctrl+c", "esc")
	// bubbletea's own signal handler turns a SIGTERM into a quit, which huh
	// reports as an answer: whatever was typed so far (bubbletea v2.0.2
	// tea.go:651-655, huh form.go:708-717). pcal catches the signals itself
	// and ends ctx, so the form leaves them alone (Gate 3b F2).
	// WithProgramOptions replaces bubbletea's options, so it goes before
	// WithInput and WithOutput, which add theirs.
	//
	// A drawn form asks the terminal about synchronized output and Unicode
	// (DECRQM 2026 and 2027, skipped over SSH unless TERM names one of a
	// few terminals: bubbletea v2.0.2 tea.go:1090-1095, 953-967) and its
	// keyboard protocol (cursed_renderer.go:389). An answer that arrives
	// after the form has ended, as when a paste typed ahead ends it at
	// once, is left for the next program to read: the shell may show
	// ^[[?2026;2$y. bubbletea has no option to skip the queries, so this is
	// accepted (Jeremy's Gate 3c decision, Gate 3b F11).
	opts := append([]tea.ProgramOption{tea.WithoutSignalHandler()}, p.options...)
	form = form.WithProgramOptions(opts...).WithTheme(theme(p.Plain)).
		WithInput(p.In).WithOutput(p.Out).WithAccessible(p.Accessible).WithKeyMap(keys)

	restore := saveTerminal(p.In)
	done := make(chan error, 1)
	go func() { done <- form.RunWithContext(ctx) }()

	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		if p.Accessible {
			// An accessible prompt is blocked reading a line, and stays
			// blocked; the process ends soon after. A password prompt
			// turned echo off, and only its own return would turn it back
			// on, so put the terminal back as it was.
			restore()
			return ErrCanceled
		}
		// The drawn form ends with ctx and restores the terminal itself, so
		// wait for that.
		err = <-done
	}
	if errors.Is(err, huh.ErrUserAborted) || ctx.Err() != nil {
		return ErrCanceled
	}
	return err
}

// theme is how pcal's prompts look: huh's base theme with attributes only
// (bold, faint, reverse) and no colors, like the rest of pcal's output, so
// a prompt reads the same on a light or a dark background, whatever huh
// learns about it (huh's default theme picks colors by the background, and
// the help line ThemeBase keeps has bubbles' colors for a dark background,
// whatever the background: help.New, bubbles v2.0.0 help/help.go:98).
//
// With plain, there are no attributes either, as pcal's own output has none
// under NO_COLOR, CLICOLOR=0, or TERM=dumb (Jeremy's Gate 3c decision): an
// accessible prompt prints no escape sequence at all. A drawn one still
// shows the cursor as a reverse cell, which bubbles draws whatever the
// theme (bubbles v2.0.0 cursor/cursor.go:240). A drawn Confirm's two
// buttons then look alike, so Phase 9 has to mark the chosen one some
// other way before it uses Confirm.
func theme(plain bool) huh.Theme {
	styled := !plain
	return huh.ThemeFunc(func(isDark bool) *huh.Styles {
		t := huh.ThemeBase(isDark)
		bold := lipgloss.NewStyle().Bold(styled)
		faint := lipgloss.NewStyle().Faint(styled)

		t.Focused.Title = bold
		t.Focused.NoteTitle = bold
		t.Focused.Description = faint
		t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Bold(styled)
		t.Focused.ErrorMessage = t.Focused.ErrorMessage.Bold(styled)
		t.Focused.FocusedButton = t.Focused.FocusedButton.UnsetForeground().UnsetBackground().Reverse(styled).Bold(styled)
		t.Focused.BlurredButton = t.Focused.BlurredButton.UnsetForeground().UnsetBackground()
		t.Focused.TextInput.Placeholder = faint

		// bubbles' help styles, which ThemeBase keeps, have colors.
		t.Help.ShortKey = lipgloss.NewStyle()
		t.Help.FullKey = lipgloss.NewStyle()
		t.Help.ShortDesc = faint
		t.Help.FullDesc = faint
		t.Help.ShortSeparator = faint
		t.Help.FullSeparator = faint
		t.Help.Ellipsis = faint

		// Blurred fields look like focused ones without the border, as in
		// ThemeBase, which derived them before the changes above.
		t.Blurred = t.Focused
		t.Blurred.Base = t.Focused.Base.BorderStyle(lipgloss.HiddenBorder())
		t.Blurred.Card = t.Blurred.Base
		t.Blurred.MultiSelectSelector = lipgloss.NewStyle().SetString("  ")
		t.Blurred.NextIndicator = lipgloss.NewStyle()
		t.Blurred.PrevIndicator = lipgloss.NewStyle()
		return t
	})
}

// terminalFd returns r's file descriptor if r is a terminal, or -1.
func terminalFd(r io.Reader) int {
	f, ok := r.(interface{ Fd() uintptr })
	if !ok {
		return -1
	}
	fd := int(f.Fd()) //nolint:gosec // G115: a file descriptor fits in an int
	if !term.IsTerminal(fd) {
		return -1
	}
	return fd
}

// saveTerminal records the state of r, if it's a terminal, and returns the
// function that puts it back.
func saveTerminal(r io.Reader) func() {
	fd := terminalFd(r)
	if fd < 0 {
		return func() {}
	}
	state, err := term.GetState(fd)
	if err != nil {
		return func() {}
	}
	return func() { _ = term.Restore(fd, state) }
}
