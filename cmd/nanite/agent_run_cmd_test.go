package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
)

// Run from inside the project: the agent directory is found from the current
// directory, the session names the loaded revision and host row, and the work
// root is the current directory. Editing the directory and running again
// names the new revision.
func TestLoadForRunUsesCurrentDirectoryAsWorkRoot(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	t.Setenv("NANITE_DB", filepath.Join(t.TempDir(), "main.db"))
	agent := filepath.Join(work, ".nanite")
	if err := os.Mkdir(agent, 0o700); err != nil {
		t.Fatal(err)
	}
	writeAgentDir(t, agent, "1", "Instructions one.", work)
	t.Chdir(work)
	want, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	first, err := loadForRun(ctx, "", service.AgentLoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if first.WorkRoot != want || first.HostSettings == nil || first.DefinitionRef.Revision != "1" {
		t.Fatalf("first = %+v", first)
	}
	writeAgentDir(t, agent, "2", "Instructions two.", work)
	second, err := loadForRun(ctx, "", service.AgentLoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if second.DefinitionRef.Revision != "2" || second.HostSettings.ID == first.HostSettings.ID {
		t.Fatalf("an edit did not reach the next run: %+v", second)
	}
}
