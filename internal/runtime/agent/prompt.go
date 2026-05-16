package agent

import (
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
)

// composeSystemPrompt builds the role-aware system prompt planted in
// CLAUDE.md / AGENTS.md / agents/<name>.md (per-provider) at boot time.
//
// Composition order (joined with two newlines):
//  1. profile.SystemPrompt — the agent's persisted base prompt.
//  2. roleFraming(role)    — orchestrator / reviewer / planner / executor
//     prefix, when role is non-empty.
//  3. modeFraming(mode)    — long-lived / one-shot / subagent etc. tail
//     framing when material.
//
// For ModeLongLived sessions the prompt is set ONCE at PTY start (claude
// does not accept --system-prompt on resume); slot changes thereafter
// regenerate <bootDir>/CLAUDE.md and rely on claude's context-recovery
// re-read.
func composeSystemPrompt(role string, profile *store.AgentProfile, mode Mode) string {
	var parts []string
	if profile != nil && profile.SystemPrompt != "" {
		parts = append(parts, strings.TrimSpace(profile.SystemPrompt))
	}
	if frame := roleFraming(role); frame != "" {
		parts = append(parts, frame)
	}
	if frame := modeFraming(mode); frame != "" {
		parts = append(parts, frame)
	}
	return strings.Join(parts, "\n\n")
}

// resolveBootPrompt is the single hook the per-provider Layout
// implementations consult. When Options.BootPromptOverride is non-empty,
// it wins verbatim — CW-20260514-0048 (boot-profile-driven launches)
// passes the fully-rendered LaunchSpec.BootPrompt through here so the
// catalog-authored prompt lands on the runtime in place of the
// role-derived composeSystemPrompt result. Empty override delegates to
// composeSystemPrompt for the prior behavior.
//
// Keeping the resolution in one place means the three live layouts
// (claude / codex / opencode) — plus any future addition — share one
// override hook rather than three independently-wired branches.
func resolveBootPrompt(profile *store.AgentProfile, opts Options) string {
	if opts.BootPromptOverride != "" {
		return opts.BootPromptOverride
	}
	return composeSystemPrompt(opts.Role, profile, opts.Mode)
}

// ResolveSystemPrompt is the exported entry point for recomputing a
// session's boot prompt OUTSIDE agent.Boot — it applies the same
// resolution resolveBootPrompt does (override wins verbatim; otherwise
// role/profile/mode-composed), but takes the inputs loose rather than
// bundled in an Options.
//
// CW-20260516-0007 round 1: the chat service's mid-session CLAUDE.md
// regeneration (regenerateBootDirSlots) uses this so a slot refresh
// re-plants the SAME system prompt the initial Boot planted — role and
// mode framing AND a bootprofile LaunchSpec's BootPromptOverride
// included. Previously the regen path wrote only the agent profile's
// bare SystemPrompt, silently thinning a bootprofile session's
// operating instructions on the first mid-run slot change.
func ResolveSystemPrompt(role string, profile *store.AgentProfile, mode Mode, bootPromptOverride string) string {
	if bootPromptOverride != "" {
		return bootPromptOverride
	}
	return composeSystemPrompt(role, profile, mode)
}

// roleFraming returns the role-specific prefix for the system prompt. Empty
// for unrecognized roles (the agent profile's SystemPrompt covers the
// default case).
func roleFraming(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "orchestrator":
		return "You are an orchestrator. Coordinate work across subagents; do not perform task work directly. Hand off implementation to the appropriate agent and synthesize results back to the user."
	case "reviewer":
		return "You are a reviewer. Audit the work product against the stated acceptance criteria; do not modify code unless explicitly asked. Report findings as a structured envelope."
	case "planner":
		return "You are a planner. Produce a step-by-step execution plan with explicit decisions, branch points, and exit criteria. Do not execute the plan."
	case "executor":
		return "You are an executor. Carry out the assigned task end-to-end. Surface blockers as discoveries; do not silently defer."
	default:
		return ""
	}
}

// modeFraming returns the mode-specific tail framing. Material only for
// ModeSubagent today (so the child knows it's nested) and ModeBackground
// (so the agent knows there's no interactive user).
func modeFraming(mode Mode) string {
	switch mode {
	case ModeSubagent:
		return "You are running as a nested subagent. Your parent session will receive your final output as a tool result; surface intermediate progress through the inbox / messaging substrate, not chat."
	case ModeBackground:
		return "You are running as a background task. There is no interactive user; do not ask questions. Report completion (or surface blockers) through the messaging substrate and exit."
	default:
		return ""
	}
}
