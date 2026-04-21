package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/classify"
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
// CW-20260419-0020 (tracked for the long-term fix): `defaultMaxTurns`
// as a fixed constant is the wrong shape — the tool broker doesn't
// know upfront whether a task is small or large, and any fixed ceiling
// cuts the agent off mid-thought when scope legitimately expands. The
// intended design (see the task for full spec) is a negotiated budget:
//
//   1. Broker picks an initial budget based on classified task size.
//   2. On hitting the budget, the loop asks the agent to explain WHY
//      it needs more (scope grew, tool failure streak, exploration
//      fan-out, etc.) instead of terminating.
//   3. Broker decides whether to raise (and by how much) or stop.
//   4. Every decision + reasoning is logged so we can audit over time.
//
// Until that lands: interim bump to 75 (from 25) after c17/c27 UAT showed
// 25 is the default-case ceiling, not a rare safety net — list+analyze
// asks routinely need 20-30 tool calls just for the fetching phase, and
// the LLM was being cut off mid-thought with no final message. 75 gives
// headroom without uncorking; hardCeiling=200 still catches true runaways.
// Tunable via user_settings once CW-20260419-0020 ships.
const (
	defaultMaxTurns            = 75
	defaultHardCeiling         = 200
	// CW-20260417-0485: ConsecutiveFailCap used to terminate the chat loop,
	// cutting the LLM off before it could respond to the tool errors it had
	// just received as tool_result blocks. The loop now threads tool errors
	// back to the LLM on every iteration (always has — the tool_result
	// blocks carry IsError=true) and only terminates on a *runaway* — see
	// defaultRunawayFailCap. The soft cap still drives the "critical"
	// tool_warning SSE event so the UI can warn the user before the runaway
	// trips, but it no longer exits the loop.
	defaultConsecutiveFailCap  = 3
	// defaultRunawayFailCap is the hard circuit-breaker. When consecutive
	// tool failures reach this count the loop emits a typed
	// `chat-loop-terminated` envelope and exits. Sized high enough that a
	// reasonable LLM has multiple turns to recover (acknowledge error, try
	// different args, try a different tool, or stop gracefully) before the
	// breaker trips.
	defaultRunawayFailCap      = 10
	defaultIdleTimeoutSeconds  = 900 // 15 minutes
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
	TerminationRunawayToolFailures TerminationCode = "runaway_tool_failures"
	TerminationMaxTurns            TerminationCode = "max_turns"
	TerminationHardCeiling         TerminationCode = "hard_ceiling"
	TerminationIdleTimeout         TerminationCode = "idle_timeout"
	TerminationRetryBudgetExhausted TerminationCode = "retry_budget_exhausted"
)

// TurnSnapshot captures diagnostic state at a continuation point.
type TurnSnapshot struct {
	ContinueSite ContinueSite       `json:"site"`
	Reason       string             `json:"reason"`
	Iteration    int                `json:"iteration"`
	MaxTurns     int                `json:"max_turns"`
	ToolCalls    []ToolCallSnapshot `json:"tool_calls,omitempty"`
	TokensUsed   int               `json:"tokens_used"`
	MessageCount int               `json:"message_count"`
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
	maxTurns            int
	hardCeiling         int
	consecutiveFailCap  int
	// runawayFailCap is the hard circuit-breaker. See defaultRunawayFailCap
	// and the CW-20260417-0485 comment there for the rationale.
	runawayFailCap      int
	idleTimeout         time.Duration
	perToolMax          map[string]int // tool name → max iterations (0 = no limit)
	defaultPerToolCap   int            // global per-tool cap from UserSettings (0 = no cap)
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

	// Iteration control.
	iteration           int
	limits              iterationLimits
	consecutiveFailures int
	retryBudget         int
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

	// Activity tracking.
	lastActivity time.Time

	// Pre-loop classification from P3 (CW-20260420-0013). Set once by
	// generateResponse before the chat loop runs; read by downstream
	// consumers (budgets, strategy loop, playbook, background-job) via
	// Classification(). Zero values (TierInvalid, PatternInvalid) indicate
	// the classifier has not yet run for this generation.
	scopeTier        classify.ScopeTier
	executionPattern classify.ExecutionPattern

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

	// P4 Scratchpad — per-turn writable key/value buffer (CW-20260419-0025).
	// Evicted automatically: loopState is created fresh per generateResponse call.
	scratchpad      map[string]any
	scratchpadBytes int
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

// newLoopState creates a loopState with resolved limits from agent constraints.
func newLoopState(constraints chat.AgentConstraints, tools []string, debugMode bool) *loopState {
	ls := &loopState{
		lastToolResults:      make(map[string]string),
		toolRepeatCount:      make(map[string]int),
		blockedTools:         make(map[string]bool),
		toolCallCounts:       make(map[string]int),
		loadedTools:          make(map[string]bool),
		maxRequestToolsCalls: defaultMaxRequestToolsCalls,
		lastActivity:         time.Now(),
		debugMode:            debugMode,
		retryBudget:          -1,
		scratchpad:           make(map[string]any),
	}

	// Populate loadedTools from initial tool set.
	for _, name := range tools {
		ls.loadedTools[name] = true
	}

	// Resolve retry budget.
	if constraints.RetryBudget > 0 {
		ls.retryBudget = constraints.RetryBudget
	}

	// Resolve iteration limits.
	ls.limits = resolveIterationLimits(constraints)

	return ls
}

// resolveIterationLimits computes effective limits from agent constraints and defaults.
func resolveIterationLimits(c chat.AgentConstraints) iterationLimits {
	lim := iterationLimits{
		maxTurns:           defaultMaxTurns,
		hardCeiling:        defaultHardCeiling,
		consecutiveFailCap: defaultConsecutiveFailCap,
		runawayFailCap:     defaultRunawayFailCap,
		idleTimeout:        time.Duration(defaultIdleTimeoutSeconds) * time.Second,
		perToolMax:         make(map[string]int),
	}

	// MaxTurns: 0 = use default, -1 = unlimited (clamped to hard ceiling), >0 = use value.
	if c.MaxTurns > 0 {
		lim.maxTurns = c.MaxTurns
	} else if c.MaxTurns == -1 {
		lim.maxTurns = -1
	}

	// Legacy MaxIterations support: if set and lower than maxTurns, use it.
	if c.MaxIterations > 0 && (lim.maxTurns == -1 || c.MaxIterations < lim.maxTurns) {
		lim.maxTurns = c.MaxIterations
	}

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

// shouldStop checks all iteration limits and returns whether the loop should
// exit, along with a structured TerminationCode + human-readable reason.
//
// CW-20260417-0485: the consecutiveFailCap (soft) threshold no longer stops
// the loop — it is now a warning-level signal only. The loop terminates on
// the runaway cap (hard circuit-breaker) instead, which gives the LLM room
// to observe its tool_result errors and self-correct or stop gracefully.
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

	// Layer 3: Hard ceiling (absolute circuit-breaker).
	// Checked before maxTurns so hitting the ceiling reports the correct code.
	// resolvedMaxTurns() clamps maxTurns to hardCeiling, so without this order
	// a hard-ceiling hit would always report as TerminationMaxTurns and the
	// hard_ceiling envelope code would be unreachable (PR #64 feedback).
	if ls.iteration >= ls.limits.hardCeiling {
		return true, TerminationHardCeiling,
			fmt.Sprintf("hard ceiling reached (%d)", ls.limits.hardCeiling)
	}

	// Layer 4: Max turns (configured, <= hardCeiling).
	maxTurns := ls.resolvedMaxTurns()
	if ls.iteration >= maxTurns {
		return true, TerminationMaxTurns,
			fmt.Sprintf("max turns reached (%d)", maxTurns)
	}

	// Layer 5: Retry budget exhausted.
	if ls.retryBudget == 0 {
		return true, TerminationRetryBudgetExhausted, "retry budget exhausted"
	}

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
		if ls.retryBudget > 0 {
			ls.retryBudget--
		}
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
		"nanite_scratchpad_read": // P4: read-only access to loopState; exempt from per-tool caps
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

// touchActivity updates the last activity timestamp.
func (ls *loopState) touchActivity() {
	ls.lastActivity = time.Now()
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
