package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func writeAgentDir(t *testing.T, root, rev, body, work string) {
	t.Helper()
	def := "---\nschema_version: \"2\"\ndefinition_id: def:load-cli\nrevision: \"" + rev + "\"\nname: load-cli\ndescription: Fixture.\nbehavior:\n  purpose: Complete the requested work.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(root, "agent.md"), []byte(def), 0o600); err != nil {
		t.Fatal(err)
	}
	host := "runtime: cli\nprovider: claude\nmodel: haiku\ndirectories: [" + work + "]\n"
	if err := os.WriteFile(filepath.Join(root, "host.yaml"), []byte(host), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Edit the instructions, load again, and the new revision is there for a new
// session while the first host row (and any session on it) is untouched.
func TestAgentLoadPublishesEditsForNewSessions(t *testing.T) {
	ctx := context.Background()
	st, err := store.New(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeStoreBestEffort(ctx, st) })
	root, work := t.TempDir(), t.TempDir()
	writeAgentDir(t, root, "1", "Instructions one.", work)

	run := func(args ...string) map[string]any {
		t.Helper()
		var out bytes.Buffer
		if err := runAgentLoad(ctx, st, args, &out); err != nil {
			t.Fatalf("load %v: %v", args, err)
		}
		var m map[string]any
		if err := json.Unmarshal(out.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	first := run(root)
	writeAgentDir(t, root, "2", "Instructions two.", work)
	second := run(root)
	if first["host_settings_id"] == second["host_settings_id"] || second["changed"] != true {
		t.Fatalf("first=%v second=%v", first, second)
	}
	if !strings.Contains(second["diff"].([]any)[0].(string), "revision 1 -> 2") {
		t.Fatalf("diff = %v", second["diff"])
	}
	if dry := run(root, "--dry-run"); dry["changed"] != false {
		t.Fatalf("dry run of an already loaded state = %v", dry)
	}
	body := second["session_request"].(map[string]any)["body"].(map[string]any)
	if body["definition_ref"].(map[string]any)["revision"] != "2" {
		t.Fatalf("the session body does not name the new revision: %v", body)
	}
}

func TestAgentLoadWithoutADirectoryExplains(t *testing.T) {
	ctx := context.Background()
	st, err := store.New(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeStoreBestEffort(ctx, st) })
	if err := runAgentLoad(ctx, st, []string{t.TempDir()}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "agent.md") {
		t.Fatalf("err = %v", err)
	}
}
