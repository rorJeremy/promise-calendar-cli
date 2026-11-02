---
name: pcal
description: |
  Use the pcal CLI for the Promise Church calendar: events, tags, RSVPs,
  images, personal access tokens, and the website token. Prefer this skill
  whenever a task is about that calendar.
---

# pcal

pcal talks to the Promise Church calendar. People see tables. Scripts see a JSON envelope. Do not put a token in an argument or in a shell command that would land in history. Use `PCAL_TOKEN` or `pcal login --with-token`, which reads stdin.

## How to call it

- Pass `--json` when you need the envelope, or `--jq` to filter it. `--jq` implies `--json`. Do not pipe pcal to an external `jq`.
- `--quiet` prints only `data`.
- A list command accepts `--csv`. A cell that starts with `=`, `+`, `-`, or `@` is prefixed with `'`. Control characters in a cell are shown as escapes, in a pipe and in a terminal.
- Human dates and times are in the calendar's time zone. JSON keeps the server's values.
- pcal asks before a delete, a revoke, or rotating the website token. The question opens on No. Pass `--yes` to skip it. Esc cancels (exit 130) and changes nothing.

## IDs

- An event argument accepts a UUID, an exact title, or a unique title prefix. An exact title wins over a prefix. Several matches is exit 8. Events outside today−1 year through today+2 years need the UUID. A series also stops after 365 occurrences from its start, so an old daily series needs the UUID.
- `tags update` and `tags delete` take an ID or a name: exact case, then a unique case-insensitive match. Two names that differ only by case is exit 8. No match is exit 2.
- `rsvps delete` takes the RSVP's ID, not the person's name. `tokens revoke` takes the token's ID, not its description.
- `--tag` takes tag names and resolves them to IDs.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | OK, including a confirmation answered No |
| 1 | Usage, 400, or 422 |
| 2 | Not found |
| 3 | Not logged in, or the token was rejected |
| 4 | Forbidden, including a read-only token |
| 5 | Rate limited |
| 6 | Network |
| 7 | Server or unexpected error |
| 8 | Ambiguous name or title |
| 130 | Canceled with Esc or Ctrl-C |
| 143 | Stopped by SIGTERM |

With `--json`, the error envelope goes to stdout. A human terminal or `--csv` writes the error to stderr. Exits 130 and 143 print one line on stderr and no envelope.

## Commands

```
pcal login [--read-only] [--remote]
pcal whoami
pcal events list [--from D --to D --tag NAME --search Q]
pcal events show EVENT
pcal events create --title T --date D [--start 6pm --end 7:30pm --repeat "weekly on sun"]
pcal events update EVENT [--date D --start 10am]
pcal events delete EVENT [--yes]
pcal tags list
pcal tags create NAME [--color #RRGGBB]
pcal rsvps list EVENT [--date D]
pcal rsvps add EVENT --name N --email E [--date D]
pcal tokens list
pcal tokens revoke ID [--yes]
pcal website-token show
pcal website-token rotate [--yes]
pcal version
```
