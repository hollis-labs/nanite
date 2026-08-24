package service

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/agent/override"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// subagentCompletionReactor is the internal/service-side implementation of
// subagent.CompletionReactor (CW-20260520-0001, Layer 2 — "harness
// reacts"). It holds the concrete *chatServiceImpl (not the ChatService
// interface) so it can reach the unexported in-flight-generation registry
// and the TriggerHarnessTurn/IsGenerating helpers without growing the
// public ChatService surface for what is purely an internal wiring seam.
type subagentCompletionReactor struct {
	chat *chatServiceImpl
}

// ReactToCompletion implements subagent.CompletionReactor. Resolves the
// parent session's subagent-completion policy and, only for
// auto_summarize, proactively triggers a harness turn — provided the
// session doesn't already have one in flight (a sync/interactive run's
// parent turn is by definition active at this point, so this naturally
// no-ops for those modes; the busy check is what makes that true rather
// than a mode branch). render_and_wait and batch (which behaves as
// render_and_wait for v1 — see chat.AgentConstraints doc) are no-ops here:
// the CW-20260512-0019 turn-start injection remains their delivery
// guarantee whenever the session's next turn happens, harness-triggered or
// not.
//
// Idempotent by construction: this is called exactly once per run, in
// -process, right after the one-and-only finalizeRun completion write — no
// dedup table needed. Bounded by construction too: the synthetic prompt
// TriggerHarnessTurn sends asks only for a summary, and any subagent that
// prompt does spawn is still capped by the existing recursion-depth check
// (ParentageChecker) — no new guardrail required here.
func (r *subagentCompletionReactor) ReactToCompletion(ctx context.Context, run *subagent.Run, messageID string) {
	if r == nil || r.chat == nil || run == nil || run.ParentSessionID == "" {
		return
	}

	policy := r.chat.resolveSubagentCompletionPolicy(ctx, run.ParentSessionID)
	if policy != chat.SubagentPolicyAutoSummarize {
		return
	}

	if r.chat.IsGenerating(run.ParentSessionID) {
		slog.Debug("subagent-reactor: session busy, skipping harness trigger",
			"session_id", run.ParentSessionID, "run_id", run.ID)
		return
	}

	if _, err := r.chat.TriggerHarnessTurn(ctx, run.ParentSessionID, "subagent_completion", run.ID); err != nil {
		slog.Warn("subagent-reactor: trigger harness turn failed",
			"session_id", run.ParentSessionID, "run_id", run.ID, "err", err)
	}
}

// resolveSubagentCompletionPolicy resolves the effective subagent-completion
// policy for sessionID via the same role->agent->task composition cascade
// primitive used by resolveMessageWakePolicy. Layers, closest wins:
//  1. role tier (base) — deliberately the zero value: roles has no
//     subagent_completion_policy-equivalent column today.
//  2. agent tier (project) — the session's agent-profile default
//     (agent_profiles.constraints via chat.AgentConstraints.
//     SubagentCompletionPolicy).
//  3. task/invocation tier (session) — an explicit per-session override
//     (sessions.metadata["subagent_completion_policy"]), the narrowest layer.
//
// Falls back to the global default — render_and_wait, the safe default for
// interactive operator sessions — when no layer resolves to a non-empty value.
// Resolution failures (session/agent lookup errors) fall back to
// render_and_wait rather than risk auto-triggering a turn on a session that
// couldn't be fully resolved.
//
// Each tier is validated against chat.IsValidSubagentCompletionPolicy
// before being trusted — an unrecognized value (typo, stale config) is
// logged and treated as absent rather than returned verbatim, so a typo'd
// "auto_summarise" doesn't silently disable the intended behavior with no
// diagnostic trail (PR #247 review).
func (s *chatServiceImpl) resolveSubagentCompletionPolicy(ctx context.Context, sessionID string) string {
	var taskLayer *override.OverrideConfig
	if session, err := s.sessions.Get(ctx, sessionID); err == nil && session != nil && session.Metadata != "" {
		var meta map[string]any
		if json.Unmarshal([]byte(session.Metadata), &meta) == nil {
			if v, ok := meta["subagent_completion_policy"].(string); ok && v != "" {
				if chat.IsValidSubagentCompletionPolicy(v) {
					taskLayer = &override.OverrideConfig{SubagentCompletionPolicy: v}
				} else {
					slog.Warn("subagent-reactor: unrecognized session policy override, ignoring",
						"session_id", sessionID, "value", v)
				}
			}
		}
	}

	var agentLayer override.OverrideConfig
	if taskLayer == nil {
		if agent, err := s.agents.ResolveForSession(ctx, sessionID); err == nil && agent != nil {
			constraints := chat.ParseAgentConstraints(agent.Constraints)
			if constraints.SubagentCompletionPolicy != "" {
				if chat.IsValidSubagentCompletionPolicy(constraints.SubagentCompletionPolicy) {
					agentLayer.SubagentCompletionPolicy = constraints.SubagentCompletionPolicy
				} else {
					slog.Warn("subagent-reactor: unrecognized agent-profile policy default, ignoring",
						"session_id", sessionID, "agent_id", agent.ID, "value", constraints.SubagentCompletionPolicy)
				}
			}
		}
	}

	resolved := override.Resolve(override.OverrideConfig{}, &agentLayer, taskLayer)
	if resolved.SubagentCompletionPolicy != "" {
		return resolved.SubagentCompletionPolicy
	}

	return chat.SubagentPolicyRenderAndWait
}
