package workflowhost

import (
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
)

func TestCompileWorkflowDefinitionRoutesCompatibleEngines(t *testing.T) {
	registry, err := newFrozenRegistry(&recordingStepExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	native := []string{"", agentworkflow.EngineBuiltin, agentworkflow.EngineHadron}
	for _, engine := range native {
		name := engine
		if name == "" {
			name = "default"
		}
		t.Run("native/"+name, func(t *testing.T) {
			compiled, compileErr := compileWorkflowDefinition(t.Context(), compatibilityDefinition(engine), registry)
			if compileErr != nil {
				t.Fatalf("compileWorkflowDefinition: %v", compileErr)
			}
			if len(compiled.material.Plan.Graph.Nodes) != 1 || compiled.material.Plan.Graph.Nodes[0].Kind != StepKindTool {
				t.Fatalf("native graph nodes = %+v", compiled.material.Plan.Graph.Nodes)
			}
		})
	}

	external := []string{
		agentworkflow.EngineLangGraph, agentworkflow.EngineCrewAI,
		agentworkflow.EngineGoogleADK, agentworkflow.EngineAutoGen,
		agentworkflow.EngineLangChain,
	}
	for _, engine := range external {
		t.Run("external/"+engine, func(t *testing.T) {
			compiled, compileErr := compileWorkflowDefinition(t.Context(), compatibilityDefinition(engine), registry)
			if compileErr != nil {
				t.Fatalf("compileWorkflowDefinition: %v", compileErr)
			}
			nodes := compiled.material.Plan.Graph.Nodes
			if len(nodes) != 1 || nodes[0].Kind != StepKindExternalEngine || nodes[0].Config["external_engine"] != engine {
				t.Fatalf("external graph nodes = %+v", nodes)
			}
		})
	}
}

func TestCompileWorkflowDefinitionRejectsUnknownEngine(t *testing.T) {
	registry, err := newFrozenRegistry(&recordingStepExecutor{})
	if err != nil {
		t.Fatal(err)
	}
	definition := compatibilityDefinition("renamed-local-sequencer")
	_, err = compileWorkflowDefinition(t.Context(), definition, registry)
	if err == nil || !strings.Contains(err.Error(), `unsupported engine "renamed-local-sequencer"`) {
		t.Fatalf("compileWorkflowDefinition error = %v, want unsupported engine", err)
	}
}

func compatibilityDefinition(engine string) agentworkflow.WorkflowDefinition {
	return agentworkflow.WorkflowDefinition{
		Name:   "engine compatibility",
		Engine: engine,
		Steps: []agentworkflow.StepDefinition{{
			ID: "work", Kind: agentworkflow.StepKindTool,
			Config: map[string]any{"tool": "noop"},
		}},
	}
}
