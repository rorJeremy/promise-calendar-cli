package commands

import (
	"encoding/json"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/rorJeremy/promise-calendar-cli/internal/version"
)

func TestVersion(t *testing.T) {
	original := version.Version
	commit, date := version.Commit, version.Date
	module := version.ModuleVersion
	t.Cleanup(func() {
		version.Version, version.Commit, version.Date = original, commit, date
		version.ModuleVersion = module
	})

	version.Version = "0.1.0"
	version.Commit = "abc123"
	version.Date = "2026-11-02T00:00:00Z"
	version.ModuleVersion = "v9.9.9"

	r := runPcal(t, "version")
	require.Equal(t, 0, r.code, r.stdout)
	var info struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Date    string `json:"date"`
		Go      string `json:"go"`
	}
	require.NoError(t, json.Unmarshal(decodeEnvelope(t, r.stdout).Data, &info))
	require.Equal(t, "0.1.0", info.Version)
	require.Equal(t, "abc123", info.Commit)
	require.Equal(t, "2026-11-02T00:00:00Z", info.Date)
	require.Equal(t, runtime.Version(), info.Go)
	require.Equal(t, "pcal 0.1.0", decodeEnvelope(t, r.stdout).Summary)

	version.Version = "dev"
	version.ModuleVersion = "v0.1.0"
	fallback := runPcal(t, "version")
	require.Equal(t, 0, fallback.code, fallback.stdout)
	require.NoError(t, json.Unmarshal(decodeEnvelope(t, fallback.stdout).Data, &info))
	require.Equal(t, "0.1.0", info.Version, "a go install build has no ldflags, so the module version is shown")

	version.ModuleVersion = "(devel)"
	version.Commit = "none"
	version.Date = "unknown"
	plain := runPcalInTerminal(t, "version")
	require.Equal(t, 0, plain.code, plain.stderr)
	require.Contains(t, plain.stdout, "dev")
	require.Contains(t, plain.stdout, "none")
	require.Contains(t, plain.stdout, "unknown")
	require.Contains(t, plain.stdout, runtime.Version())
}

func TestCompletionScripts(t *testing.T) {
	tests := []struct {
		shell string
		mark  string
	}{
		{"bash", "__start_pcal"},
		{"zsh", "#compdef pcal"},
		{"fish", "complete -c pcal"},
	}
	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			r := runPcal(t, "completion", tt.shell)
			require.Equal(t, 0, r.code, r.stdout)
			require.Contains(t, r.stdout, tt.mark)
			require.NotContains(t, r.stdout, `"ok"`)
		})
	}

	missing := runPcal(t, "completion")
	require.Equal(t, 1, missing.code)
	require.Contains(t, decodeError(t, missing.stdout).Error.Message, "bash, zsh, or fish")

	other := runPcal(t, "completion", "powershell")
	require.Equal(t, 1, other.code)
	require.Contains(t, decodeError(t, other.stdout).Error.Message, "powershell")
}
