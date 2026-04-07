package contextbroker

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// mockSource is a test ContextSource.
type mockSource struct {
	name  string
	items []ContextItem
	err   error
}

func (m *mockSource) Name() string { return m.name }
func (m *mockSource) Fetch(_ context.Context, _ Intent, budget int) ([]ContextItem, error) {
	if m.err != nil {
		return nil, m.err
	}
	// Respect budget — skip items that don't fit, don't break.
	var result []ContextItem
	used := 0
	for _, item := range m.items {
		if used+item.TokenEstimate > budget {
			continue
		}
		result = append(result, item)
		used += item.TokenEstimate
	}
	return result, nil
}

func TestBrokerFetch_NoSources(t *testing.T) {
	b := New(DefaultBudget())
	packet, err := b.Fetch(context.Background(), Intent{Type: IntentCustom})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(packet.Items) != 0 {
		t.Errorf("expected 0 items, got %d", len(packet.Items))
	}
}

func TestBrokerFetch_SingleSource(t *testing.T) {
	src := &mockSource{
		name: "test",
		items: []ContextItem{
			{Source: "test", Key: "a", Content: "hello", TokenEstimate: 2, Relevance: 0.9},
			{Source: "test", Key: "b", Content: "world", TokenEstimate: 2, Relevance: 0.5},
		},
	}

	b := New(DefaultBudget(), src)
	packet, err := b.Fetch(context.Background(), Intent{Type: IntentCustom})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(packet.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(packet.Items))
	}
	// Items should be sorted by relevance (highest first).
	if packet.Items[0].Key != "a" {
		t.Errorf("expected highest relevance item first, got %s", packet.Items[0].Key)
	}
}

func TestBrokerFetch_BudgetEnforcement(t *testing.T) {
	src := &mockSource{
		name: "test",
		items: []ContextItem{
			{Source: "test", Key: "big", Content: strings.Repeat("x", 400), TokenEstimate: 100, Relevance: 0.9},
			{Source: "test", Key: "small", Content: "tiny", TokenEstimate: 1, Relevance: 0.5},
		},
	}

	budget := BudgetConfig{
		MaxTokens:     50,
		SourceWeights: map[string]float64{"test": 1.0},
	}

	b := New(budget, src)
	packet, err := b.Fetch(context.Background(), Intent{Type: IntentCustom})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The big item (100 tokens) exceeds the 50 token source budget, only small should fit.
	if len(packet.Items) != 1 {
		t.Errorf("expected 1 item (budget enforced), got %d", len(packet.Items))
	}
	if len(packet.Items) > 0 && packet.Items[0].Key != "small" {
		t.Errorf("expected small item, got %s", packet.Items[0].Key)
	}
}

func TestBrokerFetch_MultipleSources(t *testing.T) {
	conduit := &mockSource{
		name: "conduit",
		items: []ContextItem{
			{Source: "conduit", Key: "c1", Content: "conduit data", TokenEstimate: 5, Relevance: 0.8},
		},
	}
	pcc := &mockSource{
		name: "pcc",
		items: []ContextItem{
			{Source: "pcc", Key: "p1", Content: "pcc data", TokenEstimate: 5, Relevance: 0.9},
		},
	}

	b := New(DefaultBudget(), conduit, pcc)
	packet, err := b.Fetch(context.Background(), Intent{Type: IntentCustom})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(packet.Items) != 2 {
		t.Errorf("expected 2 items from 2 sources, got %d", len(packet.Items))
	}
	if len(packet.Manifest.SourcesUsed) != 2 {
		t.Errorf("expected 2 sources used, got %d", len(packet.Manifest.SourcesUsed))
	}
}

func TestBrokerFetch_SourceError(t *testing.T) {
	good := &mockSource{
		name: "good",
		items: []ContextItem{
			{Source: "good", Key: "g1", Content: "ok", TokenEstimate: 1, Relevance: 0.5},
		},
	}
	bad := &mockSource{
		name: "bad",
		err:  fmt.Errorf("connection refused"),
	}

	b := New(DefaultBudget(), good, bad)
	packet, err := b.Fetch(context.Background(), Intent{Type: IntentCustom})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should still return items from the good source.
	if len(packet.Items) != 1 {
		t.Errorf("expected 1 item (from good source), got %d", len(packet.Items))
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"Hi", 1},
		{"Hello world!!", 4}, // 13 chars → (13+3)/4 = 4
		{strings.Repeat("a", 400), 100},
	}
	for _, tt := range tests {
		got := EstimateTokens(tt.input)
		if got != tt.want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestBudgetForIntent(t *testing.T) {
	budget := BudgetForIntent(IntentWriteCode, 10000)
	if budget.MaxTokens != 10000 {
		t.Errorf("expected MaxTokens=10000, got %d", budget.MaxTokens)
	}
	// write_code should prioritize PCC.
	if budget.SourceWeights["pcc"] < budget.SourceWeights["engine"] {
		t.Error("write_code intent should prioritize pcc over engine")
	}
}

func TestFormatPacket(t *testing.T) {
	packet := &ContextPacket{
		Items: []ContextItem{
			{Source: "conduit", Key: "app/test", Content: "test data"},
			{Source: "pcc", Key: "mentat/00_project.md", Content: "project info"},
		},
	}

	result := FormatPacket(packet)
	if !strings.Contains(result, "conduit") {
		t.Error("expected conduit section in formatted output")
	}
	if !strings.Contains(result, "pcc") {
		t.Error("expected pcc section in formatted output")
	}
	if !strings.Contains(result, "test data") {
		t.Error("expected conduit content in formatted output")
	}
}

func TestFormatPacket_Nil(t *testing.T) {
	if FormatPacket(nil) != "" {
		t.Error("nil packet should return empty string")
	}
	if FormatPacket(&ContextPacket{}) != "" {
		t.Error("empty packet should return empty string")
	}
}

func TestAllocateBudgets(t *testing.T) {
	src1 := &mockSource{name: "a"}
	src2 := &mockSource{name: "b"}

	b := New(BudgetConfig{
		MaxTokens: 1000,
		SourceWeights: map[string]float64{
			"a": 0.7,
			"b": 0.3,
		},
	}, src1, src2)

	budgets := b.allocateBudgets()
	if budgets["a"] != 700 {
		t.Errorf("expected a=700, got %d", budgets["a"])
	}
	if budgets["b"] != 300 {
		t.Errorf("expected b=300, got %d", budgets["b"])
	}
}
