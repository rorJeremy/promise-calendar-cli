#!/bin/bash
# Mutation testing for release guards
# Tests that guards catch various config mutations

set -e

cd "$(dirname "$0")/../.."
export PATH="$PATH:$(go env GOPATH)/bin"

# Save original config
cp .goreleaser.yaml .goreleaser.yaml.original

cleanup() {
    mv .goreleaser.yaml.original .goreleaser.yaml
}
trap cleanup EXIT

echo "=== MUTATION TESTING RELEASE GUARDS ==="
echo

PASS=0
FAIL=0

test_mutant() {
    local name="$1"
    local config="$2"
    local expect_fail="$3"  # "yaml" or "both" for which test should fail
    
    echo "Mutant: $name"
    echo "$config" > .goreleaser.yaml
    
    # Run YAML config test
    if go test -v ./internal/release -run TestGoReleaserConfig 2>&1 | grep -q "FAIL.*TestGoReleaserConfig"; then
        yaml_result="KILLED"
    else
        yaml_result="SURVIVED"
    fi
    
    # Determine overall result
    if [ "$expect_fail" = "yaml" ] || [ "$expect_fail" = "both" ]; then
        if [ "$yaml_result" = "KILLED" ]; then
            echo "  Result: ✓ KILLED (by YAML test)"
            PASS=$((PASS + 1))
        else
            echo "  Result: ✗ SURVIVED (YAML test should have killed it)"
            FAIL=$((FAIL + 1))
        fi
    else
        echo "  Result: ○ NOT TESTED (artifact test needed)"
    fi
    
    echo
}

# Original config template (abbreviated for speed)
read -r -d '' ORIGINAL_CONFIG << 'EOF' || true
version: 2
project_name: pcal
archives:
  - formats: [tar.gz]
    files: [LICENSE, README*, completions/*]
checksum: { name_template: checksums.txt, algorithm: sha256 }
EOF

# Mutant 1: Drop LICENSE
test_mutant "M1: Drop LICENSE" \
"version: 2
project_name: pcal
archives:
  - formats: [tar.gz]
    files: [README*, completions/*]
checksum: { name_template: checksums.txt, algorithm: sha256 }" \
"yaml"

# Mutant 2: LICENSE.bak instead of LICENSE
test_mutant "M2: LICENSE.bak (substring should not match)" \
"version: 2
project_name: pcal
archives:
  - formats: [tar.gz]
    files: [LICENSE.bak, README*, completions/*]
checksum: { name_template: checksums.txt, algorithm: sha256 }" \
"yaml"

# Mutant 3: Second archive missing LICENSE
test_mutant "M3: Second archive without LICENSE" \
"version: 2
project_name: pcal
archives:
  - formats: [tar.gz]
    files: [LICENSE, README*, completions/*]
  - formats: [zip]
    files: [README*, completions/*]
checksum: { name_template: checksums.txt, algorithm: sha256 }" \
"yaml"

# Mutant 4: LICENSE only in second archive
test_mutant "M4: LICENSE only in archive[1], not archive[0]" \
"version: 2
project_name: pcal
archives:
  - formats: [tar.gz]
    files: [README*, completions/*]
  - formats: [zip]
    files: [LICENSE, README*, completions/*]
checksum: { name_template: checksums.txt, algorithm: sha256 }" \
"yaml"

# Mutant 5: Drop README*
test_mutant "M5: Drop README*" \
"version: 2
project_name: pcal
archives:
  - formats: [tar.gz]
    files: [LICENSE, completions/*]
checksum: { name_template: checksums.txt, algorithm: sha256 }" \
"yaml"

# Mutant 6: Drop completions/*
test_mutant "M6: Drop completions/*" \
"version: 2
project_name: pcal
archives:
  - formats: [tar.gz]
    files: [LICENSE, README*]
checksum: { name_template: checksums.txt, algorithm: sha256 }" \
"yaml"

# Mutant 7: Add empty archive (no files field)
test_mutant "M7: Empty archive entry" \
"version: 2
project_name: pcal
archives:
  - formats: [tar.gz]
    files: [LICENSE, README*, completions/*]
  - formats: [zip]
    files: []
checksum: { name_template: checksums.txt, algorithm: sha256 }" \
"yaml"

# Mutant 8: LICENSE_FILE (similar but not exact)
test_mutant "M8: LICENSE_FILE (not LICENSE)" \
"version: 2
project_name: pcal
archives:
  - formats: [tar.gz]
    files: [LICENSE_FILE, README*, completions/*]
checksum: { name_template: checksums.txt, algorithm: sha256 }" \
"yaml"

# Mutant 9: README (without *)
test_mutant "M9: README (not README*)" \
"version: 2
project_name: pcal
archives:
  - formats: [tar.gz]
    files: [LICENSE, README, completions/*]
checksum: { name_template: checksums.txt, algorithm: sha256 }" \
"yaml"

# Mutant 10: completions (without /*)
test_mutant "M10: completions (not completions/*)" \
"version: 2
project_name: pcal
archives:
  - formats: [tar.gz]
    files: [LICENSE, README*, completions]
checksum: { name_template: checksums.txt, algorithm: sha256 }" \
"yaml"

echo "=== MUTATION TESTING RESULTS ==="
echo "Passed: $PASS"
echo "Failed: $FAIL"
echo

if [ $FAIL -gt 0 ]; then
    echo "⨯ Some mutants survived!"
    exit 1
else
    echo "✓ All mutants killed!"
    exit 0
fi
