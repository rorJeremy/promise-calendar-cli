# Mutation testing for release guards

`make test-mutations` runs `internal/release/test_all_mutations.sh` in a
detached git worktree. It does not edit the checkout you started from.
It is not part of `make check`.

## How a run is scored

Every mutant runs twice. The control is the unmutated guard on that
mutant's input. The mutant run applies the mutation and uses the same
input.

A test is **KILLED** only when it exits non-zero and the output contains
that mutant's assertion. A passing test is **SURVIVED**. A build error,
panic, timeout, skip, missing tool, patch that does not apply, compile
error, or unchanged file is **INVALID** or **ERROR**, with the reason
printed.

Config and artifact mutants are bad `.goreleaser.yaml` values. Both
runs use the unmutated guard, so both must be **KILLED**.

Guard mutants (G1–G12) delete or weaken one check. They test whether
that check is load-bearing. The desired outcome is **CONFIRMED**: the
control fails with the assertion and the mutant passes on that same
input. CONFIRMED is not a failure.

Any other guard outcome fails the command:

- **NOT_ISOLATED** — the control does not fail with the assertion
- **REDUNDANT_OR_INVALID_MUTANT** — the mutant still fails
- **INVALID** or **ERROR** — the run could not be scored

## Exit status

The command exits 0 only when every config and artifact mutant is
KILLED, every guard mutant is CONFIRMED, and both scoring proofs score
as intended. Anything else exits non-zero.

## Scoring proofs

The script keeps two runs that are not part of the mutant tally:

- a comment-only edit of the YAML guard, on a valid config, must score
  SURVIVED. A passing `go test` without `-v` prints `ok`, not `PASS`.
- a config goreleaser cannot archive must score ERROR, not KILLED.

If either proof scores as something else, the command fails.
