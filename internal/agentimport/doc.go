// Package agentimport implements the explicit, one-way, single-target
// import pipeline for agent definitions — the mechanism behind
// `nanite agent install <path>` / `nanite agent sync <slug> <path>` and
// their REST twins.
//
// # Registration is import, never discovery
//
// Nanite deliberately removed file-based agent discovery: the project
// (.nanite/agents/), user (~/.nanite/agents/) and plugin (plugins/*/agents/)
// directory-scan tiers are gone from internal/agent/discovery.go, and every
// registered CLIAgentAdapter's discovery entry point is a no-op. That cut
// was not "files are the wrong medium" — it was a response to DRIFT between
// a file on disk and the agent_profiles row derived from it. A pass that
// re-reads a directory on every boot has to keep answering "which one wins
// this time," and the answer kept changing.
//
// An import reads once and never re-reads, so it has no drift surface. The
// rule this package implements is one-way, one-time, with provenance:
//
//   - One-way. Nothing here writes back to the source file. Not the minted
//     identity (internal/agent/parser.go's Definition.ID comment already
//     states that rule; this package extends it to the whole path), not a
//     slug, not a timestamp. The file is read and forgotten.
//   - One-time. Nothing here runs at boot, on a timer, or on a filesystem
//     event. Every write is the direct result of one operator invocation
//     naming one path. Re-reading an edited source is `sync` — an explicit
//     second invocation, not a watch.
//   - With provenance. Every imported row records Source="import"
//     (SourceProvenance below), the originating path in SourceRef, and the
//     import timestamp in ImportedAt.
//
// A directory argument is sugar over N single imports that reports what it
// found and what it skipped. It is not a tier: it registers no watch, and
// internal/agent/discovery.go gains nothing from this package.
//
// # Ownership: an imported agent is external, and external is read-only
//
// Source="import" classifies as internal/agent.ManageClassExternal
// (source_class.go's Classify default branch — deliberately, see
// ImportProvenanceIsExternal in the tests, which pins the mapping so a
// future edit to Classify's `managed` string list can't silently promote
// imported rows to editable). External is not Editable() and IS
// CopyToManagedAllowed(), so editing an imported agent goes through
// AgentConfigService.CopyToManaged and produces an operator-owned copy,
// leaving the imported row an honest record of what was imported.
//
// Imported profiles are also seeded 'untrusted' (H1's trust vocabulary),
// matching both AutoIngestAgents' treatment of user/plugin-dropped
// definitions and AgentConfigService.Create's treatment of a fresh
// operator profile. Foreign content does not arrive trusted.
//
// # The ownership boundary is enforced on the way in, not just on the way out
//
// Import refuses to write over a row it does not own. A slug already held
// by an internal harness primitive, an operator-managed profile, or a
// plugin-provided profile is an error naming the class — never a silent
// overwrite and never a silent rename. Only a row that is already
// external (i.e. a previous import under the same slug) is updated, and
// that update is exactly what `sync` means.
//
// That refusal is load-bearing rather than merely tidy. internal/service/
// ingest.go's boot pass freezes an existing row against re-seeding, but
// carves out one exception for a genuine provenance transition
// (existing.Source != the incoming def's Source), which exists for the
// historical builtin -> internal reclassification. Probed on 2026-09-10:
// without the refusal here, importing under a slug that a compiled-in seed
// also uses (`reviewer`, `planner`, `worker`, `researcher`, `backend` are
// all real seed slugs) let the NEXT boot take that carve-out and silently
// overwrite the imported row's content and flip its source back to
// "internal". upsertAgentDef carries the matching guard for the reverse
// direction — see its `neverTransitionAwayFromExternal` comment.
//
// # Shape, pattern-matched against internal/skillinstall
//
// Skills settled this identical question first and shipped the answer:
// "authored packages installed explicitly, not a boot-time file-reingest
// target." This package mirrors internal/skillinstall's Installer — an
// injected narrow store interface, a mutex-guarded state machine, every
// failure routed through one fail() helper, and no directory-sweep entry
// point anywhere in the package.
//
// The steps differ because an agent has no content-addressed vendor store:
// the agent_profiles row IS the artifact. So the pipeline is
// Parsing -> Validating -> Writing -> Ready, with no vendoring step and
// therefore no vendor rollback. A failure before Writing leaves nothing
// behind at all.
//
// Like skillinstall, "sync" is not a separate method. Every Import call
// runs the full pipeline from the top, and the Writing step decides
// create-vs-update from what is already in the database under the parsed
// slug. The CLI and REST `sync` entry points add a guard — the slug must
// already exist and the package must declare that same slug — so a caller
// cannot relabel one definition's content onto a different agent's slug.
package agentimport
