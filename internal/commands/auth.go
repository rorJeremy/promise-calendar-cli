package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rorJeremy/promise-calendar-cli/internal/auth"
	"github.com/rorJeremy/promise-calendar-cli/internal/client"
	"github.com/rorJeremy/promise-calendar-cli/internal/output"
	"github.com/rorJeremy/promise-calendar-cli/internal/prompt"
)

// pipeHint says how to give `--with-token` its token.
const pipeHint = "Pipe the token in: `echo … | pcal auth login --with-token`."

// newAuthCmd builds `pcal auth`: login, logout, and status.
func newAuthCmd(a *app) *cobra.Command {
	cmd := newGroupCmd("auth", "Log in and out, and see how you're logged in")
	cmd.AddCommand(newLoginCmd(a, "login"), newLogoutCmd(a, "logout"), newAuthStatusCmd(a))
	return cmd
}

// newLoginCmd builds the login command as use: `pcal auth login`, and
// later `pcal login`, which is the same command (as basecamp-cli builds
// both from buildLoginCmd).
func newLoginCmd(a *app, use string) *cobra.Command {
	var withToken bool
	cmd := &cobra.Command{
		Use:   use,
		Short: "Log in to the calendar",
		Long: "Log in to the calendar and keep the token in the system keyring.\n\n" +
			"With --with-token, pcal reads a personal access token from stdin (create one\n" +
			"at /my/access_tokens on the calendar). In a terminal it asks for it without\n" +
			"showing it. pcal checks the token with the calendar before it keeps it.\n" +
			"There's no flag that takes the token itself, because it would land in your\n" +
			"shell history.",
		Example: "  pbpaste | pcal auth login --with-token",
		Args:    noTokenArgument,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !withToken {
				return output.Usagef("browser login isn't available yet; pipe a token to `pcal auth login --with-token`")
			}
			return a.loginWithToken(cmd.Context())
		},
	}
	cmd.Flags().BoolVar(&withToken, "with-token", false, "read a personal access token from stdin")
	return cmd
}

// noTokenArgument is login's Args. A token given as an argument lands in
// shell history and the process list, so it's refused, and the error never
// repeats it (cobra.NoArgs would: unknown command "<token>").
func noTokenArgument(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return &output.Error{
		Code:    output.CodeUsage,
		Message: cmd.CommandPath() + " doesn't take a token as an argument, where it would land in your shell history",
		Hint:    "Pipe it in instead: `echo … | pcal auth login --with-token`.",
	}
}

// errPCALTokenSet refuses a login that PCAL_TOKEN would shadow: every
// later command would use PCAL_TOKEN instead of the token stored here.
var errPCALTokenSet = &output.Error{
	Code:    output.CodeUsage,
	Message: "PCAL_TOKEN is set, so pcal would keep using it instead of this login",
	Hint:    "Unset PCAL_TOKEN, then log in again.",
}

// loginWithToken reads a token from stdin, checks it with GET
// /my/identity, and stores it as imported. Ctrl-C before it's stored
// cancels the login.
func (a *app) loginWithToken(ctx context.Context) error {
	if auth.EnvToken(a.getenv) != "" {
		return errPCALTokenSet
	}
	baseURL, err := a.baseURL()
	if err != nil {
		return err
	}

	ctx, stop := a.interruptible(ctx)
	defer stop()
	token, err := a.readToken(ctx)
	if err != nil {
		return err
	}
	if err := auth.CheckToken(token); err != nil {
		return err
	}
	id, err := a.fetchIdentity(ctx, a.newClient(baseURL, token))
	if ctx.Err() != nil {
		return errCanceled
	}
	if err != nil {
		return rejectedToken(err, baseURL)
	}
	// From here the token is being stored, so Ctrl-C ends pcal the usual
	// way rather than claiming nothing was stored.
	stop()
	return a.finishLogin(baseURL, auth.Credential{Token: token, Source: auth.SourceImported}, id)
}

// readToken reads a token: at a masked prompt when stdin is a terminal,
// otherwise all of stdin. Either way it's trimmed, and must not be empty.
func (a *app) readToken(ctx context.Context) (string, error) {
	if a.streams.InTTY {
		token, err := a.prompter().Password(ctx, "Paste your pcal token (it won't be shown)")
		if errors.Is(err, prompt.ErrCanceled) {
			return "", errCanceled
		}
		if err != nil {
			return "", err
		}
		if token = strings.TrimSpace(token); token == "" {
			return "", &output.Error{Code: output.CodeUsage, Message: "no token entered", Hint: pipeHint}
		}
		return token, nil
	}

	data, err := readAll(ctx, a.streams.In)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", &output.Error{Code: output.CodeUsage, Message: "no token on stdin", Hint: pipeHint}
	}
	return token, nil
}

// readAll reads r to the end, or until ctx ends (Ctrl-C while a pipe is
// still open).
func readAll(ctx context.Context, r io.Reader) ([]byte, error) {
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(r)
		done <- result{data, err}
	}()
	select {
	case res := <-done:
		if res.err != nil {
			return nil, fmt.Errorf("reading the token from stdin: %w", res.err)
		}
		return res.data, nil
	case <-ctx.Done():
		return nil, errCanceled
	}
}

// rejectedToken words a 401 from checking a token being logged in with.
// Other errors (network, server) are returned as they are.
func rejectedToken(err error, baseURL string) error {
	var e *output.Error
	if !errors.As(err, &e) || e.Code != output.CodeAuth {
		return err
	}
	return &output.Error{
		Code:    output.CodeAuth,
		Message: "the server rejected that token",
		Hint:    "Check the token, or create one at " + baseURL + "/my/access_tokens.",
		Cause:   err,
	}
}

// finishLogin stores c for baseURL and prints who's logged in. A token an
// earlier `pcal login` minted is revoked once the new one is stored, so it
// doesn't stay live on the server; an imported one is only replaced, since
// whoever created it manages it.
func (a *app) finishLogin(baseURL string, c auth.Credential, id client.Identity) error {
	store, err := a.credentials()
	if err != nil {
		return err
	}
	// A stored token that can't be read can't be revoked either; Set
	// reports any real problem with the store.
	old, oldErr := store.Get(baseURL)
	if err := store.Set(baseURL, c); err != nil {
		return err
	}
	if oldErr == nil && old.Source == auth.SourceBrowser && old.Token != c.Token {
		if err := a.revoke(context.Background(), baseURL, old.Token); err != nil {
			a.warnf("couldn't revoke the token from your previous `pcal login` (%v); revoke it at %s/my/access_tokens", err, baseURL)
		}
	}
	if a.flags.url != "" {
		a.notef("pcal doesn't save --url, so pass --url %s (or set PCAL_URL) on other commands too", baseURL)
	}
	return a.printer.Print(output.Result{
		Data:    id,
		Summary: fmt.Sprintf("Logged in as %s (%s)", id.Name, id.Permission),
	})
}

// revoke revokes token on the server with DELETE /my/current_access_token,
// which works even for a read token. A 401 means it's already gone.
func (a *app) revoke(ctx context.Context, baseURL, token string) error {
	err := a.newClient(baseURL, token).Delete(ctx, "/my/current_access_token", nil)
	var e *output.Error
	if errors.As(err, &e) && e.Code == output.CodeAuth {
		return nil
	}
	return err
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
		Args: cobra.NoArgs,
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
		Args: cobra.NoArgs,
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
