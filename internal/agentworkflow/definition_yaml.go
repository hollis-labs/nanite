package agentworkflow

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// YAML is the authoring format for a WorkflowDefinition — consistent with
// how other config-shaped things in this codebase are declared
// (internal/config and internal/plugin's catalog/plugin.yaml). Decodes directly into the
// existing agentworkflow.WorkflowDefinition/StepDefinition product DTOs rather
// than an intermediate model. The shared host translates these DTOs into Graph
// IR and is the sole graph-validation authority.

type yamlWorkflowDefinition struct {
	Name   string     `yaml:"name"`
	Engine string     `yaml:"engine"`
	Steps  []yamlStep `yaml:"steps"`
}

type yamlStep struct {
	ID        string         `yaml:"id"`
	Kind      string         `yaml:"kind"`
	DependsOn []string       `yaml:"depends_on"`
	Config    map[string]any `yaml:"config"`
	Verify    *yamlVerify    `yaml:"verify"`
}

type yamlVerify struct {
	Mode         string         `yaml:"mode"`
	EngineCheck  string         `yaml:"engine_check"`
	EngineParams map[string]any `yaml:"engine_params"`

	ReviewerPrompt    string   `yaml:"reviewer_prompt"`
	ReviewerAgentID   string   `yaml:"reviewer_agent_id"`
	ReviewerProvider  string   `yaml:"reviewer_provider"`
	ReviewerModel     string   `yaml:"reviewer_model"`
	ReviewerTools     []string `yaml:"reviewer_tools"`
	ReviewerSessionID string   `yaml:"reviewer_session_id"`
}

// LoadDefinitionYAMLFile reads and parses a YAML-authored workflow
// definition file. See ParseDefinitionYAML.
func LoadDefinitionYAMLFile(path string) (WorkflowDefinition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return WorkflowDefinition{}, fmt.Errorf("agentworkflow: read %s: %w", path, err)
	}
	wf, err := ParseDefinitionYAML(data)
	if err != nil {
		return WorkflowDefinition{}, fmt.Errorf("agentworkflow: %s: %w", path, err)
	}
	return wf, nil
}

// ParseDefinitionYAML parses YAML bytes into a WorkflowDefinition and validates
// Nanite's product fields. Dependency and cycle diagnostics are produced by
// the shared go-workflow compiler when the definition is published/launched;
// this package contains no parallel DAG validator.
func ParseDefinitionYAML(data []byte) (WorkflowDefinition, error) {
	var raw yamlWorkflowDefinition
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return WorkflowDefinition{}, fmt.Errorf("agentworkflow: parse yaml: %w", err)
	}

	wf := WorkflowDefinition{Name: raw.Name, Engine: raw.Engine}
	for _, ys := range raw.Steps {
		step := StepDefinition{
			ID:        ys.ID,
			Kind:      StepKind(ys.Kind),
			DependsOn: ys.DependsOn,
			Config:    ys.Config,
		}
		if ys.Verify != nil {
			step.Verify = &VerifySpec{
				Mode:              VerifyMode(ys.Verify.Mode),
				EngineCheck:       ys.Verify.EngineCheck,
				EngineParams:      ys.Verify.EngineParams,
				ReviewerPrompt:    ys.Verify.ReviewerPrompt,
				ReviewerAgentID:   ys.Verify.ReviewerAgentID,
				ReviewerProvider:  ys.Verify.ReviewerProvider,
				ReviewerModel:     ys.Verify.ReviewerModel,
				ReviewerTools:     ys.Verify.ReviewerTools,
				ReviewerSessionID: ys.Verify.ReviewerSessionID,
			}
		}
		wf.Steps = append(wf.Steps, step)
	}

	if err := Validate(wf); err != nil {
		return WorkflowDefinition{}, err
	}
	return wf, nil
}
