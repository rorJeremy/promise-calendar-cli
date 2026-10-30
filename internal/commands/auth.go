package commands

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/rorJeremy/promise-calendar-cli/internal/auth"
	"github.com/rorJeremy/promise-calendar-cli/internal/output"
)

// newAuthCmd builds `pcal auth`: login, logout, and status. `pcal auth
// login` and `pcal auth logout` are `pcal login` and `pcal logout`.
func newAuthCmd(a *app) *cobra.Command {
	cmd := newGroupCmd("auth", "Log in and out, and see how you're logged in")
	cmd.AddCommand(newLoginCmd(a, "login"), newLogoutCmd(a, "logout"), newAuthStatusCmd(a))
	return cmd
}

// authStatus is `pcal auth status`'s data.
type authStatus struct {
	URL          string `json:"url"`
	Name         string `json:"name"`
	EmailAddress string `json:"email_address"`
	Permission   string `json:"permission"`
	// Store is where the token is kept: keyring, file, or env.
	Store string `json:"store"`
	// Source is how pcal got it: browser (pcal login), imported (--with-token),
	// or env (PCAL_TOKEN).
	Source string `json:"source"`
}

// newAuthStatusCmd builds `pcal auth status`.
func newAuthStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show who you're logged in as, and where the token is kept",
		Long: "Show the calendar, who the token belongs to, what it may do (read or\n" +
			"write), and where pcal keeps it. It asks the calendar, so it also checks\n" +
			"that the token still works.",
		Args: unknownCommand,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := a.open()
			if err != nil {
				return err
			}
			id, err := a.identity(cmd.Context(), s)
			if err != nil {
				return err
			}
			dir, err := a.configDir()
			if err != nil {
				return err
			}
			return a.printer.Print(output.Result{
				Data: authStatus{
					URL: s.baseURL, Name: id.Name, EmailAddress: id.EmailAddress, Permission: id.Permission,
					Store: s.credential.Store, Source: string(s.credential.Source),
				},
				Card: &output.Card{Fields: []output.Field{
					{Label: "URL", Value: s.baseURL},
					{Label: "User", Value: id.Name + " <" + id.EmailAddress + ">"},
					{Label: "Permission", Value: id.Permission},
					{Label: "Token", Value: tokenLine(s.credential, dir)},
				}},
			})
		},
	}
}

// tokenLine says where c is kept and how pcal got it, for auth status.
func tokenLine(c auth.Credential, configDir string) string {
	var where string
	switch c.Store {
	case auth.StoreKeyring:
		where = "in the system keyring"
	case auth.StoreFile:
		where = "in " + filepath.Join(configDir, "credentials.json")
	default:
		return "from PCAL_TOKEN"
	}
	switch c.Source {
	case auth.SourceBrowser:
		return where + ", from `pcal login`"
	case auth.SourceImported:
		return where + ", from `pcal auth login --with-token`"
	default:
		return where
	}
}

// logoutResult is `pcal logout`'s data.
type logoutResult struct {
	URL string `json:"url"`
	// Source is how pcal got the token it forgot (browser or imported), or
	// empty when nothing was stored.
	Source string `json:"source,omitempty"`
	// LoggedOut is whether a stored token was forgotten.
	LoggedOut bool `json:"logged_out"`
	// Revoked is whether the server revoked it (only tokens `pcal login`
	// minted are revoked).
	Revoked bool `json:"revoked"`
}

// newLogoutCmd builds the logout command as use: `pcal logout` and `pcal
// auth logout` are the same command.
func newLogoutCmd(a *app, use string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: "Log out: revoke a token pcal login made, and forget the stored token",
		Long: "Log out of the calendar. A token `pcal login` made is revoked on the server,\n" +
			"then forgotten here. A token you gave `pcal auth login --with-token` is only\n" +
			"forgotten here, since whoever created it manages it; revoke it at\n" +
			"/my/access_tokens or with `pcal tokens revoke ID`.",
		Args: unknownCommand,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.logout(cmd.Context())
		},
	}
}

// logout revokes a token `pcal login` minted, then forgets the stored
// token (as basecamp-cli's revoke.go#LogoutCredential does). A failed
// revoke still forgets it, with a warning; a failed forget is an error
// that also says what happened on the server.
func (a *app) logout(ctx context.Context) error {
	baseURL, err := a.baseURL()
	if err != nil {
		return err
	}
	if auth.EnvToken(a.getenv) != "" {
		return &output.Error{
			Code:    output.CodeUsage,
			Message: "logged in via PCAL_TOKEN, which pcal can't log out of",
			Hint:    "Unset PCAL_TOKEN. To end that token, revoke it at " + baseURL + "/my/access_tokens.",
		}
	}
	store, err := a.credentials()
	if err != nil {
		return err
	}
	cred, err := store.Get(baseURL)
	if errors.Is(err, auth.ErrNoCredential) {
		return a.printer.Print(output.Result{Data: logoutResult{URL: baseURL}, Summary: "Not logged in"})
	}
	if err != nil {
		return err
	}

	result := logoutResult{URL: baseURL, Source: string(cred.Source), LoggedOut: true}
	var revokeErr error
	if cred.Source == auth.SourceBrowser {
		revokeErr = a.revoke(ctx, baseURL, cred.Token)
		result.Revoked = revokeErr == nil
	}
	if err := store.Delete(baseURL); err != nil {
		return forgetFailed(cred.Source, revokeErr, err)
	}

	var summary string
	switch {
	case cred.Source != auth.SourceBrowser:
		summary = "Logged out. That token is still valid; revoke it at " + baseURL +
			"/my/access_tokens or with `pcal tokens revoke ID`."
	case result.Revoked:
		summary = "Logged out; token revoked"
	default:
		a.warnf("couldn't revoke the token on the server (%v); it may still be live, so revoke it at %s/my/access_tokens", revokeErr, baseURL)
		summary = "Logged out here, but the token may still be live on the server"
	}
	return a.printer.Print(output.Result{Data: result, Summary: summary})
}

// forgetFailed reports a logout whose local forget failed with err: an API
// error (exit 7) that keeps err's hint and says what happened on the
// server first, since the token may be revoked, or live, either way.
func forgetFailed(source auth.Source, revokeErr, err error) error {
	var prefix string
	switch {
	case source != auth.SourceBrowser:
		prefix = "couldn't forget the token here: "
	case revokeErr == nil:
		prefix = "revoked the token on the server, but couldn't forget it here: "
	default:
		prefix = fmt.Sprintf("couldn't revoke the token on the server (%v), and couldn't forget it here: ", revokeErr)
	}
	e := *output.AsError(err)
	e.Code = output.CodeAPI
	e.Message = prefix + e.Message
	e.Cause = err
	return &e
}
