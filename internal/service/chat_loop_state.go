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
	defaultConsecutiveFailCap  = 3
	defaultIdleTimeoutSeconds  = 900 // 15 minutes
	defaultMaxRequestToolsCalls = 3
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
	idleTimeout         time.Duration
	perToolMax          map[string]int // tool name → max iterations (0 = no limit)
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
// exit, along with the reason.
func (ls *loopState) shouldStop() (bool, string) {
	// Layer 1: Consecutive failures.
	if ls.consecutiveFailures >= ls.limits.consecutiveFailCap {
		return true, fmt.Sprintf("paused after %d consecutive failures", ls.consecutiveFailures)
	}

	// Layer 2: Idle timeout.
	if time.Since(ls.lastActivity) > ls.limits.idleTimeout {
		return true, fmt.Sprintf("idle timeout after %s", ls.limits.idleTimeout)
	}

	// Layer 3: Max turns.
	maxTurns := ls.resolvedMaxTurns()
	if ls.iteration >= maxTurns {
		return true, fmt.Sprintf("max turns reached (%d)", maxTurns)
	}

	// Layer 4: Hard ceiling (absolute, separate from maxTurns).
	if ls.iteration >= ls.limits.hardCeiling {
		return true, fmt.Sprintf("hard ceiling reached (%d)", ls.limits.hardCeiling)
	}

	// Layer 5: Retry budget exhausted.
	if ls.retryBudget == 0 {
		return true, "retry budget exhausted"
	}

	return false, ""
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

	// Check per-tool max.
	if max, ok := ls.limits.perToolMax[toolName]; ok && max > 0 {
		if ls.toolCallCounts[toolName] >= max {
			ls.blockedTools[toolName] = true
			slog.Warn("chat-loop: tool exhausted", "tool", toolName, "calls", ls.toolCallCounts[toolName], "max", max)
			return true
		}
	}

	return false
}

// recordPermissionDenial increments the consecutive failure counter for a
// permission denial.
func (ls *loopState) recordPermissionDenial() {
	ls.consecutiveFailures++
}

// isToolExhausted returns true if the tool has hit its per-tool iteration limit.
func (ls *loopState) isToolExhausted(toolName string) bool {
	max, ok := ls.limits.perToolMax[toolName]
	if !ok || max <= 0 {
		return false
	}
	return ls.toolCallCounts[toolName] >= max
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
