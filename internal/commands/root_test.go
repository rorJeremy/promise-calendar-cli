package commands

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// result is what one in-process pcal run produced.
type result struct {
	code   int
	stdout string
	stderr string
}

// runPcal runs pcal in-process with args, its stdout a pipe (not a
// terminal), and returns what it produced.
func runPcal(t *testing.T, args ...string) result {
	t.Helper()
	return run(t, false, args)
}

// runPcalInTerminal runs pcal in-process as if stdout were a terminal.
func runPcalInTerminal(t *testing.T, args ...string) result {
	t.Helper()
	return run(t, true, args)
}

func run(t *testing.T, outTTY bool, args []string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(args, Streams{In: strings.NewReader(""), Out: &stdout, Err: &stderr, OutTTY: outTTY})
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// errorEnvelope is the JSON shape of a failed run.
type errorEnvelope struct {
	OK    bool `json:"ok"`
	Error struct {
		Code    string              `json:"code"`
		Message string              `json:"message"`
		Fields  map[string][]string `json:"fields"`
		Hint    string              `json:"hint"`
	} `json:"error"`
}

// decodeError decodes stdout as an error envelope.
func decodeError(t *testing.T, stdout string) errorEnvelope {
	t.Helper()
	var env errorEnvelope
	require.NoError(t, json.Unmarshal([]byte(stdout), &env), "stdout: %s", stdout)
	require.False(t, env.OK)
	return env
}

func TestHelp(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"--help", []string{"--help"}},
		{"no arguments", nil},
		{"persistent flags are accepted", []string{"--json", "--jq", ".data", "--url", "http://localhost:3000", "--yes", "--help"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runPcal(t, tt.args...)

			require.Equal(t, 0, r.code, "stderr: %s", r.stderr)
			require.Contains(t, r.stdout, "Usage:")
			require.Empty(t, r.stderr)
		})
	}
}

func TestHelpListsPersistentFlags(t *testing.T) {
	r := runPcal(t, "--help")

	require.Equal(t, 0, r.code)
	for _, flag := range []string{"--json", "--quiet", "--jq", "--url", "--yes"} {
		require.Contains(t, r.stdout, flag)
	}
}

// --verbose logs each request and response status to stderr. Unlike
// fizzy-cli's, it's hidden from help.
func TestVerboseIsAHiddenFlag(t *testing.T) {
	r := runPcal(t, "--verbose", "--help")

	require.Equal(t, 0, r.code, "stdout: %s", r.stdout)
	require.NotContains(t, r.stdout, "--verbose")
}

func TestUsageErrors(t *testing.T) {
	const helpHint = "Run `pcal --help` for usage."

	tests := []struct {
		name     string
		args     []string
		wantMsg  string
		wantHint string
	}{
		{"unknown command", []string{"frobnicate"}, `unknown command "frobnicate" for "pcal"`, helpHint},
		{"unknown flag", []string{"--frobnicate"}, "unknown flag: --frobnicate", helpHint},
		// Tokens never go in argv, where they'd land in shell history and
		// the process list.
		{"there is no --token flag", []string{"--token", "secret"}, "unknown flag: --token", helpHint},
		// Only list commands define --csv.
		{"--csv is not a persistent flag", []string{"--csv"}, "unknown flag: --csv", helpHint},
		{"--json with --quiet", []string{"--json", "--quiet"}, "--json and --quiet can't be used together", ""},
		{"an invalid --jq", []string{"--jq", ".data["}, "invalid --jq expression", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runPcal(t, tt.args...)

			require.Equal(t, 1, r.code)
			env := decodeError(t, r.stdout)
			require.Equal(t, "usage", env.Error.Code)
			require.Contains(t, env.Error.Message, tt.wantMsg)
			require.Equal(t, tt.wantHint, env.Error.Hint)
			require.Empty(t, r.stderr)
		})
	}
}

func TestUsageErrorInTerminal(t *testing.T) {
	r := runPcalInTerminal(t, "frobnicate")

	require.Equal(t, 1, r.code)
	require.Empty(t, r.stdout)
	require.Equal(t, "Error: unknown command \"frobnicate\" for \"pcal\"\nRun `pcal --help` for usage.\n", r.stderr)
}
