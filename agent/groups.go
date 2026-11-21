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

// BuiltinGroup names a set of Nanite's own tools. See Options.Builtins.
type BuiltinGroup = tools.BuiltinGroup

// The built-in tool groups.
const (
	BuiltinFilesRead  = tools.BuiltinFilesRead  // read_file, list_dir, find_files, search_files
	BuiltinFilesWrite = tools.BuiltinFilesWrite // write_file, edit_file
	BuiltinShell      = tools.BuiltinShell      // bash
	BuiltinBackground = tools.BuiltinBackground // background_start, background_status, background_cancel
	BuiltinSubtasks   = tools.BuiltinSubtasks   // subtask
	BuiltinSkills     = tools.BuiltinSkills     // skill_list, skill_get
	BuiltinNotes      = tools.BuiltinNotes      // todo_list, todo_write, scratchpad_read, scratchpad_write
	BuiltinQuestions  = tools.BuiltinQuestions  // ask_user
	BuiltinTools      = tools.BuiltinTools      // tool packages (tool__*) and tool_validate/tool_install
	BuiltinHistory    = tools.BuiltinHistory    // history_search, history_get (a root conversation's own transcript)
)

// AllBuiltins lists every built-in group, for a host that wants all but a few.
func AllBuiltins() []BuiltinGroup { return tools.AllBuiltins() }
