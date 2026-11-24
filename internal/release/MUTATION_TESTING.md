# Mutation testing for release guards

`make test-mutations` runs `internal/release/test_all_mutations.sh` in a
detached git worktree. It does not edit the checkout you started from.
It is not part of `make check`.

## Kill rule

A run is **KILLED** only when the test exits non-zero and the output
contains that mutant's assertion. A passing test is **SURVIVED**. A
build error, panic, timeout, missing tool, patch that does not apply,
compile error, unchanged file, or any other failure is **INVALID** or
**ERROR**, and the reason is printed.

The command exits non-zero if any mutant is SURVIVED, INVALID, or ERROR.

Every mutant is also run against the unmutated guard (the control). The
control must fail with the same assertion. The input is one that only
the targeted check rejects: a failure of a different check is ERROR, not
a kill.

## What the mutants are

Config and artifact mutants are bad `.goreleaser.yaml` values. The
unmutated guard should reject each one with its own assertion, so the
control and the mutant run are both KILLED.

Guard-code patches delete or weaken one check. The input is one the
unmutated guard rejects and that the weakened guard accepts. Those
mutants are SURVIVED. That is the score; it is not a kill.

## Scoring proofs

The script keeps two runs that are not part of the mutant tally:

- a comment-only edit of the YAML guard, on a valid config, must score
  SURVIVED. A passing `go test` without `-v` prints `ok`, not `PASS`.
- a config goreleaser cannot archive must score ERROR, not KILLED.

If either proof scores as something else, the command fails.
