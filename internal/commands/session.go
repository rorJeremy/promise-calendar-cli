package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/rorJeremy/promise-calendar-cli/internal/auth"
	"github.com/rorJeremy/promise-calendar-cli/internal/client"
	"github.com/rorJeremy/promise-calendar-cli/internal/config"
	"github.com/rorJeremy/promise-calendar-cli/internal/output"
	"github.com/rorJeremy/promise-calendar-cli/internal/prompt"
)

// credentialStore is what the commands use of *auth.Store. Tests wrap a
// real store to make one call fail.
type credentialStore interface {
	Resolve(baseURL string) (auth.Credential, error)
	Get(baseURL string) (auth.Credential, error)
	Set(baseURL string, c auth.Credential) error
	Delete(baseURL string) error
}

// errCanceled is how a command reports that the person canceled it with
// Ctrl-C or Esc: execute prints the message alone, and pcal exits 130.
var errCanceled = &output.Error{Code: output.CodeInterrupted, Message: "Login canceled. Nothing was stored."}

// configDir returns pcal's config directory.
func (a *app) configDir() (string, error) {
	return config.Dir(a.getenv)
}

// baseURL returns the calendar's base URL: --url, PCAL_URL, the saved
// config, or the default.
func (a *app) baseURL() (string, error) {
	dir, err := a.configDir()
	if err != nil {
		return "", err
	}
	cfg, err := config.Load(dir)
	if err != nil {
		return "", err
	}
	return config.ResolveURL(a.flags.url, a.getenv, cfg)
}

// credentials returns the credential store, opening the one in pcal's
// config directory the first time.
func (a *app) credentials() (credentialStore, error) {
	if a.store != nil {
		return a.store, nil
	}
	dir, err := a.configDir()
	if err != nil {
		return nil, err
	}
	a.store = auth.NewStore(dir, a.getenv, a.streams.Err)
	return a.store, nil
}

// newClient returns a client for baseURL that sends token. With --verbose
// it logs each request line and response status to stderr.
func (a *app) newClient(baseURL, token string) *client.Client {
	opts := a.clientOptions
	if a.flags.verbose {
		opts.Verbose = a.streams.Err
	}
	return client.New(baseURL, token, opts)
}

// session is a calendar and the credential to call it with.
type session struct {
	baseURL    string
	credential auth.Credential
	client     *client.Client
}

// open resolves the calendar and its credential (PCAL_TOKEN, or the stored
// one) and returns a session for them. Nothing stored is "not logged in"
// (exit 3).
func (a *app) open() (*session, error) {
	baseURL, err := a.baseURL()
	if err != nil {
		return nil, err
	}
	store, err := a.credentials()
	if err != nil {
		return nil, err
	}
	cred, err := store.Resolve(baseURL)
	if err != nil {
		return nil, err
	}
	return &session{baseURL: baseURL, credential: cred, client: a.newClient(baseURL, cred.Token)}, nil
}

// identity calls GET /my/identity with s's credential and caches the
// calendar's time zone. A rejected PCAL_TOKEN gets a hint about it, since
// `pcal login` can't fix that.
func (a *app) identity(ctx context.Context, s *session) (client.Identity, error) {
	id, err := a.fetchIdentity(ctx, s.client)
	var e *output.Error
	if err != nil && s.credential.Source == auth.SourceEnv && errors.As(err, &e) && e.Code == output.CodeAuth {
		rejected := *e
		rejected.Hint = "The server rejected PCAL_TOKEN. Unset it, or set it to a valid token."
		return client.Identity{}, &rejected
	}
	return id, err
}

// fetchIdentity calls GET /my/identity with c and caches the calendar's
// time zone, which commands that convert times need.
func (a *app) fetchIdentity(ctx context.Context, c *client.Client) (client.Identity, error) {
	var id client.Identity
	if err := c.Get(ctx, "/my/identity", nil, &id); err != nil {
		return client.Identity{}, err
	}
	a.cacheTimeZone(id.TimeZone)
	return id, nil
}

// cacheTimeZone saves tz in config.json if it isn't there already. The
// cache is a convenience, so a failure to save it is a warning.
func (a *app) cacheTimeZone(tz string) {
	if tz == "" {
		return
	}
	dir, err := a.configDir()
	if err == nil {
		var cfg config.Config
		if cfg, err = config.Load(dir); err == nil {
			if cfg.TimeZone == tz {
				return
			}
			cfg.TimeZone = tz
			err = config.Save(dir, cfg)
		}
	}
	if err != nil {
		a.warnf("couldn't save the calendar's time zone in config.json: %v", err)
	}
}

// warnf prints a warning on stderr. It may quote the server, so control
// characters are shown as escapes.
func (a *app) warnf(format string, args ...any) {
	_, _ = fmt.Fprintln(a.streams.Err, "warning: "+output.Visible(fmt.Sprintf(format, args...)))
}

// notef prints a note on stderr, with control characters shown as escapes.
func (a *app) notef(format string, args ...any) {
	_, _ = fmt.Fprintln(a.streams.Err, "note: "+output.Visible(fmt.Sprintf(format, args...)))
}

// interruptible returns a context that ends when the person presses Ctrl-C
// (SIGINT), and the function that stops listening for it. While it's
// listening, Ctrl-C doesn't kill pcal, so a login can say it was canceled.
func (a *app) interruptible(ctx context.Context) (context.Context, context.CancelFunc) {
	if a.interrupt != nil {
		return a.interrupt(ctx)
	}
	return signal.NotifyContext(ctx, os.Interrupt)
}

// prompter returns a Prompter that reads stdin and draws on stderr.
func (a *app) prompter() prompt.Prompter {
	return prompt.New(a.streams.In, a.streams.Err, a.getenv)
}

// describeWait says how long d is, for "up to 5 minutes": whole minutes
// in words, anything else as Go writes it.
func describeWait(d time.Duration) string {
	switch {
	case d == time.Minute:
		return "1 minute"
	case d > time.Minute && d%time.Minute == 0:
		return fmt.Sprintf("%d minutes", d/time.Minute)
	default:
		return d.String()
	}
}
