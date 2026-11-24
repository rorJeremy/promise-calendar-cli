#!/bin/bash
# Comprehensive mutation testing for release guards
# Tests both YAML config guard and artifact guard

set -e

cd "$(dirname "$0")/../.."
export PATH="$PATH:$(go env GOPATH)/bin"

# Save originals
cp .goreleaser.yaml .goreleaser.yaml.original

cleanup() {
    mv .goreleaser.yaml.original .goreleaser.yaml 2>/dev/null || true
}
trap cleanup EXIT

echo "════════════════════════════════════════════════════════════"
echo "COMPREHENSIVE RELEASE GUARD MUTATION TESTING"
echo "════════════════════════════════════════════════════════════"
echo

YAML_PASS=0
YAML_FAIL=0
ARTIFACT_PASS=0
ARTIFACT_FAIL=0

test_yaml_mutant() {
    local name="$1"
    local config="$2"
    
    echo "YAML Mutant: $name"
    echo "$config" > .goreleaser.yaml
    
    if go test -v ./internal/release -run TestGoReleaserConfig 2>&1 | grep -q "FAIL.*TestGoReleaserConfig"; then
        echo "  ✓ KILLED by YAML guard"
        YAML_PASS=$((YAML_PASS + 1))
    else
        echo "  ✗ SURVIVED (YAML guard failed to catch it)"
        YAML_FAIL=$((YAML_FAIL + 1))
    fi
    echo
}

test_artifact_mutant() {
    local name="$1"
    local config="$2"
    
    echo "Artifact Mutant: $name"
    echo "$config" > .goreleaser.yaml
    
    # Must catch the error, so check for FAIL
    if TEST_SNAPSHOT=1 go test -v ./internal/release -run TestSnapshotArtifacts 2>&1 | grep -q "LICENSE missing\|README.md missing\|no completion"; then
        echo "  ✓ KILLED by artifact guard"
        ARTIFACT_PASS=$((ARTIFACT_PASS + 1))
    else
        echo "  ✗ SURVIVED (artifact guard failed to catch it)"
        ARTIFACT_FAIL=$((ARTIFACT_FAIL + 1))
    fi
    echo
}

# Minimal valid config template
read -r -d '' VALID_CONFIG << 'EOFCONFIG' || true
version: 2
project_name: pcal
archives:
  - formats: [tar.gz]
    files: [LICENSE, README*, completions/*]
checksum: { name_template: checksums.txt }
EOFCONFIG

# Full config for artifact testing (needs builds)
read -r -d '' ARTIFACT_CONFIG << 'EOFCONFIG' || true
version: 2
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
checksum: { name_template: checksums.txt }
EOFCONFIG

echo "──────────────────────────────────────────────────────────"
echo "PART 1: YAML CONFIG GUARD MUTATIONS"
echo "──────────────────────────────────────────────────────────"
echo

# YAML mutations (fast - no builds)
test_yaml_mutant "Y1: Drop LICENSE" \
"$(echo "$VALID_CONFIG" | sed 's/files: \[LICENSE, README\*, completions\/\*\]/files: [README*, completions\/\*]/')"

test_yaml_mutant "Y2: LICENSE.bak (substring attack)" \
"$(echo "$VALID_CONFIG" | sed 's/files: \[LICENSE, README\*, completions\/\*\]/files: [LICENSE.bak, README*, completions\/\*]/')"

test_yaml_mutant "Y3: Second archive without LICENSE" \
"$(cat << 'EOFMUTANT'
version: 2
project_name: pcal
archives:
  - formats: [tar.gz]
    files: [LICENSE, README*, completions/*]
  - formats: [zip]
    files: [README*, completions/*]
checksum: { name_template: checksums.txt }
EOFMUTANT
)"

test_yaml_mutant "Y4: Drop README*" \
"$(echo "$VALID_CONFIG" | sed 's/files: \[LICENSE, README\*, completions\/\*\]/files: [LICENSE, completions\/\*]/')"

test_yaml_mutant "Y5: Drop completions/*" \
"$(echo "$VALID_CONFIG" | sed 's/files: \[LICENSE, README\*, completions\/\*\]/files: [LICENSE, README*]/')"

echo "──────────────────────────────────────────────────────────"
echo "PART 2: ARTIFACT GUARD MUTATIONS (slower - builds archives)"
echo "──────────────────────────────────────────────────────────"
echo

test_artifact_mutant "A1: Archive missing LICENSE" \
"$(echo "$ARTIFACT_CONFIG" | sed 's/files: \[LICENSE, README\*, completions\/\*\]/files: [README*, completions\/\*]/')"

test_artifact_mutant "A2: Archive missing README*" \
"$(echo "$ARTIFACT_CONFIG" | sed 's/files: \[LICENSE, README\*, completions\/\*\]/files: [LICENSE, completions\/\*]/')"

test_artifact_mutant "A3: Archive missing completions/*" \
"$(echo "$ARTIFACT_CONFIG" | sed 's/files: \[LICENSE, README\*, completions\/\*\]/files: [LICENSE, README*]/')"

echo "════════════════════════════════════════════════════════════"
echo "MUTATION TESTING RESULTS"
echo "════════════════════════════════════════════════════════════"
echo
echo "YAML Guard:     $YAML_PASS killed / $YAML_FAIL survived"
echo "Artifact Guard: $ARTIFACT_PASS killed / $ARTIFACT_FAIL survived"
echo "────────────────────────────────────────────────────────────"
TOTAL_PASS=$((YAML_PASS + ARTIFACT_PASS))
TOTAL_FAIL=$((YAML_FAIL + ARTIFACT_FAIL))
echo "Total:          $TOTAL_PASS killed / $TOTAL_FAIL survived"
echo

if [ $TOTAL_FAIL -gt 0 ]; then
    echo "⨯ Some mutants survived!"
    exit 1
else
    echo "✓ All mutants killed!"
    exit 0
fi
