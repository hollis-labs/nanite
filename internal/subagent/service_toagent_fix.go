package subagent

// This file contains the ToAgentID identity-resolution fix (CW-20260815-0027),
// mirroring the FromAgentID fix from CW-20260815-0023.

// replyToAgentID resolves the agent identity a subagent reply should be sent
// TO (CW-20260815-0027). run.ParentAgentID may contain a role slug (e.g.
// "operator") instead of the harness's agent-address format (a DB UUID or
// "file-<slug>"). While this doesn't cause the same auto-register collision
// that FromAgentID had (ToAgentID doesn't trigger auto-register), passing a
// slug as ToAgentID causes ValidateAgentID to fail because the resolver can't
// find a row with ID=slug when the real row has a different ID (e.g.
// "agt-operator-001").
//
// Resolving to the real row's ID here means messaging's resolver finds it
// immediately and validation succeeds. Falls back to the bare parentAgentID
// (the prior behavior) when no profile resolver is wired or when the lookup
// fails, which maintains compatibility with test paths and file-based agents.
//
// This mirrors replyFromAgentID's pattern: try to resolve via profiles, fall
// back to the original value if resolution isn't available or fails.
func (svc *Service) replyToAgentID(parentAgentID string) string {
	if svc.profiles != nil && parentAgentID != "" {
		// Try to resolve as a slug first. If parentAgentID is already a valid
		// UUID or file-based ID, GetAgentBySlug will return no rows and we'll
		// fall through to returning it unchanged.
		if profile, err := svc.profiles.GetAgentBySlug(parentAgentID); err == nil && profile.ID != "" {
			return profile.ID
		}
	}
	// Fallback: return the original value. This handles:
	//  - parentAgentID is already a valid UUID
	//  - parentAgentID is a file-based ID (e.g. "file-operator")
	//  - profiles resolver is not wired (test paths)
	//  - lookup failed (transient error, unknown slug)
	return parentAgentID
}
