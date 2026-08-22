// Package skillinstall implements the explicit, single-target install/sync
// pipeline for authored skill packages — docs/engineering/architecture/
// 20-skills.md's "Explicit, single-target install/sync" mechanism: "a new
// mechanism, scoped to exactly one named skill per invocation (a CLI
// command, a self-tool call, or an admin-UI import), never a sweep. This
// is the only way a skill's index row is created or updated."
//
// # Shape, pattern-matched against internal/plugin/install
//
// This package's Installer mirrors internal/plugin/install's Installer
// state machine (NotInstalled → Downloading → Verifying → Extracting →
// Validating → Loading → Ready, injected Verifier/Extractor/Validator/
// Loader/Staging interfaces, a mutex-guarded state, every failure routed
// through one fail() helper) — same shape, a different package, because a
// skill package's install steps are genuinely different: this batch's
// scope is a local directory drop only (TASKS/skills/README.md's
// ecosystem-format-adaptation scope fence), so there is no download,
// signature-verification, or archive-extraction step at all. The states
// this package actually needs are Parsing → Validating → Vendoring →
// Indexing → Ready, reflecting the four real steps docs/engineering/
// architecture/20-skills.md's "The model" section describes: read the
// package, confirm it matches the real Agent-Skills-spec shape, vendor a
// copy into the content-addressed store (internal/skillvendor,
// TASKS/skills/03), and upsert the DB index row (internal/store's Skill,
// TASKS/skills/02).
//
// # Single-target only, by design
//
// Install takes exactly one Source (a local package directory path) per
// call. There is no directory-sweep entry point anywhere in this package —
// nothing here scans a directory tree of *multiple* packages, and nothing
// here runs automatically at process boot. TASKS/skills/05 builds the
// REST/CLI trigger that calls Install for a named target; this package
// only builds the pipeline itself.
//
// # Re-sync is the same pipeline, not a separate code path
//
// "Re-sync" (installing again against the same source path after its
// content changed) is not a distinct method — every Install call runs the
// full Parse → Validate → Vendor → Index pipeline from the top, and the
// Indexing step decides internally whether this is a first install
// (create a new index row) or a re-sync (update the existing row keyed by
// the package's own slug). A re-sync that produces genuinely new content
// always gets a new vendored address (internal/skillvendor's content
// addressing) and a bumped store.Skill.Version; it never mutates the
// previously-vendored copy in place, and a re-sync of byte-identical
// content is a safe, version-preserving no-op (internal/skillvendor's own
// Reused=true idempotency fast-path).
//
// # Failure handling
//
// Every step failure routes through Installer.fail, mirroring
// internal/plugin/install's discipline. A Validate failure occurs before
// anything is vendored or indexed, so a malformed package never leaves a
// partial write of any kind. An Indexing-step failure that occurs *after*
// a fresh (non-reused) vendor write attempts a best-effort rollback
// (deleting the just-vendored address) so a failed install doesn't strand
// an unindexed vendored copy — see install.go's vendorDeleter for the
// exact contract and why a reused write is deliberately never rolled back.
package skillinstall
