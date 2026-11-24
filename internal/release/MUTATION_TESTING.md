# Mutation Testing for Release Guards

Comprehensive mutation testing: 25 mutants total

## Why Scripted (Not go-mutesting/gremlins)

1. **Config mutations** - Target `.goreleaser.yaml` (YAML), not Go code
2. **Archive validation** - Test real built archives (tar.gz/zip contents)
3. **Guard-code mutations** - Mutate test code itself (both YAML & artifact guards)
4. **Isolation** - Runs in git worktree (never modifies working tree, even on Ctrl-C)
5. **Validation** - Checks compilation first (reports compile errors as INVALID, not killed)

## Running

```bash
make test-mutations  # ONE script, all mutations (~10s, isolated worktree)
```

## Coverage (25 mutants)

**Config (10):** Drop LICENSE, LICENSE.bak, 2nd archive, drop README/completions, empty list, similar names, pattern variations

**Artifact (3):** Archives missing LICENSE, README, completions

**Guard-code (12):**
- YAML guard (5): Drop from required, substring match, only check [0], invert logic, skip checks
- Artifact guard (7): Drop LICENSE/README/completions, substring match, skip zip, only first archive, zero-length check

Mutants marked INVALID when: compile error, sed pattern fails, or file unchanged.
