package agent

// composeKickoff builds the first-message payload threaded into
// agentsessions.StartOptions.FirstTurnPayload. Per the cross-app design,
// the kickoff is intentionally tiny — a pointer to the planted boot.md.
//
//	"Boot @./boot.md"
//
// Providers that don't resolve `@` references inline (currently every
// adapter except claude — verify per provider) fall back to raw content;
// composeBootContent returns the literal file content for that path.
//
// Phase 3 fills in role / session-id / parent-session-id threading and
// the raw-content fallback.
func composeKickoff(role, sessionID, parentSessionID string) string {
	_ = role
	_ = sessionID
	_ = parentSessionID
	return "Boot @./boot.md"
}

// composeBootContent renders the file content planted as boot.md.
// Phase 3 fills this with role-aware framing (orchestrator / reviewer /
// planner / executor) and the per-task instructions.
func composeBootContent(opts Options) string {
	_ = opts
	return ""
}
