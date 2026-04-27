// Package strategy implements the E3 strategy loop / interaction harness
// (CW-20260419-0026, Phase 5). It also subsumes E4 max_turns negotiation
// (CW-20260419-0020) — the loop's iteration ceiling now comes from a
// Strategy decision rather than a hard-coded constant.
//
// v1 strawman (per ticket discussion prompt #5):
//
//   - PlanStrategy is a rule-based planner (no LLM call). It consumes the
//     E1 reflex match, the E2 grounding result, and the M1 intent
//     classification, and returns a Strategy record naming an Approach
//     plus a turn budget.
//   - ReviewMidExecution fires once at the budget-exhaustion point. v1
//     supports a single review step: if the loop ran to max_turns and no
//     tool result was load-bearing, we ask a clarifying question instead
//     of synthesising a thin packaged answer.
//   - StrategyLogger persists every decision so future tuning sees the
//     ground truth (which approach was chosen, what budget, what
//     evidence).
//
// What's NOT in v1 (deferred):
//
//   - Multiple stop_points / structured plans with checkpoints.
//   - Active sub-agent dispatch from the strategy layer (today's loop
//     still relies on the LLM choosing nanite_execute_task; the strategy
//     decision logs the recommendation).
//   - LLM-generated clarifying questions (we use a fixed template).
//   - Result-cache or tool-broker enrichment as strategy inputs.
//   - Budget extension on review (the enum entry exists but always
//     returns proceed for now).
//
// Architectural calls owned by this package:
//
//   - SYNC planning: rule-based, cheap, blocks main loop entry.
//   - LLM does NOT see the plan record (effects only — budget + review).
//   - Default approach when no signals match: direct_chain + MaxTurns=20
//     (TierMedium-equivalent budget).
package strategy

import (
	"time"

	"github.com/hollis-labs/nanite/internal/classify"
)

// Approach names the high-level execution shape the strategy chose for a
// turn. It is recorded for tuning and (in future revisions) read by the
// loop to influence dispatch behavior.
type Approach string

const (
	// ApproachDirectChain runs the standard tool loop with the Chat
	// agent. The default for trivial / small / medium intents.
	ApproachDirectChain Approach = "direct_chain"
	// ApproachSubagentDelegation recommends dispatching a Worker /
	// Planner subagent. v1 only logs this — the LLM still decides.
	ApproachSubagentDelegation Approach = "subagent_delegation"
	// ApproachAskToClarify means the strategy planned to ask a
	// clarifying question instead of executing. Reserved for cases where
	// signals say the request is too ambiguous to plan a budget for. v1
	// PlanStrategy does not emit this directly — it only emerges from
	// ReviewMidExecution as a ReviewDecision. Kept in the enum for
	// symmetry with ReviewDecision.
	ApproachAskToClarify Approach = "ask_to_clarify"
	// ApproachAlreadyAnsweredFromCache means the strategy detected the
	// answer is available without a fresh tool call (e.g., grounding
	// surfaced a high-confidence prior answer). Reserved for v2 — v1
	// does not emit this.
	ApproachAlreadyAnsweredFromCache Approach = "already_answered_from_cache"
)

// Strategy is the v1 strategy record produced by PlanStrategy. It is
// persisted via StrategyLogger and returned to the caller (chat
// generation) so it can apply MaxTurns to the loop limits.
type Strategy struct {
	// Approach is the high-level shape chosen for this turn.
	Approach Approach

	// Rationale is a short human-readable explanation of why the
	// planner picked this approach. Stored alongside the row for later
	// auditing. Not surfaced to the LLM.
	Rationale string

	// MaxTurns is the initial turn budget. Replaces the hard-coded
	// defaultMaxTurns in the chat loop (E4 absorption).
	MaxTurns int

	// EscalationBudget is the additional turns the review step may grant
	// if it returns ReviewExtendBudget. v1 never returns ExtendBudget,
	// so this stays for v2 wiring without breaking the persisted shape.
	EscalationBudget int

	// ReflexMatchID is the ID of the E1 reflex that influenced planning,
	// or "" when no reflex matched.
	ReflexMatchID string

	// GroundingConsultationIDs are the row IDs in grounding_consultations
	// that the strategy consulted (for traceability). May be empty.
	GroundingConsultationIDs []int64

	// PlaybookHit is the playbook ID consulted (when E1 evolves into a
	// full playbook runtime). Empty in v1.
	PlaybookHit string

	// CreatedAt is the wall-clock time the strategy was planned. Stored
	// for analytics; the DB also assigns a timestamp on insert so the
	// two columns can disagree by a few ms — that is acceptable.
	CreatedAt time.Time
}

// PlanInput carries the upstream signals PlanStrategy consults. Callers
// populate the fields they have; missing fields are tolerated.
type PlanInput struct {
	// UserInput is the verbatim user message for this turn. Not used in
	// v1 planning logic but recorded for traceability.
	UserInput string

	// SessionID is the chat session ID. Recorded on the strategy row.
	SessionID string

	// TurnID is the user-turn message ID, when tracked. Optional.
	TurnID string

	// Intent is the M1 classification result.
	ScopeTier        classify.ScopeTier
	ExecutionPattern classify.ExecutionPattern

	// ReflexMatch is the matched reflex from internal/reflex, or nil.
	// We keep the type indirect (an *opaque struct) to avoid an import
	// cycle later if reflex grows; v1 only reads ID + ExecutionPattern
	// hint.
	ReflexMatch *ReflexSignal

	// Grounding is the E2 grounding result, or nil. The planner reads
	// the surfaced hits (terse-reply preference, prior-success markers)
	// and records the consulted IDs.
	Grounding *GroundingSignal
}

// ReflexSignal is the strategy package's local view of a reflex match.
// Populated by the caller from internal/reflex.ReflexMatch — kept
// separate so the strategy package does not import reflex (it's a leaf
// dependency consumer pattern).
type ReflexSignal struct {
	// ReflexID is the matched reflex ID.
	ReflexID string

	// HintTier is the reflex's ScopeTier hint.
	HintTier classify.ScopeTier

	// HintPattern is the reflex's ExecutionPattern hint.
	HintPattern classify.ExecutionPattern

	// DispatchVia is the reflex's SideEffects.DispatchVia value.
	// Values: "executeTask", "executeBackground", or "" (unset).
	DispatchVia string
}

// GroundingSignal is the strategy package's local view of a grounding
// result. Populated by the caller from internal/grounding.GroundingResult.
type GroundingSignal struct {
	// Enabled mirrors GroundingResult.Enabled.
	Enabled bool

	// ConsultationIDs are the row IDs returned by LogConsultations.
	ConsultationIDs []int64

	// PrefersTerse is true when at least one surfaced memory carries a
	// "terse" or "concise" tag (or contains a relevant phrase). v1 is a
	// shallow heuristic; v2 may consult Conduit metadata directly.
	PrefersTerse bool

	// PriorSuccessApproach, when non-empty, names an Approach that a
	// prior-similar grounded turn used successfully. Adopted by
	// PlanStrategy when present. v1 callers can leave this empty —
	// the heuristic to populate it is reserved for v2.
	PriorSuccessApproach Approach
}

// TurnState is the input ReviewMidExecution evaluates at the end of a
// loop iteration cycle. It is a snapshot — the reviewer is pure
// (no side effects).
type TurnState struct {
	// TurnsConsumed is the number of iterations the loop has executed.
	TurnsConsumed int

	// TurnsRemaining is MaxTurns − TurnsConsumed. May be 0 (budget
	// exhausted) or negative (over budget; callers clamp to 0).
	TurnsRemaining int

	// HasUsableData is true when at least one tool call's result was
	// load-bearing for the eventual answer. The chat loop tracks this
	// and passes it in.
	HasUsableData bool

	// Strategy is the original plan, returned for convenience (the
	// reviewer sees the Approach in case future revisions branch on it).
	Strategy Strategy
}

// ReviewDecision names the action the reviewer wants the chat loop to
// take when budget is exhausted. v1 emits proceed | ask_to_clarify |
// wrap_with_partial_data; ExtendBudget is reserved.
type ReviewDecision string

const (
	// ReviewProceed means continue the loop unchanged. Returned when
	// TurnsRemaining > 0.
	ReviewProceed ReviewDecision = "proceed"

	// ReviewExtendBudget grants additional turns from EscalationBudget.
	// Reserved for v2; v1 ReviewMidExecution never returns this.
	ReviewExtendBudget ReviewDecision = "extend_budget"

	// ReviewAskToClarify means the loop should NOT synthesise an answer.
	// Instead, the loop appends a clarifying-question message naming
	// what's missing. Returned when budget is exhausted and no tool
	// result was load-bearing.
	ReviewAskToClarify ReviewDecision = "ask_to_clarify"

	// ReviewWrapWithPartialData means the loop should synthesise from
	// the partial-but-usable data it has. The default existing
	// early-stop synthesis handles this. Returned when budget is
	// exhausted but at least one tool was load-bearing.
	ReviewWrapWithPartialData ReviewDecision = "wrap_with_partial_data"
)

// Default budgets, derived from intent classification. These are the
// numbers locked in the strawman MVP — they are tunable in v2 once we
// have a clear corpus of telemetry.
const (
	BudgetTrivial = 10
	BudgetMedium  = 20
	BudgetLarge   = 40
)

// DefaultBudget is used when no other rule applies. Matches BudgetMedium
// per the architectural decision documented in the ticket.
const DefaultBudget = BudgetMedium
