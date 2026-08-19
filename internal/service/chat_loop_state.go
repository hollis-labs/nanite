package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/classify"
	"github.com/hollis-labs/nanite/internal/dispatcher"
	"github.com/hollis-labs/nanite/internal/effort"
)

// ContinueSite identifies why the chat loop continues for another iteration.
type ContinueSite string

const (
	ContinueToolResults  ContinueSite = "CONTINUE_TOOL_RESULTS"
	ContinueCompaction   ContinueSite = "CONTINUE_COMPACTION"
	ContinueRecovery     ContinueSite = "CONTINUE_RECOVERY"
	ContinuePermission   ContinueSite = "CONTINUE_PERMISSION"
	ContinueAgentReturn  ContinueSite = "CONTINUE_AGENT_RETURN"
	ContinueHookModified ContinueSite = "CONTINUE_HOOK_MODIFIED"
	ContinueModeChange   ContinueSite = "CONTINUE_MODE_CHANGE"
)

// Default iteration limits.
//
// CW-20260504-0001 introduced `defaultMaxTurns` / `Strategy.MaxTurns` as
// SOFT HINTS (never hard terminators) plus a one-shot `maxTurns`-crossed
// telemetry/SSE warning (`checkSoftMaxTurnsWarning`, since removed). Phase
// 0 items 11 and 12 (2026-08-18) cut both halves of that soft-budget
// layer — the strategy planner's own `MaxTurns` (item 11) and
// `chat.AgentConstraints.MaxTurns` plus `checkSoftMaxTurnsWarning`/the
// `chat-loop-budget-soft-warning` envelope (item 12) — because neither
// ever stopped the loop; they only logged. `shouldStop` (below) is the
// real story: the chat loop terminates on actual pathology —
// `runawayFailCap` (consecutive tool failures), `idleTimeout` (wall
// clock), `hardCeiling` (absolute backstop) — or on the agent's natural
// `end_turn` stop signal. `defaultMaxTurns`/`iterationLimits.maxTurns`/
// `resolvedMaxTurns()` are kept as an inert diagnostic value only (see
// `chat_generate_diag.go`'s `diagLogLoopStart` and `TurnSnapshot.MaxTurns`)
// — they have no remaining behavioral consumer.
//
// `TerminationMaxTurns` constant is preserved for back-compat with
// stored run rows + the chat-loop-terminated envelope schema enum, but
// `shouldStop` no longer returns it — only `runaway_tool_failures`,
// `idle_timeout`, `hard_ceiling`, and `retry_budget_exhausted` actively
// terminate.
const (
	defaultMaxTurns    = 75
	defaultHardCeiling = 200
	// CW-20260417-0485: ConsecutiveFailCap used to terminate the chat loop,
	// cutting the LLM off before it could respond to the tool errors it had
	// just received as tool_result blocks. The loop now threads tool errors
	// back to the LLM on every iteration (always has — the tool_result
	// blocks carry IsError=true) and only terminates on a *runaway* — see
	// defaultRunawayFailCap. The soft cap still drives the "critical"
	// tool_warning SSE event so the UI can warn the user before the runaway
	// trips, but it no longer exits the loop.
	defaultConsecutiveFailCap = 3
	// defaultRunawayFailCap is the hard circuit-breaker. When consecutive
	// tool failures reach this count the loop emits a typed
	// `chat-loop-terminated` envelope and exits. Sized high enough that a
	// reasonable LLM has multiple turns to recover (acknowledge error, try
	// different args, try a different tool, or stop gracefully) before the
	// breaker trips.
	defaultRunawayFailCap     = 10
	defaultIdleTimeoutSeconds = 900 // 15 minutes
	// subagentIdleTimeoutSeconds is the inactivity (liveness) window for a
	// subagent dispatch — CW-20260519-0073. A subagent run is no longer
	// bounded by a fixed wall-clock deadline; the governing signal is now
	// *silence*. The chat loop's idle-timeout terminator (shouldStop Layer
	// 2) resets `lastActivity` on every tool call / delta, so a worker that
	// keeps emitting events runs as long as the work needs and only a
	// genuinely stalled run (no activity for this window) trips the
	// terminator. 300s matches Torque's `TORQUE_SCHED_STALE` stale-worker
	// window (the audit's P0 recommendation). The 900s interactive default
	// above is unchanged — this scoped value only governs subagent runs,
	// where the fixed 300s wall clock previously made the idle timeout
	// structurally unreachable.
	subagentIdleTimeoutSeconds = 300
	// CW-20260419-0012 (quick fix): 3 → 6. 3 was an arbitrary
	// conservative floor; empirically the agent needs 3 passes for
	// intent-warmup and another 2-3 for follow-on exploration within
	// the same turn. The runaway_fail_cap (10) still catches infinite
	// loops. Tunable via user_settings once CW-20260419-0012 lands.
	defaultMaxRequestToolsCalls = 6
)

// TerminationCode is the machine-readable reason a chat loop terminated
// abnormally. Mirrors the `code` enum in the chat-loop-terminated envelope
// schema (internal/envelope/schemas/chat-loop-terminated.schema.json).
type TerminationCode string

const (
	TerminationRunawayToolFailures  TerminationCode = "runaway_tool_failures"
	TerminationMaxTurns             TerminationCode = "max_turns"
	TerminationHardCeiling          TerminationCode = "hard_ceiling"
	TerminationIdleTimeout          TerminationCode = "idle_timeout"
	TerminationRetryBudgetExhausted TerminationCode = "retry_budget_exhausted"
)

// TurnSnapshot captures diagnostic state at a continuation point.
type TurnSnapshot struct {
	ContinueSite ContinueSite       `json:"site"`
	Reason       string             `json:"reason"`
	Iteration    int                `json:"iteration"`
	MaxTurns     int                `json:"max_turns"`
	ToolCalls    []ToolCallSnapshot `json:"tool_calls,omitempty"`
	TokensUsed   int                `json:"tokens_used"`
	MessageCount int                `json:"message_count"`
	Timestamp    time.Time          `json:"timestamp"`
}

// ToolCallSnapshot captures a single tool call's outcome for debugging.
type ToolCallSnapshot struct {
	Name       string         `json:"name"`
	Input      map[string]any `json:"input,omitempty"`
	Output     string         `json:"output,omitempty"`
	DurationMs float64        `json:"duration_ms"`
	Success    bool           `json:"success"`
	Parallel   bool           `json:"parallel"`
}

// iterationLimits holds the resolved limits for a loop invocation.
type iterationLimits struct {
	maxTurns           int
	hardCeiling        int
	consecutiveFailCap int
	// runawayFailCap is the hard circuit-breaker. See defaultRunawayFailCap
	// and the CW-20260417-0485 comment there for the rationale.
	runawayFailCap int
	idleTimeout    time.Duration
	perToolMax     map[string]int // tool name → max iterations (0 = no limit)
	// defaultPerToolCap is a HIGH BACKSTOP on calls to any single tool
	// per turn — NOT a runaway detector. CW-20260519-0115: raised from
	// 10 → 150 after session c267 was blocked at 10 of 13
	// operator-requested torque_task_create calls. Count is a poor
	// runaway signal; the actual catch is done by pattern detectors —
	//   - consecutiveFailures → runawayFailCap (hard terminate at 10
	//     consecutive tool failures)
	//   - detectStuckLoop (chat_generate.go) — same-result-repeated
	//     detector that blocks the tool after 2 identical results
	//   - idleTimeout — wall-clock no-progress
	// 0 = no cap (the chat-loop in-memory default — only set when a
	// store-backed UserSettings.ToolPerTurnCap is read). The
	// store-side default is 150 (migration 011 / 066). Operators who
	// hit even the 150 backstop are almost certainly in a real
	// infinite-tool-call loop the pattern detectors should have caught
	// first — investigate as a pattern-detector gap, not a cap value
	// to bump.
	defaultPerToolCap int
}

// loopState consolidates all mutable state for the generateResponse loop.
type loopState struct {
	// Tool execution tracking.
	lastToolResults  map[string]string
	toolRepeatCount  map[string]int
	blockedTools     map[string]bool
	toolCallRefs     []chat.ToolCallRef
	pendingEnvelopes []string
	wasTruncated     bool
	directReturn     string

	// Iteration control.
	//
	// CW-20260512-0123 (SP-20260512-0011 W3): the `retryBudget`
	// counter was removed alongside the agent-constraint
	// `RetryBudget` field. The runaway-fail-cap (default 10
	// consecutive tool failures) is now the sole tool-failure
	// breaker; the retry-budget terminator was redundant deadweight.
	iteration           int
	limits              iterationLimits
	consecutiveFailures int
	toolCallCounts      map[string]int

	// CW-20260417-0485: track the last tool failure so the
	// chat-loop-terminated envelope can carry the triggering error.
	lastToolError string
	lastToolName  string

	// Progressive discovery.
	loadedTools              map[string]bool
	consecutiveEmptyRequests int
	totalRequestToolsCalls   int
	maxRequestToolsCalls     int

	// Phase 5 / D3 (CW-20260419-0011) — broker reflection on cap.
	// reflectionFired is set once the broker has emitted the reflection
	// prompt for this turn. The next request_tools call after that uses
	// the LLM's restated goal as a fresh broker query rather than halting
	// outright. Subsequent caps (after reflection) fall back to the
	// pre-Phase-5 hard halt; we don't reflect repeatedly within one turn.
	reflectionFired bool

	// Activity tracking.
	lastActivity time.Time

	// Pre-loop classification from P3 (CW-20260420-0013). Set once by
	// generateResponse before the chat loop runs; read by downstream
	// consumers (budgets, strategy loop, playbook, background-job) via
	// Classification(). Zero values (TierInvalid, PatternInvalid) indicate
	// the classifier has not yet run for this generation.
	scopeTier        classify.ScopeTier
	executionPattern classify.ExecutionPattern

	// Pre-loop route hint from B2 (CW-20260429-0031). Set once by
	// generateResponse alongside scopeTier; read by the dispatch seam
	// to decide whether to attempt an executor handoff before the
	// chat-direct loop runs. Empty string indicates the route classifier
	// has not yet run for this generation; consumers MUST treat that as
	// equivalent to RouteChatDirect (the conservative default — see
	// docs/architecture/classifier-routing.md §1).
	routeDecision classify.RouteDecision

	// F1 (CW-20260420-0014): Effort scalar. Set once by generateResponse from
	// the request context before the loop runs. Biases token budget (via
	// BudgetMultiplier) and reasoning-block enablement (via ReasoningCfg).
	// Orthogonal to ScopeTier — does NOT change roles, tools, or turn counts.
	currentEffort effort.Effort

	// Debug snapshots.
	debugMode bool
	snapshots []TurnSnapshot

	// Continuation tracking.
	lastSite   ContinueSite
	lastReason string

	// S3b T9 — counts how many synchronous compaction+retries we've run for
	// compact-recoverable provider failures (see
	// internal/context.IsCompactRecoverable) during this generation.
	// Originally a boolean (contextOverflowRetried → compactRecoverableRetried);
	// converted to a counter in CW-20260419-0018 after c21 UAT showed the
	// one-shot latch refusing a second compaction when we were only 86 tokens
	// over the rate budget. A second compaction pass would trivially free
	// more than that — the refusal was defensive but too strict.
	//
	// Capped at maxCompactRecoverableAttempts (2 for now). A refused recovery
	// (summarizer off, no stages applied) burns an attempt too, so the same
	// request can't loop back into the branch forever. Full progress-aware
	// guard design is tracked in CW-20260419-0018 (kept open for the deep fix).
	compactRecoverableAttempts int

	// Glass-6 (CW-20260502-0013, SP-20260502-0001) — number of
	// rate_budget_pause events already emitted for the active turn. Capped at
	// 1 auto-retry: the first pause emits suggested_action="auto_retry",
	// sleeps the WaitTime window, and re-runs StreamChat once. A second
	// pause flips suggested_action="user_action_needed" and ends the turn
	// cleanly without a fatal `error` event. The session stays alive — see
	// chat_rate_budget_pause.go for the event semantics.
	rateBudgetPauseAttempts int

	// P4 Scratchpad — per-turn writable key/value buffer (CW-20260419-0025).
	// Evicted automatically: loopState is created fresh per generateResponse call.
	scratchpad      map[string]any
	scratchpadBytes int

	// I1 (CW-20260426-0004): inspector turn ID for the dev-mode aggregator.
	// Set by generateResponse after slot assembly; propagated into
	// handleRequestTools and executeSingleTool so broker/tool producers
	// can append to the same per-turn snapshot.
	inspectorTurnID string
}

// maxCompactRecoverableAttempts caps the number of synchronous compaction
// retries per generation. 2 is the smallest value that fixes the observed
// "second compaction would obviously help" failure mode (UAT c21) without
// allowing runaway retry loops on a conversation that's truly too large.
const maxCompactRecoverableAttempts = 2

// P4 Scratchpad size caps (CW-20260419-0025, D2).
const (
	scratchpadMaxValueBytes = 8 * 1024  // 8 KiB per value
	scratchpadMaxTotalBytes = 64 * 1024 // 64 KiB total per turn
)

// newLoopState creates a loopState with resolved limits from agent
// constraints. The optional caller arg is the dispatch source (chat /
// subagent / background) — it selects the inactivity-timeout window: a
// subagent dispatch uses subagentIdleTimeoutSeconds (Torque-parity
// liveness window) instead of the 900s interactive default, since the
// fixed 300s wall clock that previously bounded subagent runs has been
// replaced by this inactivity signal (CW-20260519-0073). It is
// variadic so the many test call sites that don't exercise dispatch
// scoping can omit it (treated as CallerChat — the 900s default).
func newLoopState(constraints chat.AgentConstraints, tools []string, debugMode bool, caller ...dispatcher.CallerType) *loopState {
	ls := &loopState{
		lastToolResults:      make(map[string]string),
		toolRepeatCount:      make(map[string]int),
		blockedTools:         make(map[string]bool),
		toolCallCounts:       make(map[string]int),
		loadedTools:          make(map[string]bool),
		maxRequestToolsCalls: defaultMaxRequestToolsCalls,
		lastActivity:         time.Now(),
		debugMode:            debugMode,
		scratchpad:           make(map[string]any),
	}

	// Populate loadedTools from initial tool set.
	for _, name := range tools {
		ls.loadedTools[name] = true
	}

	// Resolve iteration limits.
	//
	// CW-20260512-0123 (SP-20260512-0011 W3): the `RetryBudget`,
	// `MaxIterations`, and `MaxTimeSeconds` agent-constraints fields
	// were removed. Phase 0 item 12 (2026-08-18) subsequently removed
	// `MaxTurns` too (soft/telemetry-only, never gated the loop) — the
	// runaway-fail-cap, hard ceiling, and idle-timeout are the
	// chat-loop's real breakers.
	ls.limits = resolveIterationLimits(constraints, caller...)

	return ls
}

// resolveIterationLimits computes effective limits from agent
// constraints and defaults. The optional caller arg selects the
// inactivity-timeout window: CallerSubagent uses
// subagentIdleTimeoutSeconds so a subagent run is bounded by *silence*
// rather than the fixed wall clock that CW-20260519-0073 removed;
// every other caller (and the omitted/empty case) keeps the 900s
// interactive default. An explicit `IdleTimeoutSeconds` agent
// constraint still overrides whichever default applies.
func resolveIterationLimits(c chat.AgentConstraints, caller ...dispatcher.CallerType) iterationLimits {
	idleTimeoutSeconds := defaultIdleTimeoutSeconds
	if len(caller) > 0 && caller[0] == dispatcher.CallerSubagent {
		idleTimeoutSeconds = subagentIdleTimeoutSeconds
	}
	lim := iterationLimits{
		maxTurns:           defaultMaxTurns,
		hardCeiling:        defaultHardCeiling,
		consecutiveFailCap: defaultConsecutiveFailCap,
		runawayFailCap:     defaultRunawayFailCap,
		idleTimeout:        time.Duration(idleTimeoutSeconds) * time.Second,
		perToolMax:         make(map[string]int),
	}

	// CW-20260512-0123 (SP-20260512-0011 W3) removed the legacy
	// `MaxIterations`/`MaxTimeSeconds`/`RetryBudget` agent-constraints
	// fields. Phase 0 item 12 (2026-08-18) removed the remaining
	// `MaxTurns` field too — it was soft/telemetry-only and never gated
	// the loop. `lim.maxTurns` now stays at `defaultMaxTurns` unconditionally;
	// it survives only as an inert diagnostic value (see `resolvedMaxTurns`).
	// The hard ceiling below is the loop's real absolute backstop.
	if c.HardCeiling > 0 {
		lim.hardCeiling = c.HardCeiling
	}

	if c.ConsecutiveFailCap > 0 {
		lim.consecutiveFailCap = c.ConsecutiveFailCap
	}

	if c.RunawayFailCap > 0 {
		lim.runawayFailCap = c.RunawayFailCap
	}

	// PR #64 feedback: a configured runaway cap lower than the soft cap would
	// terminate the loop before the "critical" tool_warning UI signal fires —
	// the whole point of splitting the caps. Clamp up so the soft-cap UX is
	// always reachable. Log so operators notice a mis-configured override.
	if lim.runawayFailCap < lim.consecutiveFailCap {
		slog.Warn(
			"RunawayFailCap lower than ConsecutiveFailCap; raising runaway cap to preserve soft-cap behavior",
			"configured_runaway_fail_cap", lim.runawayFailCap,
			"configured_consecutive_fail_cap", lim.consecutiveFailCap,
		)
		lim.runawayFailCap = lim.consecutiveFailCap
	}

	if c.IdleTimeoutSeconds > 0 {
		lim.idleTimeout = time.Duration(c.IdleTimeoutSeconds) * time.Second
	}

	return lim
}

// resolvedMaxTurns returns the effective max turns, clamped to the hard ceiling.
func (ls *loopState) resolvedMaxTurns() int {
	max := ls.limits.maxTurns
	if max == -1 {
		return ls.limits.hardCeiling
	}
	if max > ls.limits.hardCeiling {
		return ls.limits.hardCeiling
	}
	return max
}

// shouldStop checks the loop's hard terminators and returns whether the loop
// should exit, along with a structured TerminationCode + human-readable
// reason.
//
// Active terminators (in priority order):
//   - Runaway tool failures (`runawayFailCap`, default 10) — consecutive
//     tool errors trip the hard circuit-breaker (CW-20260417-0485 made
//     `consecutiveFailCap` soft; `runawayFailCap` is its hard counterpart).
//   - Idle timeout — wall-clock backstop.
//   - Hard ceiling (`hardCeiling`, default 200) — absolute turn-count
//     backstop. The agent's natural `end_turn` stop signal is still the
//     normal termination path; hardCeiling exists for true runaways.
//
// CW-20260512-0123 (SP-20260512-0011 W3): the retry-budget terminator
// was removed alongside the `RetryBudget` agent-constraints field.
// Runaway tool failures are now bounded solely by `runawayFailCap`
// (Layer 1 above). See the in-body note where Layer 4 used to live.
//
// CW-20260504-0001 made `max_turns` a non-terminator; Phase 0 item 12
// (2026-08-18) removed the soft-warning signal it used to fire
// (`checkSoftMaxTurnsWarning`) entirely, since it never stopped the
// loop either. The agent decides when exploration is done; the
// dup-detector ticket (CW-20260504-0002) closes the
// successful-but-stuck-loop gap that this softening opens.
func (ls *loopState) shouldStop() (bool, TerminationCode, string) {
	// Layer 1: Runaway tool failures (hard circuit-breaker).
	if ls.consecutiveFailures >= ls.limits.runawayFailCap {
		return true, TerminationRunawayToolFailures,
			fmt.Sprintf("runaway: %d consecutive tool failures", ls.consecutiveFailures)
	}

	// Layer 2: Idle timeout.
	if time.Since(ls.lastActivity) > ls.limits.idleTimeout {
		return true, TerminationIdleTimeout,
			fmt.Sprintf("idle timeout after %s", ls.limits.idleTimeout)
	}

	// Layer 3: Hard ceiling (absolute backstop).
	if ls.iteration >= ls.limits.hardCeiling {
		return true, TerminationHardCeiling,
			fmt.Sprintf("hard ceiling reached (%d)", ls.limits.hardCeiling)
	}

	// CW-20260512-0123 (SP-20260512-0011 W3): the retry-budget
	// terminator (Layer 4) was removed alongside the deleted
	// `RetryBudget` agent-constraints field. The
	// `TerminationRetryBudgetExhausted` constant is retained because
	// the recovery broker's envelope schema may still reference it
	// in archived telemetry; runaway tool failures are now bounded
	// by `runawayFailCap` (Layer 1).

	return false, "", ""
}

// recordToolCall updates counters after a tool call. Returns true if the tool
// is now exhausted (per-tool max iterations reached).
func (ls *loopState) recordToolCall(toolName string, success bool) bool {
	ls.toolCallCounts[toolName]++
	ls.lastActivity = time.Now()

	if success {
		ls.consecutiveFailures = 0
	} else {
		ls.consecutiveFailures++
	}

	// Meta-tools (fetch_tool_result, search_tool_result, request_tools) are
	// exempt from per-tool caps so the LLM can always recall cached results.
	if isMetaTool(toolName) {
		return false
	}

	// Check per-tool max (explicit override takes priority over global default).
	cap := ls.limits.defaultPerToolCap
	if max, ok := ls.limits.perToolMax[toolName]; ok && max > 0 {
		cap = max
	}
	if cap > 0 && ls.toolCallCounts[toolName] >= cap {
		ls.blockedTools[toolName] = true
		slog.Warn("chat-loop: tool exhausted", "tool", toolName, "calls", ls.toolCallCounts[toolName], "cap", cap)
		return true
	}

	return false
}

// recordPermissionDenial increments the consecutive failure counter for a
// permission denial.
func (ls *loopState) recordPermissionDenial() {
	ls.consecutiveFailures++
}

// recordLastError stores the last tool failure payload so that a subsequent
// chat-loop-terminated envelope can carry the triggering error. The error
// text is truncated to keep the envelope bounded (CW-20260417-0485).
func (ls *loopState) recordLastError(toolName, errText string) {
	ls.lastToolName = toolName
	const maxErrLen = 500
	// Truncate by rune count — byte-index slicing would split multibyte UTF-8
	// and produce invalid bytes in the envelope payload (PR #64 feedback).
	if runes := []rune(errText); len(runes) > maxErrLen {
		errText = string(runes[:maxErrLen]) + "... (truncated)"
	}
	ls.lastToolError = errText
}

// isToolExhausted returns true if the tool has hit its per-tool iteration limit.
// Meta-tools are always exempt.
func (ls *loopState) isToolExhausted(toolName string) bool {
	if isMetaTool(toolName) {
		return false
	}
	cap := ls.limits.defaultPerToolCap
	if max, ok := ls.limits.perToolMax[toolName]; ok && max > 0 {
		cap = max
	}
	if cap <= 0 {
		return false
	}
	return ls.toolCallCounts[toolName] >= cap
}

// isMetaTool returns true for meta-tools that are exempt from per-tool caps.
func isMetaTool(name string) bool {
	switch name {
	case "request_tools", "fetch_tool_result", "search_tool_result",
		"scratchpad_read": // P4: read-only access to loopState; exempt from per-tool caps
		return true
	}
	return false
}

// Classification returns the pre-loop (ScopeTier, ExecutionPattern) pair
// for this generation. Returns (TierInvalid, PatternInvalid) if Classify
// has not been called yet.
//
// Consumers feeding these values into metrics, budget math, or dispatch
// logic MUST check ScopeTier.IsValid() and ExecutionPattern.IsValid()
// before trusting them. The invalid-sentinel strings exist specifically to
// surface "classifier not run" as a detectable condition; letting them
// reach a log or budget computation unchecked is a bug. See the
// internal/classify package doc for the full consumer contract.
func (ls *loopState) Classification() (classify.ScopeTier, classify.ExecutionPattern) {
	return ls.scopeTier, ls.executionPattern
}

// SetClassification stores the pre-loop classification. Called by
// generateResponse once per generation, before the loop body runs.
func (ls *loopState) SetClassification(tier classify.ScopeTier, pattern classify.ExecutionPattern) {
	ls.scopeTier = tier
	ls.executionPattern = pattern
}

// RouteDecision returns the pre-loop route hint emitted by
// classify.ClassifyRoute (B2, CW-20260429-0031). Returns the zero
// RouteDecision (Route == "") when the route classifier has not yet
// run for this generation; consumers MUST treat that as equivalent to
// RouteChatDirect.
func (ls *loopState) RouteDecision() classify.RouteDecision {
	return ls.routeDecision
}

// SetRouteDecision stores the pre-loop route hint. Called by
// generateResponse once per generation, before the loop body runs.
func (ls *loopState) SetRouteDecision(d classify.RouteDecision) {
	ls.routeDecision = d
}

// touchActivity updates the last activity timestamp.
func (ls *loopState) touchActivity() {
	ls.lastActivity = time.Now()
}

// SetEffort stores the per-turn Effort scalar. Called by generateResponse
// once per generation, from the request context, before the loop body runs.
// Invalid values are silently promoted to effort.Default.
func (ls *loopState) SetEffort(e effort.Effort) {
	if !e.IsValid() {
		e = effort.Default
	}
	ls.currentEffort = e
}

// Effort returns the per-turn Effort scalar. Returns effort.Default when
// SetEffort has not been called.
func (ls *loopState) Effort() effort.Effort {
	if !ls.currentEffort.IsValid() {
		return effort.Default
	}
	return ls.currentEffort
}

// scratchpadWrite upserts key→value in the per-turn scratchpad.
// Returns an error if value exceeds the per-value cap or the write would push
// the total over the turn cap. The caller (tool handler) converts the error to
// an IsError tool result so the LLM can decide how to recover.
func (ls *loopState) scratchpadWrite(key string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("value is not JSON-serializable: %w", err)
	}
	newValueBytes := len(encoded)
	if newValueBytes > scratchpadMaxValueBytes {
		return fmt.Errorf("value too large: %d bytes exceeds per-value limit of %d bytes", newValueBytes, scratchpadMaxValueBytes)
	}

	// Subtract the old entry's byte contribution (upsert adjusts the counter).
	oldBytes := 0
	if old, ok := ls.scratchpad[key]; ok {
		if oldEncoded, encErr := json.Marshal(old); encErr == nil {
			oldBytes = len(oldEncoded)
		}
	}
	newTotal := ls.scratchpadBytes - oldBytes + newValueBytes
	if newTotal > scratchpadMaxTotalBytes {
		return fmt.Errorf("scratchpad total size exceeded: write would reach %d bytes (limit %d bytes)", newTotal, scratchpadMaxTotalBytes)
	}

	ls.scratchpad[key] = value
	ls.scratchpadBytes = newTotal
	return nil
}

// scratchpadRead returns the entries map for a single key (ok=false if missing)
// or all entries when key is empty (ok always true).
func (ls *loopState) scratchpadRead(key string) (map[string]any, bool) {
	if key != "" {
		v, ok := ls.scratchpad[key]
		if !ok {
			return nil, false
		}
		return map[string]any{key: v}, true
	}
	// Shallow copy: reference-type values (maps, slices) are shared with the scratchpad.
	result := make(map[string]any, len(ls.scratchpad))
	for k, v := range ls.scratchpad {
		result[k] = v
	}
	return result, true
}

// scratchpadClear deletes a key. Returns true if the key existed.
func (ls *loopState) scratchpadClear(key string) bool {
	v, ok := ls.scratchpad[key]
	if !ok {
		return false
	}
	// Marshal cannot fail here: scratchpadWrite already validated serializability
	// before storing. If it somehow does (direct map write bypassing scratchpadWrite),
	// we clamp to zero rather than leak bytes.
	if encoded, encErr := json.Marshal(v); encErr == nil {
		ls.scratchpadBytes -= len(encoded)
		if ls.scratchpadBytes < 0 {
			ls.scratchpadBytes = 0
		}
	} else {
		ls.scratchpadBytes = 0
	}
	delete(ls.scratchpad, key)
	return true
}

// continueWith logs a continuation site and optionally captures a snapshot.
func (ls *loopState) continueWith(site ContinueSite, reason string) {
	ls.lastSite = site
	ls.lastReason = reason
	slog.Debug("chat-loop: continue", "site", site, "reason", reason, "iter", ls.iteration)
}

// captureSnapshot records a TurnSnapshot if debug mode is active.
func (ls *loopState) captureSnapshot(site ContinueSite, reason string, tokensUsed int, messageCount int) {
	if !ls.debugMode {
		return
	}
	ls.snapshots = append(ls.snapshots, TurnSnapshot{
		ContinueSite: site,
		Reason:       reason,
		Iteration:    ls.iteration,
		MaxTurns:     ls.resolvedMaxTurns(),
		TokensUsed:   tokensUsed,
		MessageCount: messageCount,
		Timestamp:    time.Now(),
	})
}

// captureSnapshotWithTools records a TurnSnapshot with tool call details.
func (ls *loopState) captureSnapshotWithTools(site ContinueSite, reason string, tokensUsed int, messageCount int, toolCalls []ToolCallSnapshot) {
	if !ls.debugMode {
		return
	}
	ls.snapshots = append(ls.snapshots, TurnSnapshot{
		ContinueSite: site,
		Reason:       reason,
		Iteration:    ls.iteration,
		MaxTurns:     ls.resolvedMaxTurns(),
		ToolCalls:    toolCalls,
		TokensUsed:   tokensUsed,
		MessageCount: messageCount,
		Timestamp:    time.Now(),
	})
}
