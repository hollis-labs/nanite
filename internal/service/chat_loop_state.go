package service

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
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
const (
	defaultMaxTurns            = 25
	defaultHardCeiling         = 100
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
	defaultMaxRequestToolsCalls = 3
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

	// Debug snapshots.
	debugMode bool
	snapshots []TurnSnapshot

	// Continuation tracking.
	lastSite   ContinueSite
	lastReason string

	// S3b T9 — true once we've run the synchronous compaction+retry after a
	// provider returned a context-overflow error. A second overflow in the
	// same generateResponse surfaces as a user-visible error rather than
	// looping indefinitely.
	contextOverflowRetried bool
}

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
	case "request_tools", "fetch_tool_result", "search_tool_result":
		return true
	}
	return false
}

// touchActivity updates the last activity timestamp.
func (ls *loopState) touchActivity() {
	ls.lastActivity = time.Now()
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
