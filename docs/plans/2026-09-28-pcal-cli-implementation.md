# pcal — Implementation Plan (v0.1.0)

Companion to `2026-09-28-pcal-cli-design.md`. Branch: `initial-implementation`.
Server contract: `promise-church-calendar` →
`docs/plans/2026-09-28-staff-json-api-implementation.md` (PR 2), especially the
event JSON shape, nested request bodies, error shapes, and the golden JSON in
`test/fixtures/files/api/`.

TDD throughout (`go test`, table-driven, `testify/require`). Commit once per
phase, and run `make check` at the end of each phase.

References were checked against shallow clones of `basecamp/fizzy-cli` and
`basecamp/basecamp-cli` (2026-09-28). The citations below are to those repos.

## Phase 0 — Prerequisites (GATE)

**Owner: Jeremy.**

- **Go toolchain.** The local `go` is **1.17.2** (`/usr/local/bin/go`, a pkg
  install). cobra, huh, and current goreleaser need a modern Go. fizzy-cli pins
  `go 1.26.6`, and Homebrew's stable is 1.27.1. Remove the old pkg install
  (`sudo rm -rf /usr/local/go /usr/local/bin/go /usr/local/bin/gofmt`), then run
  `brew install go`. Check with `go version` → ≥ 1.26.
- `brew install goreleaser golangci-lint`.
- PR 2 Phases 7–12 are reviewed (the JSON contract is fixed). The CLI can be
  built against golden JSON before PR 2 deploys. Only the live smoke test
  (Phase 14) needs production.

## Phase 1 — Scaffold, CI, Makefile

- `go mod init github.com/rorJeremy/promise-calendar-cli`, `go 1.26`.
- `cmd/pcal/main.go` → `os.Exit(commands.Execute())`.
- `internal/version`: `Version`, `Commit`, `Date` vars (set via ldflags, as in
  basecamp-cli `.goreleaser.yaml` → `internal/version.Version`), plus
  `UserAgent()` → `pcal/<version> (<GOOS>/<GOARCH>)`.
- `internal/commands/root.go`: cobra root with the persistent flags `--json`,
  `--quiet`, `--jq EXPR`, `--url`, `--token`, `--yes`. `SilenceUsage`/`SilenceErrors`
  are on; errors go through `output` (Phase 2).
- `Makefile` (modelled on fizzy-cli's `check` default goal): `build`, `test`
  (`go test -race ./...`), `vet`, `lint` (golangci-lint), `fmt-check`,
  `tidy-check`, `check` (all of them), `golden` (Phase 5).
- `.golangci.yml`: default linters plus `errcheck`, `gosec`, `revive`.
- `.github/workflows/test.yml`: matrix `ubuntu-latest`, `macos-latest`;
  `actions/setup-go` with `go-version-file: go.mod`; `make check`. Env
  `PCAL_NO_KEYRING=1` (basecamp-cli sets `BASECAMP_NO_KEYRING` in CI).
- `.gitignore`: `bin/`, `dist/`, `completions/`.

Test: `pcal --help` exits 0, and an unknown command exits 1.

Commit: "Scaffold pcal".

## Phase 2 — `internal/output`: envelope, renderers, exit codes

**Red first** (`output_test.go`, table-driven):
- Success envelope `{"ok":true,"data":…,"summary":"…"}`. Error envelope
  `{"ok":false,"error":{"code","message","fields"?}}`.
- Mode selection: `--json` or stdout not a TTY → JSON; `--quiet` → bare
  `data`; `--jq` → implies JSON and runs gojq over the envelope (compile error
  → exit 1); `--csv` → lists only (it errors on `show`).
- TTY table (lipgloss) and key/value card. `NO_COLOR` disables styling.
- The `Error` type carries `Code`, `Message`, `Fields`, and `ExitCode()`.

Exit codes are a **const block**, pinned by a test like fizzy-cli
`internal/errors/errors_test.go#TestExitCodes`: 0 ok, 1 usage/validation, 2 not
found, 3 auth, 4 forbidden, 5 rate limit, 6 network, 7 API, 8 ambiguous.

> **Noted deviation from fizzy-cli (design wins):** fizzy maps **422 → exit 7**
> (`errors.go#FromHTTPStatus` → `NewValidationError` → `CodeAPI`). Our design
> maps 422 → **1** ("invalid input"). We keep the design; the test documents it.

Commit: "Add output envelope, renderers, and exit codes".

## Phase 3 — `internal/config` + credential storage

**Red first:**
- Precedence: flag > env (`PCAL_URL`, `PCAL_TOKEN`) > config/keyring > default
  `https://calendar.promisechurch.io`. Table test covering all 4 layers.
- Config path: `$XDG_CONFIG_HOME/pcal/config.json`, else
  `~/.config/pcal/config.json` (the design says `~/.config` on macOS too, so
  don't use `os.UserConfigDir`). It stores `url` and a cached `time_zone`.
  Written `0600`, dir `0700`.
- Credentials: keyring (`zalando/go-keyring`, service `pcal`, key = base URL);
  tests use `keyring.MockInit()`. When the keyring errors or
  `PCAL_NO_KEYRING=1` is set → `credentials.json` (`0600`) plus a one-time
  stderr warning. `Delete` removes both.

Commit: "Add config and credential storage".

## Phase 4 — `internal/client`

**Red first** against `httptest.Server`:
- Headers: `Authorization: Bearer`, `Accept: application/json`, `User-Agent`.
- JSON bodies are **nested** (`{"event": {...}}`), per the PR 2 params-wrapping
  note. A helper `Body(resource string, attrs map[string]any)`.
- Multipart (`event[featured_image]` + other fields) for image upload.
- Status → error: 400 → 1, 401 → 3 (hint "run `pcal login`"), 403 → 4 (message
  "token is read-only" when the body says `read_only_token`), 404 → 2, 422 → 1
  with `fields` from `{"errors":{…}}` and the message built from the first
  field ("Title can't be blank"), 429 → 5, 5xx → 7. Dial/timeout → 6.
- 30-second timeout. `--verbose` (hidden) logs requests to stderr like
  fizzy-cli `client.go:211`.

Commit: "Add HTTP client".

## Phase 5 — Golden JSON contract

- `testdata/api/*.json`: copied from the Rails repo's
  `test/fixtures/files/api/` via `make golden RAILS=../promise-church-calendar`.
- `internal/client/types.go`: `Event`, `Occurrence`, `Tag`, `RSVP`, `Identity`,
  `AccessToken`, `WebsiteToken`.
- **Red:** decode every golden file into its type with
  `DisallowUnknownFields` (catches server-side additions) and round-trip it.
  This becomes the drift alarm when the API changes.

Commit: "Pin API contract with golden JSON".

## Phase 6 — `pcal auth login --token`, `auth status`, `whoami`, `logout`

**Red** (commands tested through `cobra` with a fake server; output captured):
- `auth login --token T`: `GET /my/identity` → store and print "Logged in as
  Admin (write)". 401 → exit 3, nothing stored.
- `auth status`: URL, user, permission, and credential source (keyring / file /
  env). Not logged in → exit 3.
- `whoami`: identity JSON/card; caches `time_zone` in config.
- `logout`: deletes the local credential only (design; fizzy-cli does the same)
  and prints a hint to revoke at `/my/access_tokens` or with `pcal tokens revoke`.

Commit: "Add token login, status, whoami, logout".

## Phase 7 — `pcal login` (browser, PKCE)

`internal/auth/browser.go`:
- Listen on `127.0.0.1:0`. Generate 32 random bytes each for `state` and the
  verifier (base64url, 43 chars). `challenge = base64url(sha256(verifier))`.
- Open `<base>/cli/authorization/new?port&state&code_challenge&device=<hostname>&permission=<write|read>`
  with `open` / `xdg-open` / `rundll32 url.dll,FileProtocolHandler` (basecamp-cli
  `auth.go#openBrowser` delegates to a host util the same way). **Always print
  the URL too.**
- Handle `/callback`: state mismatch → 400 page + error. `error=access_denied`
  → exit 3 "Authorization denied". Success page: "You can close this tab".
  Timeout after 5 minutes → exit 3. Only the first callback is accepted.
- `POST /cli/token {code, code_verifier}` → store → `/my/identity` → "Logged in
  as <name> (<permission>)".
- `--read-only` → `permission=read`. `--url` overrides the base.

**Red** (drive the listener directly; the Rails side is an `httptest.Server`):
success; state mismatch; access_denied; timeout (injected short duration);
`/cli/token` 400 → exit 3; a second callback ignored. The browser opener is an
injectable func.

Commit: "Add browser login".

## Phase 8 — `internal/parse`: dates, times, `--repeat`

**Red first** (pure table tests, fixed "now" + `America/Chicago`):
- Dates: `2026-10-04`, `today`, `tomorrow`, `sat`, `saturday`, `next sun`
  (strictly after this week's). A bare weekday = the next occurrence, today
  included. Invalid → exit 1.
- Times: `10am`, `10:30am`, `6:30pm`, `18:30`, `noon`. Combined with a date →
  RFC 3339 with the zone offset (DST-aware: check both Oct 31 and Nov 2).
- `--repeat` → RRULE: `daily` → `FREQ=DAILY`; `weekly on sun` →
  `FREQ=WEEKLY;BYDAY=SU`; `every 2 weeks on tue,thu` →
  `FREQ=WEEKLY;INTERVAL=2;BYDAY=TU,TH`; `monthly on the 1st sun` →
  `FREQ=MONTHLY;BYDAY=1SU`; `monthly on the last fri` → `BYDAY=-1FR`;
  `yearly` → `FREQ=YEARLY`. Unknown → exit 1 with the accepted forms.
  (These are exactly the RRULE parts the server's `RecurrenceExpandable`
  supports: FREQ, INTERVAL, BYDAY, COUNT, UNTIL.)
- `--rrule RAW` passes through; `--repeat` and `--rrule` together → exit 1.

Commit: "Add date, time, and recurrence parsing".

## Phase 9 — `pcal tags`

This establishes the command/test pattern the rest copy.
`list | create NAME [--color] | update ID [--name --color --position] | delete ID [--yes]`.
Red: each against the fake server and golden JSON. 422 → exit 1 with fields.
Delete without `--yes` and without a TTY → exit 1. Delete with a TTY → huh
confirm (injectable).

Commit: "Add tags commands".

## Phase 10 — `pcal events list | show`

- `list`: `--from/--to` (default today → +30), repeatable `--tag NAME` →
  resolved via `GET /tags` (unknown → exit 1), `--search Q`, `--unpublished`
  (`published=false`), `--website`. Table columns: date, time, title, tags,
  flags (draft / web / rsvp). Summary: "12 events Oct 1–31". `--csv`.
- `show ID`: card with every field. `recurrence_text` is shown when present.
- **ID resolution** (`internal/commands/resolve.go`): a UUID passes through.
  Otherwise, a case-insensitive **title prefix** is matched against
  `GET /events.json?q=<arg>&start=<today-1y>&end=<today+2y>`, deduped by id.
  0 → exit 2, >1 → exit 8 listing the candidates (id, title, first date).
  *Limitation:* events wholly outside that window need the UUID. The list
  endpoint is occurrence-based, and a wider window costs expansion time on the
  server.

Commit: "Add events list and show".

## Phase 11 — `pcal events create | update | delete | toggles | image`

- `create`: flags per the design. Missing `--title`/`--date` with a TTY → huh
  form for just those; without a TTY → exit 1. `--description-file F` (`-` =
  stdin). `--image F` → multipart. `--draft` → `published=false`. `--website`,
  `--rsvp`.
- `update ID`: only the flags given are sent (a `cmd.Flags().Changed` guard, so
  unset flags never clear fields).
- `delete ID [--yes]` → 204.
- `publish|unpublish`, `website on|off`, `rsvp on|off` → a one-field `PATCH`.
- `image set ID FILE [--alt]` → multipart `PATCH`. `image rm ID` →
  `DELETE /events/:id/image`.
- Red: request-body assertions for each, including the nested `event` key,
  `tag_ids` resolution, RFC 3339 times, and RRULE. 403 with a read token → exit 4.

Commit: "Add event write commands".

## Phase 12 — `pcal rsvps`, `tokens`, `website-token`

- `rsvps list EVENT [--date D] [--csv]`, `add EVENT --name --email [--phone --date]`
  (a recurring event without `--date` → server 422 → exit 1, shown verbatim),
  `delete EVENT RSVP_ID [--yes]`.
- `tokens list | revoke ID [--yes]`.
- `website-token show | rotate [--yes]`. Rotate prints the new token once, with
  a warning that the church website must be updated.

Commit: "Add RSVP, token, and website-token commands".

## Phase 13 — `version`, `completion`, `skill install`, README

- `version` → version, commit, date, Go version.
- `completion bash|zsh|fish` (cobra).
- `skills/pcal/SKILL.md` via `go:embed`. `skill install` writes
  `~/.claude/skills/pcal/SKILL.md` and asks before overwriting unless `--yes`.
  The skill teaches agents to use `--json`, the exit codes, and ID resolution.
- `README.md`: install, login, 10 common recipes, output modes, exit codes.

Commit: "Add version, completion, skill, README".

## Phase 14 — Release pipeline + v0.1.0

`.goreleaser.yaml` (trimmed basecamp-cli; no signing, SBOM, nfpms, scoop, AUR):

```yaml
version: 2
project_name: pcal
before:
  hooks:
    - go mod tidy
    - sh -c 'mkdir -p completions && for s in bash zsh fish; do go run ./cmd/pcal completion $s > completions/pcal.$s; done'
builds:
  - main: ./cmd/pcal
    binary: pcal
    env: [CGO_ENABLED=0]
    goos: [darwin, linux, windows]
    goarch: [amd64, arm64]
    flags: [-trimpath]
    ldflags:
      - -s -w
      - -X github.com/rorJeremy/promise-calendar-cli/internal/version.Version={{.Version}}
      - -X github.com/rorJeremy/promise-calendar-cli/internal/version.Commit={{.Commit}}
      - -X github.com/rorJeremy/promise-calendar-cli/internal/version.Date={{.Date}}
archives:
  - formats: [tar.gz]
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    format_overrides: [{ goos: windows, formats: [zip] }]
    files: [README*, completions/*]
checksum: { name_template: checksums.txt, algorithm: sha256 }
homebrew_casks:
  - name: pcal
    repository: { owner: rorJeremy, name: homebrew-tap, token: "{{ .Env.HOMEBREW_TAP_TOKEN }}" }
    directory: Casks
    homepage: https://github.com/rorJeremy/promise-calendar-cli
    description: "Command-line interface for the Promise Church Calendar"
    binaries: [pcal]
    completions: { bash: completions/pcal.bash, zsh: completions/pcal.zsh, fish: completions/pcal.fish }
    hooks:
      post:
        install: |
          if OS.mac?
            system_command "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "#{staged_path}/pcal"]
          end
```

- **Cask, not formula.** basecamp-cli uses `homebrew_casks` (goreleaser v2's
  replacement for `brews`). The design said "formula"; the install command
  `brew install rorJeremy/tap/pcal` is unchanged.
- **Quarantine hook:** we skip code signing (design), so without it Gatekeeper
  blocks the unsigned binary on first run.
- `.github/workflows/release.yml`: on `v*` tags → `make check` → goreleaser
  action with `GITHUB_TOKEN` + `HOMEBREW_TAP_TOKEN`. Permissions:
  `contents: write`.
- Local checks: `goreleaser check`, `goreleaser release --snapshot --clean`,
  and run `dist/pcal_darwin_*/pcal version`.

Live smoke test against production (after PR 2 deploys): `pcal login` →
`whoami` → `events list` → create a draft test event → update → image set/rm →
delete → `pcal login --read-only` and a write attempt → exit 4. Then tag
`v0.1.0`.

Commit: "Add release pipeline".

## Manual follow-ups (Jeremy)

1. Phase 0: upgrade Go; install goreleaser and golangci-lint.
2. Before tagging v0.1.0:
   - Create the **public** repo `rorJeremy/homebrew-tap`. It must be public for
     `brew install` to work without auth.
   - Create a fine-grained PAT with contents read/write on `homebrew-tap` only,
     and add it as the `HOMEBREW_TAP_TOKEN` Actions secret on
     `promise-calendar-cli`.
   - Decide on a LICENSE. The repo is private now, and the cask doesn't need one.
3. **Private repo caveat:** `go install …@latest` and the release tarballs the
   cask downloads require the repo (or at least its releases) to be public.
   Either make `promise-calendar-cli` public before v0.1.0, or accept that
   installs need GitHub auth. **Decide before Phase 14.**

## Notes / risks

- **Contract drift** is caught by the golden-JSON decode tests (Phase 5) and the
  Rails-side golden test (PR 2 Phase 12).
- **Title resolution window** (Phase 10) is a deliberate limitation. The UUID
  always works.
- **Keyring on headless Linux** falls back to a `0600` file with a warning
  (design). CI forces the fallback with `PCAL_NO_KEYRING=1`.
