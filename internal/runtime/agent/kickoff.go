package agent

import (
	"fmt"
	"strings"
)

// composeKickoff returns the tiny first-message payload threaded into
// agentsessions.StartOptions.FirstTurnPayload. Per cross-app design §6 the
// kickoff is a pointer to the planted boot.md so the IPC surface stays
// small and the agent re-reads the same file on context recovery:
//
//	"Boot @./boot.md"
//
// Providers that don't resolve @-references inline use the raw-content
// fallback returned by composeKickoffRaw — which embeds composeBootContent.
func composeKickoff(role, sessionID, parentSessionID string) string {
	_ = role
	_ = sessionID
	_ = parentSessionID
	return "Boot @./boot.md"
}

// composeKickoffRaw is the fallback for adapters whose @-resolution is
// unverified (codex, opencode, gemini, etc.). It embeds the boot.md content
// directly into the first-message payload.
func composeKickoffRaw(opts Options) string {
	body := composeBootContent(opts)
	if body == "" {
		// Defensive: never send an empty payload; the bare role tag at
		// least gives the agent something to anchor on.
		return fmt.Sprintf("# Boot\n\nrole: %s\nsession: %s\n", opts.Role, opts.SessionID)
	}
	return body
}

// composeBootContent renders the file content planted as boot.md. The
// content is intentionally simple and structural; agents that need richer
// per-task framing can append via opts.SessionMeta["boot_extras"] without
// touching this function (Phase 4 follow-up).
func composeBootContent(opts Options) string {
	var b strings.Builder
	b.WriteString("# Boot\n\n")
	if opts.Role != "" {
		fmt.Fprintf(&b, "**Role:** %s\n", opts.Role)
	}
	if opts.AgentProfile != "" {
		fmt.Fprintf(&b, "**Agent profile:** %s\n", opts.AgentProfile)
	}
	if opts.SessionID != "" {
		fmt.Fprintf(&b, "**Session id:** %s\n", opts.SessionID)
	}
	if opts.ParentSessionID != "" {
		fmt.Fprintf(&b, "**Parent session id:** %s\n", opts.ParentSessionID)
	}
	if opts.Mode != ModeLongLived {
		fmt.Fprintf(&b, "**Mode:** %s\n", opts.Mode.String())
	}

	switch opts.Mode {
	case ModeOneShot:
		b.WriteString("\nThis is a one-shot invocation. Complete the task in a single response and exit.\n")
		if opts.OneShotPrompt != "" {
			fmt.Fprintf(&b, "\n## Task\n\n%s\n", strings.TrimSpace(opts.OneShotPrompt))
		}
	case ModeSubagent:
		b.WriteString("\nYou are a nested subagent. Your parent will consume your final output as a tool result.\n")
		if opts.OneShotPrompt != "" {
			fmt.Fprintf(&b, "\n## Task\n\n%s\n", strings.TrimSpace(opts.OneShotPrompt))
		}
	case ModeBackground:
		b.WriteString("\nYou are running as a background task. No interactive user is attached; surface results via the messaging substrate.\n")
	case ModeResume:
		b.WriteString("\nYou are resuming from a checkpoint. Pick up where the prior session left off.\n")
	default:
		b.WriteString("\nYou are running as an interactive session. Engage with the user via chat.\n")
	}

	return b.String()
}
