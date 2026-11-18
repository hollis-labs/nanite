package main

import (
	"fmt"
	"os"
	"path/filepath"
)

const starterAgent = `---
schema_version: "2"
definition_id: def:local-agent
revision: "1"
name: local-agent
description: Local Nanite agent.
behavior:
  purpose: Help with the current workspace.
requirements: {}
harness_profile:
  permissions:
    profile: default
continuity:
  mode: durable
---
Help with the current workspace.
`

const starterHost = `kind: stable
lifetime: on-demand
seat_id: local-agent
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
