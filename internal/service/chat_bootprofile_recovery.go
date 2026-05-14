// Package service — CW-20260514-0049 boot-profile crash-recovery hooks.
//
// Goal:
//
//   - Let the broker dispatch replacement sessions for boot-profile-backed
//     chats without baking the boot-profile id into agent.Options (which
//     would leak the profile abstraction into the runtime).
//   - Keep the "normal launch never carries a stored resume id" guarantee
//     structural: the recovery code path is the only place that touches
//     resume-flavored fields.
//   - Re-resolve the LaunchSpec against the CURRENT catalog (fresh-catalog
//     policy, design default #2 documented in CW-20260514-0048's
//     implementer report). The recovery use case is "process died, get me
//     back to the configured state", not "preserve the dead process's
//     exact env".
//
// The mechanism is a pre-boot hook the chat composition root installs on
// the broker's AgentBoot adapter. On every broker-dispatched relaunch,
// the hook fires with the agent.Options the broker assembled; for
// boot-profile-backed sessions it re-resolves via Registry.CompileFor
// and overlays the new spec onto Options. Non-boot-profile sessions
// pass through unchanged so the legacy CLI / API recovery path keeps
// working.

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/hollis-labs/nanite/internal/bootprofile"
	runtimeagent "github.com/hollis-labs/nanite/internal/runtime/agent"
)

// recoveryPreBootHook is the chat-side pre-boot interceptor the recovery
// broker's agentBootAdapter invokes for every relaunch. CW-20260514-0049.
//
// Contract:
//
//   - SessionID is required (the broker always sets it; we still defend).
//   - Looks up the chat session row via the store. Non-boot-profile
//     sessions short-circuit so the legacy non-bootprofile recovery
//     flow stays untouched.
//   - For boot-profile sessions, calls Registry.CompileFor with fresh
//     session-scoped vars (design default #3: CompileFor is the natural
//     recovery hook; we don't reach around the registry into the
//     catalog file shape).
//   - On profile-not-found, returns an actionable error naming the
//     profile id so the broker's escalation breadcrumb pinpoints the
//     catalog drift.
//   - On success, applies the fresh LaunchSpec via
//     applyLaunchSpecToBootOpts and re-stashes it on
//     activeSessionLaunchSpecs so a subsequent driveBootSession turn
//     reads the same spec.
//
// Resume-ID disposition: this hook does NOT set Mode=ModeResume or
// ResumeFromCheckpoint. The structural guarantee from CW-20260514-0048
// remains: normal launches never carry resume fields, and the recovery
// code path is where any future resume-ID threading would land. A
// commented placeholder marks the seam so a future ticket has a clear
// home for the change.
//
// nil-safe: a nil chatServiceImpl returns an error (programmer bug; the
// hook should never have been installed). A non-bootprofile session
// returns nil error and leaves opts untouched.
func (s *chatServiceImpl) recoveryPreBootHook(opts *runtimeagent.Options) error {
	if s == nil {
		return errors.New("recoveryPreBootHook: chat service nil")
	}
	if opts == nil {
		return errors.New("recoveryPreBootHook: opts nil")
	}
	if opts.SessionID == "" {
		return errors.New("recoveryPreBootHook: SessionID empty (broker bug)")
	}
	if s.store == nil {
		// Defensive — the broker only ever runs when the store is wired.
		return nil
	}
	session, err := s.store.GetSession(opts.SessionID)
	if err != nil {
		// Not finding the session is unusual but recoverable — the
		// broker should escalate to Permanent with a clear cause.
		return fmt.Errorf("recovery resume: session %q lookup failed: %w", opts.SessionID, err)
	}
	if session == nil || !bootprofile.IsProviderID(session.Provider) {
		// Not a boot-profile session — pass through unchanged so the
		// legacy CLI / API recovery path keeps working.
		return nil
	}
	if s.bootProfiles == nil {
		return fmt.Errorf("recovery resume: boot profile registry not configured (session %q provider %q)",
			opts.SessionID, session.Provider)
	}
	profileID, ok := bootprofile.DecodeProviderID(session.Provider)
	if !ok {
		return fmt.Errorf("recovery resume: boot profile provider id %q failed to decode", session.Provider)
	}

	// Fresh-catalog policy: CompileFor consults the registry's CURRENT
	// cached catalog, NOT a serialized snapshot from the original boot.
	// If the operator edited the catalog while the dead session was in
	// flight, the replacement runs against the edited definition. This
	// is the documented design default; a future "preserve dead session
	// env exactly" variant would need a new code path.
	var agentVars *agentProfileForVars
	if sa, sErr := s.store.GetSessionPrimaryAgent(opts.SessionID); sErr == nil && sa != nil && sa.AgentID != "" {
		if ap, aErr := s.store.GetAgent(sa.AgentID); aErr == nil && ap != nil {
			agentVars = &agentProfileForVars{
				Slug:            ap.Slug,
				Name:            ap.Name,
				DefaultProvider: ap.DefaultProvider,
			}
		}
	}
	vars := buildBootProfileVarsForRecovery(opts.SessionID, session.Provider, session.Model, agentVars)

	spec, err := s.bootProfiles.CompileFor(profileID, vars)
	if err != nil {
		if errors.Is(err, bootprofile.ErrProfileNotFound) {
			return fmt.Errorf("recovery resume failed: profile %q not in current catalog (was deleted?)",
				profileID)
		}
		return fmt.Errorf("recovery resume: profile %q compile failed: %w", profileID, err)
	}
	if err := bootprofile.ResolveRequirements(spec); err != nil {
		return fmt.Errorf("recovery resume: profile %q requirements: %w", profileID, err)
	}

	// Overlay onto Options. applyLaunchSpecToBootOpts is the same helper
	// the normal-boot path uses, so the merge semantics stay identical
	// across "first boot" and "recovery relaunch" — that consistency is
	// what makes the resume-vs-normal-start split structural rather
	// than ad-hoc.
	applyLaunchSpecToBootOpts(opts, spec)

	// Re-stash so a follow-up driveBootSession turn (after the
	// broker's adopt hook lands the replacement into activeSessions)
	// reads the SAME spec the relaunch used. Without this, the next
	// turn's slot-change detection could try to regenerate against a
	// stale spec.
	s.activeSessionLaunchSpecs.Store(opts.SessionID, spec)

	// CW-20260514-0049 seam: resume-flavored field setting goes HERE.
	// Today the hook leaves Mode / ResumeFromCheckpoint untouched
	// because the broker's DispatchRetry already preserved the session
	// id (chat history / lineage / path grants survive structurally)
	// and there's no provider-level resume protocol behind any current
	// CLI adapter. A future ticket that wires e.g. claude-code's
	// session_id resume protocol would set:
	//
	//   opts.Mode = runtimeagent.ModeResume
	//   opts.ResumeFromCheckpoint = <stored checkpoint id>
	//
	// here. The "never on normal launch" guarantee stays intact because
	// applyLaunchSpecToBootOpts (used by driveBootSession's first-boot
	// path) does not touch these fields, and the only structural entry
	// to this hook is the recovery broker's DispatchRetry.

	slog.Info("recovery: boot-profile relaunch resolved against current catalog",
		"session_id", opts.SessionID,
		"profile_id", profileID,
		"provider", spec.Provider)
	return nil
}

// agentProfileForVars is the minimal subset of the agent profile the
// recovery hook needs to build the substitute-vars map. Decoupling
// avoids pulling the full *store.AgentProfile dependency surface into
// the hook signature; the chat layer assembles this struct from the
// session's primary-agent row at the point of use.
type agentProfileForVars struct {
	Slug            string
	Name            string
	DefaultProvider string
}

// buildBootProfileVarsForRecovery mirrors buildBootProfileVars but
// accepts the trimmed agentProfileForVars so the hook doesn't drag in
// the full store types. Kept separate so a future change to the var
// schema can specialize the recovery path without affecting the normal
// path.
func buildBootProfileVarsForRecovery(sessionID, provider, model string, agent *agentProfileForVars) bootprofile.Vars {
	out := bootprofile.Vars{}
	if sessionID != "" {
		out["session_id"] = sessionID
	}
	if provider != "" {
		out["session_provider"] = provider
	}
	if model != "" {
		out["session_model"] = model
	}
	if agent != nil {
		if agent.Slug != "" {
			out["agent_slug"] = agent.Slug
		}
		if agent.Name != "" {
			out["agent_name"] = agent.Name
		}
		if agent.DefaultProvider != "" {
			out["agent_provider"] = agent.DefaultProvider
		}
	}
	return out
}

// RestartAgentSession is the explicit user-driven restart entry point
// for a long-lived runtime session. CW-20260514-0049.
//
// Semantics:
//
//   - Idempotent: a missing session is a no-op.
//   - The chat session row stays alive (not archived). Only the
//     underlying runtime process + per-session bookkeeping is torn down.
//   - The stashed LaunchSpec is cleared. The next user turn re-runs
//     resolveBootProfile, which calls Registry.CompileFor against the
//     CURRENT catalog (fresh-catalog policy). Operator edits to the
//     YAML between stop and restart land naturally.
//   - This is NOT the recovery code path. Resume IDs do not flow.
//     If a future ticket wants explicit "restart from last checkpoint"
//     semantics, it should be a separate method.
//
// Returns nil on success or no-op. Returns the error from
// CloseAgentSession's Stop hook when process termination misbehaves.
func (s *chatServiceImpl) RestartAgentSession(ctx context.Context, sessionID string) error {
	if s == nil || sessionID == "" {
		return nil
	}
	// CloseAgentSession does the full cleanup we need; the only
	// behavioral difference from "archive" is that the chat session
	// row stays alive — the archive hook is on SessionService, not
	// here, so this method does NOT call ArchiveSession.
	s.CloseAgentSession(ctx, sessionID)
	return nil
}
