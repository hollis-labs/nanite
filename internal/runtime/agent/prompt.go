package agent

// composeSystemPrompt builds the role-aware system prompt planted in
// CLAUDE.md / AGENTS.md / agents/<name>.md (per-provider) at boot time.
//
// Phase 3 fills in the concrete role-aware assembly using nanite's
// existing prompt-template system. For ModeLongLived sessions the prompt
// is set ONCE at PTY start (claude does not accept --system-prompt on
// resume); slot changes thereafter regenerate <bootDir>/CLAUDE.md and
// rely on claude's context-recovery re-read.
func composeSystemPrompt(role string, profile AgentProfile, mode Mode) string {
	// Phase 2 stub: empty prompt; Phase 3 plumbs the real assembly.
	_ = role
	_ = profile
	_ = mode
	return ""
}
