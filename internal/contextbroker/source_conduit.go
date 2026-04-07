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

// ConduitSource retrieves context from Vanta Conduit via MCP tools.
// It calls context_broker_fetch (or context_search for keyword queries)
// to get relevant context records.
type ConduitSource struct {
	MCP        MCPCaller
	ServerName string // MCP server name (default: "conduit")
}

// NewConduitSource creates a ConduitSource with the given MCP caller.
func NewConduitSource(mcp MCPCaller) *ConduitSource {
	return &ConduitSource{
		MCP:        mcp,
		ServerName: "conduit",
	}
}

func (s *ConduitSource) Name() string { return "conduit" }

func (s *ConduitSource) Fetch(ctx context.Context, intent Intent, budget int) ([]ContextItem, error) {
	if s.MCP == nil {
		return nil, fmt.Errorf("conduit source: no MCP caller configured")
	}

	// Map our intent to Conduit's plan intents where possible.
	conduitIntent := mapToConduitIntent(intent.Type)

	// Try context_broker_fetch first for structured retrieval.
	items, err := s.fetchViaBroker(ctx, conduitIntent, intent, budget)
	if err != nil {
		log.Printf("contextbroker/conduit: broker_fetch failed: %v — falling back to search", err)
		// Fall back to keyword search.
		return s.fetchViaSearch(ctx, intent, budget)
	}

	return items, nil
}

// fetchViaBroker calls Vanta Conduit's context_broker_fetch MCP tool.
func (s *ConduitSource) fetchViaBroker(ctx context.Context, conduitIntent string, intent Intent, budget int) ([]ContextItem, error) {
	toolName := fmt.Sprintf("mcp__%s__context_broker_fetch", s.ServerName)

	input := map[string]any{
		"intent":     conduitIntent,
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

// fetchViaSearch calls Vanta Conduit's context_search MCP tool for keyword-based retrieval.
func (s *ConduitSource) fetchViaSearch(ctx context.Context, intent Intent, budget int) ([]ContextItem, error) {
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

// parseResult converts a Vanta Conduit MCP response into ContextItems.
func (s *ConduitSource) parseResult(raw string, budget int) ([]ContextItem, error) {
	// Conduit returns JSON with records array.
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
			Source:        "conduit",
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
			Source:        "conduit",
			Key:           fmt.Sprintf("%s/%s", r.Namespace, r.Key),
			Content:       r.Value,
			TokenEstimate: tokens,
			Relevance:     0.7, // Conduit results are pre-ranked
			Metadata: map[string]string{
				"namespace": r.Namespace,
			},
		})
		usedTokens += tokens
	}

	return items, nil
}

// mapToConduitIntent maps ContextBroker intents to Conduit's 4 native intents.
func mapToConduitIntent(intentType string) string {
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
