# pcal — Promise Church Calendar CLI — Design

- **Date:** 2026-09-28
- **Status:** Approved design; reconciled with the shipped API on 2026-10-27,
  then audited against primary sources (Gate 1b, 2026-10-27). The Phase 1–5
  code was audited (Gate 2b, 2026-10-27) and its findings fixed (Gate 2c,
  2026-10-28); Jeremy's decisions from that audit are recorded below
  (Storage, Precedence, Output) and in the plan's header, as are his Gate 3
  decisions on login and logout (Auth) and input parsing (Input
  conveniences), 2026-10-28. The Phase 6–8 code was reviewed (Gate 3b,
  2026-10-29) and its findings fixed (Gate 3c, 2026-10-30); those decisions
  are in Auth, Output, the exit codes, and the plan's header. Jeremy's Gate 4
  decisions (2026-11-02) on confirmations, tag names, and the later event,
  file, and token rules are in Commands, Input conveniences, Output, the
  exit codes, and the plan's header. Phase 9 implements the tags commands
  and the confirmation rules; the rest wait for the later phases. Gate 5
  (2026-11-02) added the release pipeline described under Release
  (`.goreleaser.yaml` and `release.yml`). No license file yet. The v0.1.0
  tag is still Jeremy's.
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
| Libraries | `spf13/cobra`, `charm.land/huh/v2` (v2, whose bubbletea doesn't query the terminal at startup, only when a drawn prompt opens; Gate 3), `charmbracelet/lipgloss`, `zalando/go-keyring`, `gofrs/flock` (the credentials file lock, as basecamp-cli), `itchyny/gojq`, `stretchr/testify` |
| Shared toolkit | Do **not** depend on `basecamp/cli` (pre-1.0, multi-account machinery we don't need). Copy its conventions |
| Login | `pcal login` browser flow (PKCE + localhost callback, paste-the-callback fallback when headless); `pcal auth login --with-token` (stdin) for CI/agents |
| Input | Flags for everything; huh prompts for missing required fields only when stdin and stderr are both terminals (prompts draw on stderr; Gate 3c) |
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
              (events_write.go is the write half of the events group;
               confirm.go is the shared No-default confirmation)
  output/     envelope, table/JSON/CSV renderers, --jq, exit codes
  prompt/     huh forms for missing fields (TTY only)
  parse/      natural dates/times, --repeat → RRULE
  version/    version/commit/date via ldflags; a go install build
              falls back to the module version in the build info
skills/pcal/SKILL.md      embedded Claude Code skill (`pcal skill install`)
.goreleaser.yaml
.github/workflows/test.yml, release.yml
Makefile
```

## Commands

```
pcal login [--read-only] [--url URL] [--remote | --local] [--no-browser]
pcal login --with-token                       # token read from stdin, never an argument
pcal logout                                   # revokes a login-minted token, then forgets it locally
pcal auth login | logout | status             # auth login/logout are pcal login/logout
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

pcal tags list [--csv] | create NAME [--color] | update ID|NAME [...] | delete ID|NAME [--yes]
pcal rsvps list EVENT [--date D] [--csv]
pcal rsvps add EVENT --name N --email E [--phone --date]
pcal rsvps delete EVENT RSVP_ID [--yes]
pcal tokens list | revoke ID [--yes]
pcal website-token show | rotate [--yes]

pcal version | completion bash|zsh|fish | skill install
```

### Input conveniences

- Dates: `2026-10-04`, `today`, `tomorrow`, `sat`, `next sun`. Times: `10am`,
  `6:30pm`, `18:30`. Converted to ISO 8601 in the app's time zone
  (`time_zone` from `/my/identity`; the app uses America/Chicago). The server
  stores times separately from dates, so the CLI always puts `--start` on the
  event's start date and `--end` on its end date, and an update that moves a
  timed event moves its times too.
  A time the clocks skip in spring doesn't exist and is refused (exit 1);
  a time the clocks repeat in fall is the first one (Jeremy's Gate 3
  decision).
- `--repeat`: `daily`, `weekly on sun`, `every 2 weeks on tue,thu`,
  `monthly on the 1st sun` → RRULE, in a small grammar around those
  (`every other week`, `every 3 months on the last tue`, `weekly on
  tuesday and thursday`; Gate 3). `--rrule` passes a raw rule through; the
  server validates it (422 → exit 1).
- `--tag` takes names and resolves them to IDs via `GET /tags`; unknown name → exit 1.
  Tag names are unique only case-sensitively on the server, so an exact-case
  match wins, then a unique case-insensitive one; two that differ only by case
  → exit 8. `tags update` and `tags delete` take an ID or a name matched the
  same way, except that no match is exit 2 with a hint to run `pcal tags list`
  (Jeremy's Gate 4 decision). `rsvps delete` and `tokens revoke` take IDs only.
- The event argument on `rsvps` is an ID or a title, resolved as an event
  ID is. An empty `--phone` sends null. `rsvps add` asks for a missing name
  and email in a terminal and otherwise exits 1 before any request;
  canceling that form says "Canceled. Nothing was added." `tokens revoke`
  and `rsvps delete` report `{"deleted": true}` after a 204. Revoking the
  token pcal is using forgets a stored token, or says to unset `PCAL_TOKEN`;
  any other failure of that check is ignored. `website-token rotate` prints
  the new token once and warns that the church website must be updated.
- `version` prints the version, commit, date, and Go version. ldflags win.
  When the version is still `dev`, the module version from the build info
  is shown, with one leading `v` removed. The User-Agent uses that same
  version. `completion` takes exactly bash, zsh, or fish and prints the
  script itself, not an envelope. `skill install` writes
  `~/.claude/skills/pcal/SKILL.md` and asks before replacing it. That
  question follows the confirmation rule; answering No leaves the file
  and reports `{"installed": false}`.
- `events update` sends only the flags given; a flag with an empty value
  (`--end-date ''`, `--rrule ''`, `--tag ''`) clears that field. Moving an
  all-day event's dates sends no times. `--all-day` sends both times as
  null, and with `--start` or `--end` is a usage error.
- `events create` asks for a missing title and date when both streams are
  a terminal, and otherwise exits 1 before any request. Canceling that form
  says "Canceled. Nothing was created." (exit 130, or 143 for SIGTERM). On
  an accessible form, Esc is text, not a cancel: huh's line prompt has no
  cancel, and only Confirm cancels on Esc. A multi-field accessible form
  reads one line at a time, because huh's scanner would otherwise take every
  later answer with the first line.
- `--description-file` (`-` is stdin) reads at most 1 MiB. Together with
  `--description` it is a usage error, and `-` on create is a usage error
  when the title or the date is still missing, since both would read stdin.
  `--image` is checked before any request: over 10 MB, or a directory, is a
  usage error. The file type is the server's 422. `events image rm` reports
  `{"removed": true}`; the server answers 204 with no body.
- Event `ID` arguments accept a UUID, an exact title, or a unique title
  prefix, all case-insensitive. An exact title wins over prefixes, so
  `Worship` stays reachable when `Worship Night` exists. The server's search
  matches anywhere in a title, so the CLI does this matching itself. Multiple
  matches → exit 8 listing candidates. Nothing in the window is exit 2. The
  hint says to use the event's ID and does not claim `pcal events list
  --search` will show it: the list uses the same window, and the server
  stops a series after 365 occurrences from its start
  (`RecurrenceExpandable::MAX_OCCURRENCES`), so an old daily series needs
  its UUID (Jeremy's Gate 4c decision, G4b-F2). The hint doesn't include
  the argument, so a token-like title isn't repeated.
- `events list` shows occurrences: a recurring event appears once per
  occurrence, dated by the server's `occurrence_*` fields. Human dates and
  times use the calendar's time zone: "Sun Nov 1" (with the year when it
  isn't this year), "6:00pm–7:30pm", and "all day" when there are no times.
  JSON, `--quiet`, and `--jq` keep the server's values (Jeremy's Gate 4
  decision). RSVP dates and token timestamps use the same formatting.
  A blank `--tag` on `list` is a usage error: the list has no
  untagged filter, and on update that same flag clears the tags.
- `publish`, `website on`, `rsvp on`, etc. are `PATCH /events/:id` with one field.

## Auth

**Browser login:**
1. Listen on `127.0.0.1:0` (random port); generate 32-byte `state` and PKCE
   verifier; `challenge = base64url(sha256(verifier))`.
2. Open `<base>/cli/authorization/new?port=&state=&code_challenge=&device=<hostname>&permission=write|read`
   (print the URL too, in case the browser doesn't open).
3. Wait up to 5 minutes for `/callback?code=&state=`. A callback with
   another state (or none) gets a 400 page and is ignored; only this
   login's state decides, so a stray local request can't end the login
   (Jeremy's Gate 3c decision; basecamp-cli's first callback decides).
   Handle `error=access_denied`. Serve a small "You can close this tab" page.
4. `POST /cli/token {code, code_verifier}` → token. Check it with
   `/my/identity`, store it, revoke the token it replaces, and print
   "Logged in as <name> (write)". A new token the login doesn't keep (the
   check fails, the store fails, or Ctrl-C) is revoked, so a login never
   leaves a live token with no record of it (Jeremy's Gate 3c decisions,
   as basecamp-cli's `verifyAndStore` and `discardGrant`).

Ctrl-C while a login waits (for the browser, a pasted address, or the masked
`--with-token` prompt) cancels it: "Login canceled. Nothing was stored." and
exit 130, without the JSON envelope, as basecamp-cli's login does, though it
prints the line on stdout (Jeremy's Gate 3 decision). A SIGTERM does the
same, but says "Login terminated. Nothing was stored." and exits 143 (Gate
3c, as basecamp-cli's `ExitTerminated`). Once the code is sent, Ctrl-C
doesn't cut the exchange off, since the server may already have minted the
token; a token that comes back is revoked, and the message says whether that
worked (Gate 3c). Messages go to stderr, so the result on stdout can still
be JSON.

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
one, so logging in again leaves no stray live token (Gate 3). That revoke
is bounded at 10 seconds and Ctrl-C stops it; the new login stands either
way, and a warning says the old token may still be live (Gate 3c).

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
`--with-token`. One typed into argv anyway is never repeated in an error
(Gate 3c). Both logins refuse while `PCAL_TOKEN` is set, since every later
command would keep using it instead (Gate 3), and so does logout, which
can't end an environment token. pcal never saves `--url` in config.json
(Gate 3); use `--url` or `PCAL_URL` on every command for another calendar.
Default URL `https://calendar.promisechurch.io`. The URL must be `https://`;
`http://` is accepted only for localhost and loopback addresses
(127.0.0.0/8, ::1), so a token is never sent unencrypted (Jeremy's decision
at Gate 2b). It's canonicalized (lowercase host, no default port or trailing
dot), because it's also the credential key.

## Output

- **TTY:** lipgloss table for lists, key/value card for `show`. Styling is bold
  only, and off with `NO_COLOR`, or with `CLICOLOR=0` or `TERM=dumb` unless
  `CLICOLOR_FORCE` is set (termenv's rules). Control characters in anything
  shown, server text included, appear as visible escapes (`\x1b`), since some
  of it, such as RSVP names, comes from a public form (Jeremy's decision at
  Gate 2b). Prompts follow the same rule: no colors, only bold, faint,
  and reverse (Gate 3), and none of those either when styling is off, so
  an accessible prompt then prints no escape sequence (Gate 3c). With
  `TERM=dumb` prompts are accessible (plain lines), as huh's own fallback
  makes them (Gate 3c). A plain Confirm asks on one line (`Delete tag
  "Kids"? [y/N]`) rather than drawing two buttons, which look alike with no
  attributes; a password prompt stays drawn, since its cursor still shows
  (Jeremy's Gate 4 decision).
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
  nothing is `"data": []`, never `null`. With `--json`, the error envelope
  goes to stdout. A human terminal or `--csv` writes the error to stderr.
  Exits 130 and 143 print one line on stderr and no envelope (Gate 4c,
  G4b-F5). `--jq`, `--quiet`, and stdout that isn't a terminal also use
  the envelope; the skill tells an agent to pass `--json`.
- `--jq EXPR` — built-in gojq over the envelope; implies `--json`.
- `--quiet` — bare `data`, no envelope.
- `--csv` — lists only. A cell a spreadsheet would run as a formula (starting
  with `=`, `+`, `-`, `@`, a tab, or a carriage return) gets a leading `'`.
  Every C0, DEL, and C1 control in a cell is then shown as a Go escape, the
  same way human output does, whether or not stdout is a terminal, so a
  public RSVP name can't retitle a staff member's terminal (Jeremy's Gate 4c
  decision, G4b-F1). `--jq` still prints a raw string (accepted at Gate 2c).
- Deletes and `website-token rotate` confirm unless `--yes`. With no TTY and no
  `--yes`, they fail with exit 1, before any request. The question opens on
  No, so Enter (and Ctrl-D on an accessible prompt) changes nothing and
  exits 0: "Nothing was deleted.", with `{"deleted": false}` (rotate:
  `{"rotated": false}`). Esc, Ctrl-C, or SIGTERM cancels instead: one line
  on stderr, "Canceled. Nothing was deleted." (rotate: "Canceled. The
  website token wasn't changed."), exit 130 or 143, no envelope. Esc cancels
  on the accessible line too, at the Esc itself (Jeremy's Phase 9 review);
  Enter, Ctrl-D, y, and n on that line are unchanged. That is
  not exit 0, which is what basecamp-cli does for a canceled confirmation
  (Jeremy's Gate 4 decision). The question names the tag, or the event's
  title and date. Every confirmation uses this rule.

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
| 130 | Canceled with Ctrl-C or Esc during a login or a confirmation (128 + SIGINT; no envelope). A confirmation answered No is exit 0, not 130 (Gate 4) |
| 143 | Stopped by SIGTERM during a login or a confirmation (128 + SIGTERM, as basecamp-cli's `ExitTerminated`; no envelope; Gate 3c, extended at Gate 4) |

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
- v0.1.0 once `pcal login` works against production. Gate 5
  (2026-11-02) added the two files. Nothing is signed. The tag itself,
  the production smoke test, and the LICENSE decision are still Jeremy's.
