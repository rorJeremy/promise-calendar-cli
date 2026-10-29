package commands

import (
	"github.com/spf13/cobra"

	"github.com/rorJeremy/promise-calendar-cli/internal/output"
)

// newWhoamiCmd builds `pcal whoami`.
func newWhoamiCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show who the token belongs to",
		Long:  "Show the person the token belongs to, what it may do (read or write), and the\ncalendar's time zone.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.open()
			if err != nil {
				return err
			}
			id, err := a.identity(cmd.Context(), s)
			if err != nil {
				return err
			}
			return a.printer.Print(output.Result{
				Data: id,
				Card: &output.Card{Fields: []output.Field{
					{Label: "Name", Value: id.Name},
					{Label: "Email", Value: id.EmailAddress},
					{Label: "Permission", Value: id.Permission},
					{Label: "Time zone", Value: id.TimeZone},
					{Label: "ID", Value: id.ID},
				}},
			})
		},
	}
}
