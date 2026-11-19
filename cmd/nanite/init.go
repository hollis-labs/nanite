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

func initAgent(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil { // #nosec G703 -- The operator explicitly selects where to initialize the agent.
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }() // Closed after all files have been committed.
	files := []struct{ name, data string }{{"agent.md", starterAgent}, {"host.yaml", starterHost}, {".mcp.json", "{\"mcpServers\":{}}\n"}, {".gitignore", ".nanite/\n"}}
	for _, entry := range files {
		if _, err := root.Lstat(entry.name); err == nil {
			return fmt.Errorf("%s already exists", filepath.Join(dir, entry.name))
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if err := root.MkdirAll("skills", 0700); err != nil {
		return err
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
