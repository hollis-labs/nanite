// Package reflexes implements the FU-30 reflex engine — predicate
// evaluation, event and interval triggers, action dispatch, and
// base-reflex seeding for durable agents. A reflex evaluates a
// windowed snapshot of session-state signals and, when its trigger
// fires, stages an action such as inject_reminder, halt_session,
// send_message, force_tool_choice, or add_schedule. This matches the
// domain vocabulary used everywhere else: the agent_reflexes and
// pending_reflexes DB tables, the store.AgentReflex/store.PendingReflex
// types, and the /api/agents/{id}/reflexes API surface.
//
// Not to be confused with: internal/promptrouter — a completely
// different system, a deterministic phrase-match dispatch router that
// feeds dispatch.AssignRole from user input before nanite_execute_task
// runs. It shares no code, no lifecycle, and no runtime with this
// package. (It was previously named internal/reflex; it was renamed to
// internal/promptrouter to remove the naming collision this comment used
// to warn about.) See docs/promptrouter-catalog.md and
// docs/promptrouter-authoring.md for that system's docs.
//
// Historical note: this package was briefly named driftguard
// (CW-20260816-0062) in an attempt to resolve the naming collision
// above, then renamed back to reflexes because "driftguard" undersold
// the package's scope (general predicate/event/interval evaluation,
// not just drift detection) and broke from the surrounding domain
// vocabulary. Old references to internal/agent/driftguard mean this
// package.
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
package reflexes

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
	// ScopeTier / ExecutionPattern (Phase 4 item 02,
	// TASKS/phase-4/02-dispatch-to-agent-reflex-action-kind-and-broker-migration.md)
	// carry the CURRENT turn's pre-loop classify.ScopeTier /
	// classify.ExecutionPattern signal (internal/classify), stringified
	// via their own String() methods (e.g. "open", "subagent"). Unlike
	// every other State field — built from persisted history by
	// StateCollector — these two are populated directly by the caller
	// from the in-flight turn's already-computed classification;
	// StateCollector has no way to know a not-yet-decided turn's live
	// classification (it only reads rows already committed to the
	// store). Empty string when the caller has no classification to
	// offer. Only internal/service/chat_reflex_dispatch.go's
	// dispatch_to_agent evaluation populates these today — the general
	// inject_reminder/halt_session/etc. evaluation pass
	// (chat_reflexes.go's evaluateAndInjectReflexes, via
	// StateCollector.Collect) does not.
	ScopeTier        string `json:"scope_tier,omitempty"`
	ExecutionPattern string `json:"execution_pattern,omitempty"`
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
