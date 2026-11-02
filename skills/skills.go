// Package skills is the agent skill embedded in the pcal binary.
package skills

import _ "embed"

// Skill is skills/pcal/SKILL.md, written by `pcal skill install`.
//
//go:embed pcal/SKILL.md
var Skill []byte
