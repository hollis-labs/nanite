# Build the content-addressed vendored skill store

**Phase:** 2 — Index + vendored store (`TASKS/skills`)
**Status:** not-started
**Depends on:** none (brand-new package, zero file overlap with `01`/`02` — safe to run in
Wave 1 alongside `01`)
**Touches:** new package `internal/skillstore/` (or equivalent name — check `GLOSSARY.md` and
existing package names for collisions before locking it in; `internal/skillvendor/` is a
reasonable alternative if `skillstore` reads as too close to `internal/store`), new config field
for the vendored-store filesystem root (mirroring `config.AppConfig.Artifacts.StorageDir`'s
pattern), `docs/engineering/GLOSSARY.md` (new entry for the vendored-store term you land on).

## Context

`docs/engineering/architecture/20-skills.md`'s "The model" section: *"Install/sync... reads that
package, computes a content hash, and vendors a copy into a Nanite-owned, content-addressed
store — keyed by hash, immutable once written... Materialization always reads the vendored copy
live, every time a skill is used — never a value baked into a DB column at install time."* This
task builds that store as a standalone, reusable primitive. It does **not** implement the
install/sync pipeline that calls it (task `04`) or anything about parsing/validating a skill
package's contents (also task `04`) — this task's job is purely "given a directory tree of
bytes, compute its address, write it immutably, read it back live, detect corruption."

**The real, already-hardened precedent to adapt, confirmed this planning session (not assumed):**

- `internal/contextbroker/stash.go:100-109`'s `DeterministicArtifactID(sessionID, slotName,
  content string) string` — sha256 of three concatenated, null-separated inputs, truncated to
  16 hex chars, format `art-stash-<hash>`. Package doc: *"Stash writes are content-addressed:
  same slot + same content → same artifact_id."*
- `internal/service/slot_stash.go`'s `artifactStasher.StashSlot` (lines 133-320) — the production
  implementation of the full immutable-once-written contract this task needs to match:
  FS-write-before-DB-insert ordering (never register an address the filesystem doesn't actually
  have yet), an idempotency fast-path (stat the existing path, return `Reused: true` if
  content-identical, skip the write), a disk-loss recovery branch (DB row exists but the file
  was deleted externally — re-write content-identically at the same address rather than erroring
  or silently diverging), and an insert-race recovery branch (two concurrent writers computing
  the same address for the same content — the loser re-reads and treats it as `Reused: true`
  rather than erroring).
- **The real adaptation this task has to make, not a literal copy**: `stash.go`'s precedent
  addresses a single content *string*. A skill package is a directory tree (`SKILL.md` +
  optional `scripts/`, `references/`, `assets/` subdirectories, each with real files). The hash
  input needs to be a canonical, deterministic serialization of the whole tree (sorted relative
  paths + each file's bytes, not just a top-level content string) so that reordering-invariant,
  byte-identical trees always produce the same address, and the write/read path needs to
  materialize (or reference) a directory, not a single file.

## What to do

1. Land on a package name (check `GLOSSARY.md` and `internal/` for collisions first — see
   "Touches" above for suggested alternatives) and a filesystem-root config convention (a new
   `config.AppConfig` field, e.g. `Skills.VendorStorageDir`, mirroring
   `Artifacts.StorageDir`'s existing pattern — check `internal/config`'s loader for exactly how
   `Artifacts.StorageDir` is wired so the new field follows the same load/default/override
   precedent).
2. Implement a deterministic tree-hash function: given an in-memory representation of a package
   (e.g. `map[string][]byte` keyed by tree-relative path, mirroring the boot-dir `plant.Spec.Files`
   shape `internal/runtime/agent/bootdir_plant.go` already uses for a conceptually similar
   "here's a set of relative-path → bytes, write them all" operation — reusing that same input
   shape isn't required but is worth considering for consistency with task `10`'s consumption
   side), compute a single content-address by sorting relative paths and hashing each path+bytes
   pair into one running sha256 (following `stash.go`'s null-byte-separated concatenation
   pattern), producing an address in the same family of format (e.g. `skl-vendor-<hash[:16]>` —
   pick a prefix that's clearly distinct from `art-stash-` so the two content-addressed systems
   are never confused when debugging).
3. Implement the write path: given a package's file map + its computed address, write every file
   to `<VendorStorageDir>/<address>/<relative-path>` using the same
   FS-write-before-DB-insert-safe ordering as `slot_stash.go` (this task's own store doesn't
   necessarily need a DB row of its own — task `02`'s `Skill.VendoredPath`/`ContentHash` column
   is the index pointer — but the write path must still be safe for a caller who writes the
   files here, then separately upserts the index row in `02`'s table, potentially crashing
   in between; design for that ordering).
4. Implement the idempotency fast-path (address already exists, content matches — no-op,
   `Reused: true`), the disk-loss recovery branch (index row references an address whose
   directory is missing on disk — surface this as a real error the caller/task `04` must handle,
   since unlike `slot_stash.go`'s artifact case there's no "just re-derive the content" fallback
   here, the original source-of-truth is an operator's local package path that may no longer be
   reachable — don't silently paper over data loss), and the insert-race case (two concurrent
   installs of the same content — both should converge on `Reused: true` for the loser, matching
   `slot_stash.go`'s pattern).
5. Implement the read path: given an address, return the file map (or a live filesystem path
   into the vendored directory, whichever shape task `04`/`06`/`10` actually need — coordinate
   with those tasks' own "what to do" sections if sequencing allows, otherwise pick the simpler
   "return a real filesystem path, let callers `os.ReadFile`/`filepath.Walk` it directly" shape,
   since task `20-skills.md`'s materialization pipeline explicitly wants live reads of the
   vendored copy, not a cached-and-forgotten value).
6. Immutability enforcement: once an address's directory is written, nothing should ever mutate
   it in place — a "delete this address" operation (for uninstall, task `12`) is fine, but
   there's no "update this address's content" operation, by design (a content change always
   produces a new address). Document this invariant clearly in the package's doc comment.
7. Add a `GLOSSARY.md` entry for whatever term you land on for this store (e.g. "Skill vendor
   store" or "vendored skill package") — cross-reference `13-memory-and-knowledge-tools.md`'s
   §4a and the "Skill catalog"/"Skill attachment" entries task `02` adds, so all four terms read
   as one coherent vocabulary set, not four independently-invented names.

## Done means

- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- A test suite (analogous in rigor to whatever covers `slot_stash.go`'s
  idempotency/race/recovery behavior — check that file's own tests for the bar to match) proves:
  same tree content → same address, byte-for-byte; writing the same address twice is a safe
  no-op; a concurrent double-write of identical content converges without error; a disk-loss
  scenario (row/reference present, directory missing) surfaces a real, typed error rather than
  silently succeeding or panicking.
- The store never mutates a previously-written address's contents in place — a test asserts an
  attempt to write different content at an address that already exists either produces a new,
  different address (correct behavior) or a real, explicit error (also acceptable) — never a
  silent overwrite.
- `GLOSSARY.md` has an entry for the vendored-store term, cross-referenced against the related
  Skill/Skill-catalog terms task `02` introduces.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
