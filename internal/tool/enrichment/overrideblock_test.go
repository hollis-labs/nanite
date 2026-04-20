package enrichment_test

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/tool/enrichment"
)

// mapEnricher is a test Enricher backed by an in-memory map.
type mapEnricher map[string]enrichment.Hints

func (m mapEnricher) LookupByToolName(ctx context.Context, name string) (enrichment.Hints, bool, error) {
	h, ok := m[name]
	return h, ok, nil
}

func TestComposeOverrideBlock_EmptyWhenNoEnrichment(t *testing.T) {
	tools := []string{"tool_a", "tool_b"}
	enr := mapEnricher{}
	got, err := enrichment.ComposeOverrideBlock(context.Background(), tools, enr)
	if err != nil {
		t.Fatalf("ComposeOverrideBlock: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty block, got:\n%s", got)
	}
}

func TestComposeOverrideBlock_SingleTool(t *testing.T) {
	tools := []string{"tool_a"}
	enr := mapEnricher{
		"tool_a": {OutputShape: "list of records"},
	}
	got, err := enrichment.ComposeOverrideBlock(context.Background(), tools, enr)
	if err != nil {
		t.Fatalf("ComposeOverrideBlock: %v", err)
	}
	if !strings.Contains(got, "## Tool Overrides") {
		t.Errorf("missing header:\n%s", got)
	}
	if !strings.Contains(got, "tool_a") {
		t.Errorf("missing tool name:\n%s", got)
	}
	if !strings.Contains(got, "list of records") {
		t.Errorf("missing output shape:\n%s", got)
	}
}

func TestComposeOverrideBlock_PartialEnrichment(t *testing.T) {
	tools := []string{"tool_a", "tool_b", "tool_c"}
	enr := mapEnricher{
		"tool_b": {
			AntiPatterns: []string{"IDs are ULIDs"},
			OutputShape:  "array of x",
		},
	}
	got, err := enrichment.ComposeOverrideBlock(context.Background(), tools, enr)
	if err != nil {
		t.Fatalf("ComposeOverrideBlock: %v", err)
	}
	if !strings.Contains(got, "tool_b") {
		t.Errorf("missing enriched tool: %s", got)
	}
	if strings.Contains(got, "tool_a") || strings.Contains(got, "tool_c") {
		t.Errorf("unexpectedly included non-enriched tools: %s", got)
	}
	if !strings.Contains(got, "ULID") {
		t.Errorf("missing anti-pattern content: %s", got)
	}
}

func TestComposeOverrideBlock_EmptyToolList(t *testing.T) {
	got, err := enrichment.ComposeOverrideBlock(context.Background(), nil, mapEnricher{})
	if err != nil {
		t.Fatalf("ComposeOverrideBlock: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty, got: %s", got)
	}
}

func TestComposeOverrideBlock_NilEnricher(t *testing.T) {
	tools := []string{"tool_a"}
	got, err := enrichment.ComposeOverrideBlock(context.Background(), tools, nil)
	if err != nil {
		t.Fatalf("ComposeOverrideBlock: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty with nil enricher, got: %s", got)
	}
}
