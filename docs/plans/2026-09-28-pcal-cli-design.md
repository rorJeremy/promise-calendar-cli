# pcal — Promise Church Calendar CLI — Design

- **Date:** 2026-09-28
- **Status:** Approved design, ready for implementation planning
- **Server-side companion:** `promise-church-calendar` →
  `docs/plans/2026-09-28-cli-api-design.md` (Planning Center removal, JSON API,
  token auth). This CLI depends on that PR 2 being deployed.

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
| Libraries | `spf13/cobra`, `charmbracelet/huh`, `charmbracelet/lipgloss`, `zalando/go-keyring`, `itchyny/gojq`, `stretchr/testify` |
| Shared toolkit | Do **not** depend on `basecamp/cli` (pre-1.0, multi-account machinery we don't need). Copy its conventions |
| Login | `pcal login` browser flow (PKCE + localhost callback); `pcal auth login --token` for CI/agents |
| Input | Flags for everything; huh prompts for missing required fields only when stdin is a TTY |
| Output | Tables in a TTY; fizzy-style JSON envelope when piped or `--json` |
| Distribution | goreleaser → GitHub Releases + `rorJeremy/homebrew-tap`; `go install` works |

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
pcal login [--read-only] [--url URL]
pcal logout                                   # deletes local credential only
pcal auth login --token T  |  auth status
pcal whoami

pcal events list [--from D --to D --tag NAME... --search Q --unpublished --website]
pcal events show ID
pcal events create [--title --date --end-date --start 10am --end 12pm --all-day
                    --repeat "weekly on sun" | --rrule RAW
                    --location-name --address --map-url
                    --tag NAME... --description | --description-file F --notes
                    --image F --image-alt --website --rsvp --draft]
pcal events update ID [same flags]
pcal events delete ID [--yes]
pcal events publish|unpublish ID
pcal events website on|off ID
pcal events rsvp on|off ID
pcal events image set ID FILE [--alt TEXT]
pcal events image rm ID

pcal tags list | create NAME [--color] | update ID [...] | delete ID [--yes]
pcal rsvps list EVENT_ID [--date D] [--csv]
pcal rsvps add EVENT_ID [--name --email --phone --date]
pcal rsvps delete EVENT_ID RSVP_ID [--yes]
pcal tokens list | revoke ID
pcal website-token show | rotate [--yes]

pcal version | completion {bash,zsh,fish} | skill install
```

### Input conveniences

- Dates: `2026-10-04`, `today`, `tomorrow`, `sat`, `next sun`. Times: `10am`,
  `6:30pm`, `18:30`. Converted to ISO 8601 in the app's time zone
  (`time_zone` from `/my/identity`; the app uses America/Chicago).
- `--repeat`: `daily`, `weekly on sun`, `every 2 weeks on tue,thu`,
  `monthly on the 1st sun` → RRULE. `--rrule` passes a raw rule through.
- `--tag` takes names and resolves them to IDs via `GET /tags`; unknown name → exit 1.
- Event `ID` arguments accept a UUID or a unique title prefix; multiple
  matches → exit 8 listing candidates.
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

**Storage:** OS keyring (service `pcal`, key = base URL). If no keyring is
available (headless Linux), fall back to `~/.config/pcal/credentials.json` with
mode `0600` and warn.

**Precedence:** flags (`--url`, `--token`) > env (`PCAL_URL`, `PCAL_TOKEN`) >
config/keyring. Default URL `https://calendar.promisechurch.io`.

## Output

- **TTY:** lipgloss table for lists, key/value card for `show`; respects `NO_COLOR`.
- **`--json` or stdout not a TTY:**
  ```json
  {"ok": true, "data": [...], "summary": "12 events Oct 1–31"}
  {"ok": false, "error": {"code": "validation", "message": "Title can't be blank", "fields": {"title": ["can't be blank"]}}}
  ```
- `--jq EXPR` — built-in gojq over the envelope; implies `--json`.
- `--quiet` — bare `data`, no envelope.
- `--csv` — lists only.
- Deletes and `website-token rotate` confirm unless `--yes`. With no TTY and no
  `--yes`, they fail with exit 1.

### Exit codes (same as fizzy-cli)

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
  generated at build time, Homebrew formula pushed to `rorJeremy/homebrew-tap`
  (needs a `HOMEBREW_TAP_TOKEN` secret).
- Install: `brew install rorJeremy/tap/pcal` or
  `go install github.com/rorJeremy/promise-calendar-cli/cmd/pcal@latest`.
- v0.1.0 once `pcal login` works against production.
