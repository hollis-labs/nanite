// Package skillbroker — ranked skill selection for context-window assembly.
//
// SP-20260512-0008 W2B (CW-20260512-0106): the Skill Broker is the fourth
// member of the broker quartet (Context, Tool, Skill, Agent). It is a
// READ-ONLY consumer of the agent's candidate skill set; it does NOT
// refactor the skill registry, the skill loader, or any persistence layer.
//
// Architectural seat. The Context Broker (internal/contextbroker) decides
// which slots ship per turn. The Tool Broker (internal/toolclient) ranks
// the tool set fed into the Tools slot. The Skill Broker ranks the skill
// set fed into the Agent slot's "Available skills:" block. All three are
// pure functions over a candidate set + an intent + an agent identity —
// no I/O, no DB writes, deterministic for the same inputs.
//
// Selection criteria (v1, intentionally simple):
//
//  1. Keyword match: intent keywords / query text against skill name +
//     description + category. Hits earn points.
//  2. Agent-tag match: the agent's tag list against skill category +
//     name. A planner agent gets boost for planning-named skills, a
//     researcher gets boost for research-named skills, etc.
//  3. Mode-binding bonus: skills bound to the current mode rank above
//     mode-agnostic skills when a mode is set. (Mode filtering itself
//     happens UPSTREAM — the broker assumes Caller did E2 filtering.)
//  4. Source bias: builtin > user > project > plugin when scores tie.
//     Keeps the system feel stable when novel skills land.
//
// The heuristic is the seat for future ranking improvements (embedding
// similarity, learned weights, memory-signal carry-over). v1 is the
// floor — any future model can replace `Score` while preserving the
// SelectSkills signature.
//
// Cap. SelectSkills returns at most MaxSelectedSkills (default 25 —
// matches chat.SkillEssentialCap so we don't widen the inline budget).
// Override via Options.MaxSkills when a caller (CLI, test) needs a
// different ceiling. Zero means "use default".
//
// Pre-launch contract per `feedback_no_compat_shims`: when the broker
// trims a skill, the skill is simply not in the returned slice. No
// "deprecated" marker, no fallback to "the old way". The legacy
// "every assigned skill renders" path is dead code after this lands;
// callers MUST go through SelectSkills.
package skillbroker

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/hollis-labs/nanite/internal/contextbroker"
	"github.com/hollis-labs/nanite/internal/store"
)

// MaxSelectedSkills is the default ceiling for the ranked subset returned
// by SelectSkills. Matches chat.SkillEssentialCap so the broker preserves
// the existing inline-budget shape — overflow is folded into the
// discoverability LoadHint by the renderer (chat.skillCatalogLoadHint).
const MaxSelectedSkills = 25

// Per-signal weights — additive. Keyword is the floor (intent keyword
// matched skill name/desc/category), agent-tag is the mid-tier (agent
// identity preference), mode-binding is a small bonus.
const (
	// keywordWeight is awarded per keyword hit on name/description/category.
	keywordWeight = 2

	// queryTextWeight is awarded for any substring hit between intent.QueryText
	// tokens and skill name/description. Lower than keywordWeight because the
	// query text is noisier (full user turn) than extracted keywords.
	queryTextWeight = 1

	// agentTagWeight is awarded when one of the agent's tags matches the
	// skill's category or appears as a substring in the skill's name.
	agentTagWeight = 3

	// modeBoundWeight is a small bonus when the skill is mode-bound (its
	// ModeIDs field is non-empty / non-"[]"). Mode-bound skills are
	// already mode-filtered upstream, but the boost signals "the operator
	// specifically scoped this to a mode" — i.e. it's more curated than
	// a mode-agnostic skill.
	modeBoundWeight = 1
)

// AgentIdentity is the broker's read-only view of the requesting agent.
// Carries only the fields the ranking heuristic needs — keeps the
// SelectSkills surface narrow and stable as the underlying agent record
// evolves.
type AgentIdentity struct {
	// ID is the agent's persistent identifier (rarely used in v1; kept
	// for telemetry/debug).
	ID string
	// Slug is the agent's stable identifier ("planner", "worker",
	// "researcher", etc.) — primary input to the tag-derived role match.
	Slug string
	// Tags is the agent's tag set parsed from store.AgentProfile.Tags.
	// Used directly by the heuristic; empty tags is fine (zero contribution).
	Tags []string
}

// Options carries optional per-call overrides for SelectSkills. Zero
// values mean "use defaults". The struct is value-typed so callers can
// build it inline without worrying about nil-checks.
type Options struct {
	// MaxSkills caps the returned slice. Zero → MaxSelectedSkills.
	MaxSkills int
}

// ScoredSkill carries a skill plus its aggregate score and the per-signal
// breakdown. Returned by SelectSkillsScored for callers (mainly tests +
// telemetry) that want the rationale. SelectSkills strips the breakdown.
type ScoredSkill struct {
	Skill        store.Skill
	Score        int
	KeywordScore int
	QueryScore   int
	AgentScore   int
	ModeScore    int
}

// SelectSkills is the broker's primary entry point. Given a candidate
// skill set (already mode/agent-filtered by the caller), an intent, and
// the requesting agent identity, returns the ranked top-N subset.
//
// The function is deterministic: same inputs → same outputs. No I/O,
// no DB writes. The candidate slice is not mutated; SelectSkills
// returns a freshly allocated slice. Safe to call concurrently for
// different sessions.
//
// When candidates is empty or len(candidates) <= cap, the function
// still runs the ranker — caller can rely on the output being in
// stable rank order rather than insertion order, which makes the
// "Available skills:" block render deterministically across turns
// (preserves cacheable_prefix_tokens for slot prefixes that include
// the skill list — currently SlotAgent).
//
// Cancellation. The ctx is accepted for API symmetry with sibling
// brokers and to give future expensive rankers (e.g. embedding-based)
// a place to cancel. v1 ranking is O(n*m) on small inputs and ignores
// cancellation.
func SelectSkills(ctx context.Context, intent contextbroker.Intent, agent AgentIdentity, candidates []store.Skill, opts Options) []store.Skill {
	scored := SelectSkillsScored(ctx, intent, agent, candidates, opts)
	if len(scored) == 0 {
		return nil
	}
	out := make([]store.Skill, len(scored))
	for i, s := range scored {
		out[i] = s.Skill
	}
	return out
}

// SelectSkillsScored runs the same selection but returns the per-signal
// breakdown alongside each result. Equivalent to SelectSkills except
// for the return type — useful for telemetry, tests, and "why was this
// skill picked?" diagnostics.
func SelectSkillsScored(_ context.Context, intent contextbroker.Intent, agent AgentIdentity, candidates []store.Skill, opts Options) []ScoredSkill {
	if len(candidates) == 0 {
		return nil
	}
	cap := opts.MaxSkills
	if cap <= 0 {
		cap = MaxSelectedSkills
	}

	keywordSet := buildKeywordSet(intent.Keywords)
	queryTokens := tokenize(intent.QueryText)
	tagSet := buildKeywordSet(agent.Tags)
	if agent.Slug != "" {
		// Treat slug as an honorary tag — the heuristic should boost a
		// "research-foo" skill when the requesting agent is the
		// "researcher". Without this, a stub-tag agent (most builtin
		// roles today) would never get the role match.
		tagSet[strings.ToLower(agent.Slug)] = struct{}{}
	}

	scored := make([]ScoredSkill, 0, len(candidates))
	for _, sk := range candidates {
		s := ScoredSkill{Skill: sk}

		nameLC := strings.ToLower(sk.Name)
		descLC := strings.ToLower(sk.Description)
		catLC := strings.ToLower(sk.Category)

		// Keyword signal: each intent keyword that substring-matches any
		// of name/description/category earns keywordWeight. We dedupe per
		// keyword (a keyword matching both name and description still
		// only contributes once) — prevents a single noisy intent term
		// from dominating the rank.
		for kw := range keywordSet {
			if kw == "" {
				continue
			}
			if strings.Contains(nameLC, kw) || strings.Contains(descLC, kw) || strings.Contains(catLC, kw) {
				s.KeywordScore += keywordWeight
			}
		}

		// Query-text signal: tokens from intent.QueryText. Tokens that
		// are already in the keyword set are skipped (the keyword path
		// already credited them) so query-text doesn't double-dip the
		// same term. This keeps the floor distinct from the mid-tier.
		for _, tok := range queryTokens {
			if tok == "" {
				continue
			}
			if _, already := keywordSet[tok]; already {
				continue
			}
			if strings.Contains(nameLC, tok) || strings.Contains(descLC, tok) {
				s.QueryScore += queryTextWeight
			}
		}

		// Agent-tag signal: agent's tags (+ slug-as-honorary-tag) against
		// the skill's category and name tokens. Both directions of substring
		// containment are checked at the TOKEN level so "researcher" (agent
		// slug) lights up "research-codebase" (skill, name-token = "research"
		// — substring of "researcher"). Pure left-anchored containment is
		// too strict because slugs are typically nouns and skill names use
		// the verb root ("research" → "researcher").
		nameTokens := tokenizeNameOrCategory(nameLC)
		for tag := range tagSet {
			if tag == "" {
				continue
			}
			if catLC == tag {
				s.AgentScore += agentTagWeight
				continue
			}
			for _, tok := range nameTokens {
				if tok == "" {
					continue
				}
				// Bidirectional substring match between tag and the skill's
				// name token. Both must be ≥3 chars so we don't fire on
				// noise like "a" or "go".
				if len(tok) < 3 || len(tag) < 3 {
					continue
				}
				if strings.Contains(tok, tag) || strings.Contains(tag, tok) {
					s.AgentScore += agentTagWeight
					break
				}
			}
		}

		// Mode-bound bonus: skill carries a non-trivial mode_ids list.
		if isModeBound(sk.ModeIDs) {
			s.ModeScore = modeBoundWeight
		}

		s.Score = s.KeywordScore + s.QueryScore + s.AgentScore + s.ModeScore
		scored = append(scored, s)
	}

	// Sort. Primary by Score descending, then by source bias (builtin >
	// user > project > plugin), then by name ascending for stable order
	// across turns. The stable-order requirement is load-bearing: the
	// rendered "Available skills:" block lives in SlotAgent, which is
	// part of the cacheable prefix today (W1A intent is to keep it
	// stable across turns).
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}
		bi := sourceBias(scored[i].Skill.Source)
		bj := sourceBias(scored[j].Skill.Source)
		if bi != bj {
			return bi > bj
		}
		return scored[i].Skill.Name < scored[j].Skill.Name
	})

	if len(scored) > cap {
		scored = scored[:cap]
	}
	return scored
}

// buildKeywordSet lowercases + dedupes a slice of strings into a set.
// Empty inputs return an empty set (not nil) so callers can range over
// it unconditionally.
func buildKeywordSet(words []string) map[string]struct{} {
	out := make(map[string]struct{}, len(words))
	for _, w := range words {
		w = strings.ToLower(strings.TrimSpace(w))
		if w == "" {
			continue
		}
		out[w] = struct{}{}
	}
	return out
}

// tokenizeNameOrCategory splits a skill name or category on hyphen/underscore
// boundaries, returning lowercase tokens. Single-character tokens are kept
// here (unlike tokenize) because skill-name fragments are short by
// convention ("ai", "fe"). The function is deliberately separate from
// tokenize so the rules can diverge if real-world ranking signal demands it.
func tokenizeNameOrCategory(s string) []string {
	s = strings.ToLower(s)
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == '-' || r == '_' || r == ' ' || r == '\t' || r == '\n'
	})
	return fields
}

// tokenize splits a raw query string into lowercase word tokens. Strips
// non-alphanumeric separators. Returns nil for empty/whitespace inputs.
// Drops single-character tokens — "a", "i", "?" carry no ranking signal
// and would just inflate the loop.
func tokenize(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	s = strings.ToLower(s)
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' && r != '_'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) < 2 {
			continue
		}
		out = append(out, f)
	}
	return out
}

// isModeBound returns true when the skill's ModeIDs field carries a
// non-trivial JSON array. The persistence layer normalizes "no modes"
// as the literal "[]" string (per migration / E2 ingest); anything
// longer than 2 characters and not equal to "[]" is treated as bound.
// A malformed JSON value (shouldn't happen in production) is treated
// as not mode-bound so the broker fails open.
func isModeBound(modeIDs string) bool {
	modeIDs = strings.TrimSpace(modeIDs)
	if modeIDs == "" || modeIDs == "[]" {
		return false
	}
	var arr []string
	if err := json.Unmarshal([]byte(modeIDs), &arr); err != nil {
		return false
	}
	return len(arr) > 0
}

// sourceBias maps a skill source to a stable rank tiebreaker. Larger
// values rank earlier. Builtins are the most curated; plugins are the
// least vetted (operator-loaded code).
func sourceBias(source string) int {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "builtin":
		return 4
	case "user":
		return 3
	case "project":
		return 2
	case "claude":
		return 2
	case "plugin":
		return 1
	default:
		return 0
	}
}

// AgentIdentityFromProfile constructs an AgentIdentity from a *store.AgentProfile.
// Convenience helper for callers (chat-service) that hold the full agent
// record — keeps the conversion logic in one place and ensures tag-JSON
// parsing is consistent. Pass nil to get a zero-value AgentIdentity.
func AgentIdentityFromProfile(agent *store.AgentProfile) AgentIdentity {
	if agent == nil {
		return AgentIdentity{}
	}
	id := AgentIdentity{
		ID:   agent.ID,
		Slug: agent.Slug,
	}
	if agent.Tags == "" || agent.Tags == "[]" {
		return id
	}
	var tags []string
	if err := json.Unmarshal([]byte(agent.Tags), &tags); err != nil {
		// Tags column should always be a JSON array per the schema, but
		// rather than failing the whole selection on a malformed row
		// we treat tags as absent — the heuristic still works on
		// keyword + slug-as-honorary-tag.
		return id
	}
	id.Tags = tags
	return id
}
