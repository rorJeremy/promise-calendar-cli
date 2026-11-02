package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/rorJeremy/promise-calendar-cli/internal/output"
	"github.com/rorJeremy/promise-calendar-cli/skills"
)

var errSkillCanceled = &output.Error{
	Code:    output.CodeInterrupted,
	Message: "Canceled. The skill wasn't changed.",
}

var errSkillTerminated = &output.Error{
	Code:    output.CodeTerminated,
	Message: "Canceled. The skill wasn't changed.",
}

// skillInstall is the result of `skill install`. Installed is false when
// the person answered No and the file already there was left as it was.
type skillInstall struct {
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
}

func newSkillCmd(a *app) *cobra.Command {
	cmd := newGroupCmd("skill", "Install the agent skill")
	cmd.AddCommand(newSkillInstallCmd(a))
	return cmd
}

func newSkillInstallCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install the pcal skill for Claude Code",
		Long: "Write the embedded skill to ~/.claude/skills/pcal/SKILL.md.\n" +
			"If that file is already there, pcal asks before replacing it,\n" +
			"unless --yes is set. The question opens on No.",
		Args: unknownCommand,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return &output.Error{Code: output.CodeUsage, Message: err.Error()}
			}
			path := filepath.Join(home, ".claude", "skills", "pcal", "SKILL.md")
			_, statErr := os.Stat(path)
			exists := statErr == nil
			if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
				return &output.Error{Code: output.CodeAPI, Message: statErr.Error()}
			}
			if exists && !a.flags.yes {
				if err := a.requirePrompt("overwriting the skill"); err != nil {
					return err
				}
				ok, err := a.confirmNo(cmd, fmt.Sprintf("Overwrite the pcal skill at %s?", path), errSkillCanceled, errSkillTerminated)
				if err != nil {
					return err
				}
				if !ok {
					return a.printer.Print(output.Result{
						Data:    skillInstall{Installed: false},
						Summary: "The skill wasn't changed.",
					})
				}
			}
			if err := writeSkill(path); err != nil {
				return err
			}
			return a.printer.Print(output.Result{
				Data:    skillInstall{Installed: true, Path: path},
				Summary: "Installed the pcal skill.",
			})
		},
	}
}

func writeSkill(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // G301: a skill directory is not a secret; the person's tools have to be able to read it
		return &output.Error{Code: output.CodeAPI, Message: err.Error()}
	}
	if err := os.WriteFile(path, skills.Skill, 0o644); err != nil { //nolint:gosec // G306: a skill file is not a secret
		return &output.Error{Code: output.CodeAPI, Message: err.Error()}
	}
	return nil
}
