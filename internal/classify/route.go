// Route classification for B2 (CW-20260429-0031).
//
// This is a sibling of scope.go's ScopeTier / ExecutionPattern primitive
// and mode.go's ClassifyMode primitive but answers a different question:
// "should this turn dispatch to an executor, or stay chat-direct?"
//
// The classifier's output is INFORMATIVE. Consumers (chat_generate.go's
// dispatch seam) MAY attempt an executor handoff when a non-direct route
// is emitted. Consumers MUST NOT treat a non-direct route as a hard
// prohibition on chat-direct handling — the chat-direct path stays the
// safe fallback when the executor returns a typed Failure. This is the
// "preemptive vs reactive" mitigation from B1's decision-rules pass
// (docs/architecture/executor-handoff.md §"Rule 2").
//
// The classifier is intentionally deterministic and rules-based for v1
// (matches doc.go D5 — "rules-based MVP; LLM-judge deferred"). An LLM
// tiebreak is a tracked follow-up and MUST NOT be added here without an
// ADR.
//
// Design: docs/architecture/classifier-routing.md.
package classify

import "strings"

// Route is the dispatch hint. The constant values are stable wire
// strings — they ride into B6 telemetry and into the dispatch seam's
// log lines, so renaming them is a breaking change.
//
// The vocabulary is forward-compatible: future executor profiles
// register additional values without changing the type's shape. The
// chat-direct default and the v1 envelope-render route are the only
// values emitted today.
type Route string

const (
	// RouteChatDirect is the default — the turn stays in the chat-direct
	// loop. Emitted on empty input, ambiguous input, anything the
	// heuristics don't pin down. Consumers treat this as "no dispatch
	// hint; run the normal chat-direct loop".
	RouteChatDirect Route = "chat_direct"

	// RouteExecutorEnvelopeRender hints that the turn should dispatch to
	// the envelope-render executor pilot (B3 — CW-20260429-0032).
	// Triggered by explicit envelope-type cues, render-verb keywords,
	// demo / synthesis cues, and multi-step render keywords.
	//
	// When emitted alongside SyntheticAllowed=true (set by the
	// demo-intent rule in ClassifyRoute), the dispatching seam stamps
	// ExecutorRequest.SyntheticAllowed so the executor may synthesize
	// realistic placeholder content with a disclosure (B3 c119
	// judgment 1).
	RouteExecutorEnvelopeRender Route = "executor_envelope_render"

	// Future executor profiles register additional values here. Examples:
	//
	//   RouteExecutorKnowledgeAnswer Route = "executor_knowledge_answer"
	//   RouteExecutorResearchSweep   Route = "executor_research_sweep"
)

// IsValid reports whether r is a recognized route. Empty / unrecognized
// strings return false; consumers SHOULD treat invalid routes as
// equivalent to RouteChatDirect (the conservative default).
func (r Route) IsValid() bool {
	switch r {
	case RouteChatDirect, RouteExecutorEnvelopeRender:
		return true
	}
	return false
}

// String returns the wire-string form. Convenient for slog / log.Printf
// composition; the underlying type is already a string but methodizing
// keeps call-sites symmetric with ScopeTier.String / ExecutionPattern.String.
func (r Route) String() string { return string(r) }

// RouteDecision is the structured output of ClassifyRoute. Carries the
// route value plus the secondary signals downstream consumers need to
// build an ExecutorRequest:
//
//   - TargetEnvelopeType: best-guess envelope type when the explicit-type
//     rule fired. Empty when the route was triggered by a render verb
//     or demo cue alone (the executor's first step is then to pick the
//     type via tool_describe — for the B3 in-process pilot, an empty
//     type returns missing_context, which the dispatch seam handles
//     by falling back to chat-direct).
//   - SyntheticAllowed: true when the demo-intent rule fired. The
//     dispatching seam stamps ExecutorRequest.SyntheticAllowed so the
//     executor may synthesize realistic placeholder content with a
//     disclosure.
//
// Empty input returns RouteDecision{Route: RouteChatDirect} — the
// conservative default per ClassifyRoute's contract. Callers don't need
// to special-case empty input.
type RouteDecision struct {
	Route              Route
	TargetEnvelopeType string
	SyntheticAllowed   bool
}

// RouteRenderVerbKeywords are case-insensitive substring triggers for
// envelope-render routing when the user uses a structured-output verb
// without naming a specific envelope type. Exported so the eval suite
// (CW-20260420-0028) can tune the rubric without rewriting the
// classifier — same contract as ScopeTier*Keywords (classify.go).
var RouteRenderVerbKeywords = []string{
	"show me",
	"render",
	"display",
	"demo report",
	"demo card",
	"card for",
	"card showing",
	"preview a",
	"mock up",
	"make a card",
}

// RouteSyntheticIntentKeywords are case-insensitive substring triggers
// for the demo / synthesis intent. When one fires together with a
// render-verb keyword, ClassifyRoute sets SyntheticAllowed=true and
// routes through the envelope-render executor.
//
// Standalone (no render verb), they fall through to the next rule —
// "demo" alone may just mean "show me code" in a non-card sense.
var RouteSyntheticIntentKeywords = []string{
	"let's do some testing",
	"let me test",
	"let's test",
	"give me an example",
	"synthetic",
	"placeholder",
	"synthesize",
	"for testing",
	"demo",
}

// RouteMultiStepRenderKeywords are case-insensitive substring triggers
// for the "look up X then render Y" complexity signal. Today they
// collapse into the same envelope-render route as a render verb;
// future executor profiles will pull them out by intent (e.g., a
// knowledge-grounded executor that handles the "look up" half itself).
var RouteMultiStepRenderKeywords = []string{
	"look up",
	"fetch the",
	"pull the",
	"go grab",
}

// envelopeTypeProvider holds the production passive-renderable
// envelope-type lookup. Defined as a function (not a direct call to
// envelope.PassiveRenderableTypes) so tests can override without
// import-cycle pain — the classify package must NOT import
// internal/envelope (same anti-cycle reason internal/dispatch projects
// its own Envelope type).
//
// Production wiring registers a provider over
// envelope.PassiveRenderableTypes via SetEnvelopeTypeProvider at
// composition time (internal/service/chat_route_dispatch.go::init).
// When unset, availableEnvelopeTypes() returns the v1 baseline list
// (kept in sync manually; the drift test
// TestV1EnvelopeTypeBaseline_MatchesEnvelopePackage in
// internal/service/chat_route_dispatch_test.go catches drift in CI by
// importing both packages).
var envelopeTypeProvider func() []string

// SetEnvelopeTypeProvider installs the production envelope-type lookup
// so ClassifyRoute can gate explicit-type routing on the v1 allow-list.
// Production callers wire envelope.PassiveRenderableTypes; tests
// install fakes (or call the v1 baseline directly via the zero value).
func SetEnvelopeTypeProvider(p func() []string) {
	envelopeTypeProvider = p
}

// v1EnvelopeTypeBaseline is the static fallback list when no provider
// is installed. Kept in sync with envelope.PassiveRenderableTypes by
// CI (drift test
// TestV1EnvelopeTypeBaseline_MatchesEnvelopePackage in
// internal/service/chat_route_dispatch_test.go imports both packages
// and asserts equality).
//
// Drift cost: if a passive-renderable type is added in the envelope
// package and not here, the classifier won't recognize the explicit-type
// cue for the new type until the production provider is wired (which
// is the steady-state path). Acceptable for v1 — the drift test catches
// any divergence in CI.
var v1EnvelopeTypeBaseline = []string{
	"giphy-modal",
	"document-viewer",
	"report-card",
	"info-card",
	"list-card",
	"metric-card",
	"progress-card",
	"table-card",
	"timeline-card",
	"diff-card",
	"artifact-mini",
}

// availableEnvelopeTypes returns the active passive-renderable
// envelope-type allow-list — the production-wired provider when set,
// the v1 baseline otherwise.
func availableEnvelopeTypes() []string {
	if envelopeTypeProvider != nil {
		return envelopeTypeProvider()
	}
	return v1EnvelopeTypeBaseline
}

// V1EnvelopeTypeBaseline returns a defensive copy of the v1 fallback
// list of passive-renderable envelope types. Exported for the
// cross-package drift test (service package imports both classify and
// envelope and asserts the baseline matches envelope.PassiveRenderableTypes).
//
// Production callers should prefer SetEnvelopeTypeProvider over reading
// the baseline directly — the baseline is the no-provider fallback,
// not the source of truth.
func V1EnvelopeTypeBaseline() []string {
	out := make([]string, len(v1EnvelopeTypeBaseline))
	copy(out, v1EnvelopeTypeBaseline)
	return out
}

// ClassifyRoute walks the priority-ordered heuristics and returns the
// route hint plus secondary signals (target envelope type, synthetic
// allowance). Empty input returns RouteChatDirect, the conservative
// default.
//
// Priority order (highest first):
//
//  1. Explicit envelope-type cue. The user names a passive-renderable
//     envelope type ("show me a report-card"); routes to envelope-render
//     with TargetEnvelopeType set.
//  2. Render-verb keyword + demo-intent cue. Phrases like "show me" or
//     "render" together with a demo cue ("for testing", "demo") route
//     to envelope-render with SyntheticAllowed=true. The c117 test
//     scenario from B1 lives here.
//  3. Render-verb keyword alone. Routes to envelope-render with empty
//     TargetEnvelopeType — the executor will fail-fast with
//     missing_context for the in-process pilot, and the dispatch seam
//     falls back to chat-direct. Future LLM-driven implementations
//     pick the type via tool_describe.
//  4. Multi-step render keyword. Same effect as render-verb alone for
//     v1; future executor profiles distinguish.
//  5. Default. RouteChatDirect.
//
// When the v1 envelope-type allow-list is empty (no passive-renderables
// are reachable in this build), all envelope-render rules degrade to
// RouteChatDirect — defensive against a workspace that disabled
// passive renderables via plugin config.
func ClassifyRoute(intent IntentSignals) RouteDecision {
	msg := strings.TrimSpace(intent.Message)
	if msg == "" {
		return RouteDecision{Route: RouteChatDirect}
	}
	lower := strings.ToLower(msg)

	available := availableEnvelopeTypes()
	if len(available) == 0 {
		// Tool-availability gate — no executor target reachable.
		return RouteDecision{Route: RouteChatDirect}
	}

	// Rule 1 — explicit envelope-type cue. Substring match against the
	// active allow-list. Longest-match wins so "report-card" beats a
	// hypothetical "card" prefix.
	if envType := matchEnvelopeType(lower, available); envType != "" {
		// Demo cue may still apply (synthetic disclosure should pass
		// through even when the type is pinned).
		synthetic := containsAny(lower, RouteSyntheticIntentKeywords)
		return RouteDecision{
			Route:              RouteExecutorEnvelopeRender,
			TargetEnvelopeType: envType,
			SyntheticAllowed:   synthetic,
		}
	}

	// Rule 2 — render verb + demo intent. The c117 scenario.
	// Word-boundary anchoring on render verbs to avoid "rendering",
	// "displayed", etc. matching when the user is talking about the
	// concept rather than asking for a card.
	hasRenderVerb := anyPhraseHit(lower, RouteRenderVerbKeywords)
	hasDemoCue := anyPhraseHit(lower, RouteSyntheticIntentKeywords)
	if hasRenderVerb && hasDemoCue {
		return RouteDecision{
			Route:            RouteExecutorEnvelopeRender,
			SyntheticAllowed: true,
		}
	}

	// Rule 3 — render verb alone.
	if hasRenderVerb {
		return RouteDecision{Route: RouteExecutorEnvelopeRender}
	}

	// Rule 4 — multi-step render keywords. Treated as render-verb-equivalent
	// in v1; future profiles distinguish.
	if anyPhraseHit(lower, RouteMultiStepRenderKeywords) {
		return RouteDecision{Route: RouteExecutorEnvelopeRender}
	}

	// Rule 5 — default.
	return RouteDecision{Route: RouteChatDirect}
}

// anyPhraseHit reports whether any phrase in needles appears in
// haystack with word-boundary anchoring (mode.go::phraseHit). Used for
// render-verb / demo-cue keyword matching where "rendering" must not
// trigger "render".
func anyPhraseHit(haystack string, needles []string) bool {
	for _, n := range needles {
		if phraseHit(haystack, n) {
			return true
		}
	}
	return false
}

// matchEnvelopeType returns the longest envelope-type substring found
// in haystack, or "" if none match. Longest-match disambiguates
// e.g. "report-card" vs a hypothetical shorter prefix.
//
// Substring (not word-boundary) matching is intentional and matches
// the keyword-matching contract elsewhere in the package — envelope
// type slugs are kebab-case so substring false positives are rare
// in chat-shaped input.
func matchEnvelopeType(haystack string, types []string) string {
	best := ""
	for _, t := range types {
		if t == "" {
			continue
		}
		if strings.Contains(haystack, t) {
			if len(t) > len(best) {
				best = t
			}
		}
	}
	return best
}
