package agent

// composeEnv assembles the env vars handed to the spawned process.
//
// Layering (last write wins):
//  1. Inherit minimal base (HOME, PATH, USER, TZ, LANG; LC_ALL when set).
//  2. Profile.Env  — agent-config-declared env (e.g. ANTHROPIC_API_KEY,
//     model overrides, NANITE_* feature toggles).
//  3. Options.Env  — caller-supplied per-spawn env.
//  4. Layout.AmendEnv — provider-specific amendments
//     (e.g. OPENCODE_CONFIG_DIR=<bootDir> for opencode).
//
// Phase 3 fills in the concrete composition; the existing
// internal/agent profile env policy is the migration source.
func composeEnv(profile AgentProfile, opts Options) map[string]string {
	out := map[string]string{}
	for k, v := range profile.Env {
		out[k] = v
	}
	for k, v := range opts.Env {
		out[k] = v
	}
	return out
}
