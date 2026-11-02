package commands

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/rorJeremy/promise-calendar-cli/internal/auth"
	"github.com/rorJeremy/promise-calendar-cli/internal/client"
	"github.com/rorJeremy/promise-calendar-cli/internal/output"
)

var errTokenRevokeCanceled = &output.Error{
	Code:    output.CodeInterrupted,
	Message: "Canceled. Nothing was deleted.",
}

var errTokenRevokeTerminated = &output.Error{
	Code:    output.CodeTerminated,
	Message: "Canceled. Nothing was deleted.",
}

func newTokensCmd(a *app) *cobra.Command {
	cmd := newGroupCmd("tokens", "List and revoke personal access tokens")
	cmd.AddCommand(newTokensListCmd(a), newTokensRevokeCmd(a))
	return cmd
}

func newTokensListCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List your access tokens",
		Long:  "List your personal access tokens, newest first. The token values aren't shown.",
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
			tokens, err := a.fetchTokens(cmd, s)
			if err != nil {
				return err
			}
			return a.printer.Print(output.Result{
				Data:    tokens,
				Summary: countSummary(len(tokens), "token", "tokens"),
				Table:   tokensTable(tokens, loc, a.now()),
			})
		},
	}
	cmd.Flags().BoolVar(&a.flags.csv, "csv", false, "print the list as CSV")
	return cmd
}

func newTokensRevokeCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "revoke ID",
		Short: "Revoke an access token",
		Long: "Revoke one of your personal access tokens. ID is the token's ID,\n" +
			"not its description. pcal asks first unless --yes is set. The\n" +
			"question opens on No. If the token was the one pcal is using, a\n" +
			"stored token is forgotten too.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := a.requirePrompt("revoking a token"); err != nil {
				return err
			}
			s, err := a.open()
			if err != nil {
				return err
			}
			tokens, err := a.fetchTokens(cmd, s)
			if err != nil {
				return err
			}
			token, ok := findToken(tokens, args[0])
			if !ok {
				return &output.Error{
					Code:    output.CodeNotFound,
					Message: "no token " + displayArg(args[0]),
					Hint:    "Run `pcal tokens list`.",
				}
			}
			if !a.flags.yes {
				ok, err := a.confirmNo(cmd, fmt.Sprintf("Revoke %s?", strconv.Quote(tokenLabel(token))), errTokenRevokeCanceled, errTokenRevokeTerminated)
				if err != nil {
					return err
				}
				if !ok {
					return a.printer.Print(output.Result{
						Data:    deletion{Deleted: false},
						Summary: "Nothing was deleted.",
					})
				}
			}
			if err := s.client.Delete(cmd.Context(), "/my/access_tokens/"+url.PathEscape(token.ID), nil); err != nil {
				return err
			}
			summary := fmt.Sprintf("Revoked %s.", strconv.Quote(tokenLabel(token)))
			summary += a.ownTokenNote(cmd, s)
			return a.printer.Print(output.Result{
				Data:    deletion{Deleted: true},
				Summary: summary,
			})
		},
	}
}

func (a *app) fetchTokens(cmd *cobra.Command, s *session) ([]client.AccessToken, error) {
	tokens := []client.AccessToken{}
	if err := s.client.Get(cmd.Context(), "/my/access_tokens", nil, &tokens); err != nil {
		return nil, err
	}
	if tokens == nil {
		tokens = []client.AccessToken{}
	}
	return tokens, nil
}

func findToken(tokens []client.AccessToken, id string) (client.AccessToken, bool) {
	for _, token := range tokens {
		if token.ID == id {
			return token, true
		}
	}
	return client.AccessToken{}, false
}

func tokenLabel(token client.AccessToken) string {
	if token.Description != "" {
		return token.Description
	}
	return token.ID
}

// ownTokenNote checks whether the token just revoked was the one this
// command used. A 401 means it was. A stored token is forgotten; PCAL_TOKEN
// can't be, so the note says to unset it. Any other failure of the check
// is ignored (Jeremy's Gate 4 decision).
func (a *app) ownTokenNote(cmd *cobra.Command, s *session) string {
	var id client.Identity
	err := s.client.Get(cmd.Context(), "/my/identity", nil, &id)
	if err == nil {
		return ""
	}
	var oe *output.Error
	if !errors.As(err, &oe) || oe.Code != output.CodeAuth {
		return ""
	}
	if s.credential.Source == auth.SourceEnv {
		return " That was the token pcal was using. Unset PCAL_TOKEN."
	}
	if err := a.store.Delete(s.baseURL); err != nil {
		a.warnf("couldn't forget the stored token: %v", err)
		return ""
	}
	return " That was the token pcal was using, so you're logged out here as well."
}

func tokensTable(tokens []client.AccessToken, loc *time.Location, now time.Time) *output.Table {
	rows := make([][]string, len(tokens))
	for i, token := range tokens {
		rows[i] = []string{
			token.Description,
			token.Permission,
			formatStamp(stringOrEmpty(token.LastUsedAt), loc, now),
			formatStamp(token.CreatedAt, loc, now),
			token.ID,
		}
	}
	return &output.Table{Headers: []string{"Description", "Permission", "Last used", "Created", "ID"}, Rows: rows}
}

func formatStamp(raw string, loc *time.Location, now time.Time) string {
	if raw == "" {
		return ""
	}
	clock, ok := formatClock(&raw, loc)
	if !ok {
		return raw
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return raw
		}
	}
	return formatWhen(parsed.In(loc), now) + " " + clock
}
