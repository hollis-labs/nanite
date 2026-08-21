# Build the real SKILL.md package parser and explicit install/sync pipeline

**Phase:** 3 — Explicit install/sync (`TASKS/skills`)
**Status:** not-started
**Depends on:** `02`, `03`
**Touches:** `internal/skill/parser.go` (extend `Definition` to recognize `scripts:`/
`references:`/`assets:` directory conventions and a new `parameters:` frontmatter field), new
package `internal/skillinstall/` (the install/sync state machine), `internal/store/skills.go`
(the row-upsert primitive install/sync calls — reuse whatever survived task `01`'s
`CreateSkill`/`UpdateSkill` cut, or add a narrow new one if those were removed), `docs/engineering/GLOSSARY.md`.

## Context

`docs/engineering/architecture/20-skills.md`'s "The central tension, and how it resolves"
section: *"Explicit, single-target install/sync — a new mechanism, scoped to exactly one named
skill per invocation (a CLI command, a self-tool call, or an admin-UI import), never a sweep.
This is the only way a skill's index row is created or updated."* And "The model" section:
*"Filesystem package — a standard `SKILL.md` + `scripts/` + `references/` + `assets/`
directory, exactly the Agent-Skills-spec shape... Install/sync... reads that package, computes a
content hash, and vendors a copy... A DB row is purely an index."*

**Confirmed this planning session: no `scripts/`/`references/`/`assets/` support exists
anywhere** — `internal/skill/parser.go`'s `Definition` struct has fields for `Name, Slug,
Description, Tags, ArgumentHint, AllowedTools, Model, Effort, Context, BrokerHints` (cut in task
`01`), `Modes, Prompt, Source, SourceRef` — nothing representing a directory of scripts/
references/assets, and nothing representing declared parameters (`ArgumentHint` is a static
display string only, confirmed never bound to anything at invocation).

**The real, already-hardened precedent to pattern-match, confirmed this planning session:**
`internal/plugin/install/` — `Installer` (`install.go:116-126`) holds injected `Verifier`,
`Extractor`, `Validator`, `Loader`, `Staging` interfaces plus an `EventFunc` callback and a
mutex-guarded `state State`. `Install(ctx, src Source)` (`install.go:153`) drives
`NotInstalled → Downloading → Verifying → Extracting → Validating → [atomic Staging.Commit] →
Loading → Ready`, with every failure path routing through a single `fail()` helper. `Source`
(`install.go:42-51`, `Kind` "archive"/"directory", plus `Handle` at 55-73 carrying
`ExpectedSHA256`, `Signature`, `SignerKeyID`) is the two-type shape this task's own `Source`/
package-handle types should mirror — **same shape, new package** (`internal/skillinstall/`, not
shared code with `internal/plugin/install/`, since a skill package's verification/staging steps
are genuinely different: no signature/download-from-URL concerns for a purely local
directory-drop install, at least for this batch's scope — see `TASKS/skills/README.md`'s scope
fences on ecosystem-format adaptation).

**This task is single-target only, by design** — it parses and installs exactly one named
package path per invocation. Task `05` builds the REST/CLI trigger that calls this pipeline;
this task builds the pipeline itself and is not responsible for how it gets invoked.

## What to do

1. Extend `skill.Definition` (`internal/skill/parser.go`) with:
   - A `Parameters []ParameterSpec` field (new type — `Name`, `Description`, `Required bool`,
     and a way to bind to an existing `agent_context_resolvers` slot by name for
     dynamically-resolved values, consumed by task `06`'s Resolver — coordinate the exact shape
     with task `06`'s own "what to do" section if sequencing allows one worker to see both, since
     this is the field task `06` binds against).
   - Recognition of the package's `scripts/`, `references/`, `assets/` subdirectories relative
     to the package root (not just the single `SKILL.md` file `ParseMDFile` currently reads) —
     the parser needs a package-root-aware entry point (e.g. `ParsePackageDir(path string)
     (*Definition, PackageFiles, error)` where `PackageFiles` is a `map[string][]byte]` of every
     file in the package relative to its root, ready to hand to task `03`'s store).
   - A `Version` or content-hash-precursor concept if not already covered by task `03`'s own
     hashing (this task computes the canonical file map task `03` hashes; it does not duplicate
     the hashing logic itself).
2. Build `internal/skillinstall/`'s `Installer` state machine, pattern-matched against
   `internal/plugin/install/`'s shape (see Context): a `Source` (local directory path — this
   batch's scope, see the ecosystem-format scope fence in `TASKS/skills/README.md`), a
   `Validator` step (confirm the package matches the real Agent-Skills-spec shape — a `SKILL.md`
   with valid frontmatter, referenced `scripts:`/`references:`/`assets:` entries that actually
   exist on disk, declared `parameters:` that are well-formed), a vendoring step (call task `03`'s
   store to write the package's file map and get back its content address), and an index-upsert
   step (call task `02`'s `Skill` row upsert with the new address/hash/version/declared-dependencies).
3. Declared-dependency extraction: if the package's frontmatter references other skills (via
   whatever composition-declaration shape task `07` needs — coordinate with task `07`'s own
   scope, since task `07` is the one that gives `inline`/`fork` real semantics, but the raw
   *list* of declared dependency slugs needs to be extracted and stored at install time so task
   `07`'s cycle/recursion-limit detection has something to walk), extract and pass them through
   to the index-upsert step as task `02`'s `DeclaredDependencies` column.
4. Failure handling: every step failure should leave no partial state — no vendored files
   written without a corresponding index row, no index row referencing an address that was never
   actually vendored. Match `internal/plugin/install/`'s `fail()`-routes-everywhere discipline.
5. Re-sync (same slug, new content at the same source path) should re-run the whole pipeline
   from `Validator` onward, producing a new content address (different hash → different vendored
   location per task `03`'s immutability guarantee) and updating the existing index row's
   pointer/hash/version — never mutating the old vendored copy in place.
6. Add a `GLOSSARY.md` entry for whatever this pipeline/its trigger gets called if you introduce
   new user-facing vocabulary (e.g. "install/sync" itself may already be self-explanatory enough
   not to need an entry — use judgment, following this file's own stated purpose of preventing
   *collisions*, not documenting every noun).

## Done means

- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- A real `SKILL.md` + `scripts/` + `references/` + `assets/` test fixture (construct one) installs
  successfully end-to-end: parsed, validated, vendored (task `03`'s store has the address), and
  indexed (task `02`'s `skills` row exists with the right hash/version/declared-dependencies).
- A malformed package (bad frontmatter, a `scripts:` entry pointing at a file that doesn't
  exist) fails validation with a clear, specific error — no partial vendoring or indexing occurs.
- Re-syncing the same slug with changed content produces a new vendored address and updates the
  existing index row — the old vendored copy is confirmed still present and unmodified
  (immutability from task `03` holds).
- No sweep/directory-scan path exists anywhere in this pipeline — a test confirms installing
  requires an explicit, single target path per call; nothing runs automatically at boot.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
