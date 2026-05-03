package service

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
)

// IntentSignals captures the deterministic signals available at session-create
// time (Glass-4, CW-20260502-0015). It is exported so the classifier is
// inspectable from tests and so future signal additions are visible at the
// call site instead of buried inside the classifier function.
//
// All fields are read off Session/AgentProfile/Mode objects the caller already
// has — no extra DB reads inside the classifier itself.
type IntentSignals struct {
	IsPinned         bool
	HasSessionMode   bool
	SessionModeSlug  string // empty when no mode set
	HasWorkspace     bool
	HasProject       bool
	AgentSlug        string
	AgentTags        []string // parsed from AgentProfile.Tags JSON
	AgentCanExecute  bool     // worker-capable agents are typically long-running
	AgentIsDefault   bool     // file-default = quick chat fallback
	HasUserContext   bool     // J10 user context prompt set
	HasSessionObject bool     // session has at least one session_object
	MessageCount     int      // turns accumulated (0 at create time; non-zero at re-classify)
}

// ClassifierBands holds the score thresholds used to bucket signals into
// the three intent classes. Exposed so tests and follow-up tuning can shift
// the bands without touching the scorer.
type ClassifierBands struct {
	LongRunningMin int
	PerTurnMin     int // anything below this is ephemeral
}

// DefaultClassifierBands ships the v1 thresholds. Calibrated against the
// signal weights below so a fresh session with one strong long-running cue
// (e.g. user explicitly pinned, or mode=plan) lands as long-running, while
// a workspace-less first-message session lands as ephemeral.
var DefaultClassifierBands = ClassifierBands{
	LongRunningMin: 5,
	PerTurnMin:     1,
}

// ClassifySessionIntent runs a deterministic score-based classifier over the
// available signals and returns one of store.SessionIntent{LongRunning,PerTurn,Ephemeral}.
// Glass-4 (CW-20260502-0015) — see project_nanite_session_intent_signals
// in tracking notes for signal weights and rationale.
//
// LLM tiebreaker (Haiku-class) is deliberately deferred to a follow-up — see
// implementer report. The deterministic score is the v1 contract.
func ClassifySessionIntent(_ context.Context, signals IntentSignals) string {
	score := ScoreIntent(signals)
	switch {
	case score >= DefaultClassifierBands.LongRunningMin:
		return store.SessionIntentLongRunning
	case score >= DefaultClassifierBands.PerTurnMin:
		return store.SessionIntentPerTurn
	default:
		return store.SessionIntentEphemeral
	}
}

// ScoreIntent returns the raw additive score for the given signals. Exposed
// for tests and for telemetry: callers may want to log the score alongside
// the chosen class to tune thresholds over time.
//
// Weights:
//
//	+4 IsPinned                       — explicit user signal: "this matters, keep it"
//	+3 HasSessionMode (work/plan/research)
//	+3 AgentCanExecute                — worker/planner-capable agent → ongoing work
//	+2 HasProject                     — anchored work, not ad-hoc chat
//	+2 HasSessionObject               — session has a pinned card / object → durable scope
//	+2 HasUserContext                 — user invested in shaping the session prompt
//	+1 HasWorkspace                   — at least loosely anchored
//	+1 AgentTag includes "worker"|"planner"|"backend"|"strategic-planner"
//	-2 AgentIsDefault                 — fallback agent ⇒ quick chat
//	+1 per 3 message turns up to +3   — accumulated history => long-running by demonstration
//
// A "chat" mode slug counts as no mode for scoring purposes (the legacy
// default — neutral signal). Modes "work" / "plan" / "research" / "planning"
// trip HasSessionMode.
func ScoreIntent(s IntentSignals) int {
	score := 0

	if s.IsPinned {
		score += 4
	}
	if s.HasSessionMode && isLongRunningModeSlug(s.SessionModeSlug) {
		score += 3
	}
	if s.AgentCanExecute {
		score += 3
	}
	if s.HasProject {
		score += 2
	}
	if s.HasSessionObject {
		score += 2
	}
	if s.HasUserContext {
		score += 2
	}
	if s.HasWorkspace {
		score++
	}
	if hasAnyTag(s.AgentTags, "worker", "planner", "backend", "strategic-planner") {
		score++
	}
	if s.AgentIsDefault {
		score -= 2
	}
	// Re-classification path: prior turns are evidence of durability.
	if s.MessageCount > 0 {
		bonus := s.MessageCount / 3
		if bonus > 3 {
			bonus = 3
		}
		score += bonus
	}

	return score
}

// SignalsFromSession assembles IntentSignals from the canonical objects the
// caller has after CreateSession + EnsureSessionAgent + (optional) GetSessionMode.
// agent and mode are nil-safe; pass nil when the corresponding lookup did
// not succeed (the classifier degrades gracefully).
func SignalsFromSession(sess *store.Session, agent *store.AgentProfile, mode *store.Mode) IntentSignals {
	if sess == nil {
		return IntentSignals{}
	}
	out := IntentSignals{
		IsPinned:     sess.IsPinned,
		HasWorkspace: sess.WorkspaceID != "",
		HasProject:   sess.ProjectID != "",
		MessageCount: sess.MessageCount,
	}
	if mode != nil && mode.Slug != "" {
		out.HasSessionMode = true
		out.SessionModeSlug = mode.Slug
	}
	if agent != nil {
		out.AgentSlug = agent.Slug
		out.AgentCanExecute = agent.CanExecute
		out.AgentIsDefault = agent.Slug == "file-default" || agent.ID == "file-default"
		out.AgentTags = parseAgentTags(agent.Tags)
	}
	return out
}

// isLongRunningModeSlug returns true for mode slugs that indicate intent to
// sustain a session over many turns. The legacy "chat" / empty values are
// treated as neutral (no bump) so a fresh chat-mode session doesn't get
// classified as long-running on mode alone.
func isLongRunningModeSlug(slug string) bool {
	switch strings.ToLower(slug) {
	case "work", "plan", "planning", "research", "build", "code":
		return true
	}
	return false
}

// hasAnyTag reports whether any of `wanted` appears in `tags` (case-insensitive,
// trimmed). Used by the scorer to detect worker/planner-style agents whose
// roles aren't always reflected in CanExecute.
func hasAnyTag(tags []string, wanted ...string) bool {
	if len(tags) == 0 {
		return false
	}
	wantSet := make(map[string]struct{}, len(wanted))
	for _, w := range wanted {
		wantSet[strings.ToLower(strings.TrimSpace(w))] = struct{}{}
	}
	for _, t := range tags {
		if _, ok := wantSet[strings.ToLower(strings.TrimSpace(t))]; ok {
			return true
		}
	}
	return false
}

// parseAgentTags decodes AgentProfile.Tags (JSON-array string) into a slice.
// Returns an empty slice on any decode failure — tags are a soft signal, not
// a correctness gate.
func parseAgentTags(raw string) []string {
	if raw == "" || raw == "null" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}
