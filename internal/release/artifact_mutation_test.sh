#!/bin/bash
# Mutation testing for artifact validation guard
# Tests that the artifact guard catches missing files in real archives

set -e

cd "$(dirname "$0")/../.."
export PATH="$PATH:$(go env GOPATH)/bin"

# Save original config
cp .goreleaser.yaml .goreleaser.yaml.original

cleanup() {
    mv .goreleaser.yaml.original .goreleaser.yaml
}
trap cleanup EXIT

echo "=== ARTIFACT GUARD MUTATION TESTING ==="
echo

PASS=0
FAIL=0

test_artifact_mutant() {
    local name="$1"
    local config="$2"
    
    echo "Mutant: $name"
    echo "$config" > .goreleaser.yaml
    
    # Run artifact test with TEST_SNAPSHOT=1
    if TEST_SNAPSHOT=1 go test -v ./internal/release -run TestSnapshotArtifacts 2>&1 | grep -q "FAIL.*TestSnapshotArtifacts"; then
        echo "  Result: ✓ KILLED (artifact test caught it)"
        PASS=$((PASS + 1))
    else
        echo "  Result: ✗ SURVIVED (artifact test should have killed it)"
        FAIL=$((FAIL + 1))
    fi
    
    echo
}

# Full config template for artifact testing
read -r -d '' BASE_CONFIG << 'EOF' || true
version: 2
project_name: pcal
before:
  hooks:
    - go mod tidy
builds:
  - main: ./cmd/pcal
    binary: pcal
    env: [CGO_ENABLED=0]
    goos: [darwin, linux, windows]
    goarch: [amd64]
    flags: [-trimpath]
    ldflags:
      - -s -w
      - -X github.com/rorJeremy/promise-calendar-cli/internal/version.Version={{.Version}}
archives:
  - formats: [tar.gz]
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    format_overrides: [{ goos: windows, formats: [zip] }]
    files: [LICENSE, README*, completions/*]
checksum: { name_template: checksums.txt }
EOF

# Artifact Mutant 1: Drop LICENSE from archive
test_artifact_mutant "A1: Archive without LICENSE" \
"$(echo "$BASE_CONFIG" | sed 's/files: \[LICENSE, README\*, completions\/\*\]/files: [README*, completions\/\*]/')"

# Artifact Mutant 2: Drop README from archive
test_artifact_mutant "A2: Archive without README*" \
"$(echo "$BASE_CONFIG" | sed 's/files: \[LICENSE, README\*, completions\/\*\]/files: [LICENSE, completions\/\*]/')"

# Artifact Mutant 3: Drop completions from archive
test_artifact_mutant "A3: Archive without completions/*" \
"$(echo "$BASE_CONFIG" | sed 's/files: \[LICENSE, README\*, completions\/\*\]/files: [LICENSE, README*]/')"

# Artifact Mutant 4: Drop all extra files
test_artifact_mutant "A4: Archive with no extra files" \
"$(echo "$BASE_CONFIG" | sed 's/files: \[LICENSE, README\*, completions\/\*\]/files: []/')"

echo "=== ARTIFACT MUTATION TESTING RESULTS ==="
echo "Passed: $PASS"
echo "Failed: $FAIL"
echo

if [ $FAIL -gt 0 ]; then
    echo "⨯ Some artifact mutants survived!"
    exit 1
else
    echo "✓ All artifact mutants killed!"
    exit 0
fi
