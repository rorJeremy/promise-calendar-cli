package commands

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/rorJeremy/promise-calendar-cli/internal/auth"
	"github.com/rorJeremy/promise-calendar-cli/internal/client"
	"github.com/rorJeremy/promise-calendar-cli/internal/output"
	"github.com/rorJeremy/promise-calendar-cli/internal/prompt"
)

// loginTimeout bounds how long a browser login waits for the person to
// approve pcal, or to paste the address: the calendar keeps a code for 5
// minutes.
const loginTimeout = 5 * time.Minute

// revokeTimeout bounds revoking a token after a login: the one it replaced,
// or a new one it won't keep. The person is waiting for that cleanup, so
// it's shorter than a request's 30 seconds.
const revokeTimeout = 10 * time.Second

// pipeHint says how to give `--with-token` its token.
const pipeHint = "Pipe the token in: `echo … | pcal auth login --with-token`."

// pasteAgainHint is the hint when the paste flow has to start over.
const pasteAgainHint = "Run `pcal login` again, and paste the whole address from the browser's address bar."

// loginOptions are the login command's flags.
type loginOptions struct {
	withToken bool
	readOnly  bool
	remote    bool
	local     bool
	noBrowser bool
}

// check rejects flags that can't go together.
func (o loginOptions) check() error {
	if o.withToken && (o.readOnly || o.remote || o.local || o.noBrowser) {
		return output.Usagef("--with-token can't be used with --read-only, --remote, --local, or --no-browser")
	}
	if o.remote && o.local {
		return output.Usagef("--remote and --local can't be used together")
	}
	return nil
}

// newLoginCmd builds the login command as use. `pcal login` and `pcal auth
// login` are the same command (Jeremy's Gate 3 decision; basecamp-cli
// builds both from buildLoginCmd).
func newLoginCmd(a *app, use string) *cobra.Command {
	var opts loginOptions
	cmd := &cobra.Command{
		Use:   use,
		Short: "Log in to the calendar",
		Long: "Log in to the calendar in your browser, and keep the token in the system\n" +
			"keyring.\n\n" +
			"pcal opens the calendar's page asking you to approve it, and waits up to 5\n" +
			"minutes for the browser to come back. It always prints the link too. Over\n" +
			"SSH, in CI, or on Linux without a display, it doesn't open a browser:\n" +
			"open the link on any device, then paste the address the browser ended up\n" +
			"on (it won't load, but it holds the login code). --remote asks for that\n" +
			"flow, and --local turns the detection off.\n\n" +
			"With --with-token, pcal reads a personal access token from stdin instead\n" +
			"(create one at /my/access_tokens on the calendar). In a terminal it asks\n" +
			"for it without showing it. There's no flag that takes the token itself,\n" +
			"because it would land in your shell history.\n\n" +
			"Press Ctrl-C to cancel.",
		Example: "  pcal login\n  pcal login --read-only\n  pcal login --remote\n  pbpaste | pcal login --with-token",
		Args:    noTokenArgument,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := opts.check(); err != nil {
				return err
			}
			if opts.withToken {
				return a.loginWithToken(cmd.Context())
			}
			return a.loginWithBrowser(cmd.Context(), opts)
		},
	}
	flags := cmd.Flags()
	flags.BoolVar(&opts.withToken, "with-token", false, "read a personal access token from stdin")
	flags.BoolVar(&opts.readOnly, "read-only", false, "ask for a token that can only read")
	flags.BoolVar(&opts.remote, "remote", false, "don't open a browser; open the link anywhere and paste where it ends up")
	flags.BoolVar(&opts.local, "local", false, "open a browser here, even over SSH or without a display")
	flags.BoolVar(&opts.noBrowser, "no-browser", false, "print the link without opening a browser")
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

	sigCtx, stop := a.interruptible(ctx)
	defer stop()
	token, err := a.readToken(sigCtx)
	if err != nil {
		return err
	}
	if err := auth.CheckToken(token); err != nil {
		return err
	}
	id, err := a.fetchIdentity(sigCtx, a.newClient(baseURL, token))
	if sigCtx.Err() != nil {
		return loginStopped(sigCtx)
	}
	if err != nil {
		return rejectedToken(err, baseURL)
	}
	// From here the token is being stored, so Ctrl-C ends pcal the usual
	// way rather than claiming nothing was stored.
	stop()
	if err := a.storeLogin(ctx, baseURL, auth.Credential{Token: token, Source: auth.SourceImported}); err != nil {
		return err
	}
	return a.reportLogin(baseURL, id)
}

// readToken reads a token: at a masked prompt when stdin is a terminal,
// otherwise all of stdin. Either way it's trimmed, and must not be empty.
// The prompt is drawn on stderr, so when stderr isn't a terminal pcal
// refuses instead of waiting behind a prompt no one can see (as
// basecamp-cli's tui.ErrNotInteractive).
func (a *app) readToken(ctx context.Context) (string, error) {
	if a.streams.InTTY {
		if !a.streams.ErrTTY {
			return "", &output.Error{
				Code:    output.CodeUsage,
				Message: "stdin is a terminal but stderr isn't, so pcal can't show the prompt",
				Hint:    "Pipe the token in instead (`pcal login --with-token < token.txt`), or keep stderr on the terminal, where the prompt is drawn.",
			}
		}
		token, err := a.prompter().Password(ctx, "Paste your pcal token (it won't be shown)")
		if errors.Is(err, prompt.ErrCanceled) {
			return "", loginStopped(ctx)
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

// readAll reads r to the end, or until ctx ends (Ctrl-C or SIGTERM while a
// pipe is still open).
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
		return nil, loginStopped(ctx)
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

// loginWithBrowser logs in with the calendar's browser flow: OAuth's
// authorization code with PKCE and a loopback redirect, or, when no browser
// here can reach the person, the paste-the-callback flow. The code is
// redeemed for a token, which is checked with /my/identity and then stored
// (source browser). Ctrl-C before the token is stored cancels the login;
// a new token the login doesn't keep, for that or any other reason, is
// revoked (Jeremy's Gate 3c decisions; basecamp-cli's verifyAndStore and
// discardGrant).
func (a *app) loginWithBrowser(ctx context.Context, opts loginOptions) error {
	if auth.EnvToken(a.getenv) != "" {
		return errPCALTokenSet
	}
	baseURL, err := a.baseURL()
	if err != nil {
		return err
	}
	pkce, err := auth.NewPKCE()
	if err != nil {
		return err
	}
	l := browserLogin{baseURL: baseURL, pkce: pkce, device: a.deviceName(), permission: "write", wait: a.loginWait}
	if opts.readOnly {
		l.permission = "read"
	}
	if l.wait == 0 {
		l.wait = loginTimeout
	}
	remote, reason := opts.remote, ""
	if !remote && !opts.local {
		reason = auth.HeadlessReason(a.getenv, a.platform())
		remote = reason != ""
	}

	sigCtx, stop := a.interruptible(ctx)
	defer stop()
	waitCtx, cancel := context.WithTimeout(sigCtx, l.wait)
	defer cancel()

	var code string
	if remote {
		code, err = a.pasteCallback(waitCtx, l, reason)
	} else {
		code, err = a.listenForCallback(waitCtx, l, opts.noBrowser)
	}
	if sigCtx.Err() != nil {
		return loginStopped(sigCtx)
	}
	if err != nil {
		return err
	}

	// Ctrl-C doesn't cut the exchange off: once the code is sent, the
	// server may mint a token, and an answer pcal stopped listening for
	// would leave it live with no record of it. The client's timeout still
	// bounds it.
	tok, err := auth.Exchange(context.WithoutCancel(sigCtx), a.newClient(baseURL, ""), code, pkce.Verifier)
	if sigCtx.Err() != nil {
		if err != nil {
			return loginStopped(sigCtx)
		}
		return a.stoppedWithANewToken(ctx, sigCtx, baseURL, tok.Token)
	}
	if err != nil {
		return err
	}
	// Check the new token before storing it, as the token login does, so
	// a login that fails here leaves the old one as it was.
	id, err := a.fetchIdentity(sigCtx, a.newClient(baseURL, tok.Token))
	if sigCtx.Err() != nil {
		return a.stoppedWithANewToken(ctx, sigCtx, baseURL, tok.Token)
	}
	if err != nil {
		a.discard(ctx, baseURL, tok.Token)
		return err
	}
	// From here the token is being stored, so Ctrl-C ends pcal the usual
	// way rather than claiming nothing was stored.
	stop()
	if err := a.storeLogin(ctx, baseURL, auth.Credential{Token: tok.Token, Source: auth.SourceBrowser}); err != nil {
		a.discard(ctx, baseURL, tok.Token)
		return err
	}
	return a.reportLogin(baseURL, id)
}

// stoppedWithANewToken reports a browser login that sigCtx's Ctrl-C (or
// SIGTERM) stopped after the server minted token: it's revoked, and the
// message says whether that worked. pcal exits 130 (or 143) either way.
func (a *app) stoppedWithANewToken(ctx, sigCtx context.Context, baseURL, token string) error {
	code, stopped := output.CodeInterrupted, "canceled"
	if terminated(sigCtx) {
		code, stopped = output.CodeTerminated, "terminated"
	}
	if err := a.revokeBriefly(ctx, baseURL, token); err != nil {
		return &output.Error{
			Code:    code,
			Message: fmt.Sprintf("Login %s, but pcal couldn't revoke the new token (%v); revoke it at %s/my/access_tokens.", stopped, err, baseURL),
		}
	}
	return &output.Error{Code: code, Message: "Login " + stopped + ". The new token was revoked."}
}

// discard revokes token, a new one the login failed to keep, before the
// login returns its error; failing that, it warns that the token may be
// live.
func (a *app) discard(ctx context.Context, baseURL, token string) {
	if err := a.revokeBriefly(ctx, baseURL, token); err != nil {
		a.warnf("couldn't revoke the new token (%v); revoke it at %s/my/access_tokens", err, baseURL)
	}
}

// browserLogin is what one browser login asks the calendar for.
type browserLogin struct {
	baseURL    string
	pkce       auth.PKCE
	device     string
	permission string
	wait       time.Duration
}

// link returns the authorization page's URL, sending the browser back to
// port.
func (l browserLogin) link(port int) string {
	return auth.AuthorizationURL(l.baseURL, port, l.pkce, l.device, l.permission)
}

// listenForCallback listens on 127.0.0.1, prints the link and (unless
// noBrowser) opens it, and waits for the browser to come back with a code.
// A browser that won't open isn't a failure: the link is right there.
func (a *app) listenForCallback(ctx context.Context, l browserLogin, noBrowser bool) (string, error) {
	srv, err := auth.ListenForCallback(l.pkce.State)
	if err != nil {
		return "", err
	}
	defer func() { _ = srv.Close() }()
	if err := auth.CheckPort(srv.Port()); err != nil {
		return "", err
	}

	link := l.link(srv.Port())
	a.say("Open this link to log in:")
	a.say("  " + link)
	if !noBrowser {
		if err := a.openLink(link); err != nil {
			a.say("Couldn't open a browser. Open the link above.")
		} else {
			a.say("Opening your browser… If nothing appears, open the link above.")
		}
	}
	a.say("Waiting for you to approve pcal in the browser (up to " + describeWait(l.wait) + ")…")

	code, err := srv.Wait(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		return "", loginTimedOut("timed out waiting for you to approve pcal in the browser")
	}
	return code, err
}

// pasteCallback is the paste-the-callback flow (as basecamp-cli's
// readCallbackInput): print the link for any device, then read the address
// the browser ended up on, which fails to load (nothing listens there) but
// holds the code and state. Text that isn't such an address gets one more
// try. The code is useless without the verifier, which never leaves here.
func (a *app) pasteCallback(ctx context.Context, l browserLogin, reason string) (string, error) {
	port, err := freePort()
	if err != nil {
		return "", err
	}
	if reason != "" {
		a.say("Not opening a browser here (" + reason + "). Open this link on any device:")
	} else {
		a.say("Open this link on any device:")
	}
	a.say("  " + l.link(port))
	a.say("After you approve pcal, your browser goes to an address starting with http://127.0.0.1 that won't load. That's expected.")

	lines := bufio.NewReader(a.streams.In)
	for try := 1; ; try++ {
		_, _ = fmt.Fprint(a.streams.Err, "Paste the address your browser ended up on: ")
		line, err := readLine(ctx, lines)
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			return "", loginTimedOut("timed out waiting for the address your browser ended up on")
		case errors.Is(err, io.EOF):
			_, _ = fmt.Fprintln(a.streams.Err)
			return "", &output.Error{Code: output.CodeUsage, Message: "no address was pasted", Hint: pasteAgainHint}
		case err != nil:
			return "", err
		}

		code, err := auth.ParsePastedCallback(line, l.pkce.State)
		if !errors.Is(err, auth.ErrNotACallback) {
			return code, err
		}
		if try == 2 {
			return "", &output.Error{Code: output.CodeUsage, Message: "that isn't the address your browser ended up on", Hint: pasteAgainHint}
		}
		a.say("That isn't the address: it starts with http://127.0.0.1 and has code= in it. Try once more.")
	}
}

// readLine reads one line from r, or returns ctx's error if ctx ends
// first. A read cut off by ctx is left blocked; pcal ends soon after.
func readLine(ctx context.Context, r *bufio.Reader) (string, error) {
	type result struct {
		line string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		line, err := r.ReadString('\n')
		if err == io.EOF && line != "" {
			err = nil
		}
		done <- result{line, err}
	}()
	select {
	case res := <-done:
		return res.line, res.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// freePort returns a port on 127.0.0.1 that's free now, for the paste
// flow's link. Nothing listens there afterwards: the browser's redirect to
// it is meant to fail, leaving the code in the address bar.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("finding a free port on 127.0.0.1: %w", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, auth.CheckPort(port)
}

// loginTimedOut returns the error for a browser login that waited too
// long: exit 3, start again.
func loginTimedOut(message string) error {
	return &output.Error{Code: output.CodeAuth, Message: message, Hint: "Run `pcal login` again."}
}

// storeLogin stores c for baseURL. A token an earlier `pcal login` minted
// is revoked once the new one is stored, so it doesn't stay live on the
// server (Jeremy's Gate 3 decision); an imported one is only replaced,
// since whoever created it manages it. The login has worked by then, so a
// revoke that fails, takes too long, or is stopped with Ctrl-C is only a
// warning (Gate 3c).
func (a *app) storeLogin(ctx context.Context, baseURL string, c auth.Credential) error {
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
		if err := a.revokeBriefly(ctx, baseURL, old.Token); err != nil {
			a.warnf("couldn't revoke the token from your previous `pcal login` (%v); revoke it at %s/my/access_tokens", err, baseURL)
		}
	}
	return nil
}

// revokeBriefly revokes token for cleanup after a login, giving up after
// revokeTimeout, or at Ctrl-C or SIGTERM. Its error says why, in words a
// warning can quote.
func (a *app) revokeBriefly(ctx context.Context, baseURL, token string) error {
	wait := a.revokeWait
	if wait == 0 {
		wait = revokeTimeout
	}
	sigCtx, stop := a.interruptible(ctx)
	defer stop()
	reqCtx, cancel := context.WithTimeout(sigCtx, wait)
	defer cancel()

	err := a.revoke(reqCtx, baseURL, token)
	switch {
	case err == nil:
		return nil
	case terminated(sigCtx):
		return errors.New("stopped by SIGTERM")
	case sigCtx.Err() != nil:
		return errors.New("canceled with Ctrl-C")
	case errors.Is(reqCtx.Err(), context.DeadlineExceeded):
		// The client would say it timed out after its own 30 seconds.
		return fmt.Errorf("no answer within %s", describeWait(wait))
	default:
		return err
	}
}

// reportLogin prints who's logged in, after a note when --url picked the
// calendar, since pcal never saves it (Jeremy's Gate 3 decision).
func (a *app) reportLogin(baseURL string, id client.Identity) error {
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

// deviceName names this computer for the token ("pcal on <device>"): its
// hostname, or "unknown host", since the server needs one.
func (a *app) deviceName() string {
	hostname := os.Hostname
	if a.hostname != nil {
		hostname = a.hostname
	}
	name, err := hostname()
	if name = strings.TrimSpace(name); err != nil || name == "" {
		return "unknown host"
	}
	return name
}

// platform returns the OS (as runtime.GOOS names it), for HeadlessReason.
func (a *app) platform() string {
	if a.goos != "" {
		return a.goos
	}
	return runtime.GOOS
}

// openLink opens target in the person's browser.
func (a *app) openLink(target string) error {
	if a.openBrowser != nil {
		return a.openBrowser(target)
	}
	return auth.OpenBrowser(target)
}

// say prints a line for the person on stderr.
func (a *app) say(line string) {
	_, _ = fmt.Fprintln(a.streams.Err, line)
}
