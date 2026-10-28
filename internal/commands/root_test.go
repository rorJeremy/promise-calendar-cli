package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// runPcal runs pcal in-process with args and returns its exit code, stdout,
// and stderr.
func runPcal(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(args, Streams{In: strings.NewReader(""), Out: &stdout, Err: &stderr})
	return code, stdout.String(), stderr.String()
}

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name:       "--help prints usage",
			args:       []string{"--help"},
			wantCode:   0,
			wantStdout: "Usage:",
		},
		{
			name:       "no arguments prints usage",
			args:       nil,
			wantCode:   0,
			wantStdout: "Usage:",
		},
		{
			name:       "persistent flags are accepted",
			args:       []string{"--json", "--quiet", "--jq", ".data", "--url", "http://localhost:3000", "--yes", "--help"},
			wantCode:   0,
			wantStdout: "Usage:",
		},
		{
			name:       "unknown command is a usage error",
			args:       []string{"frobnicate"},
			wantCode:   1,
			wantStderr: `unknown command "frobnicate" for "pcal"`,
		},
		{
			name:       "unknown flag is a usage error",
			args:       []string{"--frobnicate"},
			wantCode:   1,
			wantStderr: "unknown flag: --frobnicate",
		},
		{
			// Tokens never go in argv, where they'd land in shell history
			// and the process list.
			name:       "there is no --token flag",
			args:       []string{"--token", "secret"},
			wantCode:   1,
			wantStderr: "unknown flag: --token",
		},
		{
			// Only list commands define --csv.
			name:       "--csv is not a persistent flag",
			args:       []string{"--csv"},
			wantCode:   1,
			wantStderr: "unknown flag: --csv",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runPcal(t, tt.args...)

			require.Equal(t, tt.wantCode, code, "stderr: %s", stderr)
			require.Contains(t, stdout, tt.wantStdout)
			require.Contains(t, stderr, tt.wantStderr)
		})
	}
}

func TestHelpListsPersistentFlags(t *testing.T) {
	code, stdout, _ := runPcal(t, "--help")

	require.Equal(t, 0, code)
	for _, flag := range []string{"--json", "--quiet", "--jq", "--url", "--yes"} {
		require.Contains(t, stdout, flag)
	}
}
