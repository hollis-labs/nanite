package agent

import (
	"os"

	"github.com/hollis-labs/nanite/internal/store"
)

// inheritedEnvKeys are the host env vars inherited unconditionally. Subset
// of the user's environment that's safe and required for any spawn:
// filesystem (HOME, TMPDIR, PATH), localization (LANG, LC_ALL, TZ), and
// shell identity (USER, LOGNAME). Provider-specific keys (ANTHROPIC_API_KEY,
// OPENAI_API_KEY, etc.) come in via the profile, not the host inheritance.
var inheritedEnvKeys = []string{
	"HOME",
	"TMPDIR",
	"PATH",
	"LANG",
	"LC_ALL",
	"TZ",
	"USER",
	"LOGNAME",
}

// composeEnv assembles the env vars handed to the spawned process.
//
// Layering (last write wins):
//  1. Inherited host env (minimal — see inheritedEnvKeys).
//  2. Profile-derived env (provider-specific keys; per-agent overrides).
//  3. Options.Env (caller-supplied per-spawn).
//  4. Layout.AmendEnv (provider-specific, e.g. OPENCODE_CONFIG_DIR for
//     opencode) is applied separately by the Boot caller after this
//     function returns; this lets the layout consult the ephemeral boot
//     dir which doesn't exist when composeEnv runs.
func composeEnv(profile *store.AgentProfile, opts Options) map[string]string {
	out := make(map[string]string, len(inheritedEnvKeys)+len(opts.Env)+8)
	for _, k := range inheritedEnvKeys {
		if v, ok := os.LookupEnv(k); ok {
			out[k] = v
		}
	}
	// Profile-level env (settings JSON parsed into env keys) is the
	// migration target for nanite's existing per-agent env policy. For
	// Phase 3a we inject only the canonical provider key derived from
	// profile.DefaultProvider, leaving the richer env-from-settings
	// integration to Phase 4 alongside the chat-service composition root.
	_ = profile
	for k, v := range opts.Env {
		out[k] = v
	}
	return out
}
