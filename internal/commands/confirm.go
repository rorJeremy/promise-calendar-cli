package commands

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/rorJeremy/promise-calendar-cli/internal/prompt"
)

// confirmNo asks question, opening on No. Esc, Ctrl-C, and SIGTERM cancel
// the same way a tag delete does, including Esc on the accessible line
// (Phase 9). canceled and terminated are that command's own errors, not
// the login's. The caller has already checked that stdin and stderr are
// both terminals.
func (a *app) confirmNo(cmd *cobra.Command, question string, canceled, onTerminated error) (bool, error) {
	ctx, stop := a.interruptible(cmd.Context())
	defer stop()
	ok, err := a.prompter().Confirm(ctx, question, false)
	if err != nil {
		if errors.Is(err, prompt.ErrCanceled) {
			if terminated(ctx) {
				return false, onTerminated
			}
			return false, canceled
		}
		return false, err
	}
	return ok, nil
}
