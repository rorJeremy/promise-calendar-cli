# pcal — Implementation Plan (v0.1.0)

Companion to `2026-09-28-pcal-cli-design.md`. Branch: `initial-implementation`.
Server contract (the source of truth; PR #112, merged and deployed): in
`promise-church-calendar` on `main`, the "# Staff API" section of
`docs/api.md` and the 17 golden files in `test/fixtures/files/api/`. When this
plan and those disagree, they win. The server's PR 2 implementation plan
(`docs/plans/2026-09-28-staff-json-api-implementation.md`) is history now; the
API changed during its review.

**Amended 2026-09-28 after reviewing basecamp-cli's auth** (design doc updated
to match): tokens never go in argv (no `--token` flag;
`auth login --with-token` reads stdin); `logout` revokes tokens that `pcal
login` minted; `login` has a paste-the-callback fallback on headless hosts.

**Amended 2026-10-27 against the shipped API** (Gate 1, checked against
`docs/api.md`, the golden files, `config/routes.rb`, `authentication.rb`, and
the controllers and models behind them): list items are occurrences with
their own `occurrence_*` fields; `q` matches anywhere in a title; 422 values
are lists, in body order; Rails-default 400/404/406 bodies; `/cli/token` takes
a flat body and can answer 429; the server validates RRULEs; `starts_at` and
`ends_at` are stored independently of `occurs_on`, so the CLI keeps them on
the event's dates; multipart encoding rules; the 365-occurrence expansion
cap; RSVP `--date` is the `occurrence` param. Phase 0 now says the old Go is a
Homebrew install. Jeremy's Gate 1 decisions: pin the 2026-10-27 tool
versions, created and locked with mise (`MISE_LOCKED=1` in CI); add
`--image-url`; an exact title match wins over prefix matches.

**Audited 2026-10-27 (Gate 1b)** against primary sources: the calendar app's
code at 85a00a0 (not its docs), fresh clones of fizzy-cli (65b13a2),
basecamp-cli (0dc1d47), goreleaser (v2.19.0), and jdx/mise-action (16bad93),
the library sources at the versions `go get` resolves today (cobra 1.10.2,
pflag 1.0.10, huh 1.0.0, lipgloss 1.1.0, gojq 0.12.19, go-keyring 0.2.8), and
experiments in a scratch directory with the pinned tools. Corrections: the
cask hook (deprecated syntax that fails `goreleaser check`), the `dist/` path,
`mise outdated --bump`, `go mod edit -go=1.26`, six (not seven) `error`
golden files, the 400 → 1 and error-envelope deviations from fizzy-cli, the
fizzy/basecamp citations, the `--tag` flag type, the license and Actions
billing wording, and the plan/design mismatches.

**Jeremy's Gate 2 decisions (2026-10-27)** on the items the audit left open:
keep the nested error envelope, a deliberate deviation from fizzy-cli
(Phase 2); accept the 5-minute upload timeout (Phase 4), huh's accessible
mode (Phase 6), and the `debug.ReadBuildInfo` version fallback (Phase 13).
At the Gate 2 review he added an optional `hint` to the error object.

**Audited 2026-10-27 (Gate 2b), fixed 2026-10-28 (Gate 2c).** An
independent review of the Phase 1–5 code from a cleared context found 23
issues, F1–F23 (6 Medium, 10 Low, 7 Nit; nothing Critical or High), and
Gate 2c fixed them all, test first, in five commits. Jeremy's decisions on
the choices the audit left open:
- F3/F4: pick the keyring backend once per process, on the first keyring
  call. A result or "not found" means the keyring for the rest of the run,
  and later keyring errors are returned, never a silent fallback. Any other
  error, or a timeout on that first call, means the file plus the one
  warning. Every keyring call is bounded at 10 seconds; a later timeout's
  error suggests `PCAL_NO_KEYRING=1`. Tests use a keyring interface with a
  fake, never the real keychain (Phase 3).
- F6: `http://` only for loopback hosts (localhost, 127.0.0.0/8, ::1);
  anything else is a usage error that says why (Phase 3).
- F11: lock the read-modify-write of `credentials.json` with
  `github.com/gofrs/flock` (v0.13.1, as basecamp-cli; its `go 1.25.0`
  leaves ours at `go 1.26.0`) (Phase 3).
- F14: keep the root's "unknown command" error and add cobra's suggestions
  (`cmd.SuggestionsFor`), in the hint (Phase 1).
- F5: human output shows control characters as visible escapes, including
  in error text from the server (Phase 2).
- F20: `Print` turns a nil slice into `[]` (Phase 2).
- F23: every usage error without a hint gets "Run `<command> --help` for
  usage." (Phase 2).
The other findings took the audit's proposed fix. Among them: only cobra's
own errors are usage errors (F1); a keyring miss moves a fallback-era
token into the keyring (F10); the canonical URL drops default ports and a
trailing dot (F7); pcal's directory and `credentials.json` are kept private
(F8); CSV cells are guarded against formulas (F15); styling follows
`CLICOLOR`/`TERM=dumb` as termenv does (F21); and doc fixes (F17).

**Jeremy's Gate 3 decisions (2026-10-28)**, on choices Phases 6–8 left
open, each checked against fizzy-cli (65b13a2) and basecamp-cli (8655e70):
- Ctrl-C (or Esc) at a login prompt, or while a login waits for the
  browser or a pasted address, cancels it: "Login canceled. Nothing was
  stored." on stderr, exit 130, no envelope (as basecamp-cli's login,
  `internal/commands/auth.go#loginOutcome`) (Phases 6–7).
- `logout` whose local forget fails (a keyring error from `Delete`) is an
  API error, exit 7, whose message also says what happened on the server
  (Phase 6).
- Both logins refuse while `PCAL_TOKEN` is set (exit 1), before any prompt
  or browser, since every later command would keep using it (Phases 6–7).
- A login that replaces a token an earlier `pcal login` minted revokes the
  old one once the new one is stored (a failure only warns); an imported
  token is only replaced (Phases 6–7).
- `pcal login` and `pcal auth login` are one command, and `pcal logout`
  and `pcal auth logout` another (as basecamp-cli's `buildLoginCmd` and
  `buildLogoutCmd`) (Phases 6–7).
- pcal never saves `--url` in config.json; after a login with `--url`, a
  note says other commands need it too (Phase 6).
- A time that doesn't exist (the hour the clocks skip in spring) is exit 1;
  a time that happens twice (the hour repeated in fall) is the first one
  (Phase 8).
- `--repeat` accepts a small grammar around the listed forms (Phase 8).
- At the Gate 3 review: huh v2.0.3 (`charm.land/huh/v2`) instead of v1.0.0.
  huh v1 pulls in bubbletea v1, whose package `init()` asks the terminal for
  its background color whenever stdout is a terminal (bubbletea v1.3.6
  `tea_init.go:21`), and termenv waits up to 5 seconds for the answer:
  every pcal command took 5 s longer on a pty that answers nothing (real
  terminals answer at once). bubbletea v2 has no such init. Prompts use a
  theme with no colors (bold, faint, reverse), so they read on light and
  dark backgrounds alike, as pcal's other output does (Phase 6).

TDD throughout (`go test`, table-driven, `testify/require`): every phase
starts with a failing test (the red step), then the code, then
`mise exec -- make check` green before its one commit. Commit messages carry
a comprehensive body (what, why, deviations), and the author/committer is
the noreply address (`git log --format='%ae %ce' -1` after each commit).

References were checked against shallow clones of `basecamp/fizzy-cli` and
`basecamp/basecamp-cli` (2026-09-28, re-checked 2026-10-27). The citations
below are to those repos.

## Phase 0 — Prerequisites (GATE)

**Owner: Jeremy.**

- **Toolchain via mise** (you use mise as of 2026-09-29; the calendar app
  switched in PR #103). Phase 1 adds `mise.toml` pinning Go, goreleaser, and
  golangci-lint, so Phase 0 is just: in `~/promise-calendar-cli`, run
  `mise install`, then `mise exec -- go version` → `go1.27.3`. Nothing comes
  from Homebrew.
- **Retire the old Go (recommended).** `/usr/local/bin/go` is **1.17.2**, a
  Homebrew install (`/usr/local/bin/go -> ../Cellar/go/1.17.2/bin/go`; `brew
  list --versions go` → `go 1.17.2`). Inside the repo the mise-activated `go`
  shadows it, but it's still what non-mise shells and other directories find.
  Remove it with `brew uninstall go`, and optionally `mise use -g go@1.27` for
  a global default.
- **Non-interactive shells** (agents, scripts) don't run mise's prompt hook.
  Prefix commands with `mise exec --` (e.g. `mise exec -- make check`). This
  was verified in the calendar repo, where bare `ruby` resolved to rbenv.
- **Done:** PR 2 (#112) is merged and deployed, and was checked in production
  on 2026-10-27 (401 + `WWW-Authenticate` without credentials; a real personal
  access token against `/my/identity`). The CLI is built against the golden
  JSON; the live smoke test (Phase 14) is unblocked.

## Phase 1 — Scaffold, CI, Makefile

- `mise.toml` (the single source of truth for tool versions, locally and in
  CI). Create it with mise rather than by hand:
  `mise use --pin go@1.27.3 golangci-lint@2.15.0 goreleaser@2.19.0`, then add
  the header comment:
  ```toml
  # Tool versions for pcal development, managed by mise (https://mise.jdx.dev).
  # CI installs the same versions via jdx/mise-action, from mise.lock.
  [tools]
  go = "1.27.3"
  golangci-lint = "2.15.0"
  goreleaser = "2.19.0"
  ```
  These were the latest versions on 2026-10-27 (`mise latest <tool>`; the
  2026-09-29 pins 1.27.1 / 2.14.0 / 2.18.2 were superseded and bumped at
  Jeremy's request).
- `mise.lock`: `mise lock --platform linux-x64,linux-arm64,macos-x64,macos-arm64`
  records each tool's download URL and checksum for every platform a
  developer or CI runner uses (this Mac is `macos-x64`; GitHub's
  `macos-latest` is arm64, `ubuntu-latest` x64). Commit both files.
- **Every dev tool comes from mise**: no `go install` of linters, no
  `golangci-lint-action` or `goreleaser-action`, nothing from Homebrew.
  `mise use` also installs the three tools (into mise's own directory). Phase
  1 needs them anyway, because its commit requires a green `mise exec -- make
  check`. Check `mise exec -- go version` → `go1.27.3`. Any other checkout
  just runs `mise install`. (Both `mise use --pin … --dry-run` and the
  four-platform `mise lock --dry-run` were run on 2026-10-27 with mise
  2026.10.20.)
- **Bumping later:** `mise outdated --bump` shows what's newer. A bare
  `mise outdated` compares only within the configured range, so with exact
  pins it always says "All tools are up to date" (verified with mise
  2026.10.20). `mise upgrade --bump <tool>` rewrites the pin in `mise.toml`;
  `mise lock` refreshes `mise.lock` (with no `--platform` it keeps the
  platforms already in the file); `mise exec -- make check`; commit both
  files together.
- `go mod init github.com/rorJeremy/promise-calendar-cli`, then
  `go mod edit -go=1.26`: `go mod init` writes the running toolchain's
  version (`go 1.27.3`). `go 1.26` is the minimum for `go install` users, and
  it's independent of the 1.27.3 toolchain mise pins for development. `go mod
  tidy` then wrote `go 1.26.0`, because golang.org/x/sys v0.49.0 and
  golang.org/x/term v0.47.0 declare `go 1.26.0` (accepted at the Gate 2
  review). No other dependency needs more: gojq 0.12.19 declares 1.24.0,
  gofrs/flock v0.13.1 1.25.0, huh 1.0.0 1.23.0 (`go list -m -f
  '{{.GoVersion}}' all`, Gate 2c).
- `cmd/pcal/main.go` → `os.Exit(commands.Execute())`.
- `internal/version`: `Version`, `Commit`, `Date` vars (set via ldflags, as in
  basecamp-cli `.goreleaser.yaml` → `internal/version.Version`), plus
  `UserAgent()` → `pcal/<version> (<GOOS>/<GOARCH>)`.
- `internal/commands/root.go`: cobra root with the persistent flags `--json`,
  `--quiet`, `--jq EXPR`, `--url`, `--yes`. There is deliberately no `--token`,
  so tokens never land in shell history or `ps`. `--csv` is not persistent:
  only list commands define it, so `show --csv` is an unknown flag (exit 1).
  `SilenceUsage`/`SilenceErrors` are on; until Phase 2, `Execute` returns 1
  for any error, and from Phase 2 errors go through `output`. Only cobra's
  own errors (an unknown command or flag, wrong arguments, a missing
  required flag) are usage errors: before cobra runs, every command's
  `PersistentPreRunE`, `PreRunE`, `RunE`, `PostRunE`, and
  `PersistentPostRunE` are wrapped so that a plain error from pcal's code is
  an API error, exit 7 (Gate 2b F1). The root's `Args` reports a stray word
  as `unknown command "x" for "pcal"` with cobra's suggestions in the hint,
  "Did you mean `pcal whoami`?" (F14). `cobra.NoArgs` gives none, and
  cobra's own check runs only when `Args` is nil. Group commands
  (`pcal events`) need the same `Args` in Gate 3: a non-runnable command
  returns help before cobra checks arguments.
- `Makefile` (modelled on fizzy-cli's `check` default goal): `build`, `test`
  (`go test -race ./...`), `vet`, `lint` (golangci-lint), `fmt-check`,
  `tidy-check`, `check` (all of them), `golden` (Phase 5).
- `.golangci.yml` (`version: "2"`): the standard set (errcheck, govet,
  ineffassign, staticcheck, unused, which `golangci-lint linters` lists as
  enabled with no config) plus `gosec` and `revive`, as fizzy-cli enables.
  revive's `exported` rule enforces doc comments on exported identifiers.
  Expect gosec to flag reading user-named files (`--image`,
  `--description-file`: G304) and `exec` of the browser opener (G204);
  annotate those with a reason, as basecamp-cli does (`//nolint:gosec // G204`).
- `.github/workflows/test.yml`: matrix `ubuntu-latest`, `macos-latest`;
  `actions/checkout` → `jdx/mise-action` (installs go, golangci-lint, and
  goreleaser from `mise.toml`, caches by default, and adds them to `PATH`) →
  `make check`. A `concurrency:` group per workflow and ref cancels a PR run
  that a newer push supersedes (zizmor's pedantic persona asks for one;
  Gate 2b F22). Env `PCAL_NO_KEYRING=1` (basecamp-cli sets
  `BASECAMP_NO_KEYRING` in CI) and `MISE_LOCKED=1`, so installs use only the
  URLs and checksums in `mise.lock` and fail instead of resolving anything
  new. (mise-action already runs `mise install --locked` when it finds a
  `mise.lock`, in `src/index.ts#shouldUseLockedInstall`; the env var also
  covers any later mise call.) mise itself is the action's newest release at
  least 24 hours old unless its `version:` input is set.
  This deviates from basecamp-cli in CI only. basecamp-cli also pins its
  local tools in `.mise.toml`, but its CI uses `actions/setup-go` (Go from
  `go.mod` via `go-version-file`) and `golangci-lint-action` /
  `goreleaser-action` with versions in the workflow, kept in sync with
  `.mise.toml` by comments. With mise in CI too, local and CI tool versions
  can't drift. The calendar app kept `ruby/setup-ruby` in CI (PR #103), but a
  new repo has no such legacy. Pin the action to a commit SHA, as basecamp-cli
  does (latest on 2026-10-27: v5.2.4).
- `.gitignore`: `bin/`, `dist/`, `completions/`.

**Red first** (`internal/commands/root_test.go`, `internal/version`): `pcal
--help` exits 0, an unknown command exits 1, and `UserAgent()` →
`pcal/<version> (<GOOS>/<GOARCH>)`. `go test -race` needs cgo and a C
compiler: Apple clang is present here, and GitHub's runners have one.

Commit: "Scaffold pcal".

## Phase 2 — `internal/output`: envelope, renderers, exit codes

**Red first** (`output_test.go`, table-driven):
- Success envelope `{"ok":true,"data":…,"summary":"…"}`. Error envelope
  `{"ok":false,"error":{"code","message","fields"?,"hint"?}}`.
- Mode selection: `--json` or stdout not a TTY → JSON; `--quiet` → bare
  `data`; `--jq` → implies JSON and runs gojq over the envelope (compile error
  → exit 1, as fizzy-cli's `ErrJQValidation` is a usage error); `--csv` →
  lists only (list commands define the flag; see Phase 1).
- TTY table (lipgloss) and key/value card. Styling (bold only) is off when
  `NO_COLOR` is set, or with `CLICOLOR=0` or `TERM=dumb` unless
  `CLICOLOR_FORCE` is set, as termenv decides (Gate 2b F21).
- Human output (tables, cards, summaries, and error text, the server's
  included) shows every control character as a visible Go escape (`\x1b`,
  `\u009b`), keeping line breaks in card values. RSVP names come from a
  public form, so raw escape sequences could otherwise reach a staff
  member's terminal (F5).
- `--csv` cells starting with `=`, `+`, `-`, `@`, a tab, or a carriage
  return get a leading `'`, so a spreadsheet won't run them as formulas
  (F15).
- `Print` turns a nil slice into `[]`, so an empty list is never `null`
  (F20).
- Every usage error without its own hint gets "Run `<command> --help` for
  usage.", as fizzy-cli prints for every usage error (F23).
- The `Error` type carries `Code`, `Message`, `Fields`, `Hint`, and
  `ExitCode()`.

Exit codes are a **const block**, pinned by a test like fizzy-cli
`internal/errors/errors_test.go#TestExitCodes`: 0 ok, 1 usage/validation, 2 not
found, 3 auth, 4 forbidden, 5 rate limit, 6 network, 7 API, 8 ambiguous.

> **Noted deviations from fizzy-cli (design wins):** fizzy maps **422 → exit 7**
> (`errors.go#FromHTTPStatus` → `NewValidationError` → `CodeAPI`), and its
> `default:` branch maps **400** (and any other unnamed status) → 7 too. Our
> design maps 400 and 422 → **1** ("invalid input"). We keep the design; the
> test documents both.
>
> **Error envelope (deliberate deviation from fizzy-cli; Jeremy's decision,
> 2026-10-27):** fizzy-cli's error envelope is flat,
> `{"ok":false,"error":"<message>","code":"<code>","hint":"…"}`
> (`basecamp/cli` v0.2.1 `output/envelope.go` `ErrorResponse`; codes such as
> `not_found`, `auth_required`, `api_error`). pcal keeps the design's nested
> `{"ok":false,"error":{"code","message","fields"?,"hint"?}}`, because it
> carries a 422's per-field messages in `fields`. The optional `hint`, as in
> fizzy-cli, was Jeremy's addition at the Gate 2 review.

Commit: "Add output envelope, renderers, and exit codes".

## Phase 3 — `internal/config` + credential storage (`internal/auth`)

Config lives in `internal/config`; the credential store goes in
`internal/auth` (`store.go`), where the design's layout puts it, next to the
Phase 7 browser login.

**Red first:**
- Precedence: URL: `--url` > `PCAL_URL` > config > default
  `https://calendar.promisechurch.io`. Token: `PCAL_TOKEN` > keyring/file.
  Table test covering every layer.
- Config path: `$XDG_CONFIG_HOME/pcal/config.json`, else
  `~/.config/pcal/config.json` (the design says `~/.config` on macOS too, so
  don't use `os.UserConfigDir`). It stores `url` and a cached `time_zone`.
  Written `0600`, dir `0700` (an existing directory is made 0700 on write;
  Gate 2b F8).
- The URL must be `https://`, or `http://` to localhost, 127.0.0.0/8, or
  ::1, because http would send the token unencrypted; anything else is a
  usage error saying so (F6). It's canonicalized, because it's also the
  credential key: lowercase scheme and host, no trailing dot, no default or
  empty port, the path as escaped (`%2F` stays), and no trailing slash (F7).
  No user name or password, query, or fragment.
- Credentials: keyring (`zalando/go-keyring`, service `pcal`, key = base URL);
  tests use `keyring.MockInit()`, and `keyring.MockInitWithError(err)` for
  the fallback branch (both in go-keyring v0.2.8 `keyring_mock.go`), so no
  test touches the real keychain. A missing entry is `keyring.ErrNotFound`,
  which means "not stored", not a keyring failure. `PCAL_NO_KEYRING=1` →
  `credentials.json` (`0600`), with no warning. Otherwise the first keyring
  call in a process decides (Gate 2b F3/F4, Jeremy's decision): a result or
  `ErrNotFound` → the keyring for the rest of the run, and later keyring
  errors are returned, never a silent fallback to plaintext; any other
  error, or no answer → `credentials.json` plus a one-time stderr warning.
  Every keyring call gives up after 10 seconds (go-keyring has no timeout:
  macOS runs `/usr/bin/security`, which hangs on a locked keychain with no
  one at the prompt, and D-Bus can stall; basecamp-cli uses the same
  bound). A timeout after the first call is an error whose hint suggests
  `PCAL_NO_KEYRING=1`. Tests inject a fake through a small keyring
  interface.
- `Delete` removes both, and reports a keyring failure ("it may still be
  stored there") after still removing the file copy (F2). While the keyring
  is in use, an unreadable `credentials.json` is only a warning (F9), and a
  keyring miss checks the file: an entry saved there during a fallback moves
  into the keyring (F10, after basecamp/cli's `MigrateToKeyring`). Every
  read-modify-write of the file holds `credentials.lock` (gofrs/flock),
  waiting up to 10 seconds for another pcal process (F11). A file that
  other users can read is made 0600 again, with a warning (F8).
- "Not logged in" is `auth_required`, exit 3, with the hint "Run `pcal
  login`."; `errors.Is(err, auth.ErrNoCredential)` still matches (F1). A
  token with a control character in it (say, a line break inside
  `PCAL_TOKEN`) is refused with an error that names the variable and never
  echoes the token (F19).
- The stored credential records `source`: `browser` (minted by `pcal login`)
  or `imported` (`--with-token`). `PCAL_TOKEN` is reported as source `env` and
  is never stored. Logout (Phase 6) depends on this.

Commit: "Add config and credential storage".

## Phase 4 — `internal/client`

**Red first** against `httptest.Server`:
- Headers: `Authorization: Bearer`, `Accept: application/json` (always: a
  bearer token on a non-JSON request gets a plain-text 401), `User-Agent`, and
  `Content-Type: application/json` on JSON bodies. The `pcal/<version> (…)`
  agent passes the app's `allow_browser versions: :modern` check (verified:
  the `useragent` gem parses it as browser "pcal", which isn't
  version-guarded, the same path as curl).
- JSON bodies are **nested** (`{"event": {...}}`): `docs/api.md` → Requests
  says a flat body silently drops non-column keys such as `tag_ids`. A helper
  `Body(resource string, attrs map[string]any)`. The one exception is
  `POST /cli/token`, whose body is flat (`{"code","code_verifier"}`).
- Multipart for image upload; encoding rules in Phase 11.
- Status → error, **decided by HTTP status only**; the body's `error` string
  is shown, never branched on (except `read_only_token`, which `docs/api.md`
  allows): 400 → 1, 401 → 3 (hint "run `pcal login`"), 403 → 4 (message
  "token is read-only" when the body says `read_only_token`), 404 → 2, 422 → 1,
  429 → 5, 5xx → 7, any other status (e.g. 406) → 7. Dial/timeout → 6.
  Redirects aren't followed: a 3xx → 7, "the server redirected to
  <Location>", or "the server redirected (302 Found) but didn't say where"
  when there's no `Location` (Gate 2b F18).
- Error bodies come in three shapes, and the parser must take all of them:
  `{"error":"invalid_date"}` (the app's), `{"status":400,"error":"Bad
  Request"}` (Rails defaults for malformed JSON, unknown routes, 406), and
  `{"errors":{"title":["can't be blank"]}}` (422). A body that isn't JSON (a
  proxy's 502 page) falls back to the status text.
- 422: `fields` = the `errors` object (values are **lists** of messages). The
  message comes from the **first field in body order** ("Title can't be
  blank"), so decode the object with `json.Decoder` tokens, not a Go map
  (map order is random). Humanize the key as Rails' `full_messages` does
  (activemodel 8.1.4 `Error.full_message` + activesupport `humanize`, and the
  app has no locale overrides or inflection acronyms): `_` → space, drop a
  trailing `_id`, lowercase, capitalize the first letter (`occurs_on` →
  "Occurs on", `tag_ids` → "Tag ids", `image_url` → "Image url"); `base` →
  the bare message. The same shape comes from event, tag, RSVP, and
  image-removal failures.
- Error body types (`APIError` `{error}` and `ValidationErrors` `{errors}`)
  are defined here, because this phase parses them; Phase 5 pins them to the
  golden files.
- Responses are decoded leniently (unknown fields ignored), so a server
  addition never breaks a released binary. Only the Phase 5 golden tests are
  strict.
- 30-second timeout for JSON requests. Image uploads (up to 10 MB) get a
  longer one, 5 minutes (found at Gate 1b; accepted 2026-10-27), since
  `http.Client.Timeout` covers sending the body and 10 MB over a slow uplink
  takes longer than 30 seconds.
- `--verbose` (hidden) logs `> METHOD URL` and `< STATUS text` to stderr like
  fizzy-cli `internal/client/client.go:196` and `:211`. (fizzy's `--verbose` is
  a visible root flag, `root.go:414`; hiding it is our choice.) Never log the
  `Authorization` header.

Commit: "Add HTTP client".

## Phase 5 — Golden JSON contract

- `testdata/api/*.json`: the 17 files copied from the Rails repo's
  `test/fixtures/files/api/` via `make golden RAILS=../promise-church-calendar`,
  which changes nothing unless that directory has `.json` files (Gate 2b
  F16).
  Their data is all synthetic (example.com addresses, "Admin User", zeroed
  tokens, 2099 dates), so copying them from the private app into this public
  repo exposes nothing. `make golden` is a manual target; `make check` and CI
  never need the Rails repo.
- `internal/client/types.go`, one type per golden shape:
  - `Event` (`event.json`). Its nested `tags` items are `{id,name,color}`
    with no `position`, so they get their own `EventTag` type.
    `location` is an object whose members are all nullable;
    `latitude`/`longitude` are `*float64`. `image` and `created_by` are
    nullable objects.
  - `Occurrence` (`events.json` items) = `Event` embedded, plus
    `occurrence_date`, `occurrence_starts_at`, `occurrence_ends_at` (the last
    two null for all-day events).
  - `Tag` (`tag.json`, `tags.json`), `RSVP` (`rsvps.json`; `phone` and
    `occurrence_date` nullable), `Identity` (`identity.json`),
    `AccessToken` (`access_tokens.json`), `WebsiteToken` (`api_token.json`),
    `RotatedWebsiteToken` (`api_token_rotated.json`), `CLIToken`
    (`cli_token.json`: `token`, `permission`, `user{id,name}`).
  - `APIError` (the six `error_*.json` with an `error` key: invalid_date,
    invalid_grant, invalid_parameter, not_found, read_only_token,
    unauthorized) and `ValidationErrors` (`error_validation.json`, the
    seventh). Both are defined in Phase 4.
- Timestamps stay **strings** in these wire types (the server sends
  `2099-10-25T18:00:00.000-05:00`; a `time.Time` re-marshals it as
  `2099-10-25T18:00:00-05:00`, which would break the round-trip). Parse them
  when rendering. Nullable fields are pointers **without** `omitempty`, so a
  `null` re-encodes as `null`. (Checked with Go 1.27.3: with those rules,
  `event.json` and `events.json` round-trip JSON-equal, `39.799` re-encodes
  unchanged, and `DisallowUnknownFields` still fires through the embedded
  `Event` in `Occurrence`.)
- **Red:** decode every golden file into its type with
  `DisallowUnknownFields` (catches server-side additions) and round-trip it
  (re-encode and compare as JSON values, not bytes). A test also fails if a
  file in `testdata/api/` has no entry in the table, so a new server file
  can't go unpinned. This becomes the drift alarm when the API changes.

Commit: "Pin API contract with golden JSON".

## Phase 6 — `pcal auth login --with-token`, `auth status`, `whoami`, `logout`

**Red** (commands tested through `cobra` with a fake server; output captured):
- `auth login --with-token`: reads the token from **stdin** (as in
  basecamp-cli `auth login --with-token`). When stdin is a TTY, use a masked huh
  input; when it's piped, read everything and trim it. Empty → exit 1. Verify
  with `GET /my/identity` **before** storing (source `imported`), then print
  "Logged in as Admin User (write)". 401 → exit 3, nothing stored. A
  positional token argument → exit 1 with the hint `echo … | pcal auth login
  --with-token`, and the error never repeats the token (basecamp-cli's
  `cobra.NoArgs` does). `PCAL_TOKEN` set → exit 1 before reading anything
  (Gate 3). Ctrl-C or Esc at the prompt → exit 130 (Gate 3). Replacing a
  token `pcal login` minted revokes it (Gate 3). After a login with `--url`,
  a note says pcal doesn't save it (Gate 3).
- `pcal auth` is the first group command. cobra shows help (exit 0) for any
  word after a command it can't run, before checking arguments (cobra
  1.10.2 `command.go:955`), so a group runs to show help and its `Args` is
  the root's `unknownCommand`: `pcal auth frobnicate` is exit 1, with
  suggestions. No subcommand defines a `PersistentPreRunE`, which would
  replace the root's.
- `internal/prompt` (huh v2.0.3, `charm.land/huh/v2`, not v1.0.0; see the
  header's Gate 3 decisions: `NewInput().EchoMode(EchoModePassword)`,
  `NewConfirm`). For accessibility (found at Gate 1b; accepted 2026-10-27),
  every form honors `ACCESSIBLE` (non-empty → `form.WithAccessible(true)`,
  the switch huh's README recommends), which swaps the redrawing TUI for
  plain prompts a screen reader can follow. Prompts draw on stderr, in a
  theme with no colors: huh's base theme with bold, faint, and reverse only,
  help line included. huh quits only on Ctrl-C (`keymap.go:109`); pcal adds
  Esc, as basecamp-cli does. Tests drive the drawn form with `WithInput`
  keystrokes (`\r` is Enter, `\x03` Ctrl-C) and accessible confirms with
  plain text lines; bubbletea v2 draws into a buffer only when given a
  window size and a color profile, which tests pass as program options
  (`WithProgramOptions` replaces the options `WithInput` added, so it goes
  first). An accessible *password* field reads the terminal through its
  file descriptor and, given any other reader, answers "" without a prompt
  (`field_input.go`), so it's checked in a real pty instead, and `Password`
  refuses a non-terminal there. In accessible mode huh drops every field
  error (`form.go#runAccessible`), so Ctrl-C reaches pcal only as SIGINT:
  logins catch it, and a canceled masked prompt puts the terminal's echo
  back.
- `auth status`: URL, user, permission, and credential source (keyring / file /
  env). Not logged in → exit 3. It calls `/my/identity`, so it also checks
  the token (basecamp-cli's and fizzy-cli's are local only).
- `whoami`: identity JSON/card. Every `/my/identity` call (`whoami`, `auth
  status`, both logins) caches `time_zone` in config. A command that
  converts times (Phases 10–12) and finds no cached zone, e.g. with only
  `PCAL_TOKEN` set, calls `/my/identity` first and caches it. A rejected
  `PCAL_TOKEN` gets a hint about it, since `pcal login` can't fix that.
- `--verbose` is wired to `client.Options.Verbose` (stderr).
- `logout` (basecamp-cli `revoke.go#LogoutCredential` semantics; `pcal auth
  logout` is the same command):
  - source `browser` → `DELETE /my/current_access_token`. 204 or 401 (already
    gone) → "Logged out; token revoked". This works for a read token too (the
    server skips the read-only check on this one route). Network error or 5xx → still forget
    locally, and warn "couldn't revoke …; it may still be live, so revoke it
    at /my/access_tokens". Exit 0 either way, since the local credential is gone.
  - source `imported` → forget locally only, with "That token is still
    valid; revoke it at /my/access_tokens or with `pcal tokens revoke ID`".
  - `PCAL_TOKEN` set → exit 1 "logged in via PCAL_TOKEN, which pcal can't log
    out of".
  - nothing stored → exit 0, "Not logged in".
  - The local forget fails (since Gate 2c, `Store.Delete` returns a keyring
    error, after removing any file copy) → exit 7, and the message says
    what happened on the server first ("revoked the token on the server,
    but couldn't forget it here: …"), keeping the store's hint (Gate 3).
  - Red tests cover each branch (the fake server records the DELETE).
- Test seams: `Streams.InTTY`; `app.getenv` (the environment, injected, so
  tests never set process variables); a credential-store interface, so a
  test can make `Delete` fail after `Get` worked; an injectable Ctrl-C
  context; and a `TestMain` in `commands` that installs go-keyring's mock
  and points `HOME` at a temporary directory. The mock is a package-global
  map without a lock, so those tests don't run in parallel.

Commit: "Add token login, status, whoami, logout".

## Phase 7 — `pcal login` (browser, PKCE)

`internal/auth/browser.go`:
- Listen on `127.0.0.1:0`. Generate 32 random bytes each for `state` and the
  verifier (base64url, 43 chars). `challenge = base64url(sha256(verifier))`.
- Open `<base>/cli/authorization/new?port&state&code_challenge&device=<hostname>&permission=<write|read>`
  with `open` / `xdg-open` / `rundll32 url.dll,FileProtocolHandler` (basecamp-cli
  `auth.go#openBrowser` delegates to a host util the same way). **Always print
  the URL too.**
- The server rejects bad parameters with a 400 **page**, never a redirect, so
  the CLI would just wait out its timeout. Send only valid ones
  (`Cli::AuthorizationsController#valid_request?`): `port` 4–5 digits in
  1024–65535 (the default ephemeral ranges all are: macOS and Windows
  49152–65535, Linux 32768–60999; the range is configurable, so if the
  listener gets a port below 1024, exit 1 with a clear message instead of
  waiting), `state` 1–128 chars of `A-Za-z0-9_-` (our 43-char base64url
  fits), `code_challenge` exactly 43 base64url chars (unpadded), `permission`
  `read`|`write`, and a non-blank `device`. The verifier (43 chars,
  unpadded base64url) satisfies the server's `VERIFIER_FORMAT` (43–128 of
  `A-Za-z0-9-._~`). If
  `os.Hostname()` fails or is blank, send `device=unknown host`. The server
  truncates `device` to 60 characters and names the token "pcal on <device>".
- Handle `/callback`: state mismatch → 400 page + error. `error=access_denied`
  → exit 3 "Authorization denied". Success page: "You can close this tab".
  Timeout after 5 minutes → exit 3. Only the first callback is accepted.
- `POST /cli/token` with a **flat** JSON body `{"code","code_verifier"}` → 201
  `{token, permission, user{id,name}}` → store → `/my/identity` → "Logged in
  as <name> (<permission>)".
  - 400 `invalid_grant` (unknown, expired, reused, or wrong verifier; every
    attempt consumes the code) → exit 3 "That login code expired or was
    already used. Run `pcal login` again."
  - 429 `rate_limited` (more than 10 a minute from one IP) → exit 5 "Too many
    login attempts. Wait a minute, then run `pcal login` again." No automatic
    retry.
- `--read-only` → `permission=read`. `--url` overrides the base.
- The stored credential has source `browser`.
- **Headless / paste fallback** (basecamp-cli `LoginOptions.defaults`,
  `hostutil.HeadlessReason`, `readCallbackInput`): `internal/auth/host.go`
  `HeadlessReason()` → `"SSH session"` (`SSH_CONNECTION`/`SSH_CLIENT`/`SSH_TTY`),
  `"CI environment"` (`CI` truthy), `"no display"` (Unix without
  `DISPLAY`/`WAYLAND_DISPLAY`), or `""`.
  - Remote mode (auto when headless, or `--remote`; `--local` wins over the
    detection; `--remote` + `--local` → exit 1): don't open a browser. Print
    "Not opening a browser here (SSH session). Open this link on any device:"
    plus the URL, then prompt "Paste the address your browser ended up on:".
    Parse `code`/`state` (or `error`) from the pasted URL, require a matching
    state, and exchange as usual. The prompt waits up to 5 minutes, matching the
    server's code lifetime.
  - `--no-browser`: keep the local listener but don't launch a browser; just
    print the link.
  - A failed browser launch isn't a failed login: say "Couldn't open a browser.
    Open the link above." and keep waiting.

**Red** (drive the listener directly; the Rails side is an `httptest.Server`):
success (asserting the flat body and the authorization URL's parameters);
state mismatch; access_denied; timeout (injected short duration);
`/cli/token` 400 → exit 3; `/cli/token` 429 → exit 5; a second callback ignored. The browser opener is an
injectable func. Paste mode: the URL is parsed (success, `error=access_denied`,
wrong state, garbage input → re-prompt once, then exit 1). `HeadlessReason`
table test with an injected env and GOOS. `--local` overrides the detection.

Commit: "Add browser login".

## Phase 8 — `internal/parse`: dates, times, `--repeat`

**Red first** (pure table tests, fixed "now" + `America/Chicago`):
- Dates: `2026-10-04`, `today`, `tomorrow`, `sat`, `saturday`, `next sun`
  (strictly after this week's). A bare weekday = the next occurrence, today
  included. Invalid → exit 1.
- Times: `10am`, `10:30am`, `6:30pm`, `18:30`, `noon`. Combined with a date →
  RFC 3339 with the zone offset (DST-aware: check both Oct 31 and Nov 2). The
  server would read an offset-less time as America/Chicago, but the CLI always
  sends the offset. (DST ends 2026-11-01: 6pm on Oct 31 is `-05:00`, on Nov 2
  `-06:00`, checked with Go 1.27.3.) Import `time/tzdata` so the zone loads on
  Windows and on minimal Linux images: Windows has no system zoneinfo
  sources for `LoadLocation`, and a `-trimpath` release binary has no GOROOT
  `zoneinfo.zip` to fall back to (`time/zoneinfo_read.go#loadLocation`).
- `--repeat` → RRULE: `daily` → `FREQ=DAILY`; `weekly on sun` →
  `FREQ=WEEKLY;BYDAY=SU`; `every 2 weeks on tue,thu` →
  `FREQ=WEEKLY;INTERVAL=2;BYDAY=TU,TH`; `monthly on the 1st sun` →
  `FREQ=MONTHLY;BYDAY=1SU`; `monthly on the last fri` → `BYDAY=-1FR`;
  `yearly` → `FREQ=YEARLY`. Unknown → exit 1 with the accepted forms.
  `--repeat` has no end; a series that stops needs `--rrule` with `UNTIL` or
  `COUNT` (e.g. `FREQ=WEEKLY;BYDAY=SU;UNTIL=20271231`), which the server
  supports.
  The server now **validates** rules (`docs/api.md` → Recurrence rules;
  `RecurrenceExpandable#recurrence_rule_must_be_expandable`): only FREQ,
  INTERVAL, BYDAY, COUNT, UNTIL, each once; INTERVAL/COUNT positive; UNTIL a
  date; weekly BYDAY = plain days; monthly BYDAY = one day with a position
  from -5 to 5; no BYDAY with DAILY/YEARLY; no `RRULE:` prefix. Every
  `--repeat` output must satisfy that; a test asserts each one against those
  rules.
- `--rrule RAW` passes through **verbatim**. The CLI doesn't re-validate it
  (no second copy of the rules); a bad rule comes back as a 422 on
  `recurrence_rule` with the server's message, exit 1. `--repeat` and
  `--rrule` together → exit 1.

Commit: "Add date, time, and recurrence parsing".

## Phase 9 — `pcal tags`

This establishes the command/test pattern the rest copy.
`list | create NAME [--color] | update ID [--name --color --position] | delete ID [--yes]`.
Body `{"tag":{name,color,position}}`. `color` is optional and `#RRGGBB` when
given; a duplicate name or a bad color is a 422. `GET /tags` is already
ordered by position, then name; keep that order.
Red: each against the fake server and golden JSON. 422 → exit 1 with fields.
Delete without `--yes` and without a TTY → exit 1. Delete with a TTY → huh
confirm (injectable).

Commit: "Add tags commands".

## Phase 10 — `pcal events list | show`

- `list`: `--from/--to` → `start`/`end` (`YYYY-MM-DD`, `end` inclusive;
  default today → +30, the server's default too). `--to` before `--from` →
  exit 1 before any request (the server would answer 400
  `invalid_date_range`). Repeatable `--tag NAME` → resolved via `GET /tags`
  (unknown → exit 1) → `tag_ids[]=…` (any of the tags). Name matching: exact
  case first, then a unique case-insensitive match; names that differ only
  by case → exit 8 (the server's uniqueness check and index are
  case-sensitive, so `Worship` and `worship` can coexist).
- `--tag` is a cobra `StringArrayVar` here and in Phase 11, not
  `StringSliceVar`: a slice splits `--tag "Kids, Youth"` on the comma (tag
  names may contain commas). With an array, `--tag ''` yields `[""]`, so
  blank values are dropped, and a `--tag` that was `Changed` but has no
  non-blank names means "no tags" (verified with cobra 1.10.2 / pflag 1.0.10). `--search Q` → `q`
  (case-insensitive, **anywhere** in the title). `--unpublished` →
  `published=false`, `--website` → `website=true`.
- The response is **occurrences**: a recurring event appears once per
  occurrence, each item the full event (same as `show`) plus
  `occurrence_date`, `occurrence_starts_at`, `occurrence_ends_at`. The table
  uses those, never the series `occurs_on`/`starts_at`: date =
  `occurrence_date` (for a multi-day event, the span `occurrence_date` →
  `occurrence_date + (ends_on − occurs_on)`), time = `occurrence_starts_at`
  (blank → "all day"). An item can start **before** `--from` (a multi-day
  occurrence already under way); show it, don't filter it out. Keep the
  server's order (date, then start time with all-day last, then title).
  Columns: date, time, title, tags, flags (draft / web / rsvp). Summary:
  "12 events Oct 1–31" (counts occurrences). `--csv`.
- `show ID`: card with every field. Dates there are the **series** dates;
  `recurrence_text` is shown when present.
- **ID resolution** (`internal/commands/resolve.go`): a UUID passes through.
  Otherwise fetch `GET /events?q=<arg>&start=<today-1y>&end=<today+2y>`,
  dedupe by id, then match case-insensitively, in this order (`q` matches
  anywhere, so the CLI does its own matching):
  1. **Exact title** wins. One event titled exactly the argument → that
     event, even if other titles start with it (so "Worship" still finds
     `Worship` when `Worship Night` exists). Two or more with that exact
     title → exit 8.
  2. Otherwise, titles that **start with** the argument: one → that event,
     several → exit 8.
  3. None → exit 2 with the hint "use the event's ID (`pcal events list
     --search …` shows it)".

  Exit 8 lists the candidates (id, title, first date). Red: a table test for
  each case, including exact-beats-prefix and duplicate exact titles.
  *Limitations:* events wholly outside that window need the UUID. The list
  endpoint is occurrence-based, and a wider window costs expansion time on the
  server. The server also stops expanding a series after **365 occurrences
  counted from the series start** (`RecurrenceExpandable::MAX_OCCURRENCES`),
  so a daily series that began more than a year before the window has no
  occurrences in it, and neither `list` nor title resolution sees it. That's
  a known server limitation; the README says to use the UUID.

Commit: "Add events list and show".

## Phase 11 — `pcal events create | update | delete | toggles | image`

- Flag → `event` attribute (request names differ from the response's):

  | Flag | Attribute |
  |---|---|
  | `--title` | `title` (required) |
  | `--date` | `occurs_on` (required) |
  | `--end-date` | `ends_on` (multi-day; on or after `occurs_on`) |
  | `--start`, `--end` | `starts_at`, `ends_at` (see "Dates and times" below) |
  | `--all-day` | `starts_at: null`, `ends_at: null` (all-day = neither time) |
  | `--repeat` / `--rrule` | `recurrence_rule` |
  | `--location-name`, `--address` | `location_name`, `location_address` |
  | `--map-url` | `location_map_url` (Google Maps HTTPS links only; else 422) |
  | `--tag NAME…` | `tag_ids` (**replaces** the event's tags) |
  | `--description`, `--description-file` | `description` |
  | `--notes` | `internal_notes` |
  | `--image F`, `--image-alt` | `featured_image` (multipart), `image_alt` |
  | `--image-url URL` | `image_url` (a linked image; HTTPS only, else 422) |
  | `--website`, `--rsvp`, `--draft` | `show_on_website: true`, `rsvp_enabled: true`, `published: false` |

  The booleans are always sent as JSON `true`/`false` (null is a 422). On
  create, omitted ones take the server defaults: published, not on the
  website, RSVPs off.
- `create`: missing `--title`/`--date` with a TTY → huh form for just those;
  without a TTY → exit 1. `--description-file F` (`-` = stdin).
- `update ID`: only the flags given are sent (a `cmd.Flags().Changed` guard, so
  unset flags never clear fields). A flag given an empty value clears that
  field: `--end-date ''` → `ends_on: null`, `--rrule ''` → stops repeating,
  `--tag ''` → `tag_ids: []`, `--description ''`, and so on. (Checked against
  actionpack 8.1.4: a JSON `{"event":{"tag_ids":[],"ends_on":null}}` reaches
  `permit` as `[]` and `nil`, since deep munging only drops `nil`s inside
  arrays. pflag reports `Changed` for an empty value.)
- **Dates and times.** The server stores `starts_at`/`ends_at` as full
  timestamps, independent of `occurs_on`/`ends_on`, and computes each
  occurrence's times by shifting `starts_at` by the days between `occurs_on`
  and the occurrence. Nothing on the server keeps them consistent, so the CLI
  does: `starts_at` = `occurs_on` + `--start`; `ends_at` = (`ends_on`, or
  `occurs_on` for a single-day event) + `--end`. On `update`, when any of
  `--date`, `--end-date`, `--start`, `--end` is given, `GET` the event first
  (unless the flags alone supply every date and time involved), then send
  **both** times rebuilt from the resulting dates and wall-clock times. That
  covers moving a timed event (`update --date sat` moves a 6pm event to
  Saturday at 6pm instead of leaving its times on the old day) and giving an
  all-day event a time (`update --start 10am` needs its `occurs_on`). Moving
  an all-day event's dates sends no times.
  `--all-day` with `--start`/`--end` → exit 1.
- **Multipart** (any request with `--image`): every attribute becomes a form
  field `event[name]`; booleans `true`/`false`; null → an empty value; arrays
  → repeated `event[tag_ids][]` (one empty `event[tag_ids][]` arrives as
  `[""]`, and `ids_writer`'s `compact_blank` turns it into `[]`, clearing the
  tags). The file goes in `event[featured_image]`. JPEG, PNG, GIF, or WebP,
  10 MB at most (the check is `byte_size > 10.megabytes`); the server's 422 is
  the authority.
- `delete ID [--yes]` → 204.
- `publish|unpublish`, `website on|off`, `rsvp on|off` → a one-field `PATCH`
  (`published`, `show_on_website`, `rsvp_enabled`).
- **`--image-url` (added at Gate 1, Jeremy's call).** An event has either an
  upload or a link, never both, and setting one replaces the other
  (`Event#enforce_image_mutual_exclusivity`). `--image` with `--image-url` →
  exit 1. A link is a plain JSON field, so no multipart. The old alt text
  stays unless `--image-alt` is sent.
- `image set ID FILE [--alt]` → multipart `PATCH` (`event[featured_image]`,
  `event[image_alt]`). `image set ID --url URL [--alt]` → JSON `PATCH`
  (`image_url`, `image_alt`). Exactly one of FILE and `--url` → else exit 1.
  A new upload replaces a linked `image_url` and vice versa. `image rm
  ID` → `DELETE /events/:id/image`: 204 even when there's no image; 422 (exit
  1, fields shown) if the event is otherwise invalid, in which case nothing is
  removed.
- Unknown tag ids are a 422 on `tag_ids` (not a 404). Names are resolved
  first, so this only happens if a tag is deleted mid-command.
- Red: request-body assertions for each, including the nested `event` key,
  `tag_ids` resolution, RFC 3339 times on the right dates (create, and update
  moving a timed event), the multipart encoding, `image_url` as JSON (and
  `--image` + `--image-url` → exit 1), and RRULE. 403 with a read token →
  exit 4.

Commit: "Add event write commands".

## Phase 12 — `pcal rsvps`, `tokens`, `website-token`

- `rsvps list EVENT [--date D] [--csv]` → `GET /events/:id/rsvps`, newest
  first; `--date` → `?occurrence=YYYY-MM-DD` (the param is `occurrence`, not
  `date`; a bad date is a 400 `invalid_date`, so parse it first → exit 1).
- `rsvps add EVENT --name --email [--phone --date]` → `{"rsvp":{name, email,
  phone, occurrence_date}}`. Missing `--name`/`--email` with a TTY → a huh
  form for just those (the design's rule for required fields, as in
  `events create`); without a TTY → exit 1. A recurring event without `--date`, or an event
  with RSVPs turned off, → server 422 → exit 1, shown verbatim ("Occurrence
  date is required for recurring events", "Event does not accept RSVPs").
- `rsvps delete EVENT RSVP_ID [--yes]` → 204; 404 when the RSVP belongs to
  another event.
- `tokens list` → `GET /my/access_tokens` (yours only, newest first).
  `tokens revoke ID [--yes]` → `DELETE /my/access_tokens/:id` (404 when it
  isn't yours).
- `website-token show` → `GET /api_token` (`exists`, `description`,
  `last_used_at`, `created_at`; all but `exists` null when there's none).
  `rotate [--yes]` → `POST /api_token` → 201 `{token, created_at}`. It prints
  the new token once, with a warning that the church website must be updated
  now, because the old token stops working immediately (`ApiToken.regenerate!`
  deletes the old row in the same transaction).

**Red first:** request assertions for each against the fake server and the
golden files (`rsvps.json`, `access_tokens.json`, `api_token.json`,
`api_token_rotated.json`): the `occurrence` param, the nested `rsvp` body,
422 → exit 1 with the server's messages, 404 → exit 2, `--yes` / no-TTY
confirmation, and `rotate` printing the token once.

Commit: "Add RSVP, token, and website-token commands".

## Phase 13 — `version`, `completion`, `skill install`, README

- `version` → version, commit, date, Go version. A `go install …@v0.1.0`
  build has no ldflags (found at Gate 1b; accepted 2026-10-27), so when
  `Version` is still `dev`, fall back to `debug.ReadBuildInfo().Main.Version`.
- **Red first:** `version` output (ldflags values, and the build-info
  fallback); `completion` emits a script for each shell; `skill install`
  writes the embedded file under a temp `HOME`, and won't overwrite without
  a confirmation or `--yes`.
- `completion bash|zsh|fish` (cobra).
- `skills/pcal/SKILL.md` via `go:embed`. `skill install` writes
  `~/.claude/skills/pcal/SKILL.md` and asks before overwriting unless `--yes`.
  The skill teaches agents to use `--json`, the exit codes, and ID resolution.
- `README.md`: install (Homebrew, `mise use -g github:rorJeremy/promise-calendar-cli`,
  `go install`), login, 10 common recipes, output modes, exit codes.

Commit: "Add version, completion, skill, README".

## Phase 14 — Release pipeline + v0.1.0

`.goreleaser.yaml` (trimmed basecamp-cli; no signing, SBOM, nfpms, scoop, AUR,
plus a quarantine hook basecamp-cli doesn't need). `goreleaser check`
(v2.19.0) exits 0 on this file, and `goreleaser release --snapshot --clean`
on a toy module with the same layout built all six targets, archives with
`README.md` + `completions/*`, and the cask below (checked 2026-10-27):

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
        install_steps: |
          on_macos do
            run "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "{{ .StagedPath }}/pcal"]
          end
```

- **Cask, not formula.** basecamp-cli uses `homebrew_casks` (goreleaser
  deprecated `brews` softly in v2.10 and fully in v2.16). The install command
  `brew install rorJeremy/tap/pcal` is unchanged. Homebrew renames
  `completions/pcal.zsh` to `_pcal` when it installs it
  (`cask/artifact/zshcompletion.rb`); basecamp-cli names the file `_basecamp`
  directly. Either works.
- **Quarantine hook:** we skip code signing (design), so without it Gatekeeper
  blocks the unsigned binary on first run (goreleaser's `homebrew_casks` docs,
  "Signing and Notarizing"). basecamp-cli has no such hook; it notarizes. Use
  `install_steps` (Homebrew's declarative `postflight_steps`): the old
  `install:` Ruby form (`system_command`, `#{staged_path}`) is deprecated in
  goreleaser v2.19.0, and `goreleaser check` then exits 2 ("configuration is
  valid, but uses deprecated properties"). Homebrew itself now deprecates
  `postflight` for `postflight_steps` (`cask/dsl.rb`).
- `.github/workflows/release.yml`: on `v*` tags → checkout (`fetch-depth: 0`)
  → `jdx/mise-action` → `make check` → `goreleaser release --clean` (the
  mise-pinned goreleaser, instead of `goreleaser/goreleaser-action`), with env
  `GITHUB_TOKEN` + `HOMEBREW_TAP_TOKEN`, and `MISE_LOCKED=1` as in
  `test.yml`. Permissions: `contents: write`.
- Local checks: `mise exec -- goreleaser check` (exit 0, with no deprecation
  warnings), `mise exec -- goreleaser release --snapshot --clean`, and run
  `dist/pcal_darwin_amd64_v1/pcal version` (this Mac is x64; on arm64 it's
  `dist/pcal_darwin_arm64_v8.0/pcal`). Don't use a `pcal_darwin_*` glob: it
  matches both directories, so the second path becomes an argument
  (`unknown command`, exit 1).
- Test the mise install path against the published release:
  `mise use -g github:rorJeremy/promise-calendar-cli@0.1.0` → `pcal version`.
  mise's `github` backend picks the archive for the OS/arch from goreleaser's
  `pcal_<version>_<os>_<arch>` names. (Checked: `mise lock` on
  `github:basecamp/basecamp-cli`, which uses the same template, resolved the
  right archive for linux/macOS × x64/arm64 and windows-x64 and stripped the
  tag's `v`.)
- **Red step:** none. This phase is configuration, checked by
  `goreleaser check`, the snapshot, and the live smoke test below.

Live smoke test against production (PR 2 is deployed, so this is unblocked): `pcal login` →
`whoami` → `events list` → create a draft test event → update → image set/rm →
delete → `pcal login --read-only` and a write attempt → exit 4 → `pcal logout`
(the token disappears from `/my/access_tokens`) → an SSH-session login via
paste. Then tag `v0.1.0`.

Commit: "Add release pipeline".

## Manual follow-ups (Jeremy)

1. Phase 0: `mise install` in the repo (after Phase 1 adds `mise.toml`), and
   remove the old Homebrew Go 1.17.2 with `brew uninstall go`.
2. Before tagging v0.1.0:
   - Create the **public** repo `rorJeremy/homebrew-tap`. It must be public for
     `brew install` to work without auth.
   - Create a fine-grained PAT with contents read/write on `homebrew-tap` only,
     and add it as the `HOMEBREW_TAP_TOKEN` Actions secret on
     `promise-calendar-cli`.
   - Decide on a LICENSE. The repo is public, and with no license the code is
     "all rights reserved": per choosealicense.com/no-permission, others then
     have no permission to use, copy, modify, or share it, and GitHub's terms
     only let people view and fork it. For church staff using your own tool
     that's moot in practice, but if anyone else should be able to install,
     use, or reuse it, add a license (fizzy-cli and basecamp-cli use MIT).
3. **Repo visibility: decided public (2026-09-29).** This lets `go install`,
   `mise use github:…`, and the cask download work without GitHub auth, and
   makes CI free: public repos on standard GitHub-hosted runners aren't
   billed, while a private repo on GitHub Free gets 2,000 included minutes a
   month and macOS minutes are priced about 10× Linux ($0.062 vs $0.006 a
   minute; GitHub's Actions billing docs, checked 2026-10-27). Nothing in the repo is sensitive; access control is
   the server's per-user tokens. Before the switch, commit authors were
   rewritten to `1967644+rorJeremy@users.noreply.github.com`, and the repo's
   git config uses that address, because the calendar app repo is private and
   the hotmail address had never been public.

## Notes / risks

- **Contract drift** is caught by the golden-JSON decode tests (Phase 5) and the
  Rails-side golden test (`test/controllers/golden_json_test.rb`, added in
  PR 2 Phase 12).
- **Title resolution window** (Phase 10) is a deliberate limitation. The UUID
  always works.
- **365-occurrence cap** (server, not fixed): long-running daily series drop
  out of `events list` and title resolution in later windows. Documented in
  the README; a server fix would need no CLI change.
- **Keyring on headless Linux** (or a keychain that doesn't answer within
  10 seconds on the first call) falls back to a `0600` file with a warning
  (design). CI forces the fallback with `PCAL_NO_KEYRING=1`.
