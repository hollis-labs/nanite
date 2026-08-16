// Package driftguard implements the FU-30 reflex engine — predicate
// evaluation, action dispatch, and base-reflex seeding for the agridd
// monitor loop.
//
// Naming note (CW-20260816-0062): this package was formerly
// internal/agent/reflexes. It was renamed to disambiguate it from the
// unrelated internal/reflex package (the deterministic phrase-match
// router that feeds dispatch.AssignRole — see docs/agent-reflex-catalog.md
// and docs/reflex-authoring.md). The domain vocabulary inside this
// package ("reflex" as a stored predicate/action row, store.AgentReflex,
// the agent_reflexes table, /api/agents/{id}/reflexes) is unchanged —
// only the Go package identity moved.
//
// Architecture:
//
//	State    — windowed snapshot of session signals (token usage,
//	           messages, tool calls, events) read fresh per Evaluate
//	           call. See state.go.
//	Evaluator — interprets a JSON-encoded trigger_spec AST over State
//	           and returns true/false. See evaluator.go.
//	Executor  — given fired reflexes, stages their actions
//	           (inject_reminder bodies, halt requests, etc.). See
//	           executor.go.
//	Engine    — high-level entry point: Evaluate(ctx, sessionID,
//	           agentID) → AppliedActions. See engine.go.
//
// CRITICAL design constraint (carried over from FU-21):
//
// Reflex predicates MUST be conjunctions over multiple signals. Single
// detectors false-positive — the canonical example is healthy idle
// compression vs the FU-13 cache-miss-echo attractor: both show "output
// shorter than prior pass," but one is fine and the other is a
// runaway. The Echo signature requires:
//
//	input_tokens ≈ 3 AND cache_read == 0 AND identical_output AND
//	zero_tool_calls AND double-nested envelope
//
// The healthy-compression signature shares only the surface symptom and
// MUST NOT trip the halt reflex. The TestDriftDetector_DoesNotFireOnHealthyCompression
// test encodes this contract — do not regress it.
//
// Reference: docs/durable-agents/notes/torque-supervisor-hardening-log.md
// "Diagnostic reframing 2026-05-20" entry.
package driftguard

import (
	"github.com/hollis-labs/nanite/internal/store"
)

// MessageSignal is the per-turn signal vector the evaluator inspects.
// One row per assistant message in the recent window. Ordered
// most-recent-first.
type MessageSignal struct {
	MessageID     string   `json:"message_id"`
	Role          string   `json:"role,omitempty"`
	Content       string   `json:"content"`
	CreatedAt     string   `json:"created_at,omitempty"`
	InputTokens   int      `json:"input_tokens"`
	OutputTokens  int      `json:"output_tokens"`
	CacheRead     int      `json:"cache_read"`
	ToolCalls     int      `json:"tool_calls"`
	ToolNames     []string `json:"tool_names,omitempty"`
	EnvelopeTypes []string `json:"envelope_types,omitempty"`
}

// State is the full snapshot the evaluator consults. Built fresh per
// Engine.Evaluate call by the StateCollector.
type State struct {
	SessionID  string `json:"session_id"`
	AgentID    string `json:"agent_id"`
	AgentClass string `json:"agent_class"`
	// Messages is most-recent-first. Element 0 is the latest turn.
	Messages []MessageSignal `json:"messages"`
	// UserMessages is most-recent-first. Element 0 is the latest user turn.
	UserMessages []MessageSignal `json:"user_messages,omitempty"`
	// Events is a recent event_log slice — used by event-trigger
	// reflexes. Bounded (last 50 entries). Ordered most-recent-first.
	Events []EventSignal `json:"events"`
	// MailUnreadCount is the count of unread agent_messages destined
	// for this agent (or its URN aliases). Drives the wake_on_mail
	// event detector; nonzero ⇒ event="mail_received" considered fired
	// since last reset.
	MailUnreadCount int `json:"mail_unread_count"`
	// TickN is the current tick counter (best-effort; 0 when unknown).
	// Drives interval reflex evaluation.
	TickN int `json:"tick_n"`
	// PrefixTokens is the most recent input prefix size, used by the
	// context_pressure predicate. 0 means unknown.
	PrefixTokens int `json:"prefix_tokens"`
}

// EventSignal is a thin projection of store.EventLogEntry for the
// evaluator's event predicate path.
type EventSignal struct {
	EventType string `json:"event_type"`
	Category  string `json:"category"`
	CreatedAt string `json:"created_at"`
}

// AppliedAction describes a single action that a reflex requested. The
// monitor-loop integration translates these into concrete effects
// (system-reminder injection, halt RPC, schedule insert).
type AppliedAction struct {
	ReflexID   string                 `json:"reflex_id"`
	ReflexName string                 `json:"reflex_name"`
	ActionKind string                 `json:"action_kind"`
	Spec       map[string]interface{} `json:"spec"`
}

// staged actions returned by Engine.Evaluate.
type AppliedActions struct {
	Actions []AppliedAction
	// FiredReflexes is the raw list of reflex rows whose triggers
	// evaluated true this tick. The Engine has already invoked the
	// executor for each; this field is exposed for diagnostic /
	// event-log surfaces.
	FiredReflexes []store.AgentReflex
}
