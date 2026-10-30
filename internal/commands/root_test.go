package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/rorJeremy/promise-calendar-cli/internal/auth"
	"github.com/rorJeremy/promise-calendar-cli/internal/output"
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
		// Every usage error gets the help line, as in fizzy-cli (root.go:394).
		{"--json with --quiet", []string{"--json", "--quiet"}, "--json and --quiet can't be used together", helpHint},
		{"an invalid --jq", []string{"--jq", ".data["}, "invalid --jq expression", helpHint},
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

// runWithSubcommand runs pcal in-process with sub added to the command
// tree, its stdout a pipe. sub's name must not be a real command's.
func runWithSubcommand(t *testing.T, sub *cobra.Command, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	a := &app{streams: Streams{In: strings.NewReader(""), Out: &stdout, Err: &stderr}, getenv: env(nil)}
	root := newRootCmd(a)
	root.AddCommand(sub)
	code := execute(a, root, args)
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// TestErrorClassification pins which errors are usage errors: only cobra's
// own (flags, arguments, required flags). An error pcal's code returns keeps
// its own code, and a plain one is an API error (exit 7) with no help hint.
func TestErrorClassification(t *testing.T) {
	loggedOut := auth.NewStore(t.TempDir(), func(key string) string {
		if key == "PCAL_NO_KEYRING" {
			return "1"
		}
		return ""
	}, io.Discard)
	runE := func(err error) func(*cobra.Command, []string) error {
		return func(*cobra.Command, []string) error { return err }
	}

	tests := []struct {
		name     string
		sub      *cobra.Command
		args     []string
		wantExit int
		wantCode string
		wantMsg  string
		wantHint string
	}{
		{
			name:     "a plain error from RunE is an API error",
			sub:      &cobra.Command{Use: "probe", RunE: runE(errors.New("reading config.json: unexpected end of JSON input"))},
			args:     []string{"probe"},
			wantExit: 7, wantCode: "api_error", wantMsg: "reading config.json: unexpected end of JSON input",
		},
		{
			name: "not logged in is an auth error",
			sub: &cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error {
				_, err := loggedOut.Resolve("https://calendar.promisechurch.io")
				return err
			}},
			args:     []string{"probe"},
			wantExit: 3, wantCode: "auth_required", wantMsg: "not logged in", wantHint: "Run `pcal login`.",
		},
		{
			name:     "a plain error from PreRunE is an API error",
			sub:      &cobra.Command{Use: "probe", PreRunE: runE(errors.New("boom")), RunE: runE(nil)},
			args:     []string{"probe"},
			wantExit: 7, wantCode: "api_error", wantMsg: "boom",
		},
		{
			name:     "a usage error from RunE gets the help line",
			sub:      &cobra.Command{Use: "probe", RunE: runE(output.Usagef("--to 2026-10-01 is before --from 2026-10-31"))},
			args:     []string{"probe"},
			wantExit: 1, wantCode: "usage", wantMsg: "--to 2026-10-01 is before --from 2026-10-31", wantHint: "Run `pcal probe --help` for usage.",
		},
		{
			name:     "a usage error with its own hint keeps it",
			sub:      &cobra.Command{Use: "probe", RunE: runE(&output.Error{Code: output.CodeUsage, Message: "PCAL_TOKEN is set", Hint: "Unset PCAL_TOKEN."})},
			args:     []string{"probe"},
			wantExit: 1, wantCode: "usage", wantMsg: "PCAL_TOKEN is set", wantHint: "Unset PCAL_TOKEN.",
		},
		{
			name:     "a wrapped *output.Error keeps its code",
			sub:      &cobra.Command{Use: "probe", RunE: runE(fmt.Errorf("loading: %w", &output.Error{Code: output.CodeNotFound, Message: "not_found"}))},
			args:     []string{"probe"},
			wantExit: 2, wantCode: "not_found", wantMsg: "not_found",
		},
		{
			name:     "an unknown flag is a usage error",
			sub:      &cobra.Command{Use: "probe", RunE: runE(nil)},
			args:     []string{"probe", "--frobnicate"},
			wantExit: 1, wantCode: "usage", wantMsg: "unknown flag: --frobnicate", wantHint: "Run `pcal probe --help` for usage.",
		},
		{
			name:     "an extra argument is a usage error",
			sub:      &cobra.Command{Use: "probe", Args: cobra.NoArgs, RunE: runE(nil)},
			args:     []string{"probe", "extra"},
			wantExit: 1, wantCode: "usage", wantMsg: `unknown command "extra" for "pcal probe"`, wantHint: "Run `pcal probe --help` for usage.",
		},
		{
			name: "a missing required flag is a usage error",
			sub: func() *cobra.Command {
				c := &cobra.Command{Use: "probe", RunE: runE(nil)}
				c.Flags().String("name", "", "")
				_ = c.MarkFlagRequired("name")
				return c
			}(),
			args:     []string{"probe"},
			wantExit: 1, wantCode: "usage", wantMsg: `required flag(s) "name" not set`, wantHint: "Run `pcal probe --help` for usage.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runWithSubcommand(t, tt.sub, tt.args...)

			require.Equal(t, tt.wantExit, r.code, "stdout: %s", r.stdout)
			env := decodeError(t, r.stdout)
			require.Equal(t, tt.wantCode, env.Error.Code)
			require.Equal(t, tt.wantMsg, env.Error.Message)
			require.Equal(t, tt.wantHint, env.Error.Hint)
		})
	}
}

// With no arguments, pcal shows help. cobra reads the process's own
// arguments when it's given nil, so Run must never pass it nil: here the
// "process" was started with a stray word.
func TestRunWithNilArgsIgnoresTheProcessArguments(t *testing.T) {
	saved := os.Args
	t.Cleanup(func() { os.Args = saved })
	os.Args = []string{"pcal.test", "frobnicate"}

	r := runPcal(t)

	require.Equal(t, 0, r.code, "stdout: %s", r.stdout)
	require.Contains(t, r.stdout, "Usage:")
}

// Styling (bold, nothing else) follows the environment the way termenv
// does: NO_COLOR, CLICOLOR=0, and TERM=dumb turn it off, CLICOLOR_FORCE
// overrides the last two, and NO_COLOR beats CLICOLOR_FORCE.
func TestOutputOptionsColorFromTheEnvironment(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		wantNoColor bool
	}{
		{"nothing set", nil, false},
		{"NO_COLOR", map[string]string{"NO_COLOR": "1"}, true},
		{"an empty NO_COLOR counts as unset", map[string]string{"NO_COLOR": ""}, false},
		{"CLICOLOR=0", map[string]string{"CLICOLOR": "0"}, true},
		{"CLICOLOR=1", map[string]string{"CLICOLOR": "1"}, false},
		{"CLICOLOR_FORCE overrides CLICOLOR=0", map[string]string{"CLICOLOR": "0", "CLICOLOR_FORCE": "1"}, false},
		{"CLICOLOR_FORCE=0 doesn't", map[string]string{"CLICOLOR": "0", "CLICOLOR_FORCE": "0"}, true},
		{"TERM=dumb", map[string]string{"TERM": "dumb"}, true},
		{"CLICOLOR_FORCE overrides TERM=dumb", map[string]string{"TERM": "dumb", "CLICOLOR_FORCE": "1"}, false},
		{"NO_COLOR beats CLICOLOR_FORCE", map[string]string{"NO_COLOR": "1", "CLICOLOR_FORCE": "1"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, key := range []string{"NO_COLOR", "CLICOLOR", "CLICOLOR_FORCE", "TERM"} {
				t.Setenv(key, tt.env[key])
			}
			a := &app{streams: Streams{OutTTY: true}, getenv: os.Getenv}

			require.Equal(t, tt.wantNoColor, a.outputOptions().NoColor)
		})
	}
}

// The root takes no arguments of its own, so a stray word is an unknown
// command. Like cobra's own message for a root with subcommands, it
// suggests close command names, in the hint so the message stays one line.
func TestUnknownCommandSuggestions(t *testing.T) {
	runE := func(*cobra.Command, []string) error { return nil }
	tests := []struct {
		name     string
		subs     []string
		arg      string
		wantHint string
	}{
		// pcal whoami is a real command; the others are added for the test.
		{"one close name", []string{"events"}, "whoamj", "Did you mean `pcal whoami`? Run `pcal --help` for usage."},
		{"a prefix", []string{"events"}, "ev", "Did you mean `pcal events`? Run `pcal --help` for usage."},
		{"two names it starts", []string{"tags", "tokens", "events"}, "t", "Did you mean `pcal tags` or `pcal tokens`? Run `pcal --help` for usage."},
		{"nothing close", []string{"events"}, "frobnicate", "Run `pcal --help` for usage."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			a := &app{streams: Streams{In: strings.NewReader(""), Out: &stdout, Err: &stderr}, getenv: env(nil)}
			root := newRootCmd(a)
			for _, name := range tt.subs {
				root.AddCommand(&cobra.Command{Use: name, RunE: runE})
			}

			code := execute(a, root, []string{tt.arg})

			require.Equal(t, 1, code, "stdout: %s", stdout.String())
			env := decodeError(t, stdout.String())
			require.Equal(t, "usage", env.Error.Code)
			require.Equal(t, fmt.Sprintf("unknown command %q for \"pcal\"", tt.arg), env.Error.Message)
			require.Equal(t, tt.wantHint, env.Error.Hint)
		})
	}
}

// A token typed where a command or a flag's value goes is never repeated:
// not in the message, the hint, or the JSON envelope a CI log keeps. It's in
// the shell's history already; the error shouldn't copy it into logs and
// scrollback too (Gate 3b F6). An ordinary word is still quoted.
func TestTokensTypedAsArgumentsArentRepeated(t *testing.T) {
	const withTokenValue = "--with-token doesn't take a value; it reads the token from stdin"
	tests := []struct {
		args     []string
		wantMsg  string
		wantHint string
	}{
		{[]string{"login", "--with-token=pcal_secret"}, withTokenValue, pipeHint},
		{[]string{"auth", "login", "--with-token=pcal_secret"}, withTokenValue, pipeHint},
		{[]string{"pcal_secret"}, `unknown command for "pcal" (not shown: it looks like a token)`, "Run `pcal --help` for usage."},
		{[]string{"auth", "pcal_secret"}, `unknown command for "pcal auth" (not shown: it looks like a token)`, "Run `pcal auth --help` for usage."},
		{[]string{"whoami", "pcal_secret"}, `unknown command for "pcal whoami" (not shown: it looks like a token)`, "Run `pcal whoami --help` for usage."},
		{[]string{"logout", "pcal_secret"}, `unknown command for "pcal logout" (not shown: it looks like a token)`, "Run `pcal logout --help` for usage."},
		{[]string{"auth", "logout", "pcal_secret"}, `unknown command for "pcal auth logout" (not shown: it looks like a token)`, "Run `pcal auth logout --help` for usage."},
		{[]string{"auth", "status", "pcal_secret"}, `unknown command for "pcal auth status" (not shown: it looks like a token)`, "Run `pcal auth status --help` for usage."},
		{[]string{"whoami", "frobnicate"}, `unknown command "frobnicate" for "pcal whoami"`, "Run `pcal whoami --help` for usage."},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			r := runPcal(t, tt.args...)

			require.Equal(t, 1, r.code, "stdout: %s", r.stdout)
			env := decodeError(t, r.stdout)
			require.Equal(t, "usage", env.Error.Code)
			require.Equal(t, tt.wantMsg, env.Error.Message)
			require.Equal(t, tt.wantHint, env.Error.Hint)
			require.NotContains(t, r.stdout+r.stderr, "pcal_secret")
		})
	}
}

func TestOrList(t *testing.T) {
	require.Equal(t, "a", orList([]string{"a"}))
	require.Equal(t, "a or b", orList([]string{"a", "b"}))
	require.Equal(t, "a, b, or c", orList([]string{"a", "b", "c"}))
}
