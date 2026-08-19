package toolclient

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	llmtypes "github.com/hollis-labs/go-llm-types"
)

// RankingSignals aggregates the contextual signals that contribute to a
// reasoning-augmented broker selection pass. Each signal is independently
// optional — a nil/empty signal contributes 0 to every tool's score, which
// preserves the legacy "keyword only" behaviour when neither memory nor
// skills are wired.
//
// Precedence (per ADR-003):
//
//	skills (5)  >  memory (3)  >  keyword (1-2)
//
// All three contribute additively; the result is sorted descending. A tool
// with one skill hit (5) outranks a tool with three keyword hits (max 6 if
// each kw is in name+desc — but typical is 2-3 total). One skill + one
// keyword hit (5+2=7) outranks two skill hits with no keyword (10), no —
// the math: skills are STRONG, memory is meaningful, keyword is the floor.
//
// "Err toward more not less" bias is applied at the end: when there is
// remaining token budget after the strict ranking caps, additional candidates
// from the keyword pool that didn't make the cut are appended at lower
// rank — better to load one extra description than to make the LLM call
// request_tools again.
type RankingSignals struct {
	Skills       []ToolPreferenceSkill
	MemoryHits   []ToolPatternHit
	KeywordTools []llmtypes.ToolDefinition // pre-scored by SelectByIntent
	// BrokerResult is the full selection candidate set (ToolClient.catalogTools,
	// née go-toolbroker's LocalBroker.SelectTools result — see decision log
	// §11 / Phase 0 item 22). Name kept for call-site stability.
	BrokerResult []llmtypes.ToolDefinition
}

// ScoredTool is a tool with its aggregated rank score and the per-signal
// breakdown. The breakdown is preserved for diagnostics: when an operator
// asks "why did selection pick X?", we can answer.
type ScoredTool struct {
	Tool         llmtypes.ToolDefinition
	Score        int
	SkillScore   int
	MemoryScore  int
	KeywordScore int
	SkillSources []string // skill file paths that contributed
}

// RankTools combines the ranking signals into a single ordered slice of
// scored tools. Higher score first; ties broken by the original catalog
// order (ToolClient.catalogTools' registration order, name-stable).
//
// Tools that appear in BrokerResult but score zero from every signal are
// retained at the tail with score 0 — they are the "neutral candidates"
// the err-toward-more bias may pad onto the final selection if budget
// permits.
func RankTools(s RankingSignals) []ScoredTool {
	// Build a lookup from name → tool definition. BrokerResult is the
	// authoritative tool definition (description, schema); ranking
	// signals reference tools by name.
	defByName := make(map[string]llmtypes.ToolDefinition, len(s.BrokerResult))
	for _, t := range s.BrokerResult {
		defByName[t.Name] = t
	}

	// Skill score: each matching skill adds its weight to every tool in
	// its prefer-list.
	skillScores := make(map[string]int)
	skillSources := make(map[string][]string)
	for _, sk := range s.Skills {
		w := sk.Weight
		if w <= 0 {
			w = DefaultSkillWeight
		}
		for _, name := range sk.Prefer {
			skillScores[name] += w
			skillSources[name] = append(skillSources[name], sk.Source)
		}
	}

	// Memory score: each hit adds DefaultMemoryWeight × confidence.
	memoryScores := make(map[string]int)
	for _, h := range s.MemoryHits {
		w := DefaultMemoryWeight
		if h.Confidence > 0 && h.Confidence < 1 {
			// Round-half-up scaling — confidence 0.7 → 2/3 of weight.
			scaled := int(float64(w)*h.Confidence + 0.5)
			if scaled < 1 {
				scaled = 1
			}
			w = scaled
		}
		memoryScores[h.ToolName] += w
	}

	// Keyword score: SelectByIntent already returns scored+sorted; we
	// re-derive a relative score by index (top => highest). The actual
	// numeric score is bounded by len(KeywordTools) but capped at 2 so it
	// stays within the keyword tier.
	keywordScores := make(map[string]int)
	for i, t := range s.KeywordTools {
		score := 2
		if i >= len(s.KeywordTools)/2 {
			score = 1
		}
		keywordScores[t.Name] = score
	}

	// Union of every name we care about.
	nameSet := make(map[string]struct{})
	for n := range skillScores {
		nameSet[n] = struct{}{}
	}
	for n := range memoryScores {
		nameSet[n] = struct{}{}
	}
	for n := range keywordScores {
		nameSet[n] = struct{}{}
	}
	for _, t := range s.BrokerResult {
		nameSet[t.Name] = struct{}{}
	}

	out := make([]ScoredTool, 0, len(nameSet))
	for name := range nameSet {
		def, ok := defByName[name]
		if !ok {
			// A skill or memory hit references a tool the broker didn't
			// surface. Skip it — we cannot send a tool we don't have a
			// definition for. Log so an operator can spot drift between
			// their skills file and the live tool registry.
			if _, fromSkill := skillScores[name]; fromSkill {
				slog.Debug("toolclient: skill references unknown tool; ignoring",
					"tool", name, "sources", skillSources[name])
			}
			continue
		}
		st := ScoredTool{
			Tool:         def,
			SkillScore:   skillScores[name],
			MemoryScore:  memoryScores[name],
			KeywordScore: keywordScores[name],
			SkillSources: skillSources[name],
		}
		st.Score = st.SkillScore + st.MemoryScore + st.KeywordScore
		out = append(out, st)
	}

	// Stable secondary key: original catalog order. Build an index map.
	catalogOrder := make(map[string]int, len(s.BrokerResult))
	for i, t := range s.BrokerResult {
		catalogOrder[t.Name] = i
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		oi, ok1 := catalogOrder[out[i].Tool.Name]
		oj, ok2 := catalogOrder[out[j].Tool.Name]
		if ok1 && ok2 {
			return oi < oj
		}
		if ok1 != ok2 {
			return ok1 // tools with catalog ordering precede orphans
		}
		return out[i].Tool.Name < out[j].Tool.Name
	})

	return out
}

// SelectWithSignals is the reasoning-augmented selection entry point. It
// runs the base selection pass (keyword scoring via SelectByIntent),
// gathers skills + memory signals, ranks the union, applies the token
// budget with the "err toward more" bias, and returns the final tool
// slice + diagnostic signals JSON.
//
// extraNamesPad caps how many neutral-score candidates the bias may add.
// Default 3 — enough to absorb the typical "I forgot one helper" miss
// without bloating context. Pass 0 to disable the bias.
func (tb *ToolClient) SelectWithSignals(
	ctx context.Context,
	intent string,
	hints []string,
	workspaceID, agentID string,
	windowSize int,
	skills []ToolPreferenceSkill,
	memHits []ToolPatternHit,
	extraNamesPad int,
) ([]llmtypes.ToolDefinition, string, error) {
	// Run the base selection pass (full catalog, capped + token-pruned).
	tools, err := tb.SelectTools(ctx, intent, hints, workspaceID, agentID, windowSize)
	if err != nil {
		return nil, "", err
	}

	// Score the union with all signals.
	keywordTools := tb.SelectByIntent(intent, MaxSelectedTools)
	scored := RankTools(RankingSignals{
		Skills:       MatchingSkills(skills, intent),
		MemoryHits:   memHits,
		KeywordTools: keywordTools,
		BrokerResult: tools,
	})

	// Apply token budget. Compute the budget the same way SelectTools does
	// so the err-toward-more bias respects the same ceiling, just with a
	// touch more headroom on the upper bound (per CW-20260426-0010 #3:
	// "broker errs toward 'a little more' not 'a little less'").
	budgetPct := tb.Config.ToolTokenBudgetPct
	if budgetPct <= 0 {
		budgetPct = DefaultToolTokenBudgetPct
	}
	ctxWindow := windowSize
	if ctxWindow <= 0 {
		ctxWindow = tb.Config.ContextWindowTokens
	}
	if ctxWindow <= 0 {
		ctxWindow = DefaultContextWindowTokens
	}
	tokenBudget := int(budgetPct * float64(ctxWindow))

	// Strict tier: tools with a non-zero score, in rank order.
	// When NO signals scored anything (no skills, no memory, no keyword
	// hits), fall back to the base selection result so we don't regress
	// the keyword-only path. The augmented selection should never load
	// fewer tools than the legacy path.
	strict := make([]llmtypes.ToolDefinition, 0, len(scored))
	for _, st := range scored {
		if st.Score > 0 {
			strict = append(strict, st.Tool)
		}
	}
	if len(strict) == 0 {
		strict = tools
	}
	if len(strict) > MaxSelectedTools {
		strict = strict[:MaxSelectedTools]
	}

	// Build a quick membership set for the strict tier so the bias tier
	// doesn't double-add.
	inStrict := make(map[string]bool, len(strict))
	for _, t := range strict {
		inStrict[t.Name] = true
	}

	// Bias tier: append up to extraNamesPad zero-score candidates while the
	// budget still admits them. We deliberately do this AFTER capping at
	// MaxSelectedTools — the bias pads beyond strict relevance, which is
	// where the "err toward more" axis lives.
	final := strict
	if extraNamesPad > 0 {
		added := 0
		for _, st := range scored {
			if added >= extraNamesPad {
				break
			}
			if st.Score > 0 || inStrict[st.Tool.Name] {
				continue
			}
			// Prospective add: check budget tolerance.
			candidate := append(final, st.Tool) //nolint:gocritic // intentional copy for budget check
			if EstimateToolTokens(candidate) > tokenBudget {
				break
			}
			final = candidate
			inStrict[st.Tool.Name] = true
			added++
		}
		if added > 0 {
			slog.Info("toolclient: err-toward-more bias added candidates",
				"added", added, "budget", tokenBudget, "intent", intent)
		}
	}

	// Final budget enforcement (the strict tier itself may already exceed
	// the budget if a skill loaded an oversized tool; PruneToolsToTokenBudget
	// removes from the tail until it fits, keeping at least one).
	final = PruneToolsToTokenBudget(final, tokenBudget)

	// Diagnostic signals JSON for the broker_decisions row. Compact key
	// names so the payload stays readable in a debug panel.
	signals := signalsJSON(scored, len(final))

	slog.Info("toolclient: reasoning-augmented selection",
		"intent", intent, "agent", agentID, "ws", workspaceID,
		"skills_matched", len(MatchingSkills(skills, intent)),
		"memory_hits", len(memHits),
		"final", len(final), "budget", tokenBudget,
		"ctx_window", ctxWindow, "tool_tokens", EstimateToolTokens(final),
		"err_toward_more_pad", extraNamesPad,
	)
	return final, signals, nil
}

// signalsJSON builds a tiny diagnostic blob for the broker_decisions row.
// We keep it small (top-3 only) — full per-tool breakdown lives in slog.
func signalsJSON(scored []ScoredTool, finalCount int) string {
	limit := 3
	if len(scored) < limit {
		limit = len(scored)
	}
	var b strings.Builder
	fmt.Fprintf(&b, `{"final":%d,"top":[`, finalCount)
	for i := 0; i < limit; i++ {
		st := scored[i]
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b,
			`{"name":%q,"score":%d,"sk":%d,"mem":%d,"kw":%d}`,
			st.Tool.Name, st.Score, st.SkillScore, st.MemoryScore, st.KeywordScore,
		)
	}
	b.WriteString("]}")
	return b.String()
}

// DefaultErrTowardMorePad is the default number of zero-score candidates the
// reasoning-augmented selection adds when the token budget admits them. Per
// CW-20260426-0010 #3, the broker errs toward "a little more" — losing one
// extra ~14-token description is much cheaper than the LLM round-tripping
// through request_tools because we under-loaded.
//
// Override on a per-Config basis via Config.ErrTowardMorePad.
const DefaultErrTowardMorePad = 3
