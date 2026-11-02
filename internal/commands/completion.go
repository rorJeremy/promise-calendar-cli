package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rorJeremy/promise-calendar-cli/internal/output"
)

func newCompletionCmd(_ *app) *cobra.Command {
	return &cobra.Command{
		Use:   "completion bash|zsh|fish",
		Short: "Print a shell completion script",
		Long: "Print a completion script for bash, zsh, or fish. Redirect it\n" +
			"into the shell's completion file. The script is the script, not a\n" +
			"JSON envelope, so it can be sourced.",
		Args: completionArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			root := cmd.Root()
			switch args[0] {
			case "bash":
				return root.GenBashCompletionV2(out, true)
			case "zsh":
				return root.GenZshCompletion(out)
			case "fish":
				return root.GenFishCompletion(out, true)
			default:
				return shellError(args[0])
			}
		},
	}
}

// completionArgs accepts exactly bash, zsh, or fish. cobra's own completion
// command prints help (exit 0) when no shell is named and also offers
// powershell. pcal names the three shells and exits 1 otherwise, so a
// redirect never captures the help page.
func completionArgs(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return &output.Error{
			Code:    output.CodeUsage,
			Message: "need a shell: bash, zsh, or fish",
			Hint:    helpHint(cmd),
		}
	}
	switch args[0] {
	case "bash", "zsh", "fish":
		return nil
	default:
		return shellError(args[0])
	}
}

func shellError(name string) error {
	return &output.Error{
		Code:    output.CodeUsage,
		Message: fmt.Sprintf("unknown shell %q", name),
		Hint:    "Use bash, zsh, or fish. Run `pcal completion --help` for usage.",
	}
}
