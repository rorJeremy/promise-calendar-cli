# pcal — Promise Church Calendar CLI — Design

- **Date:** 2026-09-28
- **Status:** Approved design; reconciled with the shipped API on 2026-10-27,
  then audited against primary sources (Gate 1b, 2026-10-27). The Phase 1–5
  code was audited (Gate 2b, 2026-10-27) and its findings fixed (Gate 2c,
  2026-10-28); Jeremy's decisions from that audit are recorded below
  (Storage, Precedence, Output) and in the plan's header, as are his Gate 3
  decisions on login and logout (Auth) and input parsing (Input
  conveniences), 2026-10-28.
- **Server-side companion:** `promise-church-calendar` →
  `docs/plans/2026-09-28-cli-api-design.md` (Planning Center removal, JSON API,
  token auth). PR 2 (#112) is merged and deployed. The contract is the
  "# Staff API" section of that repo's `docs/api.md` plus its golden JSON in
  `test/fixtures/files/api/`; where this doc and those differ, they win.

## Goal

`pcal` gives church staff full admin control of
https://calendar.promisechurch.io from the terminal: events, tags, RSVPs, images,
and tokens. It is readable for humans and predictable for scripts and AI agents.
Modeled on [fizzy-cli](https://github.com/basecamp/fizzy-cli) and
[basecamp-cli](https://github.com/basecamp/basecamp-cli).

## Decisions

| Decision | Choice |
|---|---|
| Language | Go. Workload is network-bound; Go gives fast iteration, stdlib HTTP, cobra, goreleaser, and a reference codebase |
| Libraries | `spf13/cobra`, `charm.land/huh/v2` (v2, whose bubbletea doesn't query the terminal at startup; Gate 3), `charmbracelet/lipgloss`, `zalando/go-keyring`, `gofrs/flock` (the credentials file lock, as basecamp-cli), `itchyny/gojq`, `stretchr/testify` |
| Shared toolkit | Do **not** depend on `basecamp/cli` (pre-1.0, multi-account machinery we don't need). Copy its conventions |
| Login | `pcal login` browser flow (PKCE + localhost callback, paste-the-callback fallback when headless); `pcal auth login --with-token` (stdin) for CI/agents |
| Input | Flags for everything; huh prompts for missing required fields only when stdin is a TTY |
| Output | Tables in a TTY; fizzy-style JSON envelope when piped or `--json` |
| Distribution | goreleaser → GitHub Releases + `rorJeremy/homebrew-tap`; `go install` and `mise use github:…` work |
| Dev toolchain | mise (`mise.toml` pins Go, golangci-lint, goreleaser; `mise.lock` pins their artifacts; CI uses `jdx/mise-action` with `MISE_LOCKED=1`) |

**Out of scope (v1):** profiles/multiple servers, MCP server, TUI, self-update,
signed releases, curl installer, breadcrumbs in the envelope.

## Layout

```
cmd/pcal/main.go
internal/
  client/     HTTP: base URL, bearer token, Accept: application/json,
              multipart uploads, HTTP status → typed errors
  auth/       browser login (listener, state, PKCE), credential storage
  config/     ~/.config/pcal/config.json; env + flag precedence
  commands/   one file per command group + *_test.go
  output/     envelope, table/JSON/CSV renderers, --jq, exit codes
  prompt/     huh forms for missing fields (TTY only)
  parse/      natural dates/times, --repeat → RRULE
  version/    version/commit/date via ldflags
skills/pcal/SKILL.md      embedded Claude Code skill
.goreleaser.yaml
.github/workflows/test.yml, release.yml
Makefile
```

## Commands

```
pcal login [--read-only] [--url URL] [--remote | --local] [--no-browser]
pcal logout                                   # revokes a login-minted token, then forgets it locally
pcal auth login --with-token  |  auth status  # token read from stdin, never an argument
pcal whoami

pcal events list [--from D --to D --tag NAME... --search Q --unpublished --website]
pcal events show ID
pcal events create [--title --date --end-date --start 10am --end 12pm --all-day
                    --repeat "weekly on sun" | --rrule RAW
                    --location-name --address --map-url
                    --tag NAME... --description | --description-file F --notes
                    --image F | --image-url URL  --image-alt
                    --website --rsvp --draft]
pcal events update ID [same flags]
pcal events delete ID [--yes]
pcal events publish|unpublish ID
pcal events website on|off ID
pcal events rsvp on|off ID
pcal events image set ID (FILE | --url URL) [--alt TEXT]   # an upload or a link; either replaces the other
pcal events image rm ID

pcal tags list | create NAME [--color] | update ID [...] | delete ID [--yes]
pcal rsvps list EVENT_ID [--date D] [--csv]
pcal rsvps add EVENT_ID --name N --email E [--phone --date]
pcal rsvps delete EVENT_ID RSVP_ID [--yes]
pcal tokens list | revoke ID [--yes]
pcal website-token show | rotate [--yes]

pcal version | completion {bash,zsh,fish} | skill install
```

### Input conveniences

- Dates: `2026-10-04`, `today`, `tomorrow`, `sat`, `next sun`. Times: `10am`,
  `6:30pm`, `18:30`. Converted to ISO 8601 in the app's time zone
  (`time_zone` from `/my/identity`; the app uses America/Chicago). The server
  stores times separately from dates, so the CLI always puts `--start` on the
  event's start date and `--end` on its end date, and an update that moves a
  timed event moves its times too.
- `--repeat`: `daily`, `weekly on sun`, `every 2 weeks on tue,thu`,
  `monthly on the 1st sun` → RRULE. `--rrule` passes a raw rule through; the
  server validates it (422 → exit 1).
- `--tag` takes names and resolves them to IDs via `GET /tags`; unknown name → exit 1.
  Tag names are unique only case-sensitively on the server, so an exact-case
  match wins, then a unique case-insensitive one; two that differ only by case
  → exit 8.
- `events update` sends only the flags given; a flag with an empty value
  (`--end-date ''`, `--rrule ''`, `--tag ''`) clears that field.
- Event `ID` arguments accept a UUID, an exact title, or a unique title
  prefix, all case-insensitive. An exact title wins over prefixes, so
  `Worship` stays reachable when `Worship Night` exists. The server's search
  matches anywhere in a title, so the CLI does this matching itself. Multiple
  matches → exit 8 listing candidates.
- `events list` shows occurrences: a recurring event appears once per
  occurrence, dated by the server's `occurrence_*` fields.
- `publish`, `website on`, `rsvp on`, etc. are `PATCH /events/:id` with one field.

## Auth

**Browser login:**
1. Listen on `127.0.0.1:0` (random port); generate 32-byte `state` and PKCE
   verifier; `challenge = base64url(sha256(verifier))`.
2. Open `<base>/cli/authorization/new?port=&state=&code_challenge=&device=<hostname>&permission=write|read`
   (print the URL too, in case the browser doesn't open).
3. Wait up to 5 minutes for `/callback?code=&state=`. Reject a state mismatch.
   Handle `error=access_denied`. Serve a small "You can close this tab" page.
4. `POST /cli/token {code, code_verifier}` → token. Store it, call
   `/my/identity`, print "Logged in as <name> (write)".

**Headless fallback** (as in basecamp-cli, `LoginOptions.defaults` +
`readCallbackInput`): on an SSH session, with `CI` set, or on Unix without
`DISPLAY`/`WAYLAND_DISPLAY`, or with `--remote`, don't open a browser. Print
the link, then ask the person to paste the `http://127.0.0.1:…/callback?…` URL
their browser was sent to (the page fails to load, but the address bar has
`code` and `state`). `--local` overrides the detection. The server keeps codes
for 5 minutes. A pasted code is useless without the PKCE verifier, which never
leaves this host.

**Logout** (as in basecamp-cli `revoke.go`; `pcal auth logout` is the same
command): a credential that `pcal login` minted is revoked with `DELETE
/my/current_access_token` (best effort; a network failure still forgets
locally and says the token may still be live). A token imported with
`--with-token` is only forgotten locally, because the person who created it
manages it. If the local forget fails (a keyring error), logout fails with
exit 7 and says what happened on the server too (Jeremy's Gate 3 decision).
A login that replaces a token an earlier `pcal login` minted revokes the old
one, so logging in again leaves no stray live token (Gate 3).

**Storage:** OS keyring (service `pcal`, key = base URL). The first keyring
call in a run decides where credentials live for the rest of it (Jeremy's
decision at Gate 2b). If it works, the keyring, and any later keyring
failure is an error, never a silent switch to plaintext. If it fails or
doesn't answer within 10 seconds (headless Linux; a locked macOS keychain
with no one at the prompt), `~/.config/pcal/credentials.json` with mode
`0600`, and one warning. Every keyring call is bounded at 10 seconds, as in
basecamp-cli; a later timeout says to set `PCAL_NO_KEYRING=1`, which picks
the file deliberately, without the warning. A token saved to the file
during a fallback moves into the keyring the next time a run finds the
keyring working. Writes to the file hold a lock (`credentials.lock`), so
two pcal processes can't lose each other's entries, and a file other users
can read is made `0600` again, with a warning.

**Precedence:** flag (`--url`) > env (`PCAL_URL`, `PCAL_TOKEN`) >
config/keyring. There is no `--token` flag: tokens never go in argv, where
they'd land in shell history and the process list. Use `PCAL_TOKEN` or
`--with-token`. Both logins refuse while `PCAL_TOKEN` is set, since every later command
would keep using it instead (Gate 3), and so does logout, which can't end
an environment token. pcal never saves `--url` in config.json (Gate 3);
use `--url` or `PCAL_URL` on every command for another calendar.
Default URL `https://calendar.promisechurch.io`. The URL must
be `https://`; `http://` is accepted only for localhost and loopback
addresses (127.0.0.0/8, ::1), so a token is never sent unencrypted (Jeremy's
decision at Gate 2b). It's canonicalized (lowercase host, no default port or
trailing dot), because it's also the credential key.

## Output

- **TTY:** lipgloss table for lists, key/value card for `show`. Styling is bold
  only, and off with `NO_COLOR`, or with `CLICOLOR=0` or `TERM=dumb` unless
  `CLICOLOR_FORCE` is set (termenv's rules). Control characters in anything
  shown, server text included, appear as visible escapes (`\x1b`), since some
  of it, such as RSVP names, comes from a public form (Jeremy's decision at
  Gate 2b). Prompts follow the same rule: no colors, only bold, faint,
  and reverse (Gate 3).
- **`--json` or stdout not a TTY:**
  ```json
  {"ok": true, "data": [...], "summary": "12 events Oct 1–31"}
  {"ok": false, "error": {"code": "validation", "message": "Title can't be blank", "fields": {"title": ["can't be blank"]}}}
  {"ok": false, "error": {"code": "auth_required", "message": "unauthorized", "hint": "Run `pcal login`."}}
  ```
  The success envelope matches fizzy-cli's. **The error envelope deliberately
  does not** (Jeremy's decision, 2026-10-27, at Gate 2): fizzy-cli's (from
  `basecamp/cli` v0.2.1 `output/envelope.go` `ErrorResponse`) is flat,
  `{"ok":false,"error":"<message>","code":"not_found","hint":"…"}`. pcal keeps
  the nested shape because it carries a 422's per-field messages in `fields`,
  which a flat message string can't. Like fizzy-cli's, it has a `hint` saying
  what to do next (Jeremy, at the Gate 2 review). `fields` and `hint` are
  left out when empty. Every usage error has a hint, at least "Run
  `<command> --help` for usage.", and an unknown command suggests close
  command names there (Jeremy's decisions at Gate 2b). A list that found
  nothing is `"data": []`, never `null`.
- `--jq EXPR` — built-in gojq over the envelope; implies `--json`.
- `--quiet` — bare `data`, no envelope.
- `--csv` — lists only. A cell a spreadsheet would run as a formula (starting
  with `=`, `+`, `-`, or `@`) gets a leading `'`.
- Deletes and `website-token rotate` confirm unless `--yes`. With no TTY and no
  `--yes`, they fail with exit 1.

### Exit codes (fizzy-cli's numbers; 400 and 422 → 1)

fizzy-cli maps every 4xx it doesn't name, 400 included, and 422 to exit 7
(`internal/errors/errors.go#FromHTTPStatus`). pcal treats both as invalid
input (exit 1). Decided by HTTP status alone. The body's `error` string is shown, and only
words a message (`read_only_token` → "token is read-only"); it never picks the
exit code.

| Code | Meaning |
|---|---|
| 0 | OK |
| 1 | Usage / invalid input / validation (422) |
| 2 | Not found (404) |
| 3 | Auth (401, not logged in) |
| 4 | Forbidden (403, read-only token) |
| 5 | Rate limited (429) |
| 6 | Network |
| 7 | API / server error (5xx, unexpected) |
| 8 | Ambiguous title match |

## Testing

- Table-driven unit tests: date/time parsing, `--repeat` → RRULE, title
  resolution, HTTP status → exit code.
- Command tests against `httptest.Server` using golden JSON copied from the
  Rails jbuilder output (the shared contract; refresh when the API changes).
- Browser login: drive the localhost listener directly (state mismatch,
  access_denied, timeout, success).
- CI (`test.yml`): `go vet`, `golangci-lint`, `go test -race ./...` on macOS + Linux.

## Release

- Tag `v*` → `release.yml` runs goreleaser:
  darwin/linux/windows × amd64/arm64, `checksums.txt`, shell completions
  generated at build time, Homebrew cask pushed to `rorJeremy/homebrew-tap`
  (needs a `HOMEBREW_TAP_TOKEN` secret). goreleaser v2 deprecated `brews`
  formulas in favor of `homebrew_casks`; the install command is the same.
- Install: `brew install rorJeremy/tap/pcal`,
  `mise use -g github:rorJeremy/promise-calendar-cli` (from the release
  archives, no extra packaging), or
  `go install github.com/rorJeremy/promise-calendar-cli/cmd/pcal@latest`.
- v0.1.0 once `pcal login` works against production.
