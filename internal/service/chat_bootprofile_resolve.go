package service

import (
	"errors"
	"fmt"

	"github.com/hollis-labs/nanite/internal/bootprofile"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/store"
)

// resolveBootProfile is the chat-resolve layer hook for
// CW-20260514-0048. If providerName is an encoded bootprofile
// provider id ("bootprofile:<profile_id>") AND the chat service has a
// boot-profile registry wired, this helper:
//
//  1. Compiles the named profile against the cached catalog with
//     session-scoped vars (vars derived from the chat session +
//     agent profile so authors can {{session_id}}, {{role}}, etc.).
//  2. Drains the Requirement list via ResolveRequirements. CW-20260514-0048
//     scope is deliberately narrow: text + static slot sources resolve at
//     compile time; cmd / http / role_summary / skill_index surface as a
//     pointed error so the operator gets actionable feedback instead of
//     a silent half-rendered prompt. The "real" resolvers are deferred to
//     a future ticket.
//  3. Stashes the compiled spec on the chat service so driveBootSession
//     can thread Env / Args / Workdir / BootPromptOverride into agent.Boot.
//
// The returned resolvedProvider is:
//
//   - The CLI-routable form of spec.Provider — typically spec.ProviderAlias
//     (e.g. "pty-claude") so chat.IsCLIProvider returns true and the
//     downstream classifyNilProvider routes to nilProviderRouteCLI. When
//     the launch declared a bare-adapter alias (e.g. just "claude"),
//     this function synthesizes the "pty-<adapter>" form so the existing
//     CLI bypass keeps firing. IsCLIProvider stays narrow per design
//     question #2 — only the substituted form ever reaches the classifier.
//   - The input verbatim when the input is NOT a bootprofile id (so the
//     caller can chain through legacy paths unchanged).
//
// Errors are wrapped with the profile id so operators see which entry
// failed without re-deriving from logs. Callers MUST surface the error
// to the user — silently falling back to the original provider name
// would mask a misconfigured catalog entry as a generic "provider not
// available" footer.
func (s *chatServiceImpl) resolveBootProfile(sessionID, providerName string, session *store.Session, agent *store.AgentProfile) (resolvedProvider string, spec *bootprofile.LaunchSpec, err error) {
	if providerName == "" {
		return providerName, nil, nil
	}
	if !bootprofile.IsProviderID(providerName) {
		return providerName, nil, nil
	}
	if s.bootProfiles == nil {
		// Encoded id arrived but the registry isn't wired — surface a
		// pointed error rather than letting the classifier degrade to
		// the generic fatal branch (which would say "provider not
		// available" instead of "boot profile registry not configured").
		return providerName, nil, fmt.Errorf("boot profile registry not configured: cannot resolve provider %q", providerName)
	}
	profileID, ok := bootprofile.DecodeProviderID(providerName)
	if !ok {
		// IsProviderID returned true but Decode said no — defensive.
		return providerName, nil, fmt.Errorf("boot profile provider id %q failed to decode", providerName)
	}

	vars := buildBootProfileVars(sessionID, session, agent)

	compiled, err := s.bootProfiles.CompileFor(profileID, vars)
	if err != nil {
		if errors.Is(err, bootprofile.ErrProfileNotFound) {
			return providerName, nil, fmt.Errorf("boot profile not found: %q", profileID)
		}
		return providerName, nil, fmt.Errorf("boot profile %q compile failed: %w", profileID, err)
	}

	if err := bootprofile.ResolveRequirements(compiled); err != nil {
		// ResolveRequirements already names the slot + source type.
		return providerName, nil, fmt.Errorf("boot profile %q: %w", profileID, err)
	}

	// Stash the spec so driveBootSession can read it at boot time.
	// nil-safe in the existing flow: when sessionID is empty (defensive),
	// driveBootSession will not find it and falls back to the bare CLI
	// branch.
	if sessionID != "" {
		s.activeSessionLaunchSpecs.Store(sessionID, compiled)
	}

	return cliRoutableProvider(compiled), compiled, nil
}

// cliRoutableProvider returns the CLI-routable form of a compiled
// LaunchSpec's provider. The two-step rule:
//
//   - When ProviderAlias was a CLI-aliased form ("pty-claude",
//     "pty-codex", "pty-opencode", legacy "pty", or "sub-<x>"),
//     return it verbatim — chat.IsCLIProvider already matches.
//   - Otherwise synthesize "pty-<spec.Provider>" so the bare-adapter
//     form authored in some catalogs still triggers IsCLIProvider.
//
// The downstream agent-runtime ProviderAdapter closure strips the
// "pty-" prefix back to the bare adapter name before looking up the
// CLIAdapter, so both forms converge at the same registered adapter.
//
// CW-20260514-0048: kept narrow + structural so a future change that
// adds another CLI prefix (e.g. "agentsession-*") drops in by
// extending chat.IsCLIProvider alone.
func cliRoutableProvider(spec *bootprofile.LaunchSpec) string {
	if spec == nil {
		return ""
	}
	alias := spec.ProviderAlias
	if alias != "" {
		// IsCLIProvider matches pty / pty-* / sub-* — we deliberately
		// don't re-implement that table here; defer to the canonical
		// helper so a future rename catches both sites.
		if chat.IsCLIProvider(alias) {
			return alias
		}
	}
	bare := spec.Provider
	if bare == "" {
		return alias
	}
	return "pty-" + bare
}

// buildBootProfileVars assembles the session-scoped variable map fed
// into bootprofile.Substitute via CompileFor. The keys mirror the names
// catalog authors use in their slot text fields. Identity-derived vars
// are pre-populated by the compiler's buildVars; we only add the
// session-scoped overlay here so authors can reference {{session_id}},
// {{role}}, {{agent_slug}}, {{workdir}} in slot bodies.
//
// Keep this list short and explicit — adding a key here is a public
// surface change (catalog YAML may start referring to it), so a future
// ticket should not silently introduce {{user_message}} or similar.
func buildBootProfileVars(sessionID string, session *store.Session, agent *store.AgentProfile) bootprofile.Vars {
	out := bootprofile.Vars{}
	if sessionID != "" {
		out["session_id"] = sessionID
	}
	if session != nil {
		if session.Provider != "" {
			out["session_provider"] = session.Provider
		}
		if session.Model != "" {
			out["session_model"] = session.Model
		}
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

// launchSpecFor returns the LaunchSpec previously stashed for
// sessionID, or nil when none is registered (the session isn't
// boot-profile-backed). Used by driveBootSession to decide whether to
// thread per-profile knobs into agent.Boot's Options.
func (s *chatServiceImpl) launchSpecFor(sessionID string) *bootprofile.LaunchSpec {
	if sessionID == "" {
		return nil
	}
	v, ok := s.activeSessionLaunchSpecs.Load(sessionID)
	if !ok {
		return nil
	}
	spec, typeOK := v.(*bootprofile.LaunchSpec)
	if !typeOK {
		return nil
	}
	return spec
}
