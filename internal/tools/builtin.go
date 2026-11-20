package tools

import (
	"fmt"
	"strings"
)

// BuiltinGroup names a set of Nanite's own tools that a host can withhold
// from an agent.
type BuiltinGroup string

// The built-in tool groups. The tools that page through a cached tool result
// belong to no group and are always offered.
const (
	BuiltinFilesRead  BuiltinGroup = "files-read"  // read_file, list_dir, find_files, search_files
	BuiltinFilesWrite BuiltinGroup = "files-write" // write_file, edit_file
	BuiltinShell      BuiltinGroup = "shell"       // bash
	BuiltinBackground BuiltinGroup = "background"  // background_start, background_status, background_cancel
	BuiltinSubtasks   BuiltinGroup = "subtasks"    // subtask; parent_history_search, parent_history_get (children only)
	BuiltinSkills     BuiltinGroup = "skills"      // skill_list, skill_get
	BuiltinNotes      BuiltinGroup = "notes"       // todo_list, todo_write, scratchpad_read, scratchpad_write
	BuiltinQuestions  BuiltinGroup = "questions"   // ask_user
	// BuiltinTools offers the agent's tool packages (tools/<name>/, as
	// tool__<name> and tool__<name>__<tool>) and tool_validate.
	BuiltinTools BuiltinGroup = "tools"
	// BuiltinHistory offers history_search and history_get to root
	// conversations: their own full transcript, including what compaction
	// summarized.
	BuiltinHistory BuiltinGroup = "history"
)

// ToolPackagePrefix starts the name of every tool a tool package offers.
const ToolPackagePrefix = "tool__"

var builtinGroups = map[string]BuiltinGroup{
	"read_file": BuiltinFilesRead, "list_dir": BuiltinFilesRead, "find_files": BuiltinFilesRead, "search_files": BuiltinFilesRead,
	"write_file": BuiltinFilesWrite, "edit_file": BuiltinFilesWrite,
	"bash":             BuiltinShell,
	"background_start": BuiltinBackground, "background_status": BuiltinBackground, "background_cancel": BuiltinBackground,
	"subtask": BuiltinSubtasks, "parent_history_search": BuiltinSubtasks, "parent_history_get": BuiltinSubtasks,
	"skill_list": BuiltinSkills, "skill_get": BuiltinSkills,
	"todo_list": BuiltinNotes, "todo_write": BuiltinNotes, "scratchpad_read": BuiltinNotes, "scratchpad_write": BuiltinNotes,
	"ask_user":       BuiltinQuestions,
	"tool_validate":  BuiltinTools,
	"history_search": BuiltinHistory, "history_get": BuiltinHistory,
}

// AllBuiltins lists every built-in group.
func AllBuiltins() []BuiltinGroup {
	return []BuiltinGroup{BuiltinFilesRead, BuiltinFilesWrite, BuiltinShell, BuiltinBackground, BuiltinSubtasks, BuiltinSkills, BuiltinNotes, BuiltinQuestions, BuiltinTools, BuiltinHistory}
}

// Builtins is the set of groups an agent is offered. A nil set offers all of
// them; an empty one offers none.
type Builtins map[BuiltinGroup]bool

// NewBuiltins builds the set from a host's list. A nil list means all groups.
func NewBuiltins(groups []BuiltinGroup) (Builtins, error) {
	if groups == nil {
		return nil, nil
	}
	known := make(map[BuiltinGroup]bool)
	for _, group := range AllBuiltins() {
		known[group] = true
	}
	set := make(Builtins, len(groups))
	for _, group := range groups {
		if !known[group] {
			return nil, fmt.Errorf("unknown built-in tool group %q", group)
		}
		set[group] = true
	}
	return set, nil
}

// Has reports whether the group is offered.
func (b Builtins) Has(group BuiltinGroup) bool { return b == nil || b[group] }

// Offers reports whether a built-in tool is offered. Names outside every
// group, such as the cached-result tools and MCP tools, are always offered.
func (b Builtins) Offers(tool string) bool {
	if strings.HasPrefix(tool, ToolPackagePrefix) {
		return b.Has(BuiltinTools)
	}
	group, grouped := builtinGroups[tool]
	return !grouped || b.Has(group)
}
