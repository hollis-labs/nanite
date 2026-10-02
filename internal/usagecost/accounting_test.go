package usagecost

import (
	"math"
	"testing"

	"github.com/hollis-labs/go-modelsdev/modelsdev"
	ledger "github.com/hollis-labs/go-usage-ledger"
)

type testCatalog struct{ rates modelsdev.Pricing }

func (c *testCatalog) Get(_, _ string) (modelsdev.Model, bool) {
	return modelsdev.Model{Cost: c.rates}, true
}

// Provider rules: OpenAI cached tokens are part of prompt input, reasoning is
// part of output: https://developers.openai.com/api/docs/guides/agents-api/observability
// https://developers.openai.com/api/docs/guides/reasoning
// Anthropic cache is additional to uncached input; thinking is part of output:
// https://platform.claude.com/docs/en/build-with-claude/prompt-caching
// https://platform.claude.com/docs/en/build-with-claude/extended-thinking
func TestProviderInclusionRules(t *testing.T) {
	for _, tc := range []struct {
		name, raw    string
		input, total int64
		cost         float64
	}{
		{"openai", `{"prompt_tokens":1000,"completion_tokens":300,"prompt_tokens_details":{"cached_tokens":250,"cache_write_tokens":100},"completion_tokens_details":{"reasoning_tokens":200}}`, 650, 1300, .004},
		{"anthropic", `{"input_tokens":1000,"output_tokens":300,"cache_read_input_tokens":250,"cache_creation_input_tokens":100,"output_tokens_details":{"thinking_tokens":200}}`, 1000, 1650, .0047},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := FromRaw(tc.name, tc.raw)
			roundtrip, ok := Parse(Content(report))
			if !ok {
				t.Fatal("accounting payload lost version")
			}
			row := NewRow(&testCatalog{modelsdev.Pricing{Input: 2, Output: 8, CacheRead: .2, CacheWrite: 2.5}}, tc.name, "fixture")
			row.Usage = roundtrip.Usage(tc.name)
			if row.Usage.UncachedInputTokens.Tokens != tc.input || row.Usage.OutputTokens.Tokens != 100 || row.Usage.ReasoningTokens.Tokens != 200 || row.Usage.TotalTokens() != tc.total {
				t.Fatalf("normalized usage: %+v", row.Usage)
			}
			snapshot, cost, err := Freeze([]ledger.Row{row})
			if err != nil || snapshot.Status != "COMPLETE" || math.Abs(cost-tc.cost) > 1e-12 {
				t.Fatalf("cost=%v status=%s err=%v", cost, snapshot.Status, err)
			}
		})
	}
}

func TestResponsesAccountingAndUnknownPresence(t *testing.T) {
	measured := FromRaw("openai", `{"input_tokens":1000,"output_tokens":300,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens_details":{"reasoning_tokens":0}}`)
	omitted := FromRaw("openai", `{"input_tokens":1000,"output_tokens":300}`)
	for _, tc := range []struct {
		name   string
		report Report
		status string
	}{
		{"reported zero", measured, "COMPLETE"}, {"omitted", omitted, "PARTIAL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := NewRow(&testCatalog{modelsdev.Pricing{Input: 2, Output: 8}}, "openai", "fixture")
			row.Usage = tc.report.Usage("openai")
			snapshot, cost, err := Freeze([]ledger.Row{row})
			if err != nil || snapshot.Status != tc.status || math.Abs(cost-.0044) > 1e-12 {
				t.Fatalf("snapshot=%+v cost=%v err=%v", snapshot, cost, err)
			}
			if tc.status == "PARTIAL" && row.Usage.ReasoningTokens.Provenance != ledger.ProvenanceUnknown {
				t.Fatal("omitted reasoning became measured zero")
			}
		})
	}
}

func TestAnthropicCumulativeUsageAndFinalThinking(t *testing.T) {
	var r Report
	r.Merge(FromRaw("anthropic", `{"input_tokens":10,"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":1}`))
	r.Merge(FromRaw("anthropic", `{"output_tokens":20}`))
	r.Merge(FromRaw("anthropic", `{"output_tokens":100,"output_tokens_details":{"thinking_tokens":60}}`))
	u := r.Usage("anthropic")
	if u.OutputTokens.Tokens != 40 || u.ReasoningTokens.Tokens != 60 || u.UncachedInputTokens.Tokens != 10 || u.TotalTokens() != 110 {
		t.Fatalf("cumulative counts summed twice: %+v", u)
	}
}

func TestSnapshotPriceRefreshAndLegacy(t *testing.T) {
	cat := &testCatalog{modelsdev.Pricing{Input: 2, Output: 8}}
	row := NewRow(cat, "openai", "fixture")
	row.Usage = FromRaw("openai", `{"input_tokens":1000,"output_tokens":300,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"output_tokens_details":{"reasoning_tokens":100}}`).Usage("openai")
	cat.rates = modelsdev.Pricing{Input: 200, Output: 800}
	_, cost, err := Freeze([]ledger.Row{row})
	if err != nil || math.Abs(cost-.0044) > 1e-12 {
		t.Fatalf("repriced old row: cost=%v err=%v", cost, err)
	}
	legacy := NewRow(nil, "anthropic", "claude-sonnet-4-20250514")
	if legacy.Price == nil || legacy.Price.InputPerMillion != 3 || legacy.Price.OutputPerMillion != 15 {
		t.Fatalf("archived price lost: %+v", legacy)
	}
	unknown := NewRow(nil, "openai", "unknown-model")
	snapshot, _, err := Freeze([]ledger.Row{unknown})
	if err != nil || snapshot.Status != "PARTIAL" || snapshot.Costs[0].Priced {
		t.Fatalf("missing model marked free: %+v err=%v", snapshot, err)
	}
}

func TestInconsistentProviderCountsRejected(t *testing.T) {
	row := NewRow(nil, "openai", "fixture")
	row.Usage = FromRaw("openai", `{"input_tokens":10,"input_tokens_details":{"cached_tokens":20},"output_tokens":5,"output_tokens_details":{"reasoning_tokens":10}}`).Usage("openai")
	if _, _, err := Freeze([]ledger.Row{row}); err == nil {
		t.Fatal("overlapping counts exceeding inclusive total accepted")
	}
}
