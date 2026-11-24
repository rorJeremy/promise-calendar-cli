#!/bin/bash
# Comprehensive mutation testing: config + artifact + guard-code
# ONE script to rule them all

set -e
cd "$(dirname "$0")/../.."
export PATH="$PATH:$(go env GOPATH)/bin"

# Backups
cp .goreleaser.yaml .goreleaser.yaml.original
cp internal/release/goreleaser_test.go internal/release/goreleaser_test.go.original
cp internal/release/artifacts_test.go internal/release/artifacts_test.go.original

cleanup() {
    mv .goreleaser.yaml.original .goreleaser.yaml 2>/dev/null || true
    mv internal/release/goreleaser_test.go.original internal/release/goreleaser_test.go 2>/dev/null || true
    mv internal/release/artifacts_test.go.original internal/release/artifacts_test.go 2>/dev/null || true
}
trap cleanup EXIT

echo "════════════════════════════════════════════════════════════════════════════════"
echo "COMPREHENSIVE MUTATION TESTING"
echo "Tests: Config mutations | Artifact mutations | Guard-code mutations"
echo "════════════════════════════════════════════════════════════════════════════════"
echo

CONFIG_KILLED=0
ARTIFACT_KILLED=0
GUARD_KILLED=0
TOTAL_SURVIVED=0

##############################################################################
# CONFIG MUTATIONS (mutate .goreleaser.yaml, test with YAML guard)
##############################################################################

test_config_mutant() {
    local name="$1"
    local config="$2"
    
    echo "Config: $name"
    echo "$config" > .goreleaser.yaml
    
    if go test -run TestGoReleaserConfig ./internal/release 2>&1 | grep -q "FAIL"; then
        echo "  ✓ KILLED by YAML guard"
        CONFIG_KILLED=$((CONFIG_KILLED + 1))
    else
        echo "  ✗ SURVIVED"
        TOTAL_SURVIVED=$((TOTAL_SURVIVED + 1))
    fi
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
    echo "$config" > .goreleaser.yaml
    
    # Must build archives and check them
    if TEST_SNAPSHOT=1 go test -run TestSnapshotArtifacts ./internal/release 2>&1 | grep -q "missing\|FAIL"; then
        echo "  ✓ KILLED by artifact guard"
        ARTIFACT_KILLED=$((ARTIFACT_KILLED + 1))
    else
        echo "  ✗ SURVIVED"
        TOTAL_SURVIVED=$((TOTAL_SURVIVED + 1))
    fi
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
##############################################################################

test_guard_mutant() {
    local name="$1"
    local file="$2"
    local sed_pattern="$3"
    local bad_config="$4"
    
    echo "Guard: $name"
    
    # Apply mutation to guard code
    cp internal/release/$file.original internal/release/$file
    sed -i "$sed_pattern" internal/release/$file
    
    # Set up bad config
    echo "$bad_config" > .goreleaser.yaml
    
    # Run test - if PASSES, mutant SURVIVED (bad!)
    local test_name="TestGoReleaserConfig"
    if [[ "$file" == "artifacts_test.go" ]]; then
        test_name="TestSnapshotArtifacts"
    fi
    
    if go test -run "$test_name" ./internal/release 2>&1 | grep -q "PASS"; then
        echo "  ✗ SURVIVED (broken guard not detected)"
        TOTAL_SURVIVED=$((TOTAL_SURVIVED + 1))
    else
        echo "  ✓ KILLED (broken guard detected)"
        GUARD_KILLED=$((GUARD_KILLED + 1))
    fi
    
    # Restore
    cp internal/release/$file.original internal/release/$file
    echo
}

echo "────────────────────────────────────────────────────────────────────────────────"
echo "PART 3: GUARD-CODE MUTATIONS (7 mutants)"
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

test_guard_mutant \
    "G1: Drop LICENSE from required" \
    "goreleaser_test.go" \
    '/LICENSE/d' \
    "$BAD_NO_LICENSE"

test_guard_mutant \
    "G2: Drop completions from required" \
    "goreleaser_test.go" \
    '/completions/d' \
    "$BAD_NO_COMPLETIONS"

test_guard_mutant \
    "G3: Only check archives[0]" \
    "goreleaser_test.go" \
    's/for i, archive := range config.Archives {/if len(config.Archives) > 0 { i := 0; archive := config.Archives[0]; _ = i; _ = archive/' \
    "$BAD_SECOND_ARCHIVE"

test_guard_mutant \
    "G4: Use substring match for LICENSE" \
    "goreleaser_test.go" \
    's/if file == requiredFile {/if strings.Contains(file, requiredFile) {/' \
    "$BAD_LICENSE_BAK"

test_guard_mutant \
    "G5: Invert found logic" \
    "goreleaser_test.go" \
    's/if !found {/if found {/' \
    "$BAD_NO_LICENSE"

test_guard_mutant \
    "G6: Accept empty required list" \
    "goreleaser_test.go" \
    's/required := \[\]string{"LICENSE", "README\*", "completions\/\*"}/required := []string{}/' \
    "$BAD_NO_LICENSE"

test_guard_mutant \
    "G7: Skip check if no archives" \
    "goreleaser_test.go" \
    's/if len(config.Archives) == 0 {/if len(config.Archives) <= 999 {/' \
    "$BAD_NO_LICENSE"

##############################################################################
# RESULTS
##############################################################################

echo "════════════════════════════════════════════════════════════════════════════════"
echo "MUTATION TESTING RESULTS"
echo "════════════════════════════════════════════════════════════════════════════════"
echo
echo "Config mutations:     $CONFIG_KILLED / 10 killed"
echo "Artifact mutations:   $ARTIFACT_KILLED / 3 killed"
echo "Guard-code mutations: $GUARD_KILLED / 7 killed"
echo "────────────────────────────────────────────────────────────────────────────────"
TOTAL_KILLED=$((CONFIG_KILLED + ARTIFACT_KILLED + GUARD_KILLED))
TOTAL_MUTANTS=$((10 + 3 + 7))
echo "TOTAL:                $TOTAL_KILLED / $TOTAL_MUTANTS killed"
echo "Survived:             $TOTAL_SURVIVED mutants"
echo

if [ $TOTAL_SURVIVED -gt 0 ]; then
    echo "⨯ Some mutants survived!"
    exit 1
else
    echo "✓ All mutants killed! (100% kill rate)"
    exit 0
fi
