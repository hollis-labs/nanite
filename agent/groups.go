package agent

import (
	"github.com/hollis-labs/nanite/internal/skills"
	"github.com/hollis-labs/nanite/internal/tools"
)

// Tool is a host tool spec and handler.
type Tool = tools.Tool

// ToolCall is a model's request to execute a tool.
type ToolCall = tools.Call

// ToolResult is a tool handler's response.
type ToolResult = tools.Result

// Skill contains a host skill's listing metadata and body.
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
