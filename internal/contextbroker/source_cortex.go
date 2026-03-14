package contextbroker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
)

// MCPCaller is the interface for calling MCP tools. This matches the
// subset of mcp.Manager that ContextBroker needs, avoiding a direct import.
type MCPCaller interface {
	ExecuteTool(ctx context.Context, name string, input map[string]any) (string, error)
}

// CortexSource retrieves context from Cortex via MCP tools.
// It calls context_broker_fetch (or context_search for keyword queries)
// to get relevant context records.
type CortexSource struct {
	MCP        MCPCaller
	ServerName string // MCP server name (default: "cortex")
}

// NewCortexSource creates a CortexSource with the given MCP caller.
func NewCortexSource(mcp MCPCaller) *CortexSource {
	return &CortexSource{
		MCP:        mcp,
		ServerName: "cortex",
	}
}

func (s *CortexSource) Name() string { return "cortex" }

func (s *CortexSource) Fetch(ctx context.Context, intent Intent, budget int) ([]ContextItem, error) {
	if s.MCP == nil {
		return nil, fmt.Errorf("cortex source: no MCP caller configured")
	}

	// Map our intent to Cortex's plan intents where possible.
	cortexIntent := mapToCortexIntent(intent.Type)

	// Try context_broker_fetch first for structured retrieval.
	items, err := s.fetchViaBroker(ctx, cortexIntent, intent, budget)
	if err != nil {
		log.Printf("contextbroker/cortex: broker_fetch failed: %v — falling back to search", err)
		// Fall back to keyword search.
		return s.fetchViaSearch(ctx, intent, budget)
	}

	return items, nil
}

// fetchViaBroker calls Cortex's context_broker_fetch MCP tool.
func (s *CortexSource) fetchViaBroker(ctx context.Context, cortexIntent string, intent Intent, budget int) ([]ContextItem, error) {
	toolName := fmt.Sprintf("mcp__%s__context_broker_fetch", s.ServerName)

	input := map[string]any{
		"intent":     cortexIntent,
		"max_tokens": budget,
	}
	if intent.Scope != "" {
		input["namespace"] = intent.Scope
	}
	if len(intent.Keywords) > 0 {
		input["keywords"] = strings.Join(intent.Keywords, " ")
	}

	result, err := s.MCP.ExecuteTool(ctx, toolName, input)
	if err != nil {
		return nil, fmt.Errorf("context_broker_fetch: %w", err)
	}

	return s.parseResult(result, budget)
}

// fetchViaSearch calls Cortex's context_search MCP tool for keyword-based retrieval.
func (s *CortexSource) fetchViaSearch(ctx context.Context, intent Intent, budget int) ([]ContextItem, error) {
	if len(intent.Keywords) == 0 {
		return nil, nil
	}

	toolName := fmt.Sprintf("mcp__%s__context_search", s.ServerName)

	input := map[string]any{
		"query": strings.Join(intent.Keywords, " "),
	}
	if intent.Scope != "" {
		input["namespace"] = intent.Scope
	}

	result, err := s.MCP.ExecuteTool(ctx, toolName, input)
	if err != nil {
		return nil, fmt.Errorf("context_search: %w", err)
	}

	return s.parseResult(result, budget)
}

// parseResult converts a Cortex MCP response into ContextItems.
func (s *CortexSource) parseResult(raw string, budget int) ([]ContextItem, error) {
	// Cortex returns JSON with records array.
	var response struct {
		Records []struct {
			Namespace string `json:"namespace"`
			Key       string `json:"key"`
			Value     string `json:"value"`
			Type      string `json:"type"`
		} `json:"records"`
		// Flat format (context_search returns items).
		Items []struct {
			Namespace string `json:"namespace"`
			Key       string `json:"key"`
			Value     string `json:"value"`
		} `json:"items"`
	}

	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		// If it's not JSON, treat the whole response as a single item.
		tokens := EstimateTokens(raw)
		if tokens > budget {
			raw = raw[:budget*4]
		}
		return []ContextItem{{
			Source:        "cortex",
			Key:           "raw",
			Content:       raw,
			TokenEstimate: EstimateTokens(raw),
			Relevance:     0.5,
		}}, nil
	}

	// Merge records and items into a single list.
	type record struct {
		Namespace, Key, Value string
	}
	var records []record
	for _, r := range response.Records {
		records = append(records, record{r.Namespace, r.Key, r.Value})
	}
	for _, r := range response.Items {
		records = append(records, record{r.Namespace, r.Key, r.Value})
	}

	var items []ContextItem
	usedTokens := 0

	for _, r := range records {
		tokens := EstimateTokens(r.Value)
		if usedTokens+tokens > budget {
			break
		}
		items = append(items, ContextItem{
			Source:        "cortex",
			Key:           fmt.Sprintf("%s/%s", r.Namespace, r.Key),
			Content:       r.Value,
			TokenEstimate: tokens,
			Relevance:     0.7, // Cortex results are pre-ranked
			Metadata: map[string]string{
				"namespace": r.Namespace,
			},
		})
		usedTokens += tokens
	}

	return items, nil
}

// mapToCortexIntent maps ContextBroker intents to Cortex's 4 native intents.
func mapToCortexIntent(intentType string) string {
	switch intentType {
	case IntentResumeTask:
		return "resume_task"
	case IntentBootProject:
		return "boot_project"
	case IntentReviewSession:
		return "review_session"
	case IntentWriteCode, IntentDebugIssue, IntentPlanFeature, IntentRecallDecision:
		return "custom"
	default:
		return "custom"
	}
}
