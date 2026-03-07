package workflow

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/hollis-labs/mentat-chat/internal/provider"
	"github.com/hollis-labs/mentat-chat/internal/store"
)

// WorkflowDef represents a parsed YAML workflow definition.
type WorkflowDef struct {
	Name        string     `yaml:"name"        json:"name"`
	Description string     `yaml:"description" json:"description"`
	Inputs      []InputDef `yaml:"inputs"      json:"inputs"`
	Steps       []StepDef  `yaml:"steps"       json:"steps"`
}

// InputDef represents a workflow input field.
type InputDef struct {
	Name     string `yaml:"name"     json:"name"`
	Type     string `yaml:"type"     json:"type"`     // text, textarea, select, number
	Label    string `yaml:"label"    json:"label"`
	Required bool   `yaml:"required" json:"required"`
	Default  string `yaml:"default"  json:"default,omitempty"`
}

// StepDef represents a single step in a workflow.
type StepDef struct {
	Name   string            `yaml:"name"   json:"name"`
	Action string            `yaml:"action" json:"action"` // llm_call, store_artifact, create_task
	Params map[string]string `yaml:"params" json:"params"`
}

// StepResult holds the output of a single step execution.
type StepResult struct {
	StepName string `json:"step_name"`
	Action   string `json:"action"`
	Output   string `json:"output"`
	Error    string `json:"error,omitempty"`
}

// WorkflowResult holds the result of a full workflow execution.
type WorkflowResult struct {
	WorkflowName string       `json:"workflow_name"`
	Steps        []StepResult `json:"steps"`
	FinalOutput  string       `json:"final_output"`
}

// Engine executes workflow definitions.
type Engine struct {
	Providers *provider.Registry
	Store     *store.Store
}

// NewEngine creates a new workflow Engine.
func NewEngine(providers *provider.Registry, s *store.Store) *Engine {
	return &Engine{
		Providers: providers,
		Store:     s,
	}
}

// Execute runs a workflow definition with the given inputs, executing steps sequentially
// and passing results between steps.
func (e *Engine) Execute(ctx context.Context, def *WorkflowDef, inputs map[string]string) (*WorkflowResult, error) {
	result := &WorkflowResult{
		WorkflowName: def.Name,
	}

	// Validate required inputs.
	for _, input := range def.Inputs {
		if input.Required {
			val, ok := inputs[input.Name]
			if !ok || val == "" {
				if input.Default != "" {
					inputs[input.Name] = input.Default
				} else {
					return nil, fmt.Errorf("required input %q is missing", input.Name)
				}
			}
		}
	}

	// Execute steps sequentially.
	stepOutputs := make(map[string]string)
	var lastOutput string

	for _, step := range def.Steps {
		log.Printf("workflow: executing step %q (action=%s)", step.Name, step.Action)

		// Resolve template variables in params.
		resolvedParams := make(map[string]string)
		for k, v := range step.Params {
			resolvedParams[k] = resolveTemplates(v, inputs, stepOutputs)
		}

		var stepResult StepResult
		stepResult.StepName = step.Name
		stepResult.Action = step.Action

		switch step.Action {
		case "llm_call":
			output, err := e.executeLLMCall(ctx, resolvedParams)
			if err != nil {
				stepResult.Error = err.Error()
				log.Printf("workflow: step %q failed: %v", step.Name, err)
			} else {
				stepResult.Output = output
				lastOutput = output
			}

		case "store_artifact":
			output, err := e.executeStoreArtifact(resolvedParams)
			if err != nil {
				stepResult.Error = err.Error()
				log.Printf("workflow: step %q failed: %v", step.Name, err)
			} else {
				stepResult.Output = output
				lastOutput = output
			}

		case "create_task":
			output, err := e.executeCreateTask(resolvedParams)
			if err != nil {
				stepResult.Error = err.Error()
				log.Printf("workflow: step %q failed: %v", step.Name, err)
			} else {
				stepResult.Output = output
				lastOutput = output
			}

		default:
			stepResult.Error = fmt.Sprintf("unknown action: %s", step.Action)
			log.Printf("workflow: step %q has unknown action %q", step.Name, step.Action)
		}

		stepOutputs[step.Name] = stepResult.Output
		result.Steps = append(result.Steps, stepResult)
	}

	result.FinalOutput = lastOutput
	return result, nil
}

// resolveTemplates replaces {{input.name}} and {{step.name}} placeholders in a string.
func resolveTemplates(s string, inputs map[string]string, stepOutputs map[string]string) string {
	result := s
	for k, v := range inputs {
		result = strings.ReplaceAll(result, "{{input."+k+"}}", v)
	}
	for k, v := range stepOutputs {
		result = strings.ReplaceAll(result, "{{step."+k+"}}", v)
	}
	return result
}

// executeLLMCall sends a prompt to the provider and returns the response.
func (e *Engine) executeLLMCall(ctx context.Context, params map[string]string) (string, error) {
	providerName := params["provider"]
	if providerName == "" {
		providerName = "anthropic"
	}

	prov, ok := e.Providers.Get(providerName)
	if !ok {
		return "", fmt.Errorf("provider %q not registered", providerName)
	}

	prompt := params["prompt"]
	if prompt == "" {
		return "", fmt.Errorf("llm_call requires a 'prompt' param")
	}

	model := params["model"]
	systemPrompt := params["system_prompt"]

	messages := []provider.ChatMessage{
		{Role: "user", Content: prompt},
	}

	result, err := prov.Complete(ctx, systemPrompt, messages, model)
	if err != nil {
		return "", fmt.Errorf("llm call: %w", err)
	}

	return result, nil
}

// executeStoreArtifact saves content as an artifact in the store.
func (e *Engine) executeStoreArtifact(params map[string]string) (string, error) {
	name := params["name"]
	if name == "" {
		name = "workflow-artifact"
	}

	content := params["content"]
	mimeType := params["mime_type"]
	if mimeType == "" {
		mimeType = "text/plain"
	}

	sessionID := params["session_id"]

	artifact := &store.Artifact{
		SessionID:   sessionID,
		Name:        name,
		MimeType:    mimeType,
		SizeBytes:   int64(len(content)),
		StoragePath: "inline:" + name,
		Metadata:    fmt.Sprintf(`{"source":"workflow","content":%q}`, content),
	}

	if err := e.Store.CreateArtifact(artifact); err != nil {
		return "", fmt.Errorf("store artifact: %w", err)
	}

	return fmt.Sprintf("artifact:%s", artifact.ID), nil
}

// executeCreateTask creates a placeholder task record (logged for now, as Volon integration is external).
func (e *Engine) executeCreateTask(params map[string]string) (string, error) {
	title := params["title"]
	if title == "" {
		return "", fmt.Errorf("create_task requires a 'title' param")
	}

	description := params["description"]
	project := params["project"]

	// Log the task creation as a placeholder. In production, this would call the Volon MCP.
	log.Printf("workflow: create_task placeholder — title=%q project=%q description=%q", title, project, description)

	return fmt.Sprintf("task_placeholder:%s", title), nil
}
