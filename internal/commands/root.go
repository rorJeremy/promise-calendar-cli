// Package commands defines pcal's command tree: one file per command group,
// each tested in-process through Run against a fake server.
package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/rorJeremy/promise-calendar-cli/internal/client"
	"github.com/rorJeremy/promise-calendar-cli/internal/output"
)

// Streams are the standard streams a pcal run reads from and writes to.
// Tests pass buffers; Execute passes the process's own.
type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
	// OutTTY reports whether Out is a terminal, which picks human output
	// over JSON. Execute detects it; tests set it.
	OutTTY bool
	// InTTY reports whether In is a terminal, where pcal can prompt.
	// Execute detects it; tests set it.
	InTTY bool
	// ErrTTY reports whether Err is a terminal. Prompts are drawn there, so
	// pcal prompts only when In and Err both are. Execute detects it; tests
	// set it.
	ErrTTY bool
}

// globalOptions holds the persistent flags every command shares, plus
// --csv, which only list commands define.
type globalOptions struct {
	json    bool
	quiet   bool
	jq      string
	url     string
	yes     bool
	verbose bool
	csv     bool
}

// app is the state one pcal run shares across its commands.
type app struct {
	streams Streams
	flags   globalOptions
	// printer renders results and errors. The root's PersistentPreRunE sets
	// it once the flags are parsed.
	printer *output.Printer
	// getenv reads the environment: os.Getenv, or a map in tests.
	getenv func(string) string

	// The rest are for tests; the zero value is production.

	// store, when set, is the credential store to use instead of the one in
	// pcal's config directory.
	store credentialStore
	// interrupt, when set, replaces catching Ctrl-C (SIGINT) as the end of a
	// login's waits and of the revokes after it (see interruptible).
	interrupt func(context.Context) (context.Context, context.CancelFunc)
	// clientOptions are passed to every client (shorter timeouts in tests).
	clientOptions client.Options
	// openBrowser opens a URL in the person's browser (auth.OpenBrowser
	// when nil).
	openBrowser func(string) error
	// goos is the OS, for HeadlessReason (runtime.GOOS when empty).
	goos string
	// hostname names this computer, for the token's name (os.Hostname
	// when nil).
	hostname func() (string, error)
	// loginWait bounds how long a browser login waits for the person
	// (loginTimeout when zero).
	loginWait time.Duration
	// revokeWait bounds revoking a token after a login (revokeTimeout when
	// zero).
	revokeWait time.Duration
	// promptOptions are extra bubbletea options for prompts: tests give a
	// window size, which a terminal would, so a drawn prompt draws into a
	// buffer.
	promptOptions []tea.ProgramOption
}

// Execute runs pcal with the process's arguments and standard streams and
// returns the exit code for main to pass to os.Exit.
func Execute() int {
	return Run(os.Args[1:], Streams{
		In:     os.Stdin,
		Out:    os.Stdout,
		Err:    os.Stderr,
		OutTTY: isTerminal(os.Stdout),
		InTTY:  isTerminal(os.Stdin),
		ErrTTY: isTerminal(os.Stderr),
	})
}

// isTerminal reports whether f is a terminal.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

// Run runs pcal with args (not including the program name) and returns the
// exit code. Every run builds a fresh command tree, so runs share no state.
func Run(args []string, streams Streams) int {
	a := &app{streams: streams, getenv: os.Getenv}
	return execute(a, newRootCmd(a), args)
}

// execute runs root, which newRootCmd built for a, with args and returns
// the exit code. Tests call it with throwaway subcommands added to root.
func execute(a *app, root *cobra.Command, args []string) int {
	// Given nil, cobra parses the process's own arguments (os.Args[1:]),
	// which in a test are the test binary's.
	if args == nil {
		args = []string{}
	}
	typeOwnErrors(root)
	root.SetArgs(args)
	root.SetIn(a.streams.In)
	root.SetOut(a.streams.Out)
	root.SetErr(a.streams.Err)

	cmd, err := root.ExecuteC()
	if err == nil {
		return output.ExitOK
	}

	var e *output.Error
	if !errors.As(err, &e) {
		// typeOwnErrors made every error pcal's own code returns an
		// *output.Error, so this one is cobra's: an unknown command or
		// flag, the wrong arguments, or a missing required flag.
		e = &output.Error{Code: output.CodeUsage, Message: err.Error(), Cause: err}
	}
	if e.Code == output.CodeInterrupted || e.Code == output.CodeTerminated {
		// Canceling isn't a failure: one line on stderr, below whatever
		// prompt was showing, and never the envelope (as basecamp-cli's
		// login; its root exits 130 or 143 without rendering an error). The
		// line may quote the server, as when a canceled login couldn't
		// revoke its new token.
		_, _ = fmt.Fprintf(a.streams.Err, "\n%s\n", output.Visible(e.Message))
		return e.ExitCode()
	}
	if e.Code == output.CodeUsage && e.Hint == "" {
		// Every usage error says where the usage is, as in fizzy-cli
		// (root.go:394). A copy, so the error itself is left as it was.
		withHint := *e
		withHint.Hint = helpHint(cmd)
		e = &withHint
	}
	return a.errorPrinter().PrintError(e)
}

// helpHint returns "Run `<command> --help` for usage." for cmd, the
// command cobra was running (the root if it got no further).
func helpHint(cmd *cobra.Command) string {
	path := "pcal"
	if cmd != nil {
		path = cmd.CommandPath()
	}
	return fmt.Sprintf("Run `%s --help` for usage.", path)
}

// newGroupCmd returns a command that only groups subcommands, such as
// `pcal auth`. cobra shows help (exit 0) for any word after a command it
// can't run, before checking arguments (cobra 1.10.2 command.go:955), so a
// group runs to show help, like the root, and its Args is unknownCommand,
// which needs SuggestionsMinimumDistance set to suggest anything.
func newGroupCmd(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:                        use,
		Short:                      short,
		Args:                       unknownCommand,
		SuggestionsMinimumDistance: 2,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
}

// unknownCommand is the Args of the root and of every group. They take no
// arguments of their own, so a stray word is an unknown command. cobra.NoArgs says so but suggests
// nothing, and once the root has subcommands, cobra's own check (legacyArgs,
// used only when Args is nil) puts its suggestions in the message across
// several lines. Here they go in the hint, so the message stays one line:
// "Did you mean `pcal whoami`? Run `pcal --help` for usage."
func unknownCommand(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	hint := helpHint(cmd)
	if suggestions := cmd.SuggestionsFor(args[0]); len(suggestions) > 0 {
		names := make([]string, len(suggestions))
		for i, name := range suggestions {
			names[i] = "`" + cmd.CommandPath() + " " + name + "`"
		}
		hint = "Did you mean " + orList(names) + "? " + hint
	}
	return &output.Error{
		Code:    output.CodeUsage,
		Message: fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath()),
		Hint:    hint,
	}
}

// orList joins items as English does: "a", "a or b", "a, b, or c".
func orList(items []string) string {
	switch len(items) {
	case 1:
		return items[0]
	case 2:
		return items[0] + " or " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + ", or " + items[len(items)-1]
	}
}

// typeOwnErrors wraps the error-returning hooks and RunE of cmd and every
// command under it, so an error pcal's own code returns is always an
// *output.Error: a plain one becomes an API error (exit 7, no help hint)
// through output.AsError. Any other error execute sees is then cobra's own,
// a usage error. Args validators aren't wrapped: their errors are usage
// errors. Nor are the help and completion commands cobra adds while it
// executes; their errors are cobra's too.
func typeOwnErrors(cmd *cobra.Command) {
	for _, hook := range []*func(*cobra.Command, []string) error{
		&cmd.PersistentPreRunE, &cmd.PreRunE, &cmd.RunE, &cmd.PostRunE, &cmd.PersistentPostRunE,
	} {
		run := *hook
		if run == nil {
			continue
		}
		*hook = func(c *cobra.Command, args []string) error {
			if err := run(c, args); err != nil {
				return output.AsError(err)
			}
			return nil
		}
	}
	for _, sub := range cmd.Commands() {
		typeOwnErrors(sub)
	}
}

// outputOptions maps the parsed flags and the environment to output options.
func (a *app) outputOptions() output.Options {
	return output.Options{
		JSON:    a.flags.json,
		Quiet:   a.flags.quiet,
		JQ:      a.flags.jq,
		CSV:     a.flags.csv,
		TTY:     a.streams.OutTTY,
		NoColor: colorDisabled(a.getenv),
	}
}

// colorDisabled reports whether the environment turns styling off, by the
// rules termenv 0.16.0 follows (EnvNoColor, and EnvColorProfile with its
// Unix ColorProfile): NO_COLOR set to anything; otherwise CLICOLOR=0 or
// TERM=dumb, unless CLICOLOR_FORCE is set to something other than "0".
// CLICOLOR_FORCE can't add styling where there's no terminal: without one,
// pcal prints JSON or CSV, which have none.
func colorDisabled(getenv func(string) string) bool {
	if getenv("NO_COLOR") != "" {
		return true
	}
	if forced := getenv("CLICOLOR_FORCE"); forced != "" && forced != "0" {
		return false
	}
	return getenv("CLICOLOR") == "0" || getenv("TERM") == "dumb"
}

// errorPrinter returns the printer to report a failed run with: the run's
// own once the flags got that far, otherwise one built from whatever flags
// were parsed, even if they were the problem.
func (a *app) errorPrinter() *output.Printer {
	if a.printer != nil {
		return a.printer
	}
	return output.NewForErrors(a.streams.Out, a.streams.Err, a.outputOptions())
}

// newRootCmd builds the `pcal` command with its persistent flags.
func newRootCmd(a *app) *cobra.Command {
	root := &cobra.Command{
		Use:   "pcal",
		Short: "Manage the Promise Church calendar from the terminal",
		Long: "pcal manages events, tags, RSVPs, images, and tokens on the Promise Church\n" +
			"calendar. It prints tables in a terminal and a JSON envelope when piped.",
		// The root runs only to show help, so a stray word is an "unknown
		// command" error with suggestions (see unknownCommand). Without an
		// Args, cobra accepts any argument while the root has no
		// subcommands.
		Args: unknownCommand,
		// cobra's default suggestion distance, which only its own check sets.
		SuggestionsMinimumDistance: 2,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			p, err := output.New(a.streams.Out, a.streams.Err, a.outputOptions())
			if err != nil {
				return err
			}
			a.printer = p
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// There is deliberately no --token flag: tokens never go in argv, where
	// they'd land in shell history and the process list. Use PCAL_TOKEN or
	// `pcal auth login --with-token`.
	flags := root.PersistentFlags()
	flags.BoolVar(&a.flags.json, "json", false, "print the JSON envelope (the default when stdout isn't a terminal)")
	flags.BoolVar(&a.flags.quiet, "quiet", false, "print only the data, without the envelope")
	flags.StringVar(&a.flags.jq, "jq", "", "filter the JSON envelope with a jq `expression` (implies --json)")
	flags.StringVar(&a.flags.url, "url", "", "calendar `URL` (default https://calendar.promisechurch.io, or $PCAL_URL)")
	flags.BoolVar(&a.flags.yes, "yes", false, "answer yes to confirmation prompts")
	// Hidden, unlike fizzy-cli's: it's for debugging, and logs each request
	// line and response status (never headers) to stderr.
	flags.BoolVar(&a.flags.verbose, "verbose", false, "log each request and response status to stderr")
	_ = flags.MarkHidden("verbose")

	root.AddCommand(
		newAuthCmd(a),
		newLoginCmd(a, "login"),
		newLogoutCmd(a, "logout"),
		newWhoamiCmd(a),
	)
	return root
}
