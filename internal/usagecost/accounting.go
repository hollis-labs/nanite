// Package usagecost preserves provider accounting and immutable catalog prices.
package usagecost

import (
	"encoding/json"
	"strings"
	"time"

	llmtypes "github.com/hollis-labs/go-llm-types"
	costcalc "github.com/hollis-labs/go-modelsdev-catalog-helpers"
	ledger "github.com/hollis-labs/go-usage-ledger"
	"github.com/hollis-labs/nanite/pkg/models"
)

// Report retains presence: nil is unreported, while a pointer to zero is measured.
// Counts are cumulative within one provider call, never between calls.
type Report struct {
	Input      *int64 `json:"input,omitempty"`
	Output     *int64 `json:"output,omitempty"`
	CacheRead  *int64 `json:"cache_read,omitempty"`
	CacheWrite *int64 `json:"cache_write,omitempty"`
	Reasoning  *int64 `json:"reasoning,omitempty"`
	// models.dev exposes one cache-write rate, not the higher 1h TTL rate.
	CacheWriteRateUnknown bool `json:"cache_write_rate_unknown,omitempty"`
}

type payload struct {
	Version int    `json:"nanite_accounting_version"`
	Report  Report `json:"report"`
}

// Content carries adapter-only metadata on EventUsage. It is consumed before
// chat events reach the UI; go-llm-types v0.5.1 cannot express these fields.
// Remove this side channel when the shared Usage type carries count presence
// and reasoning tokens; the upstream follow-up is tracked in Torque.
func Content(r Report) string {
	data, _ := json.Marshal(payload{Version: 1, Report: r})
	return string(data)
}

func Parse(content string) (Report, bool) {
	var p payload
	err := json.Unmarshal([]byte(content), &p)
	return p.Report, err == nil && p.Version == 1
}

func (r *Report) Merge(next Report) {
	r.CacheWriteRateUnknown = r.CacheWriteRateUnknown || next.CacheWriteRateUnknown
	for _, pair := range []struct {
		dst **int64
		src *int64
	}{
		{&r.Input, next.Input}, {&r.Output, next.Output}, {&r.CacheRead, next.CacheRead},
		{&r.CacheWrite, next.CacheWrite}, {&r.Reasoning, next.Reasoning},
	} {
		if pair.src != nil {
			value := *pair.src
			*pair.dst = &value
		}
	}
}

// FromRaw reads the actual JSON rather than the SDK's zero-valued fields.
func FromRaw(provider, raw string) Report {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &fields) != nil {
		return Report{}
	}
	number := func(m map[string]json.RawMessage, key string) *int64 {
		var n *int64
		if json.Unmarshal(m[key], &n) != nil || n == nil || *n < 0 {
			return nil
		}
		return n
	}
	details := func(key string) map[string]json.RawMessage {
		var m map[string]json.RawMessage
		_ = json.Unmarshal(fields[key], &m)
		return m
	}
	if provider == "anthropic" {
		r := Report{Input: number(fields, "input_tokens"), Output: number(fields, "output_tokens"), CacheRead: number(fields, "cache_read_input_tokens"), CacheWrite: number(fields, "cache_creation_input_tokens")}
		r.Reasoning = number(details("output_tokens_details"), "thinking_tokens")
		// Anthropic SDK Usage.CacheCreation distinguishes 5m and 1h writes.
		// https://platform.claude.com/docs/en/build-with-claude/prompt-caching
		if oneHour := number(details("cache_creation"), "ephemeral_1h_input_tokens"); oneHour != nil && *oneHour > 0 {
			r.CacheWriteRateUnknown = true
		}
		return r
	}
	input, output, inDetails, outDetails := "input_tokens", "output_tokens", "input_tokens_details", "output_tokens_details"
	if _, exists := fields["prompt_tokens"]; exists {
		input, output, inDetails, outDetails = "prompt_tokens", "completion_tokens", "prompt_tokens_details", "completion_tokens_details"
	}
	return Report{Input: number(fields, input), Output: number(fields, output), CacheRead: number(details(inDetails), "cached_tokens"), CacheWrite: number(details(inDetails), "cache_write_tokens"), Reasoning: number(details(outDetails), "reasoning_tokens")}
}

// Fallback cannot recover measured zeroes from the older shared Usage type.
func Fallback(u *llmtypes.Usage) Report {
	if u == nil {
		return Report{}
	}
	positive := func(n int) *int64 {
		if n <= 0 {
			return nil
		}
		v := int64(n)
		return &v
	}
	return Report{Input: positive(u.InputTokens), Output: positive(u.OutputTokens), CacheRead: positive(u.CacheReadTokens), CacheWrite: positive(u.CacheCreationTokens)}
}

func component(n *int64) ledger.Component {
	if n == nil {
		return ledger.Component{Provenance: ledger.ProvenanceUnknown}
	}
	return ledger.Component{Tokens: *n, Provenance: ledger.ProvenanceMeasured}
}

func (r Report) Usage(provider string) ledger.Usage {
	u := ledger.NewUsage()
	u.UncachedInputTokens = component(r.Input)
	u.OutputTokens = component(r.Output)
	u.CacheReadTokens = component(r.CacheRead)
	u.CacheWriteTokens = component(r.CacheWrite)
	u.ReasoningTokens = component(r.Reasoning)
	// Captured go-providers v0.42.0 codex/app_server_turn.transcript.jsonl:
	// totalTokens=14248 = inputTokens=14242 + outputTokens=6, with
	// cachedInputTokens=13056 included in input. pty_codex_events.go passes
	// those inclusive input counts and cached counts through separately.
	cli := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(provider, "pty-"), "sub-"), "acp-")
	inputIncludesCache := provider == "openai" || cli == "codex"
	if inputIncludesCache && r.Input != nil {
		cache := u.CacheReadTokens.Tokens + u.CacheWriteTokens.Tokens
		if cache > *r.Input {
			// Keep the inclusive input count, but the impossible subdivisions
			// are unknown. Never emit negatives or lose the entire usage row.
			u.CacheReadTokens = component(nil)
			u.CacheWriteTokens = component(nil)
		} else {
			u.UncachedInputTokens.Tokens -= cache
		}
		if r.CacheRead == nil || r.CacheWrite == nil {
			u.UncachedInputTokens.Provenance = ledger.ProvenanceEstimated
		}
	}
	// Both HTTP providers bill reasoning within output. Separate it once.
	if (provider == "openai" || provider == "anthropic" || cli == "codex") && r.Output != nil {
		if u.ReasoningTokens.Tokens > *r.Output {
			u.ReasoningTokens = component(nil)
		} else {
			u.OutputTokens.Tokens -= u.ReasoningTokens.Tokens
		}
		if r.Reasoning == nil {
			u.OutputTokens.Provenance = ledger.ProvenanceEstimated
		}
	}
	return u
}

// Apply retains measured counts while withholding an unsupported cache tariff.
func (r Report) Apply(row *ledger.Row) {
	row.Usage = r.Usage(row.Provider)
	if r.CacheWriteRateUnknown && row.Price != nil {
		price := *row.Price
		price.CacheWritePerMillion = 0
		row.Price = &price
	}
}

// NewRow snapshots prices once per call. Only archived registry entries are a
// fallback for catalog misses; an active model's stale registry rate is unsafe.
func NewRow(cat costcalc.Catalog, provider, model string) ledger.Row {
	row := ledger.Row{Provider: provider, Model: model, Usage: ledger.NewUsage(), RecordedAt: time.Now().UTC()}
	var snap ledger.PriceSnapshot
	var found bool
	if cat != nil {
		snap, found = costcalc.SnapshotPrice(cat, provider, model)
	}
	if !found {
		if m, ok := models.ByModelID(model); ok && m.IsLegacy && m.Provider == provider {
			snap = ledger.PriceSnapshot{InputPerMillion: m.InputPricePerM, OutputPerMillion: m.OutputPricePerM}
			found = true
		}
	}
	if found && (snap.InputPerMillion != 0 || snap.OutputPerMillion != 0 || snap.CacheReadPerMillion != 0 || snap.CacheWritePerMillion != 0 || snap.ReasoningPerMillion != 0) {
		// Reasoning is billed at output rates by these providers. The catalog's
		// optional zero cannot distinguish an omitted reasoning rate from free.
		if snap.ReasoningPerMillion == 0 && (provider == "openai" || provider == "anthropic") {
			snap.ReasoningPerMillion = snap.OutputPerMillion
		}
		row.Price = &snap
	}
	return row
}

// Snapshot stays ON the usage row. Calls keep their own counts and rates so
// omitted components in one call never become measured zero through aggregation.
type Snapshot struct {
	Version int             `json:"version"`
	Calls   []ledger.Row    `json:"calls"`
	Costs   []costcalc.Cost `json:"costs"`
	Status  string          `json:"status"`
}

func Freeze(calls []ledger.Row) (Snapshot, float64, error) {
	snapshot := Snapshot{Version: 1, Calls: calls, Status: "COMPLETE", Costs: make([]costcalc.Cost, 0, len(calls))}
	var total float64
	if len(calls) == 0 {
		snapshot.Status = "PARTIAL"
	}
	for _, row := range calls {
		if err := row.Usage.Validate(); err != nil {
			return Snapshot{}, 0, err
		}
		cost := costcalc.PriceRow(row)
		snapshot.Costs = append(snapshot.Costs, cost)
		total += cost.Total()
		if !cost.Priced || cost.Provenance != ledger.ProvenanceMeasured {
			snapshot.Status = "PARTIAL"
		}
		// Optional catalog zeroes are ambiguous. A reported nonzero component
		// without a known rate must never be represented as fully priced.
		if row.Price != nil {
			if (row.Usage.UncachedInputTokens.Tokens > 0 && row.Price.InputPerMillion == 0) || (row.Usage.OutputTokens.Tokens > 0 && row.Price.OutputPerMillion == 0) || (row.Usage.CacheReadTokens.Tokens > 0 && row.Price.CacheReadPerMillion == 0) || (row.Usage.CacheWriteTokens.Tokens > 0 && row.Price.CacheWritePerMillion == 0) || (row.Usage.ReasoningTokens.Tokens > 0 && row.Price.ReasoningPerMillion == 0) {
				snapshot.Status = "PARTIAL"
			}
		}
	}
	return snapshot, total, nil
}
