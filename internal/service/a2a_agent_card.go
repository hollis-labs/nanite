package service

import (
	"fmt"

	"github.com/hollis-labs/nanite/internal/a2a"
	"github.com/hollis-labs/nanite/internal/agentworkflow"
)

// AgentCardGenerator builds an A2A-compliant Agent Card from Nanite's
// workflow registry. This is the service-layer implementation behind
// GET /.well-known/agent-card.json.
type AgentCardGenerator struct {
	workflowRegistry *agentworkflow.Registry
	baseURL          string
	version          string
}

// NewAgentCardGenerator constructs an AgentCardGenerator.
func NewAgentCardGenerator(
	workflowRegistry *agentworkflow.Registry,
	baseURL string,
	version string,
) *AgentCardGenerator {
	return &AgentCardGenerator{
		workflowRegistry: workflowRegistry,
		baseURL:          baseURL,
		version:          version,
	}
}

// Generate builds the Agent Card. One card represents the Nanite host, not
// one per durable-agent instance. Skills are derived from the named
// workflow definitions in the Agent Workflows registry.
//
// TASKS/phase-2/04-retire-boot-profile-catalog.md: this used to also
// derive skills from the boot-profile catalog's compiled LaunchSpecs
// (skillFromBootProfile). That catalog is retired in full; workflow
// definitions are the sole skill source now.
func (g *AgentCardGenerator) Generate() (*a2a.AgentCard, error) {
	var skills []a2a.Skill

	// Add workflow-derived skills
	if g.workflowRegistry != nil {
		for _, name := range g.workflowRegistry.Names() {
			wf, ok := g.workflowRegistry.Get(name)
			if !ok {
				continue
			}
			skills = append(skills, skillFromWorkflow(wf))
		}
	}

	return &a2a.AgentCard{
		Name:        "Nanite",
		Description: "AI agent orchestration platform with durable workflows and session management",
		URL:         g.baseURL,
		Provider: a2a.Provider{
			Organization: "Hollis Labs",
		},
		Version: g.version,
		Capabilities: a2a.Capabilities{
			Streaming:         true, // Nanite supports streaming via SSE
			PushNotifications: true, // Implemented via best-effort HTTP POST
		},
		DefaultInputModes:  []string{"application/json", "text/plain"},
		DefaultOutputModes: []string{"application/json", "text/plain"},
		Skills:             skills,
	}, nil
}

// skillFromWorkflow converts a WorkflowDefinition into an A2A Skill.
// The InputSchema is derived from the workflow's required inputs.
func skillFromWorkflow(wf agentworkflow.WorkflowDefinition) a2a.Skill {
	// Build the input schema from the workflow's required inputs
	inputProps := make(map[string]a2a.SchemaProperty)
	var required []string

	// Extract required inputs from the workflow definition's steps
	// For now, we'll create a generic params field - a future pass could
	// introspect step configs to derive richer schemas
	inputProps["params"] = a2a.SchemaProperty{
		Type:        "object",
		Description: "Workflow parameters",
	}

	return a2a.Skill{
		ID:          wf.Name,
		Name:        wf.Name,
		Description: fmt.Sprintf("Workflow: %s", wf.Name),
		Tags:        []string{"workflow", "automation"},
		InputSchema: a2a.InputSchema{
			Type:       "object",
			Properties: inputProps,
			Required:   required,
		},
	}
}
