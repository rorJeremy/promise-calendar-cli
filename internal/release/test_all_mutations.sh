#!/usr/bin/env bash
# Score release-guard mutants.
#
# KILLED only when the test exits non-zero AND its output contains the
# assertion tied to that mutant. A passing test is SURVIVED. A build
# error, panic, timeout, patch miss, compile error, or any other failure
# is INVALID or ERROR. The process exits non-zero if any mutant is
# SURVIVED, INVALID, or ERROR.
#
# Every mutant is also run, unchanged, against the unmutated guard. That
# control must fail with the same assertion. Guard-code patches delete or
# weaken one check; the input is one that only that check rejects, so the
# mutated guard passes (SURVIVED) when the patch does what it says.
#
# Two scoring proofs run after the mutants and are not part of the tally.
# A comment-only edit must score SURVIVED. A config goreleaser cannot
# build must score ERROR. A proof that scores as anything else fails the
# process. The proofs stay in this script so the scorer cannot quietly go
# back to treating "no PASS word" as a kill: a passing go test without
# -v prints "ok", not "PASS".
#
# Runs in a detached git worktree. Does not edit the checkout that
# started it. Not part of `make check`.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
PATCH_DIR="$(cd "$(dirname "$0")/mutants" && pwd)"

# Preserve Go's caches before HOME moves. Hooks run `go run ./cmd/pcal`,
# which must not touch a real keyring, a real config dir, or the
# production calendar.
export GOCACHE="${GOCACHE:-$(go env GOCACHE)}"
export GOMODCACHE="${GOMODCACHE:-$(go env GOMODCACHE)}"
export GOPATH="${GOPATH:-$(go env GOPATH)}"
export PATH="${PATH}:${GOPATH}/bin"
SCRATCH="$(mktemp -d -t pcal-home.XXXXXX)"
export HOME="${SCRATCH}/home"
export XDG_CONFIG_HOME="${SCRATCH}/config"
mkdir -p "${HOME}" "${XDG_CONFIG_HOME}"
export PCAL_NO_KEYRING=1
export PCAL_URL="${PCAL_URL:-http://127.0.0.1:8765}"

LOG_DIR="$(mktemp -d -t pcal-mutation-logs.XXXXXX)"
WORKTREE_DIR=""
FAKE_PID=""
TREE_BEFORE="$(git -C "${REPO_ROOT}" status --porcelain)"

cleanup() {
	cd "${REPO_ROOT}" 2>/dev/null || true
	if [[ -n "${WORKTREE_DIR}" ]]; then
		git worktree remove --force "${WORKTREE_DIR}" 2>/dev/null || true
		rm -rf "${WORKTREE_DIR}"
	fi
	git -C "${REPO_ROOT}" worktree prune 2>/dev/null || true
	if [[ -n "${FAKE_PID}" ]]; then
		kill "${FAKE_PID}" 2>/dev/null || true
	fi
}
trap cleanup EXIT

if ! curl -sf -o /dev/null --max-time 0.3 "${PCAL_URL}/"; then
	python3 -m http.server 8765 --bind 127.0.0.1 >"${LOG_DIR}/fake-server.log" 2>&1 &
	FAKE_PID=$!
	sleep 0.2
fi

WORKTREE_DIR="$(mktemp -d -t pcal-mutations.XXXXXX)"
git -C "${REPO_ROOT}" worktree add --detach "${WORKTREE_DIR}" HEAD >/dev/null
cd "${WORKTREE_DIR}"

# Matchers for the mutant currently being scored. Empty arrays are fine
# under set -u when expanded as ${arr[@]+"${arr[@]}"}.
expect_arr=()
forbid_arr=()
require_re=()
forbid_re=()

reset_rules() {
	expect_arr=()
	forbid_arr=()
	require_re=()
	forbid_re=()
}

OUTPUT=""
TEST_STATUS=0
CLASS=""
REASON=""

# Build-failure markers. These are ERROR even when the assertion text
# also appears somewhere in the log (a goreleaser dump of the config, a
# panic, a missing tool).
error_markers=(
	"goreleaser snapshot failed"
	"goreleaser not found"
	"panic:"
	"git status failed:"
	"repo tree modified by build"
	"failed to read .goreleaser.yaml"
	"failed to parse .goreleaser.yaml"
)

classify() {
	CLASS=""
	REASON=""
	if [[ "${TEST_STATUS}" -eq 124 ]]; then
		CLASS="ERROR"
		REASON="timed out"
		return
	fi
	# A skip exits 0. That is not a pass of the assertion and not evidence
	# the mutant survived; the test did not run.
	if grep -qF -- "--- SKIP:" <<<"${OUTPUT}"; then
		CLASS="ERROR"
		REASON="test skipped"
		return
	fi
	if [[ "${TEST_STATUS}" -eq 0 ]]; then
		CLASS="SURVIVED"
		REASON="test passed (exit 0)"
		return
	fi
	local marker
	for marker in "${error_markers[@]}"; do
		if grep -qF -- "${marker}" <<<"${OUTPUT}"; then
			CLASS="ERROR"
			REASON="failed outside the assertion (${marker})"
			return
		fi
	done
	local need
	if [[ ${#expect_arr[@]} -eq 0 ]]; then
		CLASS="ERROR"
		REASON="no assertion configured"
		return
	fi
	for need in "${expect_arr[@]}"; do
		if ! grep -qF -- "${need}" <<<"${OUTPUT}"; then
			CLASS="ERROR"
			REASON="failed without assertion: ${need}"
			return
		fi
	done
	local banned
	if [[ ${#forbid_arr[@]} -gt 0 ]]; then
		for banned in "${forbid_arr[@]}"; do
			if grep -qF -- "${banned}" <<<"${OUTPUT}"; then
				CLASS="ERROR"
				REASON="not isolated; also saw: ${banned}"
				return
			fi
		done
	fi
	local re
	if [[ ${#require_re[@]} -gt 0 ]]; then
		for re in "${require_re[@]}"; do
			if ! grep -Eq -- "${re}" <<<"${OUTPUT}"; then
				CLASS="ERROR"
				REASON="failed without required pattern: ${re}"
				return
			fi
		done
	fi
	if [[ ${#forbid_re[@]} -gt 0 ]]; then
		for re in "${forbid_re[@]}"; do
			if grep -Eq -- "${re}" <<<"${OUTPUT}"; then
				CLASS="ERROR"
				REASON="not isolated; matched ${re}"
				return
			fi
		done
	fi
	CLASS="KILLED"
	REASON="assertion matched"
}

run_go_test() {
	local test_name="$1"
	local log_path="$2"
	set +e
	if [[ "${test_name}" == "TestSnapshotArtifacts" ]]; then
		OUTPUT="$(timeout 180 env TEST_SNAPSHOT=1 go test -count=1 -v -run '^TestSnapshotArtifacts$' ./internal/release 2>&1)"
		TEST_STATUS=$?
	else
		OUTPUT="$(timeout 60 go test -count=1 -v -run '^TestGoReleaserConfig$' ./internal/release 2>&1)"
		TEST_STATUS=$?
	fi
	set -e
	# timeout prefixes nothing; the exit status is 124.
	printf '%s\n' "${OUTPUT}" >"${log_path}"
}

reset_tree() {
	git reset --hard HEAD >/dev/null
	git clean -fd >/dev/null
}

# Write the bad config and any extra files. Does not reset tracked
# edits, so a guard patch applied just before this stays applied.
prepare_input() {
	local config="$1"
	local setup="$2"
	git clean -fd >/dev/null
	if [[ -n "${setup}" ]]; then
		bash -c "${setup}"
	fi
	if [[ -n "${config}" ]]; then
		printf '%s\n' "${config}" >.goreleaser.yaml
	fi
}

apply_patch() {
	local patch="$1"
	local err_file="$2"
	SETUP_ERROR=""
	if [[ -z "${patch}" ]]; then
		return 0
	fi
	if [[ ! -f "${PATCH_DIR}/${patch}" ]]; then
		SETUP_ERROR="patch file missing: ${patch}"
		return 1
	fi
	if ! git apply "${PATCH_DIR}/${patch}" 2>"${err_file}"; then
		SETUP_ERROR="patch did not apply: $(tr '\n' ' ' <"${err_file}")"
		return 1
	fi
	if git diff --quiet; then
		SETUP_ERROR="patch left the file unchanged"
		return 1
	fi
	if ! go test -c -o /dev/null ./internal/release 2>"${err_file}"; then
		SETUP_ERROR="mutated guard does not compile: $(tail -n 8 "${err_file}" | tr '\n' ' ')"
		return 1
	fi
	return 0
}

RESULTS="${LOG_DIR}/results.tsv"
printf 'id\tmutation\tinput\tcontrol\tmutant\tstatus\treason\n' >"${RESULTS}"

KILLED_N=0
SURVIVED_N=0
INVALID_N=0
ERROR_N=0
EXIT_CODE=0

record() {
	local id="$1" mutation="$2" input="$3" control="$4" mutant="$5" status="$6" reason="$7"
	# Keep the table machine-readable: no tabs or newlines in cells.
	reason="${reason//$'\t'/ }"
	reason="${reason//$'\n'/ }"
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
		"${id}" "${mutation}" "${input}" "${control}" "${mutant}" "${status}" "${reason}" >>"${RESULTS}"
	printf '%-4s control=%-8s mutant=%-8s status=%-8s %s\n' \
		"${id}" "${control}" "${mutant}" "${status}" "${mutation}"
	if [[ "${status}" != "KILLED" ]]; then
		printf '     input: %s\n     %s\n' "${input}" "${reason}"
	fi
	case "${status}" in
	KILLED) KILLED_N=$((KILLED_N + 1)) ;;
	SURVIVED)
		SURVIVED_N=$((SURVIVED_N + 1))
		EXIT_CODE=1
		;;
	INVALID)
		INVALID_N=$((INVALID_N + 1))
		EXIT_CODE=1
		;;
	ERROR)
		ERROR_N=$((ERROR_N + 1))
		EXIT_CODE=1
		;;
	*)
		ERROR_N=$((ERROR_N + 1))
		EXIT_CODE=1
		;;
	esac
}

# id kind mutation input test patch setup config
# Matchers must already be set. kind is config, artifact, or guard.
run_mutant() {
	local id="$1"
	local mutation="$2"
	local input="$3"
	local test_name="$4"
	local patch="$5"
	local setup="$6"
	local config="$7"

	local control_class="ERROR"
	local control_reason="not run"
	local mutant_class="ERROR"
	local mutant_reason="not run"
	local final="ERROR"
	local final_reason=""
	SETUP_ERROR=""

	reset_tree
	prepare_input "${config}" "${setup}"
	run_go_test "${test_name}" "${LOG_DIR}/${id}-control.txt"
	classify
	control_class="${CLASS}"
	control_reason="${REASON}"

	reset_tree
	if ! apply_patch "${patch}" "${LOG_DIR}/${id}-apply.err"; then
		mutant_class="INVALID"
		mutant_reason="${SETUP_ERROR}"
	else
		prepare_input "${config}" "${setup}"
		run_go_test "${test_name}" "${LOG_DIR}/${id}-mutant.txt"
		classify
		mutant_class="${CLASS}"
		mutant_reason="${REASON}"
	fi

	if [[ "${control_class}" != "KILLED" ]]; then
		final="INVALID"
		final_reason="control was ${control_class} (${control_reason}); unmutated guard did not reject this input with the assertion"
	elif [[ "${mutant_class}" == "INVALID" ]]; then
		final="INVALID"
		final_reason="${mutant_reason}"
	else
		final="${mutant_class}"
		final_reason="${mutant_reason}"
	fi

	record "${id}" "${mutation}" "${input}" "${control_class}" "${mutant_class}" "${final}" "${final_reason}"
}

artifact_config() {
	local body="$1"
	cat <<EOF
version: 2
project_name: pcal
before:
  hooks:
    - go mod tidy
    - sh -c 'set -eu; mkdir -p completions; for s in bash zsh fish; do go run ./cmd/pcal completion \$s > completions/pcal.\$s; test -s completions/pcal.\$s || exit 1; done'
builds:
  - main: ./cmd/pcal
    binary: pcal
    env: [CGO_ENABLED=0]
    goos: [linux]
    goarch: [amd64]
    flags: [-trimpath]
    ldflags: [-s -w]
archives:
${body}
checksum: { name_template: checksums.txt }
EOF
}

yaml_config() {
	local files_block="$1"
	cat <<EOF
version: 2
archives:
${files_block}
checksum: { name_template: checksums.txt }
EOF
}

echo "Release-guard mutation scoring"
echo "  worktree: ${WORKTREE_DIR}"
echo "  logs:     ${LOG_DIR}"
echo "  goreleaser: $(command -v goreleaser || echo MISSING)"
echo "  rule: KILLED only on non-zero exit plus that mutant's assertion"
echo

echo "Config mutants (bad .goreleaser.yaml, unmutated YAML guard)"

reset_rules
expect_arr+=('archives[0]: "LICENSE" missing from files. Found: [README* completions/*]')
forbid_arr+=(
	'archives[0]: "README*" missing from files'
	'archives[0]: "completions/*" missing from files'
)
run_mutant C1 \
	"Drop LICENSE from files" \
	"files [README*, completions/*]" \
	TestGoReleaserConfig "" "" \
	"$(yaml_config '  - formats: [tar.gz]
    files: [README*, completions/*]')"

reset_rules
expect_arr+=('archives[0]: "LICENSE" missing from files. Found: [LICENSE.bak README* completions/*]')
forbid_arr+=(
	'archives[0]: "README*" missing from files'
	'archives[0]: "completions/*" missing from files'
)
run_mutant C2 \
	"Rename LICENSE to LICENSE.bak" \
	"files [LICENSE.bak, README*, completions/*]" \
	TestGoReleaserConfig "" "" \
	"$(yaml_config '  - formats: [tar.gz]
    files: [LICENSE.bak, README*, completions/*]')"

reset_rules
expect_arr+=('archives[1]: "LICENSE" missing from files. Found: [README* completions/*]')
forbid_arr+=(
	'archives[0]:'
	'"README*" missing from files'
	'"completions/*" missing from files'
)
run_mutant C3 \
	"Second archive omits LICENSE" \
	"archive[0] complete, archive[1] files [README*, completions/*]" \
	TestGoReleaserConfig "" "" \
	"$(yaml_config '  - formats: [tar.gz]
    files: [LICENSE, README*, completions/*]
  - formats: [zip]
    files: [README*, completions/*]')"

reset_rules
expect_arr+=('archives[0]: "LICENSE" missing from files. Found: [README* completions/*]')
forbid_arr+=(
	'archives[1]:'
	'"README*" missing from files'
	'"completions/*" missing from files'
)
run_mutant C4 \
	"LICENSE only on archive[1]" \
	"archive[0] files [README*, completions/*], archive[1] complete" \
	TestGoReleaserConfig "" "" \
	"$(yaml_config '  - formats: [tar.gz]
    files: [README*, completions/*]
  - formats: [zip]
    files: [LICENSE, README*, completions/*]')"

reset_rules
expect_arr+=('archives[0]: "README*" missing from files. Found: [LICENSE completions/*]')
forbid_arr+=(
	'archives[0]: "LICENSE" missing from files'
	'archives[0]: "completions/*" missing from files'
)
run_mutant C5 \
	"Drop README* from files" \
	"files [LICENSE, completions/*]" \
	TestGoReleaserConfig "" "" \
	"$(yaml_config '  - formats: [tar.gz]
    files: [LICENSE, completions/*]')"

reset_rules
expect_arr+=('archives[0]: "completions/*" missing from files. Found: [LICENSE README*]')
forbid_arr+=(
	'archives[0]: "LICENSE" missing from files'
	'archives[0]: "README*" missing from files'
)
run_mutant C6 \
	"Drop completions/* from files" \
	"files [LICENSE, README*]" \
	TestGoReleaserConfig "" "" \
	"$(yaml_config '  - formats: [tar.gz]
    files: [LICENSE, README*]')"

reset_rules
expect_arr+=(
	'archives[0]: "LICENSE" missing from files. Found: []'
	'archives[0]: "README*" missing from files. Found: []'
	'archives[0]: "completions/*" missing from files. Found: []'
)
run_mutant C7 \
	"Empty files list" \
	"files []" \
	TestGoReleaserConfig "" "" \
	"$(yaml_config '  - formats: [tar.gz]
    files: []')"

reset_rules
expect_arr+=('archives[0]: "LICENSE" missing from files. Found: [LICENSE_FILE README* completions/*]')
forbid_arr+=(
	'archives[0]: "README*" missing from files'
	'archives[0]: "completions/*" missing from files'
)
run_mutant C8 \
	"Rename LICENSE to LICENSE_FILE" \
	"files [LICENSE_FILE, README*, completions/*]" \
	TestGoReleaserConfig "" "" \
	"$(yaml_config '  - formats: [tar.gz]
    files: [LICENSE_FILE, README*, completions/*]')"

reset_rules
expect_arr+=('archives[0]: "README*" missing from files. Found: [LICENSE README completions/*]')
forbid_arr+=(
	'archives[0]: "LICENSE" missing from files'
	'archives[0]: "completions/*" missing from files'
)
run_mutant C9 \
	"README without the glob star" \
	"files [LICENSE, README, completions/*]" \
	TestGoReleaserConfig "" "" \
	"$(yaml_config '  - formats: [tar.gz]
    files: [LICENSE, README, completions/*]')"

reset_rules
expect_arr+=('archives[0]: "completions/*" missing from files. Found: [LICENSE README* completions]')
forbid_arr+=(
	'archives[0]: "LICENSE" missing from files'
	'archives[0]: "README*" missing from files'
)
run_mutant C10 \
	"completions without the glob star" \
	"files [LICENSE, README*, completions]" \
	TestGoReleaserConfig "" "" \
	"$(yaml_config '  - formats: [tar.gz]
    files: [LICENSE, README*, completions]')"

echo
echo "Artifact mutants (bad archive contents, unmutated artifact guard)"

TAR_NO_LICENSE="$(artifact_config '  - formats: [tar.gz]
    files: [README*, completions/*]')"
TAR_NO_README="$(artifact_config '  - formats: [tar.gz]
    files: [LICENSE, completions/*]')"
TAR_NO_COMPLETIONS="$(artifact_config '  - formats: [tar.gz]
    files: [LICENSE, README*]')"

reset_rules
expect_arr+=('missing required file: LICENSE')
forbid_arr+=(
	'missing required file: README.md'
	'no completion files'
)
run_mutant A1 \
	"Archive omits LICENSE" \
	"tar.gz has README.md and completions, no LICENSE" \
	TestSnapshotArtifacts "" "" \
	"${TAR_NO_LICENSE}"

reset_rules
expect_arr+=('missing required file: README.md')
forbid_arr+=(
	'missing required file: LICENSE'
	'no completion files'
)
run_mutant A2 \
	"Archive omits README" \
	"tar.gz has LICENSE and completions, no README.md" \
	TestSnapshotArtifacts "" "" \
	"${TAR_NO_README}"

reset_rules
expect_arr+=('no completion files (pcal.bash/zsh/fish) found in archive')
forbid_arr+=(
	'missing required file: LICENSE'
	'missing required file: README.md'
)
run_mutant A3 \
	"Archive omits completions" \
	"tar.gz has LICENSE and README.md, no completion scripts" \
	TestSnapshotArtifacts "" "" \
	"${TAR_NO_COMPLETIONS}"

echo
echo "Guard-code mutants (one check weakened; input only that check rejects)"

reset_rules
expect_arr+=('archives[0]: "LICENSE" missing from files. Found: [README* completions/*]')
forbid_arr+=(
	'archives[0]: "README*" missing from files'
	'archives[0]: "completions/*" missing from files'
)
run_mutant G1 \
	"Drop LICENSE from the required list" \
	"files [README*, completions/*]" \
	TestGoReleaserConfig g1-drop-license.patch "" \
	"$(yaml_config '  - formats: [tar.gz]
    files: [README*, completions/*]')"

reset_rules
expect_arr+=('archives[0]: "completions/*" missing from files. Found: [LICENSE README*]')
forbid_arr+=(
	'archives[0]: "LICENSE" missing from files'
	'archives[0]: "README*" missing from files'
)
run_mutant G2 \
	"Drop completions/* from the required list" \
	"files [LICENSE, README*]" \
	TestGoReleaserConfig g2-drop-completions.patch "" \
	"$(yaml_config '  - formats: [tar.gz]
    files: [LICENSE, README*]')"

reset_rules
expect_arr+=('archives[1]: "LICENSE" missing from files. Found: [README* completions/*]')
forbid_arr+=(
	'archives[0]:'
	'"README*" missing from files'
	'"completions/*" missing from files'
)
run_mutant G3 \
	"Check only archives[0]" \
	"archive[0] complete, archive[1] omits LICENSE" \
	TestGoReleaserConfig g3-only-first-archive.patch "" \
	"$(yaml_config '  - formats: [tar.gz]
    files: [LICENSE, README*, completions/*]
  - formats: [zip]
    files: [README*, completions/*]')"

reset_rules
expect_arr+=('archives[0]: "LICENSE" missing from files. Found: [LICENSE.bak README* completions/*]')
forbid_arr+=(
	'archives[0]: "README*" missing from files'
	'archives[0]: "completions/*" missing from files'
)
run_mutant G4 \
	"Accept LICENSE as a substring" \
	"files [LICENSE.bak, README*, completions/*]" \
	TestGoReleaserConfig g4-substring-match.patch "" \
	"$(yaml_config '  - formats: [tar.gz]
    files: [LICENSE.bak, README*, completions/*]')"

reset_rules
expect_arr+=(
	'archives[0]: "LICENSE" missing from files. Found: []'
	'archives[0]: "README*" missing from files. Found: []'
	'archives[0]: "completions/*" missing from files. Found: []'
)
run_mutant G5 \
	"Invert the found check" \
	"files [] (nothing matches, so an inverted check does not fire)" \
	TestGoReleaserConfig g5-invert-found.patch "" \
	"$(yaml_config '  - formats: [tar.gz]
    files: []')"

reset_rules
expect_arr+=('missing required file: LICENSE')
forbid_arr+=(
	'missing required file: README.md'
	'no completion files'
)
run_mutant G6 \
	"Drop the LICENSE assertion" \
	"tar.gz has README.md and completions, no LICENSE" \
	TestSnapshotArtifacts g6-drop-license-check.patch "" \
	"${TAR_NO_LICENSE}"

reset_rules
expect_arr+=('missing required file: README.md')
forbid_arr+=(
	'missing required file: LICENSE'
	'no completion files'
)
run_mutant G7 \
	"Drop the README assertion" \
	"tar.gz has LICENSE and completions, no README.md" \
	TestSnapshotArtifacts g7-drop-readme-check.patch "" \
	"${TAR_NO_README}"

reset_rules
expect_arr+=('no completion files (pcal.bash/zsh/fish) found in archive')
forbid_arr+=(
	'missing required file: LICENSE'
	'missing required file: README.md'
)
run_mutant G8 \
	"Drop the completions assertion" \
	"tar.gz has LICENSE and README.md, no completion scripts" \
	TestSnapshotArtifacts g8-drop-completions-check.patch "" \
	"${TAR_NO_COMPLETIONS}"

reset_rules
expect_arr+=(
	'missing required file: LICENSE'
	'LICENSE.bak'
)
forbid_arr+=(
	'missing required file: README.md'
	'no completion files'
)
run_mutant G9 \
	"Accept any base name containing LICENSE" \
	"tar.gz contains LICENSE.bak, README.md, and completions, not LICENSE" \
	TestSnapshotArtifacts g9-license-substring.patch "cp LICENSE LICENSE.bak" \
	"$(artifact_config '  - formats: [tar.gz]
    files: [LICENSE.bak, README*, completions/*]')"

# Zip is the only broken archive. Skipping zip, or checking only the
# first path (tar.gz is globbed first), lets this input through.
SPLIT_ZIP_BROKEN="$(artifact_config '  - id: tar
    formats: [tar.gz]
    files: [LICENSE, README*, completions/*]
  - id: zip
    formats: [zip]
    files: [README*, completions/*]')"

reset_rules
expect_arr+=('missing required file: LICENSE')
forbid_arr+=(
	'missing required file: README.md'
	'no completion files'
)
require_re+=(
	'--- FAIL: TestSnapshotArtifacts/.*\.zip '
	'--- PASS: TestSnapshotArtifacts/.*\.tar\.gz '
)
forbid_re+=('--- FAIL: TestSnapshotArtifacts/.*\.tar\.gz')
run_mutant G10 \
	"Do not inspect zip archives" \
	"tar.gz complete; zip has README and completions but no LICENSE" \
	TestSnapshotArtifacts g10-skip-zip.patch "" \
	"${SPLIT_ZIP_BROKEN}"

reset_rules
expect_arr+=('no archives found in temp dist')
forbid_arr+=(
	'missing required file: LICENSE'
	'missing required file: README.md'
	'no completion files'
)
run_mutant G11 \
	"Treat zero archives as success" \
	"snapshot format binary: dist has no tar.gz and no zip" \
	TestSnapshotArtifacts g11-zero-archives.patch "" \
	"$(artifact_config '  - formats: [binary]')"

reset_rules
expect_arr+=('missing required file: LICENSE')
forbid_arr+=(
	'missing required file: README.md'
	'no completion files'
)
require_re+=(
	'--- FAIL: TestSnapshotArtifacts/.*\.zip '
	'--- PASS: TestSnapshotArtifacts/.*\.tar\.gz '
)
forbid_re+=('--- FAIL: TestSnapshotArtifacts/.*\.tar\.gz')
run_mutant G12 \
	"Check only the first archive path" \
	"tar.gz (first) complete; zip (second) has no LICENSE" \
	TestSnapshotArtifacts g12-only-first-archive.patch "" \
	"${SPLIT_ZIP_BROKEN}"

echo
echo "Scoring proofs (not part of the mutant tally)"

# Proof 1: equivalent mutant. go test without -v prints "ok", which does
# not contain PASS. The old rule scored that as KILLED.
reset_tree
if ! apply_patch proof-noop.patch "${LOG_DIR}/proof-noop-apply.err"; then
	echo "P1  no-op comment  INVALID  ${SETUP_ERROR}"
	EXIT_CODE=1
	P1_CLASS="INVALID"
else
	run_go_test_nov() {
		set +e
		OUTPUT="$(go test -count=1 -run '^TestGoReleaserConfig$' ./internal/release 2>&1)"
		TEST_STATUS=$?
		set -e
		printf '%s\n' "${OUTPUT}" >"${LOG_DIR}/proof-noop.txt"
	}
	run_go_test_nov
	# Classify with the strict rule. Exit 0 is SURVIVED regardless of expect.
	reset_rules
	expect_arr+=('archives[0]: "LICENSE" missing from files')
	classify
	P1_CLASS="${CLASS}"
	old_rule="KILLED"
	if grep -q "PASS" <<<"${OUTPUT}"; then
		old_rule="SURVIVED"
	fi
	echo "P1  no-op comment on the YAML guard, valid .goreleaser.yaml"
	echo "    go test (no -v) output: $(tr '\n' ' ' <<<"${OUTPUT}")"
	echo "    word PASS present: $(grep -q "PASS" <<<"${OUTPUT}" && echo yes || echo no)"
	echo "    old rule (no PASS => KILLED): ${old_rule}"
	echo "    strict rule: ${P1_CLASS} (${REASON})"
	if [[ "${P1_CLASS}" != "SURVIVED" ]]; then
		echo "    PROOF FAILED: expected SURVIVED"
		EXIT_CODE=1
	fi
fi

# Proof 2: a build goreleaser rejects. The test fails, but not with the
# LICENSE assertion. That is ERROR, not KILLED.
reset_tree
BROKEN="$(artifact_config '  - formats: [tar.gz]
    files: [THIS_FILE_DOES_NOT_EXIST]')"
printf '%s\n' "${BROKEN}" >.goreleaser.yaml
run_go_test TestSnapshotArtifacts "${LOG_DIR}/proof-broken.txt"
reset_rules
expect_arr+=('missing required file: LICENSE')
classify
P2_CLASS="${CLASS}"
old_rule="KILLED"
if grep -q "PASS" <<<"${OUTPUT}"; then
	old_rule="SURVIVED"
fi
echo "P2  archive lists a file that is not in the tree"
echo "    old rule (no PASS => KILLED): ${old_rule}"
echo "    strict rule: ${P2_CLASS} (${REASON})"
if [[ "${P2_CLASS}" != "ERROR" ]]; then
	echo "    PROOF FAILED: expected ERROR, not KILLED"
	EXIT_CODE=1
fi

echo
echo "MUTATION_TABLE_BEGIN"
cat "${RESULTS}"
echo "MUTATION_TABLE_END"
echo
echo "killed=${KILLED_N} survived=${SURVIVED_N} invalid=${INVALID_N} error=${ERROR_N}"
echo "scoring proofs: P1=${P1_CLASS} (want SURVIVED) P2=${P2_CLASS} (want ERROR)"
echo "logs: ${LOG_DIR}"
if [[ "${SURVIVED_N}" -gt 0 ]]; then
	echo "Guard-code mutants that survive removed the only check that rejects their input."
	echo "The unmutated control rejected that same input. Survived is not counted as killed."
fi

cd "${REPO_ROOT}"
TREE_AFTER="$(git -C "${REPO_ROOT}" status --porcelain)"
if [[ "${TREE_BEFORE}" != "${TREE_AFTER}" ]]; then
	echo "ERROR: the starting checkout changed during mutation testing"
	echo "before: ${TREE_BEFORE}"
	echo "after:  ${TREE_AFTER}"
	EXIT_CODE=1
fi

exit "${EXIT_CODE}"
