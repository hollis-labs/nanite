package service

// messagingWakeReactor is the internal/service-side implementation of
// messaging.WakeReactor (CW-20260816-0065, "unify SendMessage to trigger
// live wake"). Sibling to subagentCompletionReactor: same shape (resolve
// policy → busy-check → TriggerXxx), different event source (a live
// go-messaging/mailbox A2A send vs. a subagent completion) and a different
// default policy — see resolveMessageWakePolicy's doc comment for why the
// default is inverted relative to resolveSubagentCompletionPolicy's.

import (
	"context"
	"encoding/json"
	"log/slog"

	messaging "github.com/hollis-labs/go-messaging/mailbox"
	"github.com/hollis-labs/nanite/internal/agent/override"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/subagent"
)

type messagingWakeReactor struct {
	chat *chatServiceImpl
}

// ReactToMessage implements messaging.WakeReactor. Resolves the
// recipient session's message-wake policy and, for auto_summarize,
// proactively triggers a harness turn carrying the message — provided
// the session doesn't already have one in flight. render_and_wait and
// batch (v1: behaves as render_and_wait) are no-ops here: the message
// stays durably queryable via message_inbox/message_thread, just
// without a proactive nudge.
//
// Called by mailbox.Service.SendMessage in its own goroutine for every send.
// The first guard below is Nanite's host-owned exclusion for subagent results,
// leaving that kind exclusively to subagent.CompletionReactor.
func (r *messagingWakeReactor) ReactToMessage(ctx context.Context, msg *messaging.Message) {
	if r == nil || r.chat == nil || msg == nil || msg.ToSessionID == "" || msg.Kind == subagent.ResultMessageKind {
		return
	}

	policy := r.chat.resolveMessageWakePolicy(ctx, msg.ToSessionID)
	if policy != chat.SubagentPolicyAutoSummarize {
		return
	}

	if r.chat.IsGenerating(msg.ToSessionID) {
		slog.Debug("messaging-reactor: session busy, skipping wake trigger",
			"session_id", msg.ToSessionID, "message_id", msg.ID)
		return
	}

	if _, err := r.chat.TriggerMessageWake(ctx, msg.ToSessionID, msg); err != nil {
		slog.Warn("messaging-reactor: trigger message wake failed",
			"session_id", msg.ToSessionID, "message_id", msg.ID, "err", err)
	}
}

// resolveMessageWakePolicy resolves the effective message-wake policy for
// sessionID via the role->agent->task composition cascade
// (internal/agent/override, the same merge engine
// internal/service/role_cascade.go's ResolveAgentCascade is built on —
// see that file and architecture/01-agent-construction.md for the
// broader cascade this reuses), rather than its own independent
// three-step bespoke walk. Layers, closest wins:
//  1. role tier (base) — deliberately the zero value: roles has no
//     message_wake_policy-equivalent column today (same "no live
//     role-level source column yet, but wire the seam" status as
//     ModelID's precedent in role_cascade.go/merge.go), so this layer
//     never contributes a value in practice yet.
//  2. agent tier (project) — the session's agent-profile default
//     (agent_profiles.constraints via chat.AgentConstraints.
//     MessageWakePolicy).
//  3. task/invocation tier (session) — an explicit per-session override
//     (sessions.metadata["message_wake_policy"]), the narrowest layer.
//
// Falls back to the global default — auto_summarize (CW-20260816-0065) —
// when no layer resolves to a non-empty value.
//
// This default is the opposite of resolveSubagentCompletionPolicy's
// (render_and_wait): a subagent completion still reaches the model via
// the kind=subagent_result turn-start injection (CW-20260512-0019) even
// when render_and_wait suppresses the proactive trigger, but a generic
// A2A message has no equivalent fallback delivery path — defaulting to
// render_and_wait here would just reproduce the poll-only gap this
// ticket exists to close for any agent that hasn't explicitly opted in.
// Resolution failures (session/agent lookup errors) fall back to the
// same auto_summarize default for the same reason, rather than the
// conservative render_and_wait a subagent-completion resolution failure
// falls back to.
//
// Mirrors resolveSubagentCompletionPolicy's tiering and typo-safety
// (each tier is validated via chat.IsValidSubagentCompletionPolicy so a
// stale or misspelled override is logged and ignored — left out of its
// layer entirely so the merge falls through to the next tier — rather
// than trusted verbatim) but is intentionally a separate resolver and
// session-metadata key: whether an agent wants its own dispatched
// subagents to auto-summarize and whether it wants to be woken by a
// peer's message are independent operator choices that happen to share
// the same three-state vocabulary (chat.SubagentPolicy* constants)
// rather than warranting a third, differently-shaped gating mechanism.
func (s *chatServiceImpl) resolveMessageWakePolicy(ctx context.Context, sessionID string) string {
	var agentLayer override.OverrideConfig

	// Read-only lookup deliberately: this is a fire-and-forget policy
	// check that runs on every eligible SendMessage (see
	// messaging.Service.SendMessage's call site), not an interactive
	// turn establishing a real session-agent binding. ResolveForSession
	// would auto-assign a session_agents row (EnsureSessionAgent) and
	// emit AgentAssigned as a side effect for any unbound session it
	// touches — ResolveForSessionReadOnly runs the identical resolution
	// chain without that mutation. See internal/service/agent.go's doc
	// comment on both methods for the full rationale.
	if agent, err := s.agents.ResolveForSessionReadOnly(ctx, sessionID); err == nil && agent != nil {
		constraints := chat.ParseAgentConstraints(agent.Constraints)
		if constraints.MessageWakePolicy != "" {
			if chat.IsValidSubagentCompletionPolicy(constraints.MessageWakePolicy) {
				agentLayer.MessageWakePolicy = constraints.MessageWakePolicy
			} else {
				slog.Warn("messaging-reactor: unrecognized agent-profile policy default, ignoring",
					"session_id", sessionID, "agent_id", agent.ID, "value", constraints.MessageWakePolicy)
			}
		}
	}

	var taskLayer *override.OverrideConfig
	if session, err := s.sessions.Get(ctx, sessionID); err == nil && session != nil && session.Metadata != "" {
		var meta map[string]any
		if json.Unmarshal([]byte(session.Metadata), &meta) == nil {
			if v, ok := meta["message_wake_policy"].(string); ok && v != "" {
				if chat.IsValidSubagentCompletionPolicy(v) {
					taskLayer = &override.OverrideConfig{MessageWakePolicy: v}
				} else {
					slog.Warn("messaging-reactor: unrecognized session policy override, ignoring",
						"session_id", sessionID, "value", v)
				}
			}
		}
	}

	// override.Resolve(base, project, session) applies role -> agent ->
	// task in that order, closest (task) wins. base is the zero value —
	// see doc comment above.
	resolved := override.Resolve(override.OverrideConfig{}, &agentLayer, taskLayer)
	if resolved.MessageWakePolicy != "" {
		return resolved.MessageWakePolicy
	}

	return chat.SubagentPolicyAutoSummarize
}
