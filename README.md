# pcal

pcal is the command-line interface for the [Promise Church calendar](https://calendar.promisechurch.io). It lists and edits events, tags, and RSVPs, and it manages the personal access tokens and the website token.

In a terminal it prints tables. Piped, or with `--json`, it prints a JSON envelope. `--quiet` prints only the data, and `--jq` filters the envelope.

## Install

Homebrew, once the tap exists:

```sh
brew install rorJeremy/tap/pcal
```

mise:

```sh
mise use -g github:rorJeremy/promise-calendar-cli
```

Go:

```sh
go install github.com/rorJeremy/promise-calendar-cli/cmd/pcal@latest
```

A `go install` build has no version stamped into it. `pcal version` then reports the module version from the build info. A release build reports the version, commit, and date it was built with.

Shell completions:

```sh
pcal completion bash > /etc/bash_completion.d/pcal
pcal completion zsh > "${fpath[1]}/_pcal"
pcal completion fish > ~/.config/fish/completions/pcal.fish
```

The agent skill, for Claude Code:

```sh
pcal skill install
```

That writes `~/.claude/skills/pcal/SKILL.md` and asks before replacing a file that's already there.

## Login

```sh
pcal login
```

The browser opens on this computer. On another machine, `pcal login --remote` prints a link and asks you to paste the final address back. A token never goes in an argument. For one you already have:

```sh
pcal login --with-token
```

pcal reads it from stdin. `PCAL_TOKEN` in the environment is used as-is and is not stored. `pcal whoami` checks the token. `pcal logout` revokes a token this login minted and forgets it on this computer.

The default calendar is `https://calendar.promisechurch.io`. Point at another one with `--url` or `PCAL_URL` on every command. pcal does not save that URL.

## Recipes

1. See who the token belongs to.

   ```sh
   pcal whoami
   ```

2. List the next month of events.

   ```sh
   pcal events list
   ```

3. Show one event by its title, or by its ID when the title is outside the search window.

   ```sh
   pcal events show "Worship Night"
   ```

4. Create a weekly event. In a terminal, a missing title or date is asked for.

   ```sh
   pcal events create --title "Prayer" --date sat --start 6pm --end 7:30pm --repeat "weekly on sun"
   ```

5. Move that event to next Saturday and keep its times.

   ```sh
   pcal events update "Prayer" --date "next sat"
   ```

6. Publish it on the church website, with RSVPs.

   ```sh
   pcal events website on "Prayer"
   pcal events rsvp on "Prayer"
   ```

7. Delete it. The question opens on No. `--yes` skips the question.

   ```sh
   pcal events delete "Prayer"
   ```

8. List tags, and add one.

   ```sh
   pcal tags list
   pcal tags create Kids --color "#33FF57"
   ```

9. Read the RSVPs for one occurrence.

   ```sh
   pcal rsvps list "Prayer" --date sat
   ```

10. See the website token's status, and your own access tokens.

    ```sh
    pcal website-token show
    pcal tokens list
    ```

Dates accept `2026-11-07`, `today`, `tomorrow`, `sat`, and `next sun`. Times accept `6pm` and `18:30`. Both are interpreted in the calendar's time zone.

## Output

A terminal prints a table or a card, with bold labels and no color. `NO_COLOR`, `CLICOLOR=0`, and `TERM=dumb` turn the bold off too, unless `CLICOLOR_FORCE` is set.

Anywhere else, and whenever `--json` is set, the shape is:

```json
{"ok": true, "data": [], "summary": "0 events Nov 2–Dec 2"}
{"ok": false, "error": {"code": "not_found", "message": "no event \"Prayer\"", "hint": "use the event's ID (`pcal events list --search Prayer` shows it)"}}
```

`--jq` filters that envelope and implies `--json`. `--quiet` prints `data` only. `--csv` is for lists. A cell a spreadsheet would treat as a formula, one starting with `=`, `+`, `-`, or `@`, is prefixed with `'`. Control characters in a cell are shown as escapes, in a pipe and in a terminal.

Confirmations open on No. Enter and, on an accessible prompt, Ctrl-D answer No and exit 0. Esc, Ctrl-C, and SIGTERM cancel: one line on stderr, exit 130 or 143, and no envelope.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | OK, including a confirmation answered No |
| 1 | Usage, invalid input, or the server's 422 |
| 2 | Not found |
| 3 | Not logged in, or the token was rejected |
| 4 | Forbidden, including a read-only token |
| 5 | Rate limited |
| 6 | Network |
| 7 | The server failed, or the response was not one pcal understands |
| 8 | The name or title matches more than one record |
| 130 | Canceled with Esc or Ctrl-C |
| 143 | Stopped by SIGTERM |

## License

No license is set yet. Until there is one, the code is all rights reserved.
