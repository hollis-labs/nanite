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

func TestParseDefinitionYAML_PreservesGraphForSharedCompiler(t *testing.T) {
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
	wf, err := ParseDefinitionYAML(data)
	if err != nil {
		t.Fatalf("ParseDefinitionYAML: %v", err)
	}
	if len(wf.Steps) != 2 || len(wf.Steps[0].DependsOn) != 1 || wf.Steps[0].DependsOn[0] != "b" {
		t.Fatalf("graph DTO was not preserved: %+v", wf.Steps)
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

// TestParseDefinitionYAML_Engine_Empty proves an unset engine field decodes
// to the shared-host default rather than an accidental compatibility value.
func TestParseDefinitionYAML_Engine_Empty(t *testing.T) {
	data := []byte(`
name: no-engine-field
steps:
  - id: a
    kind: tool
    config:
      tool: noop
`)
	wf, err := ParseDefinitionYAML(data)
	if err != nil {
		t.Fatalf("ParseDefinitionYAML: %v", err)
	}
	if wf.Engine != "" {
		t.Fatalf("Engine = %q, want empty", wf.Engine)
	}
}

// TestParseDefinitionYAML_Engine_External proves an explicit engine: value
// (e.g. "langgraph") round-trips onto WorkflowDefinition.Engine.
func TestParseDefinitionYAML_Engine_External(t *testing.T) {
	data := []byte(`
name: langgraph-workflow
engine: langgraph
steps:
  - id: a
    kind: tool
    config:
      tool: noop
`)
	wf, err := ParseDefinitionYAML(data)
	if err != nil {
		t.Fatalf("ParseDefinitionYAML: %v", err)
	}
	if wf.Engine != EngineLangGraph {
		t.Fatalf("Engine = %q, want %q", wf.Engine, EngineLangGraph)
	}
}

func TestParseDefinitionYAML_EngineCompatibilityValues(t *testing.T) {
	engines := []string{
		"", EngineBuiltin, EngineHadron, EngineLangGraph, EngineCrewAI,
		EngineGoogleADK, EngineAutoGen, EngineLangChain,
	}
	for _, engine := range engines {
		name := engine
		if name == "" {
			name = "default"
		}
		t.Run(name, func(t *testing.T) {
			definition := "name: accepted\n"
			if engine != "" {
				definition += "engine: " + engine + "\n"
			}
			definition += "steps:\n  - id: work\n    kind: tool\n    config:\n      tool: noop\n"
			workflow, err := ParseDefinitionYAML([]byte(definition))
			if err != nil {
				t.Fatalf("ParseDefinitionYAML: %v", err)
			}
			if workflow.Engine != engine {
				t.Fatalf("Engine = %q, want %q", workflow.Engine, engine)
			}
		})
	}
}

func TestParseDefinitionYAML_RejectsUnknownEngine(t *testing.T) {
	_, err := ParseDefinitionYAML([]byte(`
name: unsupported
engine: renamed-local-sequencer
steps:
  - id: work
    kind: tool
    config:
      tool: noop
`))
	if err == nil || !strings.Contains(err.Error(), `unsupported engine "renamed-local-sequencer"`) {
		t.Fatalf("ParseDefinitionYAML error = %v, want unsupported engine", err)
	}
}
