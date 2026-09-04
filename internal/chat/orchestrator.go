package chat

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	llmtypes "github.com/hollis-labs/go-llm-types"
	"github.com/hollis-labs/go-providers/provider"
	"github.com/hollis-labs/nanite/internal/mcp"
)

// OrchestrationPlan represents the plan for executing decomposed sub-tasks.
type OrchestrationPlan struct {
	SubTasks     []SubTask `json:"sub_tasks"`
	Aggregation  string    `json:"aggregation"`
	HasTesseract bool      `json:"has_tesseract"` // whether Tesseract is available for durable context
	PlanOnly     bool      `json:"plan_only"`     // true if no MCP services available to execute
}

// SubTaskResult holds the result of executing a single sub-task.
type SubTaskResult struct {
	Title  string `json:"title"`
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
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
// It checks for Tesseract availability for knowledge grounding.
func (o *Orchestrator) BuildPlan(ctx context.Context, decomposition *DecompositionResult, projectID string) (*OrchestrationPlan, error) {
	plan := &OrchestrationPlan{
		SubTasks:     decomposition.SubTasks,
		Aggregation:  decomposition.Aggregation,
		HasTesseract: o.hasToolPrefix("tesseract"),
	}

	// If no MCP manager, return plan only.
	if o.MCPManager == nil {
		plan.PlanOnly = true
		slog.Info("orchestrator: no MCP manager — returning plan only")
		return plan, nil
	}

	// Check Tesseract for relevant knowledge before executing sub-tasks.
	if plan.HasTesseract {
		slog.Info("orchestrator: tesseract available — sub-tasks can leverage durable context")
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

	messages := []llmtypes.ChatMessage{
		{Role: "user", Content: sb.String()},
	}

	finalOutput, err := prov.Complete(ctx, llmtypes.ChatRequest{SystemPrompt: aggregatePrompt, Messages: messages, Model: model})
	if err != nil {
		slog.Warn("orchestrator: aggregation LLM call failed — using raw concatenation", "err", err)
		orchResult.FinalOutput = sb.String()
		return orchResult, nil
	}

	orchResult.FinalOutput = finalOutput
	return orchResult, nil
}

// hasToolPrefix reports whether the orchestrator has tools from a named
// MCP server. Replaces the legacy "scan tool names for `mcp__<prefix>__`"
// check with a direct server-presence query (ADR-002).
func (o *Orchestrator) hasToolPrefix(serverName string) bool {
	if o.MCPManager == nil {
		return false
	}
	return o.MCPManager.HasServer(serverName)
}
