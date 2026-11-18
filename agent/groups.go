package agent

import (
	"github.com/hollis-labs/nanite/internal/skills"
	"github.com/hollis-labs/nanite/internal/tools"
)

// Tool describes a model-visible operation. InputSchema is a JSON Schema
// object. Handler receives approved calls. ReadOnly and Destructive inform policy.
type Tool = tools.Tool

// ToolCall contains the provider's call ID, tool name, and approved JSON input.
type ToolCall = tools.Call

// ToolResult carries the text returned to the model and whether the tool
// reports failure. A handler may also return a Go error.
type ToolResult = tools.Result

// Skill contains a name and description for the skills slot and instructions
// that are returned on demand by the skill_get tool.
type Skill = skills.Skill

// ToolSet holds named groups of host tools.
type ToolSet struct{ groups tools.Groups }

// SetGroup validates and atomically replaces a tool group.
func (t *ToolSet) SetGroup(group string, specs ...Tool) error {
	return t.groups.SetGroup(group, specs...)
}

// RemoveGroup removes a tool group.
func (t *ToolSet) RemoveGroup(group string) { t.groups.RemoveGroup(group) }

// SkillSet holds named groups of host skills.
type SkillSet struct{ groups skills.Groups }

// SetGroup validates and atomically replaces a skill group.
func (s *SkillSet) SetGroup(group string, specs ...Skill) error {
	return s.groups.SetGroup(group, specs...)
}

// RemoveGroup removes a skill group.
func (s *SkillSet) RemoveGroup(group string) { s.groups.RemoveGroup(group) }
