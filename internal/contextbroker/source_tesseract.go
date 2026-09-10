package contextbroker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// MCPCaller is the subset of the MCP manager needed by context sources.
type MCPCaller interface {
	ExecuteTool(ctx context.Context, name string, input map[string]any) (string, error)
	ExecuteToolOnServer(ctx context.Context, server, tool string, input map[string]any) (string, error)
}

// TesseractSource retrieves context through Tesseract v0.10's merged
// context_plan execute arm. Retired broker_fetch/search fallbacks are
// deliberately absent so schema drift fails visibly.
type TesseractSource struct {
	MCP        MCPCaller
	ServerName string
}

func NewTesseractSource(mcp MCPCaller) *TesseractSource {
	return &TesseractSource{MCP: mcp, ServerName: "tesseract"}
}

func (s *TesseractSource) Name() string { return "tesseract" }

func (s *TesseractSource) Fetch(ctx context.Context, intent Intent, budget int) ([]ContextItem, error) {
	if s.MCP == nil {
		return nil, fmt.Errorf("tesseract source: no MCP caller configured")
	}
	if budget <= 0 {
		return []ContextItem{}, nil
	}

	summaryParts := []string{intent.QueryText}
	if len(intent.Keywords) > 0 {
		summaryParts = append(summaryParts, strings.Join(intent.Keywords, " "))
	}
	if intent.Scope != "" {
		summaryParts = append(summaryParts, "scope "+intent.Scope)
	}
	maxItems := budget / 64
	if maxItems < 1 {
		maxItems = 1
	}
	if maxItems > 50 {
		maxItems = 50
	}
	// The assembly budget is spelled max_items / max_tokens_estimate. Tesseract
	// v0.10.0 retired the older budget_items / budget_tokens spelling on
	// context_plan and REFUSES it rather than ignoring it, because the old
	// names also carried a different token default (4000, against 8000
	// everywhere else) — an ignored name would have silently doubled the
	// budget. Note budget_tokens still exists on tesseract_recall/_history/_get
	// as a genuinely different knob (the response serialization ceiling), so
	// this rename is specific to the packet-assembly surface.
	input := map[string]any{
		"execute":             true,
		"intent":              mapToTesseractIntent(intent.Type),
		"summary":             strings.TrimSpace(strings.Join(summaryParts, " ")),
		"max_items":           maxItems,
		"max_tokens_estimate": budget,
	}

	raw, err := s.MCP.ExecuteToolOnServer(ctx, s.ServerName, "context_plan", input)
	if err != nil {
		return nil, fmt.Errorf("tesseract context_plan: %w", err)
	}
	return parseTesseractPlanResult(raw, budget)
}

func parseTesseractPlanResult(raw string, budget int) ([]ContextItem, error) {
	var response struct {
		Items []struct {
			Namespace        string          `json:"namespace"`
			Key              string          `json:"key"`
			Payload          json.RawMessage `json:"payload"`
			PayloadHead      string          `json:"payload_head"`
			PayloadTruncated bool            `json:"payload_truncated"`
		} `json:"items"`
		Manifest json.RawMessage `json:"manifest"`
	}
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		return nil, fmt.Errorf("tesseract context_plan response: decode JSON: %w", err)
	}
	if response.Items == nil || response.Manifest == nil {
		return nil, fmt.Errorf("tesseract context_plan response: missing items or manifest")
	}

	items := make([]ContextItem, 0, len(response.Items))
	usedTokens := 0
	for _, record := range response.Items {
		content := strings.TrimSpace(string(record.Payload))
		if content == "" || content == "null" {
			content = record.PayloadHead
		}
		if record.Namespace == "" || record.Key == "" || content == "" {
			return nil, fmt.Errorf("tesseract context_plan response: item missing namespace, key, or payload")
		}
		tokens := EstimateTokens(content)
		if usedTokens+tokens > budget {
			break
		}
		metadata := map[string]string{"namespace": record.Namespace}
		if record.PayloadTruncated {
			metadata["payload_truncated"] = "true"
		}
		items = append(items, ContextItem{
			Source:        "tesseract",
			Key:           fmt.Sprintf("%s/%s", record.Namespace, record.Key),
			Content:       content,
			TokenEstimate: tokens,
			Relevance:     0.7,
			Metadata:      metadata,
		})
		usedTokens += tokens
	}
	return items, nil
}

func mapToTesseractIntent(intentType string) string {
	switch intentType {
	case IntentResumeTask:
		return "resume_task"
	case IntentBootProject:
		return "boot_project"
	case IntentReviewSession:
		return "review_session"
	default:
		return "custom"
	}
}
