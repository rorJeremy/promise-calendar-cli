# Mutation Testing for Release Guards

Comprehensive mutation testing: 20 mutants total (100% kill rate)

## Why Scripted (Not go-mutesting/gremlins)

1. **Config mutations** - Target `.goreleaser.yaml` (YAML), not Go code
2. **Archive validation** - Test real built archives (tar.gz/zip contents)
3. **Guard-code mutations** - Also mutate the test code itself
4. **Fast & targeted** - ~8s for 20 mutants vs slow full-codebase mutation

## Running

```bash
make test-mutations  # ONE script, all mutations (~8s)
```

## Coverage (20 mutants)

**Config (10):** Drop LICENSE, LICENSE.bak, 2nd archive, drop README/completions, empty list, similar names, pattern variations

**Artifact (3):** Archives missing LICENSE, README, completions

**Guard-code (7):** Drop from required, substring match, only check [0], invert logic, empty required, skip checks

**Result:** 20/20 killed (100%)
