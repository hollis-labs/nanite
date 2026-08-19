package skill

// DiscoverOptions configures skill discovery.
//
// TASKS/phase-1/08 ("Kill the file-reingest-on-boot pattern, in full"): files
// are not skill storage going forward, except the compiled-in builtin/seed
// skills (loaded separately via internal/skill/builtin, not through this
// function). The project (.nanite/skills/), user (~/.nanite/skills/), Claude
// Code ecosystem (.claude/skills/), and plugin (plugins/*/skills/*.md)
// directory-scan tiers that used to live here were removed in full — a file
// dropped in any of those locations is no longer discovered or
// auto-ingested into the skills table at boot, ever. Same treatment as
// agents, per the operator's "no debt carries forward" directive; unlike
// agents, skills have no CLI-flag or adapter-registry tier to preserve, so
// nothing is left in DiscoverOptions or Discover() beyond the shape callers
// still use. See the task's Work Log Round 2 entry for the
// .claude/skills/-specific investigation (no dependent found; cut matches
// agents).
type DiscoverOptions struct {
	// WorkingDir and PluginsDir/HomeDir are intentionally not present here
	// anymore — every tier that used to read them was removed. Kept as an
	// empty struct (rather than deleting DiscoverOptions/Discover outright)
	// so the container.go call site and skill.Definition's Source-driven
	// downstream logic (e.g. skillbroker.sourceBias for historical
	// source='project'/'user'/'claude'/'plugin' rows already in the DB)
	// don't need restructuring, and so a future non-file discovery source
	// has an obvious place to attach.
}

// Discover always returns an empty result: every file-based discovery tier
// was cut by TASKS/phase-1/08. Builtin skills are loaded separately by the
// caller (internal/skill/builtin.BuiltinSkills), not through this function.
func Discover(_ DiscoverOptions) ([]*Definition, error) {
	return nil, nil
}
