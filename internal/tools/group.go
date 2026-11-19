package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
)

type Call struct {
	ID, Name string
	Input    json.RawMessage
}

type Result struct {
	Text    string
	IsError bool
}

type Tool struct {
	Name, Description     string
	InputSchema           json.RawMessage
	ReadOnly, Destructive bool
	Handler               func(context.Context, Call) (Result, error)
}

type Groups struct {
	mu     sync.RWMutex
	groups map[string][]Tool
}

func (g *Groups) SetGroup(group string, tools ...Tool) error {
	if strings.TrimSpace(group) == "" {
		return fmt.Errorf("tool group name required")
	}
	next := slices.Clone(tools)
	names := make(map[string]bool, len(next))
	for i, tool := range next {
		if strings.TrimSpace(tool.Name) == "" || tool.Handler == nil {
			return fmt.Errorf("tool name and handler required")
		}
		var schema map[string]json.RawMessage
		if err := json.Unmarshal(tool.InputSchema, &schema); err != nil || schema == nil {
			return fmt.Errorf("tool %q requires a JSON Schema object", tool.Name)
		}
		if builtin(tool.Name) || names[tool.Name] {
			return fmt.Errorf("tool name %q is reserved or duplicated", tool.Name)
		}
		names[tool.Name] = true
		next[i].InputSchema = slices.Clone(tool.InputSchema)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	for other, existing := range g.groups {
		if other == group {
			continue
		}
		for _, tool := range existing {
			if names[tool.Name] {
				return fmt.Errorf("tool %q already belongs to group %q", tool.Name, other)
			}
		}
	}
	if g.groups == nil {
		g.groups = make(map[string][]Tool)
	}
	g.groups[group] = next
	return nil
}

func (g *Groups) RemoveGroup(group string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.groups, group)
}

func (g *Groups) Snapshot() []Tool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []Tool
	for _, group := range slices.Sorted(maps.Keys(g.groups)) {
		for _, tool := range g.groups[group] {
			tool.InputSchema = slices.Clone(tool.InputSchema)
			out = append(out, tool)
		}
	}
	return out
}

func builtin(name string) bool {
	switch name {
	case "todo_write", "todo_list", "scratchpad_write", "scratchpad_read",
		"skill_list", "skill_get", "subagent_spawn", "subagent_status", "subagent_cancel",
		"background_start", "background_status", "background_cancel", "fetch_tool_result",
		"search_tool_result", "ask_user", "read_file", "write_file", "edit_file", "list_dir", "find_files", "search_files", "bash":
		return true
	default:
		return strings.HasPrefix(name, "mcp__")
	}
}
