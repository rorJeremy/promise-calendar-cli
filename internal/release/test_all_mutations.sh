#!/bin/bash
# Comprehensive mutation testing: config + artifact + guard-code
# Runs in isolated git worktree - never modifies working tree

set -e
REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
export PATH="$PATH:$(go env GOPATH)/bin"

# Create isolated worktree for mutations
WORKTREE_DIR=$(mktemp -d -t pcal-mutations.XXXXXX)
cleanup() {
    cd "$REPO_ROOT"
    git worktree remove --force "$WORKTREE_DIR" 2>/dev/null || true
    rm -rf "$WORKTREE_DIR"
    # Prune stale worktree registrations (from hard-killed runs)
    git worktree prune 2>/dev/null || true
}
trap cleanup EXIT

git worktree add --detach "$WORKTREE_DIR" HEAD >/dev/null 2>&1
cd "$WORKTREE_DIR"

echo "════════════════════════════════════════════════════════════════════════════════"
echo "COMPREHENSIVE MUTATION TESTING (isolated worktree)"
echo "Tests: Config mutations | Artifact mutations | Guard-code mutations"
echo "════════════════════════════════════════════════════════════════════════════════"
echo

CONFIG_KILLED=0
CONFIG_INVALID=0
ARTIFACT_KILLED=0
ARTIFACT_INVALID=0
GUARD_KILLED=0
GUARD_INVALID=0
TOTAL_SURVIVED=0

##############################################################################
# CONFIG MUTATIONS (mutate .goreleaser.yaml, test with YAML guard)
##############################################################################

test_config_mutant() {
    local name="$1"
    local config="$2"
    
    echo "Config: $name"
    
    # Save original
    cp .goreleaser.yaml .goreleaser.yaml.backup
    
    # Apply mutation
    echo "$config" > .goreleaser.yaml
    
    # Check if it compiles
    if ! go vet ./internal/release >/dev/null 2>&1; then
        echo "  ⊘ INVALID (compile error)"
        CONFIG_INVALID=$((CONFIG_INVALID + 1))
        cp .goreleaser.yaml.backup .goreleaser.yaml
        echo
        return
    fi
    
    # Run test and capture specific error
    output=$(go test -run TestGoReleaserConfig ./internal/release 2>&1 || true)
    
    if echo "$output" | grep -q "FAIL.*TestGoReleaserConfig"; then
        # Verify it's a real assertion failure, not a panic/compile
        if echo "$output" | grep -qE "missing from files|does not match required"; then
            echo "  ✓ KILLED by YAML guard"
            CONFIG_KILLED=$((CONFIG_KILLED + 1))
        else
            echo "  ⊘ INVALID (unexpected failure)"
            CONFIG_INVALID=$((CONFIG_INVALID + 1))
        fi
    else
        echo "  ✗ SURVIVED"
        TOTAL_SURVIVED=$((TOTAL_SURVIVED + 1))
    fi
    
    # Restore
    cp .goreleaser.yaml.backup .goreleaser.yaml
    echo
}

echo "────────────────────────────────────────────────────────────────────────────────"
echo "PART 1: CONFIG MUTATIONS (10 mutants)"
echo "────────────────────────────────────────────────────────────────────────────────"
echo

VALID_CONFIG='version: 2
archives:
  - formats: [tar.gz]
    files: [LICENSE, README*, completions/*]
checksum: { name_template: checksums.txt }'

test_config_mutant "C1: Drop LICENSE" \
"$(echo "$VALID_CONFIG" | sed 's/LICENSE, //')"

test_config_mutant "C2: LICENSE.bak (substring attack)" \
"$(echo "$VALID_CONFIG" | sed 's/LICENSE/LICENSE.bak/')"

test_config_mutant "C3: Second archive without LICENSE" \
"version: 2
archives:
  - formats: [tar.gz]
    files: [LICENSE, README*, completions/*]
  - formats: [zip]
    files: [README*, completions/*]
checksum: { name_template: checksums.txt }"

test_config_mutant "C4: LICENSE only in archive[1]" \
"version: 2
archives:
  - formats: [tar.gz]
    files: [README*, completions/*]
  - formats: [zip]
    files: [LICENSE, README*, completions/*]
checksum: { name_template: checksums.txt }"

test_config_mutant "C5: Drop README*" \
"$(echo "$VALID_CONFIG" | sed 's/, README\*//')"

test_config_mutant "C6: Drop completions/*" \
"$(echo "$VALID_CONFIG" | sed 's/, completions\/\*//')"

test_config_mutant "C7: Empty files list" \
"$(echo "$VALID_CONFIG" | sed 's/files: \[LICENSE, README\*, completions\/\*\]/files: []/')"

test_config_mutant "C8: LICENSE_FILE (similar name)" \
"$(echo "$VALID_CONFIG" | sed 's/LICENSE/LICENSE_FILE/')"

test_config_mutant "C9: README (not README*)" \
"$(echo "$VALID_CONFIG" | sed 's/README\*/README/')"

test_config_mutant "C10: completions (not completions/*)" \
"$(echo "$VALID_CONFIG" | sed 's/completions\/\*/completions/')"

##############################################################################
# ARTIFACT MUTATIONS (mutate config, test with artifact guard - slower)
##############################################################################

test_artifact_mutant() {
    local name="$1"
    local config="$2"
    
    echo "Artifact: $name"
    
    cp .goreleaser.yaml .goreleaser.yaml.backup
    echo "$config" > .goreleaser.yaml
    
    # Check compilation
    if ! go vet ./internal/release >/dev/null 2>&1; then
        echo "  ⊘ INVALID (compile error)"
        ARTIFACT_INVALID=$((ARTIFACT_INVALID + 1))
        cp .goreleaser.yaml.backup .goreleaser.yaml
        echo
        return
    fi
    
    # Run artifact test
    output=$(TEST_SNAPSHOT=1 go test -run TestSnapshotArtifacts ./internal/release 2>&1 || true)
    
    if echo "$output" | grep -q "FAIL.*TestSnapshotArtifacts"; then
        # Verify specific assertion failure
        if echo "$output" | grep -qE "missing required file|no completion files"; then
            echo "  ✓ KILLED by artifact guard"
            ARTIFACT_KILLED=$((ARTIFACT_KILLED + 1))
        else
            echo "  ⊘ INVALID (unexpected failure)"
            ARTIFACT_INVALID=$((ARTIFACT_INVALID + 1))
        fi
    else
        echo "  ✗ SURVIVED"
        TOTAL_SURVIVED=$((TOTAL_SURVIVED + 1))
    fi
    
    cp .goreleaser.yaml.backup .goreleaser.yaml
    echo
}

echo "────────────────────────────────────────────────────────────────────────────────"
echo "PART 2: ARTIFACT MUTATIONS (3 mutants - slower, builds archives)"
echo "────────────────────────────────────────────────────────────────────────────────"
echo

BUILD_CONFIG='version: 2
project_name: pcal
before:
  hooks:
    - go mod tidy
builds:
  - main: ./cmd/pcal
    binary: pcal
    env: [CGO_ENABLED=0]
    goos: [linux]
    goarch: [amd64]
    flags: [-trimpath]
    ldflags: [-s -w]
archives:
  - formats: [tar.gz]
    files: [LICENSE, README*, completions/*]
checksum: { name_template: checksums.txt }'

test_artifact_mutant "A1: Archive missing LICENSE" \
"$(echo "$BUILD_CONFIG" | sed 's/LICENSE, //')"

test_artifact_mutant "A2: Archive missing README*" \
"$(echo "$BUILD_CONFIG" | sed 's/, README\*//')"

test_artifact_mutant "A3: Archive missing completions/*" \
"$(echo "$BUILD_CONFIG" | sed 's/, completions\/\*//')"

##############################################################################
# GUARD-CODE MUTATIONS (mutate test code, test with bad config)
# Uses git apply with unified diff patches for reliability
##############################################################################

test_guard_mutant_simple() {
    # For simple mutations that work with sed
    local name="$1"
    local test_name="$2"
    local file="$3"
    local sed_pattern="$4"
    local bad_config="$5"
    
    echo "Guard: $name"
    
    # Backup
    cp internal/release/$file internal/release/$file.backup
    
    # Apply mutation
    if ! sed -i "$sed_pattern" internal/release/$file 2>/dev/null; then
        echo "  ⊘ INVALID (sed pattern failed)"
        GUARD_INVALID=$((GUARD_INVALID + 1))
        cp internal/release/$file.backup internal/release/$file
        echo
        return
    fi
    
    # Check if file actually changed
    if diff -q internal/release/$file internal/release/$file.backup >/dev/null 2>&1; then
        echo "  ⊘ INVALID (file unchanged - sed pattern didn't match)"
        GUARD_INVALID=$((GUARD_INVALID + 1))
        cp internal/release/$file.backup internal/release/$file
        echo
        return
    fi
    
    # Check compilation
    if ! go test -c ./internal/release -o /dev/null 2>/dev/null; then
        echo "  ⊘ INVALID (mutated code doesn't compile)"
        GUARD_INVALID=$((GUARD_INVALID + 1))
        cp internal/release/$file.backup internal/release/$file
        echo
        return
    fi
    
    # Set up bad config
    cp .goreleaser.yaml .goreleaser.yaml.backup
    echo "$bad_config" > .goreleaser.yaml
    
    # Run test
    if [[ "$test_name" == "TestSnapshotArtifacts" ]]; then
        output=$(TEST_SNAPSHOT=1 go test -run "$test_name" ./internal/release 2>&1 || true)
    else
        output=$(go test -run "$test_name" ./internal/release 2>&1 || true)
    fi
    
    # Check result
    if echo "$output" | grep -q "PASS"; then
        echo "  ✗ SURVIVED (broken guard not detected)"
        TOTAL_SURVIVED=$((TOTAL_SURVIVED + 1))
    else
        echo "  ✓ KILLED (broken guard detected)"
        GUARD_KILLED=$((GUARD_KILLED + 1))
    fi
    
    # Restore
    cp internal/release/$file.backup internal/release/$file
    cp .goreleaser.yaml.backup .goreleaser.yaml
    echo
}

test_guard_mutant_patch() {
    # For complex mutations using unified diff patches
    local name="$1"
    local test_name="$2"
    local patch_file="$3"
    local bad_config="$4"
    
    echo "Guard: $name"
    
    # Apply patch
    if ! git apply "$REPO_ROOT/internal/release/mutants/$patch_file" 2>/dev/null; then
        echo "  ⊘ INVALID (patch failed to apply)"
        GUARD_INVALID=$((GUARD_INVALID + 1))
        git reset --hard HEAD >/dev/null 2>&1
        echo
        return
    fi
    
    # Check compilation
    if ! go test -c ./internal/release -o /dev/null 2>/dev/null; then
        echo "  ⊘ INVALID (mutated code doesn't compile)"
        GUARD_INVALID=$((GUARD_INVALID + 1))
        git reset --hard HEAD >/dev/null 2>&1
        echo
        return
    fi
    
    # Set up bad config
    cp .goreleaser.yaml .goreleaser.yaml.backup
    echo "$bad_config" > .goreleaser.yaml
    
    # Run test
    if [[ "$test_name" == "TestSnapshotArtifacts" ]]; then
        output=$(TEST_SNAPSHOT=1 go test -run "$test_name" ./internal/release 2>&1 || true)
    else
        output=$(go test -run "$test_name" ./internal/release 2>&1 || true)
    fi
    
    # Check result
    if echo "$output" | grep -q "PASS"; then
        echo "  ✗ SURVIVED (broken guard not detected)"
        TOTAL_SURVIVED=$((TOTAL_SURVIVED + 1))
    else
        echo "  ✓ KILLED (broken guard detected)"
        GUARD_KILLED=$((GUARD_KILLED + 1))
    fi
    
    # Restore
    git reset --hard HEAD >/dev/null 2>&1
    cp .goreleaser.yaml.backup .goreleaser.yaml
    echo
}

echo "────────────────────────────────────────────────────────────────────────────────"
echo "PART 3: GUARD-CODE MUTATIONS (12 mutants)"
echo "────────────────────────────────────────────────────────────────────────────────"
echo

BAD_NO_LICENSE='version: 2
archives:
  - formats: [tar.gz]
    files: [README*, completions/*]
checksum: { name_template: checksums.txt }'

BAD_NO_COMPLETIONS='version: 2
archives:
  - formats: [tar.gz]
    files: [LICENSE, README*]
checksum: { name_template: checksums.txt }'

BAD_SECOND_ARCHIVE='version: 2
archives:
  - formats: [tar.gz]
    files: [LICENSE, README*, completions/*]
  - formats: [zip]
    files: [README*, completions/*]
checksum: { name_template: checksums.txt }'

BAD_LICENSE_BAK='version: 2
archives:
  - formats: [tar.gz]
    files: [LICENSE.bak, README*, completions/*]
checksum: { name_template: checksums.txt }'

BAD_NO_README='version: 2
archives:
  - formats: [tar.gz]
    files: [LICENSE, completions/*]
checksum: { name_template: checksums.txt }'

echo "YAML Guard Mutations (5 mutants):"
echo

test_guard_mutant_simple \
    "G1: Drop LICENSE from required" \
    "TestGoReleaserConfig" \
    "goreleaser_test.go" \
    's/"LICENSE", //' \
    "$BAD_NO_LICENSE"

test_guard_mutant_simple \
    "G2: Drop completions from required" \
    "TestGoReleaserConfig" \
    "goreleaser_test.go" \
    's/, "completions\/\*"//' \
    "$BAD_NO_COMPLETIONS"

test_guard_mutant_simple \
    "G3: Only check archives[0]" \
    "TestGoReleaserConfig" \
    "goreleaser_test.go" \
    's/for i, archive := range config.Archives {/if len(config.Archives) > 0 { i := 0; archive := config.Archives[0]; _ = i; _ = archive/' \
    "$BAD_SECOND_ARCHIVE"

test_guard_mutant_patch \
    "G4: Use substring match for LICENSE" \
    "TestGoReleaserConfig" \
    "g4-substring-match.patch" \
    "$BAD_LICENSE_BAK"

test_guard_mutant_simple \
    "G5: Invert found logic" \
    "TestGoReleaserConfig" \
    "goreleaser_test.go" \
    's/if !found {/if found {/' \
    "$BAD_NO_LICENSE"

echo "Artifact Guard Mutations (7 mutants):"
echo

# Build config for artifact mutations
BUILD_BAD_NO_LICENSE='version: 2
project_name: pcal
before:
  hooks:
    - go mod tidy
builds:
  - main: ./cmd/pcal
    binary: pcal
    env: [CGO_ENABLED=0]
    goos: [linux]
    goarch: [amd64]
archives:
  - formats: [tar.gz, zip]
    files: [README*, completions/*]
checksum: { name_template: checksums.txt }'

BUILD_BAD_NO_README='version: 2
project_name: pcal
before:
  hooks:
    - go mod tidy
builds:
  - main: ./cmd/pcal
    binary: pcal
    env: [CGO_ENABLED=0]
    goos: [linux]
    goarch: [amd64]
archives:
  - formats: [tar.gz, zip]
    files: [LICENSE, completions/*]
checksum: { name_template: checksums.txt }'

BUILD_BAD_NO_COMPLETIONS='version: 2
project_name: pcal
before:
  hooks:
    - go mod tidy
builds:
  - main: ./cmd/pcal
    binary: pcal
    env: [CGO_ENABLED=0]
    goos: [linux]
    goarch: [amd64]
archives:
  - formats: [tar.gz, zip]
    files: [LICENSE, README*]
checksum: { name_template: checksums.txt }'

BUILD_BAD_LICENSE_BAK='version: 2
project_name: pcal
before:
  hooks:
    - go mod tidy
builds:
  - main: ./cmd/pcal
    binary: pcal
    env: [CGO_ENABLED=0]
    goos: [linux]
    goarch: [amd64]
archives:
  - formats: [tar.gz, zip]
    files: [LICENSE.bak, README*, completions/*]
checksum: { name_template: checksums.txt }'

test_guard_mutant_patch \
    "G6: Drop LICENSE check from artifact guard" \
    "TestSnapshotArtifacts" \
    "g6-drop-license-check.patch" \
    "$BUILD_BAD_NO_LICENSE"

test_guard_mutant_patch \
    "G7: Drop README check from artifact guard" \
    "TestSnapshotArtifacts" \
    "g7-drop-readme-check.patch" \
    "$BUILD_BAD_NO_README"

test_guard_mutant_patch \
    "G8: Drop completions check from artifact guard" \
    "TestSnapshotArtifacts" \
    "g8-drop-completions-check.patch" \
    "$BUILD_BAD_NO_COMPLETIONS"

test_guard_mutant_patch \
    "G9: Accept any base name for LICENSE" \
    "TestSnapshotArtifacts" \
    "g9-license-substring.patch" \
    "$BUILD_BAD_LICENSE_BAK"

test_guard_mutant_patch \
    "G10: Skip zip archives" \
    "TestSnapshotArtifacts" \
    "g10-skip-zip.patch" \
    "$BUILD_BAD_NO_LICENSE"

test_guard_mutant_simple \
    "G11: Treat zero archives as pass" \
    "TestSnapshotArtifacts" \
    "artifacts_test.go" \
    's/if len(archives) == 0 {/if len(archives) < 0 {/' \
    "$BUILD_BAD_NO_LICENSE"

test_guard_mutant_simple \
    "G12: Only check first archive" \
    "TestSnapshotArtifacts" \
    "artifacts_test.go" \
    's/for _, archivePath := range archives {/for i, archivePath := range archives { if i > 0 { continue }/' \
    "version: 2
project_name: pcal
before:
  hooks:
    - go mod tidy
builds:
  - main: ./cmd/pcal
    binary: pcal
    env: [CGO_ENABLED=0]
    goos: [linux]
    goarch: [amd64]
archives:
  - formats: [tar.gz]
    files: [LICENSE, README*, completions/*]
  - formats: [zip]
    files: [README*, completions/*]
checksum: { name_template: checksums.txt }"

##############################################################################
# RESULTS
##############################################################################

echo "════════════════════════════════════════════════════════════════════════════════"
echo "MUTATION TESTING RESULTS"
echo "════════════════════════════════════════════════════════════════════════════════"
echo
echo "Config mutations:     $CONFIG_KILLED killed, $CONFIG_INVALID invalid"
echo "Artifact mutations:   $ARTIFACT_KILLED killed, $ARTIFACT_INVALID invalid"
echo "Guard-code mutations: $GUARD_KILLED killed, $GUARD_INVALID invalid"
echo "────────────────────────────────────────────────────────────────────────────────"
TOTAL_KILLED=$((CONFIG_KILLED + ARTIFACT_KILLED + GUARD_KILLED))
TOTAL_INVALID=$((CONFIG_INVALID + ARTIFACT_INVALID + GUARD_INVALID))
TOTAL_MUTANTS=$((10 + 3 + 12))
echo "TOTAL:                $TOTAL_KILLED / $TOTAL_MUTANTS killed ($TOTAL_INVALID invalid)"
echo "Survived:             $TOTAL_SURVIVED mutants"
echo

if [ $TOTAL_SURVIVED -gt 0 ]; then
    echo "⨯ Some mutants survived!"
    exit 1
else
    if [ $TOTAL_INVALID -gt 0 ]; then
        echo "⚠ Some mutants were invalid"
        exit 1
    fi
    echo "✓ All mutants killed!"
    exit 0
fi
