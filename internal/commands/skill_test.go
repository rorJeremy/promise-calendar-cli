package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSkillInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".claude", "skills", "pcal", "SKILL.md")

	first := runPcal(t, "skill", "install")
	require.Equal(t, 0, first.code, first.stdout)
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	text := string(body)
	require.Contains(t, text, "--json")
	require.Contains(t, text, "exit")
	require.Contains(t, text, "title")
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())

	again := runPcal(t, "skill", "install")
	require.Equal(t, 1, again.code)
	require.Contains(t, decodeError(t, again.stdout).Error.Message, "--yes wasn't set")
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, text, string(unchanged))

	yes := runPcal(t, "--yes", "skill", "install")
	require.Equal(t, 0, yes.code, yes.stdout)
	rewritten, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, text, string(rewritten))
}

func TestSkillInstallConfirm(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".claude", "skills", "pcal", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("old skill\n"), 0o644))

	h := newHarness(t, "http://127.0.0.1")
	h.inTTY = true
	h.errTTY = true
	h.env["TERM"] = "dumb"
	h.env["NO_COLOR"] = "1"
	h.stdin = strings.NewReader("n\n")

	no := h.run("skill", "install")
	require.Equal(t, 0, no.code, no.stderr)
	require.Contains(t, no.stdout, `"installed": false`)
	kept, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "old skill\n", string(kept))

	h.stdin = strings.NewReader("\x1b")
	h.env["ACCESSIBLE"] = "1"
	esc := h.run("skill", "install")
	require.Equal(t, 130, esc.code, esc.stderr)
	require.Empty(t, esc.stdout)
	require.Contains(t, esc.stderr, "Canceled. The skill wasn't changed.")
	kept, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "old skill\n", string(kept))
}
