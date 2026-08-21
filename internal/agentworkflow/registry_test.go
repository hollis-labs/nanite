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

// TestRegistry_Register_MakesDefinitionReachableViaGet is
// TASKS/teams/08-team-run-launcher.md's own regression coverage for the
// Register method it added: a dynamically-built WorkflowDefinition (no
// YAML file on disk at all) becomes reachable via Get once registered,
// the same way a LoadRegistryDir-loaded definition already is.
func TestRegistry_Register_MakesDefinitionReachableViaGet(t *testing.T) {
	reg := NewRegistry(nil)
	wf := WorkflowDefinition{
		Name: "dynamic-team-run",
		Steps: []StepDefinition{
			{ID: "only", Kind: StepKindTool, Config: map[string]any{"tool": "noop"}},
		},
	}
	if err := reg.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := reg.Get("dynamic-team-run")
	if !ok {
		t.Fatal("Get(dynamic-team-run) ok = false after Register")
	}
	if got.Name != "dynamic-team-run" || len(got.Steps) != 1 {
		t.Fatalf("got = %+v", got)
	}
	names := reg.Names()
	if len(names) != 1 || names[0] != "dynamic-team-run" {
		t.Fatalf("Names() = %v", names)
	}
}

func TestRegistry_Register_RejectsEmptyName(t *testing.T) {
	reg := NewRegistry(nil)
	err := reg.Register(WorkflowDefinition{Steps: []StepDefinition{{ID: "only", Kind: StepKindTool, Config: map[string]any{"tool": "noop"}}}})
	if err == nil || !strings.Contains(err.Error(), "no name") {
		t.Fatalf("err = %v, want \"no name\"", err)
	}
}

func TestRegistry_Register_RejectsInvalidDefinition(t *testing.T) {
	reg := NewRegistry(nil)
	wf := WorkflowDefinition{
		Name: "cyclic",
		Steps: []StepDefinition{
			{ID: "a", Kind: StepKindTool, DependsOn: []string{"b"}, Config: map[string]any{"tool": "noop"}},
			{ID: "b", Kind: StepKindTool, DependsOn: []string{"a"}, Config: map[string]any{"tool": "noop"}},
		},
	}
	if err := reg.Register(wf); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("err = %v, want cycle rejection", err)
	}
	if _, ok := reg.Get("cyclic"); ok {
		t.Fatal("Get(cyclic) ok = true, want the invalid definition to never be registered")
	}
}

func TestRegistry_Register_RejectsDuplicateName(t *testing.T) {
	reg := NewRegistry(nil)
	wf := WorkflowDefinition{
		Name:  "dup",
		Steps: []StepDefinition{{ID: "only", Kind: StepKindTool, Config: map[string]any{"tool": "noop"}}},
	}
	if err := reg.Register(wf); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := reg.Register(wf); err == nil || !strings.Contains(err.Error(), "duplicate workflow name") {
		t.Fatalf("second Register err = %v, want duplicate workflow name", err)
	}
}

// TestRegistry_Register_ConcurrentWithGet exercises Register's own doc
// comment claim directly: concurrent Register calls (simulating more than
// one TeamRun launching at once against the shared *Registry
// cmd/nanite/main.go constructs once) never race a concurrent Get/Names
// reader. Run with -race to actually catch a regression here.
func TestRegistry_Register_ConcurrentWithGet(t *testing.T) {
	reg := NewRegistry(nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			reg.Get("whatever")
			reg.Names()
		}
	}()
	for i := 0; i < 50; i++ {
		wf := WorkflowDefinition{
			Name:  concurrentRegisterTestName(i),
			Steps: []StepDefinition{{ID: "only", Kind: StepKindTool, Config: map[string]any{"tool": "noop"}}},
		}
		if err := reg.Register(wf); err != nil {
			t.Fatalf("Register(%d): %v", i, err)
		}
	}
	<-done
	if len(reg.Names()) != 50 {
		t.Fatalf("Names() len = %d, want 50", len(reg.Names()))
	}
}

func concurrentRegisterTestName(i int) string {
	return "concurrent-register-" + string(rune('a'+i%26)) + string(rune('0'+i/26))
}

// TestRegistry_Unregister_RemovesEntry is TASKS/teams/11-team-run-launch-
// api.md's required registry-growth regression coverage: a registered
// definition is reachable via Get/Names before Unregister, and gone after.
func TestRegistry_Unregister_RemovesEntry(t *testing.T) {
	reg := NewRegistry(nil)
	wf := WorkflowDefinition{
		Name:  "team-run:Feature Development:01ABC",
		Steps: []StepDefinition{{ID: "only", Kind: StepKindTool, Config: map[string]any{"tool": "noop"}}},
	}
	if err := reg.Register(wf); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, ok := reg.Get(wf.Name); !ok {
		t.Fatal("Get before Unregister: ok = false, want true")
	}

	reg.Unregister(wf.Name)

	if _, ok := reg.Get(wf.Name); ok {
		t.Fatal("Get after Unregister: ok = true, want false")
	}
	if len(reg.Names()) != 0 {
		t.Fatalf("Names() after Unregister = %v, want empty", reg.Names())
	}
}

// TestRegistry_Unregister_UnknownNameIsNoop confirms Unregister never
// errors/panics for a name that was never registered -- a caller (e.g. a
// racing double-unregister) shouldn't need to guard against this.
func TestRegistry_Unregister_UnknownNameIsNoop(t *testing.T) {
	reg := NewRegistry(nil)
	reg.Unregister("does-not-exist")
	var nilReg *Registry
	nilReg.Unregister("also-fine-on-a-nil-registry")
}

// TestIsTeamRunDefinitionName exercises the naming-convention filter
// AgentCardGenerator.Generate relies on to exclude per-launch compiled
// TeamRun definitions from the public A2A skill-discovery response
// (internal/service/a2a_agent_card_test.go covers that consumer directly;
// this test covers the predicate itself, including the exact literal shape
// team_run_launcher.go produces: "team-run:<team-name>:<ulid>").
func TestIsTeamRunDefinitionName(t *testing.T) {
	cases := map[string]bool{
		"team-run:Feature Development:01K5ZQ2VXH8P8": true,
		"team-run:":                         true,
		"workflow-a":                        false,
		"team-run":                          false, // no trailing ":" -- not this prefix
		"my-team-run:not-actually-prefixed": false,
		"":                                  false,
	}
	for name, want := range cases {
		if got := IsTeamRunDefinitionName(name); got != want {
			t.Errorf("IsTeamRunDefinitionName(%q) = %v, want %v", name, got, want)
		}
	}
}
