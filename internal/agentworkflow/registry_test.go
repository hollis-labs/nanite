package agentworkflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeWorkflowFile(t *testing.T, dir, filename, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
}

func TestLoadRegistryDir_EmptyPath_ReturnsInertRegistry(t *testing.T) {
	reg, err := LoadRegistryDir("")
	if err != nil {
		t.Fatalf("LoadRegistryDir(\"\") err = %v, want nil", err)
	}
	if len(reg.Names()) != 0 {
		t.Fatalf("Names() = %v, want empty", reg.Names())
	}
	if _, ok := reg.Get("anything"); ok {
		t.Fatal("Get on empty registry returned ok=true")
	}
}

func TestLoadRegistryDir_MissingDir_ReturnsInertRegistry(t *testing.T) {
	reg, err := LoadRegistryDir(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("LoadRegistryDir(missing) err = %v, want nil (configured-but-missing degrades to empty)", err)
	}
	if len(reg.Names()) != 0 {
		t.Fatalf("Names() = %v, want empty", reg.Names())
	}
}

func TestLoadRegistryDir_LoadsAndIndexesByName(t *testing.T) {
	dir := t.TempDir()
	writeWorkflowFile(t, dir, "a.yaml", `
name: workflow-a
steps:
  - id: only
    kind: tool
    config:
      tool: noop
`)
	writeWorkflowFile(t, dir, "b.yml", `
name: workflow-b
steps:
  - id: only
    kind: tool
    config:
      tool: noop
`)
	// Non-yaml files must be ignored, not error.
	writeWorkflowFile(t, dir, "README.md", "not a workflow")

	reg, err := LoadRegistryDir(dir)
	if err != nil {
		t.Fatalf("LoadRegistryDir: %v", err)
	}

	names := reg.Names()
	if len(names) != 2 || names[0] != "workflow-a" || names[1] != "workflow-b" {
		t.Fatalf("Names() = %v, want [workflow-a workflow-b]", names)
	}

	wf, ok := reg.Get("workflow-a")
	if !ok {
		t.Fatal("Get(workflow-a) ok = false")
	}
	if wf.Name != "workflow-a" || len(wf.Steps) != 1 {
		t.Fatalf("wf = %+v", wf)
	}

	if _, ok := reg.Get("workflow-c"); ok {
		t.Fatal("Get(workflow-c) ok = true, want false")
	}
}

func TestLoadRegistryDir_RejectsDuplicateName(t *testing.T) {
	dir := t.TempDir()
	writeWorkflowFile(t, dir, "a.yaml", `
name: dup
steps:
  - id: only
    kind: tool
    config:
      tool: noop
`)
	writeWorkflowFile(t, dir, "b.yaml", `
name: dup
steps:
  - id: only
    kind: tool
    config:
      tool: noop
`)

	_, err := LoadRegistryDir(dir)
	if err == nil || !strings.Contains(err.Error(), "duplicate workflow name") {
		t.Fatalf("err = %v, want duplicate workflow name", err)
	}
}

func TestLoadRegistryDir_PropagatesInvalidDefinition(t *testing.T) {
	dir := t.TempDir()
	writeWorkflowFile(t, dir, "cyclic.yaml", `
name: cyclic
steps:
  - id: a
    kind: tool
    depends_on: [b]
    config:
      tool: noop
  - id: b
    kind: tool
    depends_on: [a]
    config:
      tool: noop
`)

	_, err := LoadRegistryDir(dir)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("err = %v, want cycle rejection to propagate", err)
	}
}
