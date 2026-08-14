package agentworkflow

import (
	"strings"
	"testing"
)

func TestParseDefinitionYAML_Valid(t *testing.T) {
	data := []byte(`
name: fetch-and-summarize
steps:
  - id: fetch
    kind: tool
    config:
      tool: torque_task_get
      agent_id: agent-1
      args:
        id: "{{ input.task_id }}"
    verify:
      mode: engine
      engine_check: no_error

  - id: summarize
    kind: llm
    depends_on: [fetch]
    config:
      provider: anthropic
      model: claude-sonnet-5
      prompt: "Summarize: {{ steps.fetch.output }}"
      agent_id: agent-1
      tools: [some_tool]
    verify:
      mode: agent
      reviewer_provider: anthropic
      reviewer_model: claude-sonnet-5

  - id: approve
    kind: gate
    depends_on: [summarize]
    config:
      description: "Approve before publishing"
`)

	wf, err := ParseDefinitionYAML(data)
	if err != nil {
		t.Fatalf("ParseDefinitionYAML: %v", err)
	}
	if wf.Name != "fetch-and-summarize" {
		t.Fatalf("Name = %q", wf.Name)
	}
	if len(wf.Steps) != 3 {
		t.Fatalf("len(Steps) = %d, want 3", len(wf.Steps))
	}

	fetch := wf.Steps[0]
	if fetch.Kind != StepKindTool {
		t.Fatalf("fetch.Kind = %q", fetch.Kind)
	}
	if fetch.Config["tool"] != "torque_task_get" {
		t.Fatalf("fetch.Config[tool] = %v", fetch.Config["tool"])
	}
	if fetch.Verify == nil || fetch.Verify.Mode != VerifyModeEngine || fetch.Verify.EngineCheck != "no_error" {
		t.Fatalf("fetch.Verify = %+v", fetch.Verify)
	}

	summarize := wf.Steps[1]
	if summarize.Kind != StepKindLLM {
		t.Fatalf("summarize.Kind = %q", summarize.Kind)
	}
	if len(summarize.DependsOn) != 1 || summarize.DependsOn[0] != "fetch" {
		t.Fatalf("summarize.DependsOn = %v", summarize.DependsOn)
	}
	if summarize.Verify == nil || summarize.Verify.Mode != VerifyModeAgent || summarize.Verify.ReviewerProvider != "anthropic" {
		t.Fatalf("summarize.Verify = %+v", summarize.Verify)
	}

	approve := wf.Steps[2]
	if approve.Kind != StepKindGate {
		t.Fatalf("approve.Kind = %q", approve.Kind)
	}
}

func TestParseDefinitionYAML_RejectsCycle(t *testing.T) {
	data := []byte(`
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
	_, err := ParseDefinitionYAML(data)
	if err == nil {
		t.Fatal("expected cycle rejection, got nil error")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("error = %v, want cycle mention", err)
	}
}

func TestParseDefinitionYAML_RejectsUnknownDependency(t *testing.T) {
	data := []byte(`
name: dangling
steps:
  - id: a
    kind: tool
    depends_on: [nonexistent]
    config:
      tool: noop
`)
	_, err := ParseDefinitionYAML(data)
	if err == nil {
		t.Fatal("expected unknown-dependency rejection, got nil error")
	}
	if !strings.Contains(err.Error(), "unknown step") {
		t.Fatalf("error = %v, want unknown-step mention", err)
	}
}

func TestParseDefinitionYAML_RejectsUnknownKind(t *testing.T) {
	data := []byte(`
name: bad-kind
steps:
  - id: a
    kind: mystery
    config: {}
`)
	_, err := ParseDefinitionYAML(data)
	if err == nil {
		t.Fatal("expected unknown-kind rejection, got nil error")
	}
	if !strings.Contains(err.Error(), "unknown kind") {
		t.Fatalf("error = %v, want unknown-kind mention", err)
	}
}

func TestParseDefinitionYAML_RejectsMalformedVerify(t *testing.T) {
	data := []byte(`
name: bad-verify
steps:
  - id: a
    kind: tool
    config:
      tool: noop
    verify:
      mode: engine
`)
	_, err := ParseDefinitionYAML(data)
	if err == nil {
		t.Fatal("expected malformed verify rejection, got nil error")
	}
	if !strings.Contains(err.Error(), "engine_check") {
		t.Fatalf("error = %v, want engine_check mention", err)
	}
}

func TestParseDefinitionYAML_RejectsDuplicateStepID(t *testing.T) {
	data := []byte(`
name: dup
steps:
  - id: a
    kind: tool
    config:
      tool: noop
  - id: a
    kind: tool
    config:
      tool: noop
`)
	_, err := ParseDefinitionYAML(data)
	if err == nil {
		t.Fatal("expected duplicate-id rejection, got nil error")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("error = %v, want duplicate mention", err)
	}
}
