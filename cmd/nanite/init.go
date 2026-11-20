package main

import (
	"fmt"
	"os"
	"path/filepath"
)

const starterAgent = `---
schema_version: "2"
definition_id: def:assistant
revision: "1"
name: assistant
description: General-purpose assistant.
behavior:
  purpose: Help the person with whatever they ask, from questions and writing to work on files in this directory.
requirements: {}
harness_profile:
  permissions:
    profile: default
continuity:
  mode: durable
---
You are a general-purpose assistant running in a terminal, in the directory
this file lives in.

Help with whatever the person asks: answering questions, explaining, writing,
planning, and working on the files and code here. Read a file before
describing it and run a command before reporting what it does.

Be specific: name the file, the line, the command. Prefer small changes the
person can review over sweeping ones.
`

const starterHost = `kind: stable
lifetime: on-demand
seat_id: assistant
runtime:
  provider: anthropic
permissions:
  mode: default
`

// starterVariant is an authored subtask variant, written as a commented
// example of the format; nanite validate accepts it.
const starterVariant = `---
# An authored subtask variant. The parent agent starts it by name:
#   subtask({goal: "...", return_spec: "...", variant: "reviewer"})
# Every field is optional. A variant only narrows the tools this agent
# already has; it never adds one, and every call still passes the
# agent's permissions.
description: Reviews a change or a file and returns findings; changes nothing.
read_only: true          # offer only read-only tools
tools:
  deny: ["mcp__*"]       # an MCP server's read-only hint is its own claim; allow MCP tools by name if you trust them
# provider: anthropic    # run children on another provider and model
# model: claude-sonnet-5-5
budgets:
  steps: 20              # model steps; never more than host.yaml allows
  wall: 10m              # stop after ten minutes
last_n: 4                # parent messages copied into the child: 1-50; 0 or unset means 6
# result_cap: 3000       # tokens; a larger result is stored and returned as a pointer
# can_spawn: true        # let this variant start subtasks one level down (not with read_only)
---
You are a reviewer subtask. Read the work named in the goal and check it
for mistakes: wrong behavior, missing cases, unclear wording, and anything
that contradicts the goal or the inputs.

Return each finding with where it is (a file and line, or a quote) and
why it matters. If you found nothing, say so plainly; do not invent
findings to fill the answer.
`

func initAgent(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil { // #nosec G703 -- The operator explicitly selects where to initialize the agent.
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }() // Closed after all files have been committed.
	files := []struct{ name, data string }{{"agent.md", starterAgent}, {"host.yaml", starterHost}, {".mcp.json", "{\"mcpServers\":{}}\n"}, {".gitignore", ".nanite/\n"}, {"variants/reviewer.md", starterVariant}}
	for _, entry := range files {
		if _, err := root.Lstat(entry.name); err == nil {
			return fmt.Errorf("%s already exists", filepath.Join(dir, entry.name))
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	for _, sub := range []string{"skills", "variants"} {
		if err := root.MkdirAll(sub, 0700); err != nil {
			return err
		}
	}
	var created []string
	for _, entry := range files {
		f, err := root.OpenFile(entry.name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			created = append(created, entry.name)
			_, err = f.WriteString(entry.data)
			if closeErr := f.Close(); err == nil {
				err = closeErr
			}
		}
		if err != nil {
			for _, name := range created {
				_ = root.Remove(name)
			}
			return err
		}
	}
	return nil
}
