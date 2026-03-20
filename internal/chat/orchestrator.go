package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/hollis-labs/conduit/internal/mcp"
	"github.com/hollis-labs/conduit/internal/provider"
)

// OrchestrationPlan represents the plan for executing decomposed sub-tasks.
type OrchestrationPlan struct {
	SubTasks      []SubTask `json:"sub_tasks"`
	Aggregation   string    `json:"aggregation"`
	SprintID      string    `json:"sprint_id,omitempty"`       // Volon sprint ID if created
	TaskIDs       []string  `json:"task_ids,omitempty"`        // Volon task IDs if created
	HasVolon      bool      `json:"has_volon"`                 // whether Volon integration is available
	HasCortex     bool      `json:"has_cortex"`                // whether Cortex is available for knowledge
	PlanOnly      bool      `json:"plan_only"`                 // true if no MCP services available to execute
}

// SubTaskResult holds the result of executing a single sub-task.
type SubTaskResult struct {
	Title   string `json:"title"`
	Output  string `json:"output"`
	Error   string `json:"error,omitempty"`
	TaskID  string `json:"task_id,omitempty"` // Volon task ID if tracked
}

// OrchestrationResult holds the final aggregated output.
type OrchestrationResult struct {
	Plan        *OrchestrationPlan `json:"plan"`
	Results     []SubTaskResult    `json:"results"`
	FinalOutput string             `json:"final_output"`
}

// Orchestrator coordinates the execution of decomposed sub-tasks.
type Orchestrator struct {
	Providers  *provider.Registry
	MCPManager *mcp.Manager
	Decomposer *Decomposer
}

// NewOrchestrator creates a new Orchestrator.
func NewOrchestrator(providers *provider.Registry, mcpManager *mcp.Manager) *Orchestrator {
	return &Orchestrator{
		Providers:  providers,
		MCPManager: mcpManager,
		Decomposer: NewDecomposer(providers),
	}
}

// HasDecomposer reports whether the orchestrator has a working decomposer.
func (o *Orchestrator) HasDecomposer() bool {
	return o.Decomposer != nil
}

// BuildPlan creates an orchestration plan from a decomposition result.
// It checks for Volon/Cortex availability and optionally creates a sprint.
func (o *Orchestrator) BuildPlan(ctx context.Context, decomposition *DecompositionResult, projectID string) (*OrchestrationPlan, error) {
	plan := &OrchestrationPlan{
		SubTasks:    decomposition.SubTasks,
		Aggregation: decomposition.Aggregation,
		HasVolon:    o.hasToolPrefix("engine"),
		HasCortex:   o.hasToolPrefix("cortex"),
	}

	// If no MCP manager, return plan only.
	if o.MCPManager == nil {
		plan.PlanOnly = true
		log.Printf("orchestrator: no MCP manager — returning plan only")
		return plan, nil
	}

	// Check Cortex for relevant knowledge before creating tasks.
	if plan.HasCortex {
		log.Printf("orchestrator: cortex available — sub-tasks can leverage agent knowledge")
	}

	// Create Volon sprint + tasks if available.
	if plan.HasVolon && projectID != "" {
		sprintID, taskIDs, err := o.createVolonSprint(ctx, projectID, decomposition)
		if err != nil {
			log.Printf("orchestrator: failed to create Volon sprint: %v (continuing without tracking)", err)
		} else {
			plan.SprintID = sprintID
			plan.TaskIDs = taskIDs
			log.Printf("orchestrator: created Volon sprint %s with %d tasks", sprintID, len(taskIDs))
		}
	}

	return plan, nil
}

// Aggregate collects sub-task results and produces a final combined output.
func (o *Orchestrator) Aggregate(ctx context.Context, plan *OrchestrationPlan, results []SubTaskResult, model string) (*OrchestrationResult, error) {
	orchResult := &OrchestrationResult{
		Plan:    plan,
		Results: results,
	}

	// Build a summary of all results for the aggregation LLM call.
	var sb strings.Builder
	for i, r := range results {
		fmt.Fprintf(&sb, "## Sub-task %d: %s\n", i+1, r.Title)
		if r.Error != "" {
			fmt.Fprintf(&sb, "Error: %s\n", r.Error)
		} else {
			fmt.Fprintf(&sb, "%s\n", r.Output)
		}
		sb.WriteString("\n")
	}

	prov, ok := o.Providers.Get("anthropic")
	if !ok {
		// No provider — just concatenate results.
		orchResult.FinalOutput = sb.String()
		return orchResult, nil
	}

	aggregatePrompt := fmt.Sprintf(
		"You are aggregating results from multiple sub-tasks into a coherent final response.\n\n"+
			"Aggregation strategy: %s\n\n"+
			"Combine the sub-task results below into a single, well-structured response for the user. "+
			"Do not mention the sub-task decomposition — present the result as a unified answer.",
		plan.Aggregation,
	)

	messages := []provider.ChatMessage{
		{Role: "user", Content: sb.String()},
	}

	finalOutput, err := prov.Complete(ctx, aggregatePrompt, messages, model)
	if err != nil {
		log.Printf("orchestrator: aggregation LLM call failed: %v — using raw concatenation", err)
		orchResult.FinalOutput = sb.String()
		return orchResult, nil
	}

	orchResult.FinalOutput = finalOutput
	return orchResult, nil
}

// hasToolPrefix checks whether any registered tool starts with the given prefix.
func (o *Orchestrator) hasToolPrefix(prefix string) bool {
	if o.MCPManager == nil {
		return false
	}
	tools := o.MCPManager.GetAllTools()
	target := "mcp__" + prefix
	for _, t := range tools {
		if strings.HasPrefix(t.Name, target) {
			return true
		}
	}
	return false
}

// createVolonSprint creates a Volon sprint with one task per sub-task.
func (o *Orchestrator) createVolonSprint(ctx context.Context, projectID string, decomposition *DecompositionResult) (string, []string, error) {
	if o.MCPManager == nil {
		return "", nil, fmt.Errorf("no MCP manager")
	}

	// Create sprint via MCP tool call.
	sprintResult, err := o.MCPManager.ExecuteTool(ctx, "mcp__engine__engine_sprint_create", map[string]any{
		"project_id":  projectID,
		"title":       "Auto-decomposed task sprint",
		"description": fmt.Sprintf("Sprint with %d sub-tasks from task decomposition", len(decomposition.SubTasks)),
	})
	if err != nil {
		return "", nil, fmt.Errorf("create sprint: %w", err)
	}

	// Parse sprint ID from result.
	var sprintResp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(sprintResult), &sprintResp); err != nil {
		log.Printf("orchestrator: could not parse sprint response: %v", err)
		return "", nil, fmt.Errorf("parse sprint response: %w", err)
	}

	// Create a task for each sub-task.
	var taskIDs []string
	for _, st := range decomposition.SubTasks {
		taskResult, err := o.MCPManager.ExecuteTool(ctx, "mcp__engine__engine_task_create", map[string]any{
			"project_id":  projectID,
			"sprint_id":   sprintResp.ID,
			"title":       st.Title,
			"description": st.Description,
		})
		if err != nil {
			log.Printf("orchestrator: failed to create Volon task for %q: %v", st.Title, err)
			continue
		}

		var taskResp struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(taskResult), &taskResp); err != nil {
			log.Printf("orchestrator: could not parse task response: %v", err)
			continue
		}
		taskIDs = append(taskIDs, taskResp.ID)
	}

	return sprintResp.ID, taskIDs, nil
}
