// Package bootprofile is Nanite's compiler and loader for shared boot
// profiles compatible with Tether's catalog shape.
//
// A boot profile (YAML under <catalog-root>/boot-profiles/<id>.yaml)
// describes how to populate a named-slot boot prompt for an agent — its
// identity metadata, the slot sources that feed each section of the
// canonical 7-section prompt, an optional template override, and the
// launch ID it should drive when started from a UI dropdown or CLI.
//
// A launch profile (YAML under <catalog-root>/launches/<id>.yaml)
// describes the runtime contract for spinning up a session against the
// boot profile: the provider/CLI adapter to use, the working directory,
// optional env overrides, optional command/args override, and prompt
// composition policy.
//
// The compiler produces a LaunchSpec — a flat, JSON-serializable struct
// the Nanite chat runtime and provider-dropdown UI consume directly. It
// does not start a session, plant files, run commands, or fetch URLs.
// Downstream tickets (CW-20260514-0047 dropdown surfacing,
// CW-20260514-0048 runtime hookup) consume LaunchSpec as-is; if a slot
// source requires deferred resolution (cmd, http, role_summary,
// skill_index), the spec surfaces it as a structured Requirement rather
// than executing it here.
//
// Design constraints for this ticket (CW-20260514-0046):
//
//   - Stays inside Nanite. The package is laid out so it can be lifted
//     into a shared boot-profile module later; public types avoid
//     Nanite-specific surface where possible.
//   - Pure compiler: no os/exec, no net/http, no agent.Boot calls. Only
//     filesystem reads under the catalog root for `static` / `text`
//     slot resolution. cmd / http / role_summary / skill_index slots
//     pass through as Requirement entries on the LaunchSpec.
//   - Provider alias normalization delegates to chat.NormalizeCLIProvider
//     so the alias table has exactly one source of truth.
//   - Variable substitution is intentionally narrow: `{{var}}` literals
//     resolved from a caller-supplied vars map plus the profile's own
//     Identity fields. Unknown variables are an error (pinned in tests).
//     The Go text/template engine Tether uses for full prompt templating
//     is out of scope here.
//
// Public types:
//
//	Profile       — boot profile YAML as loaded from disk
//	Launch        — launch profile YAML as loaded from disk
//	Identity      — agent identity fields embedded in Profile
//	SlotSource    — one slot's source definition (type + parameters)
//	Catalog       — loaded boot profiles + launches keyed by ID
//	LaunchSpec    — compiled runtime contract returned by Compile
//	Requirement   — deferred slot a downstream stage must resolve
//	Vars          — variable substitution map type alias
//
// Public functions:
//
//	LoadCatalog(root string) (*Catalog, error)
//	LoadProfile(path string) (Profile, error)
//	LoadLaunch(path string)  (Launch, error)
//	Compile(profile, launch, vars) (*LaunchSpec, error)
//
// See internal/bootprofile/compiler.go for the LaunchSpec contract and
// the rationale for each field; downstream callers should depend on the
// LaunchSpec shape rather than re-parsing YAML.
package bootprofile
