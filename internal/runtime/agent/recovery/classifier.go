package recovery

// Classify decides the broker's response to a FailureEvent. Returns
// (Class, Reason, Remediation) packed in a Classification.
//
// Phase 1: skeleton — always returns ClassPermanent so the broker
// degrades safely if reached before the rule table lands. Phase 2
// fills in the rule set documented in the implementer prompt.
//
// The classifier is pure: same input always produces same output. It
// does not read or write broker state. Per-session memory (e.g. "this
// is the second transient retry on the same cause") flows via the
// FailureEvent.Attempt field — populated by the broker before calling
// Classify.
func Classify(ev *FailureEvent) Classification {
	if ev == nil || ev.Exit == nil {
		return Classification{
			Class:       ClassPermanent,
			Reason:      "nil failure event or exit info",
			Remediation: RemediationNone,
		}
	}

	// Phase 2 fleshes out the full rule table:
	//
	//   Cause idle_timeout            -> Transient, no remediation
	//   Cause watchdog_kill           -> ConfigPermissions, RegenerateCLAUDEMD
	//   Cause oom_kill                -> Transient (single retry)
	//   Cause restart_exhausted       -> Permanent
	//   Cause pty_eof + Code==0       -> not a failure (skip)
	//   SandboxDirState.Missing       -> ConfigPermissions, RepopulateSandbox
	//   MCPTransport.Down             -> ConfigPermissions, RefreshMCPTransport
	//   Signal 11 (SEGV)              -> Transient (single retry)
	//   Signal 9 + age < 5s           -> Permanent (likely missing binary)
	//   Signal 9 + age >= 5s          -> Transient
	//   Code 127 (command not found)  -> Permanent
	//   Stderr ~ 401/403/unauthorized -> ConfigPermissions, RefreshCredentials
	//   anything else with Code != 0  -> Transient on first occurrence; Permanent on second
	//   Attempt >= broker.maxRetries  -> Permanent (hard cap)
	//
	// All implemented in Phase 2 with table-driven tests.
	return Classification{
		Class:       ClassPermanent,
		Reason:      "classifier skeleton (Phase 1) — Phase 2 will fill rule table",
		Remediation: RemediationNone,
	}
}
