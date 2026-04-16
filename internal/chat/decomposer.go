package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/hollis-labs/go-providers/provider"
)

// SubTask represents a discrete unit of work decomposed from a complex task.
type SubTask struct {
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	ContextHints     []string `json:"context_hints"`
	ToolRequirements []string `json:"tool_requirements"`
}

// DecompositionResult holds the output of task decomposition.
type DecompositionResult struct {
	SubTasks    []SubTask `json:"sub_tasks"`
	Aggregation string    `json:"aggregation"` // description of how to combine results
	IsComplex   bool      `json:"is_complex"`  // whether decomposition was needed
}

// Decomposer analyzes user messages and breaks complex tasks into sub-tasks.
type Decomposer struct {
	Providers *provider.Registry
}

// NewDecomposer creates a new Decomposer.
func NewDecomposer(providers *provider.Registry) *Decomposer {
	return &Decomposer{Providers: providers}
}

// decompositionPrompt is the system prompt used to analyze and decompose tasks.
const decompositionPrompt = `You are a task decomposition agent. Analyze the user's message and determine if it represents a complex multi-step task that should be broken into discrete sub-tasks.

Rules:
- If the task is simple (single action, single context), set is_complex to false and return an empty sub_tasks array.
- If the task is complex (multiple steps, multiple contexts, requires research + action), decompose it.
- Each sub-task should be independently executable with minimal context.
- context_hints should list only the specific files, topics, or data each sub-task needs — NOT everything.
- tool_requirements should list tool categories needed (e.g. "file_read", "web_search", "code_edit", "engine", "conduit").
- The aggregation field describes how to combine sub-task results into a final answer.
- Keep sub-tasks to 2-6 items. More than 6 means you should consolidate.

Respond with ONLY valid JSON matching this schema:
{
  "is_complex": boolean,
  "sub_tasks": [
    {
      "title": "short title",
      "description": "what to do",
      "context_hints": ["specific file or topic"],
      "tool_requirements": ["tool_category"]
    }
  ],
  "aggregation": "how to combine results"
}`

// DecomposeTask analyzes a user message and optionally decomposes it into sub-tasks.
// The agentContext provides the agent's system prompt for understanding the agent's capabilities.
func (d *Decomposer) DecomposeTask(ctx context.Context, userMessage string, agentContext string, model string) (*DecompositionResult, error) {
	prov, ok := d.Providers.Get("anthropic")
	if !ok {
		return nil, fmt.Errorf("anthropic provider not available for decomposition")
	}

	prompt := decompositionPrompt
	if agentContext != "" {
		prompt += fmt.Sprintf("\n\nAgent context (capabilities and role):\n%s", agentContext)
	}

	messages := []provider.ChatMessage{
		{Role: "user", Content: userMessage},
	}

	raw, err := prov.Complete(ctx, provider.ChatRequest{SystemPrompt: prompt, Messages: messages, Model: model})
	if err != nil {
		return nil, fmt.Errorf("decomposition LLM call: %w", err)
	}

	var result DecompositionResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		slog.Warn("decomposer: failed to parse LLM response", "err", err, "response_body", raw)
		return &DecompositionResult{IsComplex: false}, nil
	}

	slog.Info("decomposer: analyzed message", "complex", result.IsComplex, "sub_tasks", len(result.SubTasks))
	return &result, nil
}
