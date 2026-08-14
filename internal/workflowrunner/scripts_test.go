package workflowrunner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializeScripts_WritesEmbeddedFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "scripts-out")

	paths, err := MaterializeScripts(dir)
	if err != nil {
		t.Fatalf("MaterializeScripts: %v", err)
	}

	for _, name := range []string{
		"langgraph_runner.py",
		"crewai_runner.py",
		"google_adk_runner.py",
		"autogen_runner.py",
		"langchain_runner.py",
		"requirements.txt",
	} {
		path, ok := paths[name]
		if !ok {
			t.Fatalf("paths missing %q: %+v", name, paths)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read materialized %s: %v", name, err)
		}
		if len(data) == 0 {
			t.Errorf("%s materialized empty", name)
		}
	}
}

// TestMaterializeScripts_OverwritesStaleCopy proves a prior run's on-disk
// copy is replaced, not preserved — the embedded bytes are always this
// binary's source of truth (an upgraded binary must not keep serving a
// stale script from a previous version).
func TestMaterializeScripts_OverwritesStaleCopy(t *testing.T) {
	dir := t.TempDir()
	stalePath := filepath.Join(dir, "langgraph_runner.py")
	if err := os.WriteFile(stalePath, []byte("stale content from a prior version"), 0o644); err != nil {
		t.Fatalf("seed stale file: %v", err)
	}

	paths, err := MaterializeScripts(dir)
	if err != nil {
		t.Fatalf("MaterializeScripts: %v", err)
	}
	data, err := os.ReadFile(paths["langgraph_runner.py"])
	if err != nil {
		t.Fatalf("read materialized file: %v", err)
	}
	if string(data) == "stale content from a prior version" {
		t.Fatal("stale on-disk copy was not overwritten")
	}
}

func TestMaterializeScripts_RequiresDir(t *testing.T) {
	if _, err := MaterializeScripts(""); err == nil {
		t.Fatal("expected an error for an empty dir")
	}
}
