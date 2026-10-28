// Package commands defines pcal's command tree: one file per command group,
// each tested in-process through Run against a fake server.
package commands

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// Streams are the standard streams a pcal run reads from and writes to.
// Tests pass buffers; Execute passes the process's own.
type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

// globalOptions holds the persistent flags every command shares.
type globalOptions struct {
	json  bool
	quiet bool
	jq    string
	url   string
	yes   bool
}

// Execute runs pcal with the process's arguments and standard streams and
// returns the exit code for main to pass to os.Exit.
func Execute() int {
	return Run(os.Args[1:], Streams{In: os.Stdin, Out: os.Stdout, Err: os.Stderr})
}

// Run runs pcal with args (not including the program name) and returns the
// exit code. Every run builds a fresh command tree, so runs share no state.
func Run(args []string, streams Streams) int {
	root := newRootCmd(&globalOptions{})
	root.SetArgs(args)
	root.SetIn(streams.In)
	root.SetOut(streams.Out)
	root.SetErr(streams.Err)

	if err := root.Execute(); err != nil {
		_, _ = fmt.Fprintln(streams.Err, "Error:", err)
		return 1
	}
	return 0
}

// newRootCmd builds the `pcal` command with its persistent flags.
func newRootCmd(opts *globalOptions) *cobra.Command {
	root := &cobra.Command{
		Use:   "pcal",
		Short: "Manage the Promise Church calendar from the terminal",
		Long: "pcal manages events, tags, RSVPs, images, and tokens on the Promise Church\n" +
			"calendar. It prints tables in a terminal and a JSON envelope when piped.",
		// The root runs only to show help. NoArgs makes a stray word an
		// "unknown command" error; without it, cobra accepts any argument on a
		// command with no subcommands.
		Args: cobra.NoArgs,
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
	flags.BoolVar(&opts.json, "json", false, "print the JSON envelope (the default when stdout isn't a terminal)")
	flags.BoolVar(&opts.quiet, "quiet", false, "print only the data, without the envelope")
	flags.StringVar(&opts.jq, "jq", "", "filter the JSON envelope with a jq `expression` (implies --json)")
	flags.StringVar(&opts.url, "url", "", "calendar `URL` (default https://calendar.promisechurch.io, or $PCAL_URL)")
	flags.BoolVar(&opts.yes, "yes", false, "answer yes to confirmation prompts")

	return root
}
