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

	// options are extra bubbletea options. Tests give a window size and a
	// color profile, which a terminal would; without them bubbletea draws
	// nothing into a buffer.
	options []tea.ProgramOption
}

// New returns a Prompter for in and out that's Accessible when the
// ACCESSIBLE environment variable is set to anything, the switch huh's
// README recommends.
func New(in io.Reader, out io.Writer, getenv func(string) string) Prompter {
	return Prompter{In: in, Out: out, Accessible: getenv("ACCESSIBLE") != ""}
}

// Password asks for a secret under title without echoing it.
func (p Prompter) Password(ctx context.Context, title string) (string, error) {
	// huh's accessible password field reads the terminal through its file
	// descriptor (term.ReadPassword). Given any other reader it prints
	// nothing and answers "" (huh v2.0.3 field_input.go#RunAccessible).
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
// accessible prompt), is ErrCanceled.
func (p Prompter) run(ctx context.Context, form *huh.Form) error {
	keys := huh.NewDefaultKeyMap()
	// huh quits only on Ctrl-C; basecamp-cli adds Esc too
	// (internal/tui/forms.go#escKeyMap).
	keys.Quit.SetKeys("ctrl+c", "esc")
	// WithProgramOptions replaces bubbletea's options, so it goes before
	// WithInput and WithOutput, which add theirs.
	form = form.WithProgramOptions(p.options...).WithTheme(theme()).
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
// bubbles' help line uses light/dark colors too).
func theme() huh.Theme {
	return huh.ThemeFunc(func(isDark bool) *huh.Styles {
		t := huh.ThemeBase(isDark)
		bold := lipgloss.NewStyle().Bold(true)
		faint := lipgloss.NewStyle().Faint(true)

		t.Focused.Title = bold
		t.Focused.NoteTitle = bold
		t.Focused.Description = faint
		t.Focused.ErrorIndicator = t.Focused.ErrorIndicator.Bold(true)
		t.Focused.ErrorMessage = t.Focused.ErrorMessage.Bold(true)
		t.Focused.FocusedButton = t.Focused.FocusedButton.UnsetForeground().UnsetBackground().Reverse(true).Bold(true)
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
