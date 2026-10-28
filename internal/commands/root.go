// Package commands defines pcal's command tree: one file per command group,
// each tested in-process through Run against a fake server.
package commands

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

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
}

// globalOptions holds the persistent flags every command shares, plus
// --csv, which only list commands define.
type globalOptions struct {
	json  bool
	quiet bool
	jq    string
	url   string
	yes   bool
	csv   bool
}

// app is the state one pcal run shares across its commands.
type app struct {
	streams Streams
	flags   globalOptions
	// printer renders results and errors. The root's PersistentPreRunE sets
	// it once the flags are parsed.
	printer *output.Printer
}

// Execute runs pcal with the process's arguments and standard streams and
// returns the exit code for main to pass to os.Exit.
func Execute() int {
	return Run(os.Args[1:], Streams{
		In:     os.Stdin,
		Out:    os.Stdout,
		Err:    os.Stderr,
		OutTTY: isTerminal(os.Stdout),
	})
}

// isTerminal reports whether f is a terminal.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

// Run runs pcal with args (not including the program name) and returns the
// exit code. Every run builds a fresh command tree, so runs share no state.
func Run(args []string, streams Streams) int {
	a := &app{streams: streams}
	root := newRootCmd(a)
	root.SetArgs(args)
	root.SetIn(streams.In)
	root.SetOut(streams.Out)
	root.SetErr(streams.Err)

	cmd, err := root.ExecuteC()
	if err == nil {
		return output.ExitOK
	}

	var e *output.Error
	if !errors.As(err, &e) {
		// Errors that aren't pcal's own come from cobra: an unknown command
		// or flag, or the wrong number of arguments.
		e = &output.Error{Code: output.CodeUsage, Message: err.Error(), Cause: err}
		if cmd != nil {
			e.Hint = fmt.Sprintf("Run `%s --help` for usage.", cmd.CommandPath())
		}
	}
	return a.errorPrinter().PrintError(e)
}

// outputOptions maps the parsed flags and the environment to output options.
func (a *app) outputOptions() output.Options {
	return output.Options{
		JSON:    a.flags.json,
		Quiet:   a.flags.quiet,
		JQ:      a.flags.jq,
		CSV:     a.flags.csv,
		TTY:     a.streams.OutTTY,
		NoColor: os.Getenv("NO_COLOR") != "",
	}
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
		// The root runs only to show help. NoArgs makes a stray word an
		// "unknown command" error; without it, cobra accepts any argument on a
		// command with no subcommands.
		Args: cobra.NoArgs,
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

	return root
}
