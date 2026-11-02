package commands

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/rorJeremy/promise-calendar-cli/internal/client"
	"github.com/rorJeremy/promise-calendar-cli/internal/output"
)

var errRotateCanceled = &output.Error{
	Code:    output.CodeInterrupted,
	Message: "Canceled. The website token wasn't changed.",
}

var errRotateTerminated = &output.Error{
	Code:    output.CodeTerminated,
	Message: "Canceled. The website token wasn't changed.",
}

// rotation is the data of a rotate the person declined.
type rotation struct {
	Rotated bool `json:"rotated"`
}

func newWebsiteTokenCmd(a *app) *cobra.Command {
	cmd := newGroupCmd("website-token", "Show or rotate the website token")
	cmd.AddCommand(newWebsiteTokenShowCmd(a), newWebsiteTokenRotateCmd(a))
	return cmd
}

func newWebsiteTokenShowCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show the website token's status",
		Long:  "Show whether the church website's API token exists. The token value isn't shown.",
		Args:  unknownCommand,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.open()
			if err != nil {
				return err
			}
			loc, err := a.calendarLocation(cmd, s)
			if err != nil {
				return err
			}
			token, err := a.fetchWebsiteToken(cmd, s)
			if err != nil {
				return err
			}
			summary := "No website token."
			if token.Exists {
				summary = "The website token exists."
			}
			return a.printer.Print(output.Result{
				Data:    token,
				Summary: summary,
				Card:    websiteTokenCard(token, loc, a.now()),
			})
		},
	}
}

func newWebsiteTokenRotateCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "rotate",
		Short: "Rotate the website token",
		Long: "Replace the church website's API token. The old one stops working\n" +
			"immediately, and the new one is shown once. pcal asks first unless\n" +
			"--yes is set. The question opens on No.",
		Args: unknownCommand,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := a.requirePrompt("rotating the website token"); err != nil {
				return err
			}
			s, err := a.open()
			if err != nil {
				return err
			}
			if !a.flags.yes {
				ok, err := a.confirmNo(cmd, "Rotate the website token? The one the church website uses stops working immediately.", errRotateCanceled, errRotateTerminated)
				if err != nil {
					return err
				}
				if !ok {
					return a.printer.Print(output.Result{
						Data:    rotation{Rotated: false},
						Summary: "The website token wasn't changed.",
					})
				}
			}
			loc, err := a.calendarLocation(cmd, s)
			if err != nil {
				return err
			}
			var created client.RotatedWebsiteToken
			if err := s.client.Post(cmd.Context(), "/api_token", nil, &created); err != nil {
				return err
			}
			a.warnf("Update the church website now. The old token stopped working.")
			return a.printer.Print(output.Result{
				Data:    created,
				Summary: "Rotated the website token.",
				Card:    rotatedTokenCard(created, loc, a.now()),
			})
		},
	}
}

func (a *app) fetchWebsiteToken(cmd *cobra.Command, s *session) (client.WebsiteToken, error) {
	var token client.WebsiteToken
	if err := s.client.Get(cmd.Context(), "/api_token", nil, &token); err != nil {
		return client.WebsiteToken{}, err
	}
	return token, nil
}

func websiteTokenCard(token client.WebsiteToken, loc *time.Location, now time.Time) *output.Card {
	exists := "no"
	if token.Exists {
		exists = "yes"
	}
	return &output.Card{Fields: []output.Field{
		{Label: "Exists", Value: exists},
		{Label: "Description", Value: stringOrEmpty(token.Description)},
		{Label: "Last used", Value: formatStamp(stringOrEmpty(token.LastUsedAt), loc, now)},
		{Label: "Created", Value: formatStamp(stringOrEmpty(token.CreatedAt), loc, now)},
	}}
}

func rotatedTokenCard(token client.RotatedWebsiteToken, loc *time.Location, now time.Time) *output.Card {
	return &output.Card{Fields: []output.Field{
		{Label: "Token", Value: token.Token},
		{Label: "Created", Value: formatStamp(token.CreatedAt, loc, now)},
	}}
}
