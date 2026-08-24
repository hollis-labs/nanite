package dispatcher

import (
	"log/slog"
	"time"
)

// CompletionMode names how a run-another-agent caller observes the
// dispatched turn's completion. The three surfaces that route through
// Dispatcher.Run — REST delegation, LLM-triggered subagent dispatch,
// and durable-agent wake — have genuinely different completion
// semantics (docs/engineering/architecture/04-harness.md, "Run-another-
// agent surfaces, unified"). This type preserves that difference as
// data instead of collapsing all three onto one assumed shape; see
// AgentRunRequest.Completion / AgentRunResult.Completion.
type CompletionMode string

const (
	// CompletionSyncDrain blocks the caller until the dispatched turn
	// ends — channel close, a caller-imposed timeout, or ctx
	// cancellation — accumulating stream content directly in the
	// caller's own goroutine. REST delegation's contract
	// (internal/service/delegation.go's DelegateTask; hard 5-minute
	// timeout).
	CompletionSyncDrain CompletionMode = "sync_drain"

	// CompletionAsyncCapture drains asynchronously against a
	// persisted run row, subject to the fabrication-suspected and
	// zero-output safety classifiers (CW-20260512-0095,
	// CW-20260519-0067) before the outcome is trusted. LLM-triggered
	// subagent dispatch's contract (internal/service/subagent_runner.go's
	// ChatRunner.Run / drainCapture).
	CompletionAsyncCapture CompletionMode = "async_capture"

	// CompletionQueued returns as soon as the turn is queued for
	// delivery; the caller observes completion (if at all) via a
	// separate status-polling surface, not by draining a channel.
	// Durable-agent wake's contract (internal/service/durable_wake.go's
	// Wake) — it fundamentally cannot synchronously drain (wakes are
	// scheduled/background by nature), so "queued" is this surface's
	// legitimate terminal shape, not a stand-in for a drain it can't
	// perform.
	CompletionQueued CompletionMode = "queued"
)

// RunStatus is the terminal classification an AgentRunResult reports.
// A strict superset of the three surfaces' independently-evolved
// status vocabularies (delegation's Success/Error pair,
// subagent_runs' CW-20260519-0074 taxonomy, durable-agent wake's
// lifecycle-event trail) — every value any one surface needs is
// represented here, and no surface is forced to approximate its real
// outcome with a value that doesn't fit.
//
// Not every value is reachable from every integration point. In
// particular RunStatusOverBudget is part of subagent_runs' real
// taxonomy (internal/subagent's classifyRunOutcome), but that
// distinction is only resolved AFTER internal/service's
// ChatRunner.Run returns — classifyRunOutcome needs the run's
// post-attempt ctx state, which is context ChatRunner.Run's own
// AgentRunResult construction does not duplicate (duplicating that
// classifier would risk the two drifting apart on a safety-relevant
// call). RunStatusOverBudget is kept in this taxonomy for
// completeness/documentation of the full status space subagent_runs
// can reach, even though this package's own LogOutcome call sites
// never emit it directly.
type RunStatus string

const (
	RunStatusCompleted            RunStatus = "completed"
	RunStatusFailed               RunStatus = "failed"
	RunStatusTimedOut             RunStatus = "timed_out"
	RunStatusCanceled             RunStatus = "canceled"
	RunStatusStalled              RunStatus = "stalled"
	RunStatusOverBudget           RunStatus = "over_budget"
	RunStatusFabricationSuspected RunStatus = "fabrication_suspected"
	RunStatusQueued               RunStatus = "queued"
	RunStatusSkipped              RunStatus = "skipped"
)

// AgentRunRequest is the shared, surface-agnostic shape each of the
// three run-another-agent surfaces assembles before invoking the
// dispatch path. It sits one level above the narrower Request
// Dispatcher.Run itself consumes — Request stays the minimal boundary
// its own doc comment requires (SessionID / AssistantMsgID /
// UserContent / CallerType only, "not a place to grow per-caller
// fields"). AgentRunRequest is where each surface's own upstream
// shape (delegation's task title+description, subagent dispatch's
// role+mode+retry policy, durable wake's wake payload) is normalized
// down to what actually varies across all three, before the caller
// derives the narrow dispatcher.Request from it.
type AgentRunRequest struct {
	CallerType CallerType
	Completion CompletionMode

	// TargetSessionID is the worker/child/woken session the turn will
	// run against — never the parent/caller session.
	TargetSessionID string

	// Prompt is the verbatim turn content — delegation's task
	// description, the subagent's spawn prompt, or the durable
	// agent's wake prompt.
	Prompt string

	// Timeout bounds a CompletionSyncDrain caller's local wait
	// (delegation's 5 minutes). Zero means the surface has no
	// caller-local bound of its own: subagent dispatch is bounded by
	// its own wall-clock backstop / inactivity watchdog further
	// downstream instead, and durable wake doesn't drain at all.
	Timeout time.Duration
}

// AgentRunResult is the shared, surface-agnostic outcome shape all
// three surfaces build once their own (unchanged) drain / classification
// logic has run. It normalizes reporting across the three surfaces
// (see LogOutcome) without replacing any surface's existing
// wire-facing or persisted result type (chat.DelegationResult,
// subagent.Result + subagent_runs.status, DurableAgentWakeResult):
// those keep their own field names / JSON tags exactly as before.
// AgentRunResult is an additional, normalized view derived from
// values each surface has already computed — not the thing those
// existing types are computed FROM — specifically so this
// unification cannot accidentally change what a surface's existing
// behavior does (Phase 4 task 04's explicit constraint).
type AgentRunResult struct {
	CallerType      CallerType
	Completion      CompletionMode
	TargetSessionID string

	// Status is the terminal classification. See RunStatus.
	Status RunStatus

	// Content is the accumulated assistant text for CompletionSyncDrain
	// / CompletionAsyncCapture callers. Empty for CompletionQueued —
	// nothing has run yet when the caller gets this back.
	Content string

	// Envelope is the subagent surface's structured result payload
	// ("{}" default when set); unused by delegation and durable wake.
	Envelope string

	TokensUsed int

	// Err carries the terminal error for any non-success Status. nil
	// for RunStatusCompleted / RunStatusQueued.
	Err error

	// ToolCalls / ToolResultsSuccess / ToolResultsError surface the
	// subagent-dispatch-only tool-usage counts that feed the
	// fabrication-suspected detector (CW-20260512-0095). Zero for
	// delegation and durable wake, which don't compute this signal —
	// kept on the shared type anyway (strict superset, not a
	// lowest-common-denominator) rather than living on a
	// subagent-only side channel.
	ToolCalls          int
	ToolResultsSuccess int
	ToolResultsError   int
}

// LogOutcome emits one structured log line with field names shared
// across all three run-another-agent surfaces, so an operator can
// grep a single event name ("agent-run: outcome") instead of three
// surfaces' independently-worded log lines. Purely additive — it does
// not replace any surface's own existing logging, which stays exactly
// as before alongside its own preserved behavior.
func LogOutcome(outcome AgentRunResult) {
	// NOTE: the log key is "completion_mode", not "completion" —
	// internal/slogx's PII redactor treats the bare key "completion" as
	// sensitive (LLM completion text), which would blank out this
	// enum-valued field (sync_drain / async_capture / queued) for no
	// reason. See internal/slogx/slogx.go's sensitiveAttrKeys.
	args := []any{
		"caller", outcome.CallerType.String(),
		"completion_mode", string(outcome.Completion),
		"status", string(outcome.Status),
		"session_id", outcome.TargetSessionID,
		"content_chars", len(outcome.Content),
	}
	if outcome.TokensUsed > 0 {
		args = append(args, "tokens_used", outcome.TokensUsed)
	}
	if outcome.ToolCalls > 0 || outcome.ToolResultsSuccess > 0 || outcome.ToolResultsError > 0 {
		args = append(args,
			"tool_calls", outcome.ToolCalls,
			"tool_results_success", outcome.ToolResultsSuccess,
			"tool_results_error", outcome.ToolResultsError,
		)
	}
	if outcome.Err != nil {
		args = append(args, "err", outcome.Err.Error())
		slog.Warn("agent-run: outcome", args...)
		return
	}
	slog.Info("agent-run: outcome", args...)
}
