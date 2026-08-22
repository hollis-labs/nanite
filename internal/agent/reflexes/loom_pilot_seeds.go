package reflexes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/store"
)

// AgentReflexSeed declares an AgentID-scoped reflex keyed by the target
// agent's SLUG rather than its resolved agent_profiles.id — the id is
// not known until agent.Discover + ReconcileManagedAgentIDs +
// AutoIngestAgents have run against .nanite/agents/*.md (container.go's
// boot sequence resolves it before SeedAgentReflexesBySlug is called;
// see container.go's ordering comment next to the call site). This is
// the AgentID-scoped sibling of BaseReflexSeed (class-scoped): where
// BaseSeeds()/SeedBaseReflexes() idempotently attach reflexes to every
// agent of a class, AgentReflexSeed/SeedAgentReflexesBySlug idempotently
// attach a reflex to one specific, named agent — for cases (like
// CW-20260816-0023's Loom Curator/Weaver pilot pair) where class-wide
// scoping would sweep in unrelated agents of the same class.
type AgentReflexSeed struct {
	AgentSlug   string
	Name        string
	TriggerKind string
	TriggerSpec map[string]interface{}
	ActionKind  string
	ActionSpec  map[string]interface{}
	Priority    int64
}

// nanitewikiTopicPattern anchors check_before_answer's text match to
// Nanite's own recurring subsystem vocabulary (see CLAUDE.md: envelope
// system, boot-profile CLI harness, MCP trust tiers, durable agents /
// agent_profiles, the slot system, the reflex engine itself, Loom /
// the wiki). Deliberately not a bare "nanite" match — Weaver's entire
// session is Nanite-scoped by construction, so a brand-only term would
// not discriminate anything; the AND-conjunction's real discriminating
// power comes from pairing this with tool_name_window below, but the
// topic clause itself is still kept to genuine subsystem vocabulary
// per the reflex-engine design discipline against loose single-term
// predicates (loom-architecture.md §8).
const naniteWikiTopicPattern = `(?i)\b(envelope|boot[- ]?profile|mcp trust|durable[- ]?agent|plugin (architecture|system|sdk)|slot system|context broker|tool[- ]result cache|agent_profiles|reflex(es)?|wiki|loom curator|loom weaver)\b`

// discoveryLanguagePattern anchors capture_on_discovery's text match to
// language that signals a agent has just surfaced something worth
// keeping, as named in loom-architecture.md §8 / CW-20260816-0023's
// brief ("discovered", "found that", "turns out", "worth documenting",
// "TIL").
const discoveryLanguagePattern = `(?i)\b(discovered|found that|turns out|worth documenting|worth capturing|worth keeping|noteworthy|TIL)\b`

// wikiOrLoomToolPattern matches the wiki_*/loom_* MCP tool-name family
// check_before_answer uses to confirm the agent hasn't already
// consulted the bundle this window.
const wikiOrLoomToolPattern = `^(wiki_|loom_)`

// LoomPilotReflexSeeds returns CW-20260816-0023's two-reflex pilot,
// scoped to Loom Curator's and Loom Weaver's resolved agent profiles —
// see the ticket's Step 1 findings for why: .nanite/config.yaml's
// project-agent personas (nanite-backend, nanite-frontend, ...) never
// receive an agent_profiles row and are never evaluated by
// reflexEngine.Evaluate, so they cannot host these reflexes; Curator
// and Weaver are the only reflex-engine-compatible, non-portfolio-wide
// candidates, accepted here as a first structural dry-run despite the
// circularity (they maintain the wiki bundle rather than consume it —
// tracked as a follow-up: a real tag-based agent_reflexes scope, or a
// genuine Nanite-dev-facing advisor agent, is the intended long-term
// target).
//
// check_before_answer is scoped to Weaver ONLY: Weaver is the agent
// that actually answers ("Do not answer broad user questions as your
// main output" is explicit in loom-curator.md's own system prompt —
// nudging Curator to "check before answering" contradicts its
// documented job).
//
// capture_on_discovery is scoped to BOTH Curator and Weaver: both
// agents' own procedures already describe "notice something worth
// keeping -> notify Fragments Engine's inbox via relay_*/message_*"
// (Weaver's propose_kb_update; Curator's write_or_stage_page and
// scheduled_lint_and_export) — the reflex is a same-shape safety-net
// nudge reinforcing behavior both classes are already supposed to
// perform, unlike check_before_answer's answer-specific semantics.
// Seeding the identical trigger/action spec against two different
// agent slugs also exercises the AgentID-scoped seeder against
// multiple real targets in one pass, not just one.
func LoomPilotReflexSeeds() []AgentReflexSeed {
	checkBeforeAnswerTrigger := map[string]interface{}{
		"kind": "AND",
		"clauses": []interface{}{
			map[string]interface{}{
				"kind":    "text_regex_window",
				"scope":   "user",
				"window":  2,
				"pattern": naniteWikiTopicPattern,
			},
			map[string]interface{}{
				"kind":    "tool_name_window",
				"window":  2,
				"mode":    "none",
				"pattern": wikiOrLoomToolPattern,
			},
		},
	}

	captureOnDiscoveryTrigger := map[string]interface{}{
		"kind": "AND",
		"clauses": []interface{}{
			map[string]interface{}{
				"kind":    "regex_match_window",
				"window":  2,
				"pattern": discoveryLanguagePattern,
			},
			map[string]interface{}{
				"kind":   "tool_calls_window",
				"window": 2,
				"op":     ">",
				"value":  0,
			},
		},
	}

	return []AgentReflexSeed{
		{
			AgentSlug:   "loom-weaver",
			Name:        "check_before_answer",
			TriggerKind: "predicate",
			Priority:    60,
			TriggerSpec: checkBeforeAnswerTrigger,
			ActionKind:  "inject_reminder",
			ActionSpec: map[string]interface{}{
				"body":    "Reflex check_before_answer: the recent question touches Nanite wiki-bundle topics and wiki_*/loom_* tools haven't been called yet this window. Search the nanite bundle via loom_page_search (then loom_page_get / loom_fetch_result / loom_search_result as needed) before answering from memory — per your own answer_with_sources procedure.",
				"urgency": "info",
			},
		},
		{
			AgentSlug:   "loom-weaver",
			Name:        "capture_on_discovery",
			TriggerKind: "predicate",
			Priority:    55,
			TriggerSpec: captureOnDiscoveryTrigger,
			ActionKind:  "inject_reminder",
			ActionSpec: map[string]interface{}{
				"body":    "Reflex capture_on_discovery: recent output reads like a fresh finding backed by real tool activity. If this surfaced a gap, staleness, or contradiction worth keeping in the nanite wiki bundle, drop a concise note into Fragments Engine's inbox now via relay_*/message_* (affected page, source refs, observed gap, confidence) so Loom Curator's next pass can act on it — per your own propose_kb_update procedure.",
				"urgency": "info",
			},
		},
		{
			AgentSlug:   "loom-curator",
			Name:        "capture_on_discovery",
			TriggerKind: "predicate",
			Priority:    55,
			TriggerSpec: captureOnDiscoveryTrigger,
			ActionKind:  "inject_reminder",
			ActionSpec: map[string]interface{}{
				"body":    "Reflex capture_on_discovery: recent output reads like a fresh finding backed by real tool activity, outside the normal classify/compile flow. If it's a wiki-worthy observation not already covered by write_or_stage_page's staging step or scheduled_lint_and_export's inbox routing, drop a concise note into Fragments Engine's inbox now via relay_*/message_* so it isn't lost.",
				"urgency": "info",
			},
		},
	}
}

// SeedAgentReflexesBySlug idempotently inserts AgentID-scoped reflexes
// for seeds whose target agent has already been ingested into
// agent_profiles (resolved via GetAgentBySlug). The AgentID-scoped
// mirror of SeedBaseReflexes: idempotency is checked per (resolved
// agent_id, name) via CountAgentReflexByName rather than per
// (class_tag, name), and — same discipline as SeedBaseReflexes — an
// existing row is left alone (not upserted), so fired_count /
// last_fired_at survive repeated boots and an operator's pause/delete/
// re-priority edits are never silently undone.
//
// A seed whose AgentSlug isn't in agent_profiles yet (sql.ErrNoRows
// from GetAgentBySlug) is skipped with a warning, not treated as
// fatal: this mirrors SyncManagedDurableAgentConfigs's per-config
// warn-and-continue handling of the same "profile not ingested on
// this boot" condition, since agent.Discover + ReconcileManagedAgentIDs
// + AutoIngestAgents may not have processed the seed's .nanite/agents/
// file yet in every environment this seeder runs in (e.g. before a
// fresh checkout's first full boot). Re-running this function on a
// later boot, once the profile exists, seeds it then.
//
// Returns the number of newly inserted rows.
func SeedAgentReflexesBySlug(ctx context.Context, st *store.Store, seeds []AgentReflexSeed, logger *slog.Logger) (int, error) {
	if logger == nil {
		logger = slog.Default()
	}
	inserted := 0
	for _, s := range seeds {
		profile, err := st.GetAgentBySlug(ctx, s.AgentSlug)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				logger.Warn("reflex agent-seed: target agent not yet ingested, skipping",
					"agent_slug", s.AgentSlug, "name", s.Name)
				continue
			}
			logger.Warn("reflex agent-seed: resolve agent failed",
				"agent_slug", s.AgentSlug, "name", s.Name, "err", err)
			continue
		}
		n, err := st.CountAgentReflexByName(ctx, profile.ID, s.Name)
		if err != nil {
			logger.Warn("reflex agent-seed: count failed",
				"agent_id", profile.ID, "name", s.Name, "err", err)
			continue
		}
		if n > 0 {
			continue
		}
		triggerJSON, err := json.Marshal(s.TriggerSpec)
		if err != nil {
			return inserted, fmt.Errorf("marshal trigger %s/%s: %w", s.AgentSlug, s.Name, err)
		}
		actionJSON, err := json.Marshal(s.ActionSpec)
		if err != nil {
			return inserted, fmt.Errorf("marshal action %s/%s: %w", s.AgentSlug, s.Name, err)
		}
		if _, err := st.InsertAgentReflex(ctx, store.AgentReflex{
			AgentID:     profile.ID,
			Name:        s.Name,
			TriggerKind: s.TriggerKind,
			TriggerSpec: string(triggerJSON),
			ActionKind:  s.ActionKind,
			ActionSpec:  string(actionJSON),
			Priority:    s.Priority,
			CreatedBy:   "system",
			// All of this pilot's seeds are inject_reminder nudges, not
			// safety-critical halts — permissive default (Phase 1 item
			// 07, TASKS/phase-1/07-add-reflex-opt-out-field.md).
			OptOutAllowed: true,
		}); err != nil {
			return inserted, fmt.Errorf("seed %s/%s: %w", s.AgentSlug, s.Name, err)
		}
		inserted++
	}
	return inserted, nil
}
