# Mutation Testing for Release Guards

## Why Scripted Approach (Not go-mutesting/gremlins)

We use custom bash scripts for mutation testing instead of tools like `go-mutesting` or `gremlins` because:

1. **Config mutations** - Most mutations target `.goreleaser.yaml` (YAML config), not Go code. Standard Go mutation tools only mutate Go source.

2. **Archive validation** - We need to test that real built archives contain files. This requires running goreleaser and inspecting tar.gz/zip contents, which is outside the scope of Go code mutation.

3. **Fast targeted mutations** - Our scripted approach tests specific failure modes (LICENSE.bak, missing README, etc.) in ~6s. Full Go mutation testing would be much slower and test irrelevant code paths.

4. **Clear failure modes** - Each test explicitly names what should be caught: "Drop LICENSE", "Second archive without LICENSE". This is clearer than automatic mutations like "changed && to ||".

5. **Two-layer testing** - We test both the YAML config guard (fast, no builds) and artifact guard (slower, builds archives). Go mutation tools can't distinguish these layers.

## When to Use Go Mutation Tools

If we wanted to mutate the guard _code_ itself (e.g. invert the `if file == "LICENSE"` check), tools like gremlins would work. However:

- The guards are simple (exact string matching, file existence checks)
- More value in testing they catch real config errors
- Scripted mutations of test logic are fragile (changing test code to pass with wrong config)

## Running Mutation Tests

```bash
# YAML guard only (fast: ~1s, 5 mutants)
bash internal/release/mutation_test.sh

# Artifact guard only (slower: ~6s, 3 mutants)
bash internal/release/artifact_mutation_test.sh

# Both (comprehensive: ~6s, 8 mutants)
make test-mutations

# Or directly:
bash internal/release/full_mutation_test.sh
```

## Mutation Coverage

### YAML Config Guard (5 mutants)
- Y1: Drop LICENSE
- Y2: LICENSE.bak (substring attack)
- Y3: Second archive without LICENSE
- Y4: Drop README*
- Y5: Drop completions/*

### Artifact Guard (3 mutants)
- A1: Archive missing LICENSE
- A2: Archive missing README*
- A3: Archive missing completions/*

**Result:** 8/8 killed (100%)
