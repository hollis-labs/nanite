# Build the real SKILL.md package parser and explicit install/sync pipeline

**Phase:** 3 — Explicit install/sync (`TASKS/skills`)
**Status:** implemented
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

**Pre-flight.** Confirmed the stated dependency base: `internal/skillvendor` exists with `New`,
`Write`, `Path`, `ReadFiles`, `Delete`, `Address`/`ValidateAddress`, address family
`skl-vendor-<hash[:16]>` (task `03`, implemented). `internal/store/skills.go`'s `Skill` struct has
`ID/Name/Slug/Description/Category/Icon/InputSchema/SourceTier/ContentHash/Version/Enabled/
DeclaredDependencies/InstalledAt/UpdatedAt` and no `Prompt`/`ToolBindings`/`IsBuiltin`/`Settings`/
`ModeIDs` (task `02`, implemented). Both matched the task file's own sanity-check description — no
anomaly, proceeded. Read `docs/engineering/architecture/20-skills.md` in full,
`TASKS/skills/README.md`, and tasks `02`/`03`/`05`/`06`/`07` (siblings this task's own text asks to
coordinate shape with) before writing any code, per the task's own coordination hedges.

**1. `skill.Definition` extension (`internal/skill/parser.go`).** Added `Parameters
[]ParameterSpec` (new type: `Name`, `Description`, `Required bool`, `ResolverSlot string` — the
last field is the binding point task `06`'s Skill Resolver will read, naming an existing
`agent_context_resolvers` row by its `slot_name` column; not yet consumed by anything since task
`06` hasn't landed, but shaped to match that table's real column name so task `06` doesn't have to
guess or rename). Added `Scripts`, `References`, `Assets []string` — frontmatter-declared,
package-relative path lists (e.g. `scripts/run.sh`) that install-time validation (below) confirms
actually exist in the package's file map; **note**: no established Agent-Skills-spec convention
requires these as explicit frontmatter *lists* (the real spec treats `scripts/`/`references/`/
`assets/` as pure directory conventions, discovered by walking, not declared by name) — this
batch's Done-means explicitly wants "a `scripts:` entry pointing at a file that doesn't exist" to
be a distinct validation failure, which requires *something* to name path-by-path; recognizing
declared entries as an explicit frontmatter list (while `ParsePackageDir`, below, still walks and
vendors the *entire* directory tree regardless of whether every file is declared) satisfies both
the task's literal validation requirement and the real spec's "everything under scripts/ is part
of the package" behavior. Added `Dependencies []string` (frontmatter key `dependencies:`) for the
declared-dependency extraction item `3` asks for — also not an established spec convention (task's
own text: "whatever composition-declaration shape task `07` needs — coordinate... if sequencing
allows"); task `07` hadn't landed at the time of this work, so this is this task's own provisional
choice, documented here for task `07` to adopt, rename, or extend rather than silently guessing at
a shape that was never written down.

Added `PackageFiles = map[string][]byte` (structurally identical to, but not importing,
`skillvendor.FileMap` — this package doesn't need a dependency on `internal/skillvendor` just for a
map-shape alias; `internal/skillinstall` does the explicit type conversion at its one call site) and
`ParsePackageDir(dir string) (*Definition, PackageFiles, error)`: reads `SKILL.md` at the package
root via the existing `ParseMD`, falls back to the package directory's own base name for the slug
(mirroring `ParseMDFile`'s filename-fallback), then walks the whole directory tree into a
`PackageFiles` map keyed by forward-slash, dir-relative path (every file under the root, not just
declared `scripts:`/`references:`/`assets:` entries — the whole tree is what task `03`'s store
hashes and vendors). `ParsePackageDir` deliberately does not itself check that a declared
`scripts:`/`references:`/`assets:`/`dependencies:` entry is well-formed or actually present — that
is `internal/skillinstall`'s Validator step's job (a distinct install-time concern, per the task's
own item 2), not a parse-time one; a package that will later fail validation still parses
successfully here.

Did **not** add a separate `Version`/content-hash-precursor field to `Definition` itself, per the
task's own hedge ("if not already covered by task 03's own hashing... this task computes the
canonical file map task 03 hashes; it does not duplicate the hashing logic itself"): the
`PackageFiles` map `ParsePackageDir` produces *is* that canonical file map — `skillvendor.Address`/
`Write` hash it directly. The numeric `Version` that actually needs incrementing lives on
`store.Skill` (task `02`'s column) and is owned by the install pipeline's index-upsert step, not by
the parsed `Definition`.

**Also extended `internal/skill/convert.go`'s `ToStoreSkill()`** to derive `InputSchema` from
`Definition.Parameters` (a minimal JSON Schema: `type: object`, one string-typed `properties` entry
per parameter with its `description` if set, a `required` array for parameters marked `Required`)
via a new `inputSchemaFromParameters` helper, rather than leaving `InputSchema` hardcoded to `"{}"`
unconditionally. This directly closes task `02`'s own forward-reference: `store.Skill`'s doc comment
already says `InputSchema` is "schema for declared parameters, now sourced from the package's own
frontmatter via task 04's parser" — task `02` couldn't build this itself since `Parameters` didn't
exist on `Definition` yet at that point. A skill with no declared parameters still gets `"{}"`
(verified via the existing, unmodified `TestToStoreSkill`/`TestToStoreSkill_DefaultsSourceTierToUser`
tests, both still green) — this is additive, not a behavior change for the pre-parameters case.

**2. `internal/skillinstall/` package** (`doc.go`, `install.go`, `validate.go`,
`install_test.go`, plus `testdata/fixtures/{sample-skill,malformed-missing-script,
malformed-frontmatter}/`). Pattern-matched `internal/plugin/install/`'s shape per the task's own
explicit instruction, adapted to this batch's genuinely narrower scope (local-directory-drop
install only — no download/signature/archive-extraction steps exist for a skill package, per
`TASKS/skills/README.md`'s ecosystem-format scope fence):

- `Source{Path string}` — a local package directory (this batch's only supported kind, unlike
  `plugin/install`'s `Source` interface with archive vs. directory `Handle.Kind`).
- State machine: `NotInstalled → Parsing → Validating → Vendoring → Indexing → Ready`, with
  `Failed` as the universal failure sink — narrower than `plugin/install`'s
  `Downloading/Verifying/Extracting/Loading` states since those steps don't apply here (no archive,
  no runtime "load into a running host" step — that's tasks `06`/`08`/`10`/`11`'s job, not this
  one's). Every failure routes through one `fail()` helper, mirroring `plugin/install.go`'s own
  discipline exactly.
- `Validator` interface + `DefaultValidator{}` (used when `Installer.Validate` is nil, matching
  `plugin/install`'s always-required-but-swappable `Validator` shape): enforces the real
  Agent-Skills-spec's mandatory `name`/`description` frontmatter fields are non-empty, `Context` is
  exactly `"inline"`/`"fork"`, every declared `scripts:`/`references:`/`assets:` entry
  normalizes to a well-formed package-relative path and is actually present in the parsed file map,
  every declared parameter has a non-empty unique name, and every declared dependency slug is
  non-empty/unique/not-self-referential (a single-package sanity check only — real
  cycle/recursion-limit detection against the *installed* graph is explicitly task `07`'s job per
  both this task's own text and `07`'s own task file, not duplicated here). All failures aggregate
  into one `*ValidationError` so a caller sees every problem in one pass, not one-typo-per-attempt.
- `Vendorer`/`vendorDeleter`/`IndexStore` interfaces narrowly slice `*skillvendor.Store`'s and
  `*store.Store`'s real, already-existing methods (`Write`; optional `Delete`; `GetSkillBySlug`/
  `CreateSkill`/`UpdateSkill`) — both concrete types satisfy these interfaces as-is with zero
  wrapper code needed in production; the interfaces exist purely so tests can inject fakes for the
  failure/rollback-path coverage below, matching `plugin/install`'s own DI-for-testability
  rationale rather than DI-for-multiple-real-implementations (there's only ever one real
  `Vendorer`/`IndexStore` in production).
- `Install(ctx, Source) (Result, error)` is the **only** entry point — there is no separate `Sync`
  method. Re-sync (task item 5) is the *same* pipeline run again against the same source path: the
  Indexing step looks up the parsed package's own slug via `GetSkillBySlug` and decides internally
  whether to `CreateSkill` (first install) or `UpdateSkill` (existing row) — never re-deriving a new
  row ID on re-sync, and only bumping `Version`/`ContentHash` when the vendored address actually
  changed (an unchanged re-sync is a real no-op beyond refreshing
  name/description/category/declared-dependencies from the package, matching `skillvendor`'s own
  `Reused=true` idempotency contract one level up).
- Declared-dependency extraction (item 3): `extractDeclaredDependencies` copies
  `Definition.Dependencies` (already validated well-formed by this point) into the JSON array stored
  in `store.Skill.DeclaredDependencies` — extraction only, no cycle detection, no `inline`/`fork`
  behavior, exactly the scope task `07`'s own task file confirms is *its* job, not this one's.
- Failure handling (item 4): a `Validate` failure runs *before* `Vendor.Write` is ever called, so a
  malformed package leaves literally nothing on disk or in the index — verified directly by a test
  that walks the vendor store's root after a validation failure and asserts zero non-staging
  entries exist. An `Indexing`-step failure that happens *after* a fresh (non-`Reused`) vendor write
  triggers a best-effort rollback (`Vendor.(vendorDeleter).Delete(address)`) so a failed install
  never strands an unindexed vendored copy; a `Reused` write is deliberately **never** rolled back
  this way, since its content already existed before this call (e.g. shared with another already-
  installed skill, or a benign re-sync-of-unchanged-content race) and an unrelated indexing failure
  must not delete content something else may depend on — both branches have dedicated tests using a
  fake `Vendorer`/`IndexStore` pair.
- No sweep/directory-scan path exists anywhere in this package — `Install` requires a non-empty
  `Source.Path` per call (verified by `TestInstall_RequiresExplicitSourcePath`, which also asserts
  zero filesystem/index side effects from the rejected empty-`Source` call), and grepping this
  package finds no exported function that accepts "a root directory of many packages" or is wired
  to any boot-time hook.

**3. GLOSSARY.md.** Re-checked the file before writing anything (per this repo's standing
discipline) and decided **not** to add a new entry, using the task's own explicit "use judgment...
not documenting every noun" hedge (item 6): the existing **Skill**/**Skill catalog**/**Skill vendor
store** entries (task `02`/`03`, already landed) already describe "installed explicitly" as part of
the **Skill** definition itself, and this task introduces no new *product-facing* vocabulary beyond
that — `Source`/`Validator`/`Vendorer`/`IndexStore`/`ParameterSpec`/`PackageFiles` are internal Go
type names mirroring `internal/plugin/install`'s own naming convention (itself not glossary-listed),
not new terms a human operator or another task would need disambiguated. `Parameters`/`Scripts`/
`References`/`Assets`/`Dependencies` are frontmatter field names, already covered conceptually by
the architecture doc's own "The model" section language. No collision risk either way (grepped the
current 96-line file for `Parameter`, `Vendorer`, `Validator`, `PackageFiles`, `Dependencies` before
deciding — zero hits).

**Coordination notes for tasks `06`/`07`, since both were `not-started` at the time this task
landed and their own task files ask this task to leave a decidable shape behind, not to block on
their sequencing:**
- Task `06`'s Resolver: `ParameterSpec.ResolverSlot` is the binding field, named to match
  `store.AgentContextResolver.SlotName` (`internal/store/agent_context_resolvers.go`) exactly, so
  task `06` can look the row up by that string with no translation layer.
- Task `07`'s cycle detection: `store.Skill.DeclaredDependencies` is populated as a plain JSON array
  of slug strings (`["other-skill"]`, or `"[]"` for none) at install time by this task, exactly the
  shape task `02`'s own column doc comment already specified — task `07`'s Installer-pipeline
  extension (its own item 3) can read this column directly for every already-installed skill to
  build its graph; this task does not attempt cycle detection itself, per both task files' explicit
  scope split.

**Test fixtures** (`internal/skillinstall/testdata/fixtures/`): `sample-skill/` is a real,
complete `SKILL.md` + `scripts/run.sh` + `references/notes.md` + `assets/logo.txt` package with two
declared parameters (one required, one not) and one declared dependency slug (`other-skill` — not
itself installed anywhere in this task's tests, since this task's own scope is extraction, not
resolution). `malformed-missing-script/` declares a `scripts:` entry that doesn't exist on disk.
`malformed-frontmatter/` omits the mandatory `name` field. All three exercise the Done-means
end-to-end/malformed-package requirements directly (see test list below).

**Validation.** `go build ./cmd/nanite/`, `go vet ./...` (only the two pre-existing
`container.go` `stopReaper`/`stopRuntimeReaper` findings tasks `01`/`02`/`03` already confirmed
predate this batch via `git blame`), and the full `go test ./...` suite all pass — including the new
`internal/skillinstall` package (8 tests) and the extended `internal/skill` package (added
`TestParsePackageDir_FullPackage`, `TestParsePackageDir_SlugFallsBackToDirName`,
`TestParsePackageDir_MissingSkillFile`, `TestToStoreSkill_InputSchemaFromParameters`, all passing
alongside the existing, unmodified skill-package tests). Key `internal/skillinstall` tests, mapped
to Done-means:
`TestInstall_EndToEnd` (real fixture installs end-to-end: parsed, validated, vendored — confirmed
via `vendor.ReadFiles` — and indexed with the right `ContentHash`/`DeclaredDependencies`/non-default
`InputSchema`); `TestInstall_MalformedPackage_MissingScript` /
`TestInstall_MalformedFrontmatter_FailsCleanly` (malformed packages fail with a specific
`*ValidationError` or parse error, zero vendored entries, zero index rows);
`TestInstall_Resync_IdenticalContent_IsIdempotent` (`Reused=true`, `Version` unchanged);
`TestInstall_Resync_ChangedContent_NewAddressAndVersionBump` (new address, `Version` bumped, same
row `ID` updated in place, **old vendored address independently re-read and confirmed byte-for-byte
unmodified** — the literal immutability check the Done-means asks for);
`TestInstall_RequiresExplicitSourcePath` (no-sweep guarantee);
`TestInstall_IndexFailureAfterFreshVendorWrite_RollsBackVendoredAddress` /
`TestInstall_IndexFailureAfterReusedVendorWrite_DoesNotDelete` (the rollback/no-rollback failure
paths, using fakes since the real `*skillvendor.Store`/`*store.Store` don't offer an easy way to
force an index-write failure after a real vendor write in an in-process test).

Did not perform a separate live `nanite serve` dogfeed for this task: task `05` (not yet started at
the time of this work) is the REST/CLI trigger surface that actually exercises this pipeline through
a running server/CLI process, and this task's own Done-means criteria are all satisfiable — and were
satisfied — through direct, real (non-mocked, except for the two narrow rollback-path tests)
package-level tests against a real `*skillvendor.Store` rooted at a temp directory and a real
`*store.Store` built via `store.New` against a temp SQLite file with migrations applied. No
`.nanite/`-relative paths, no repo-root-relative writes, and no schema migration was touched by this
task (task `02` already landed `136`/`137`), so `EXECUTION-PROCESS.md`'s scratch-path/backup-copy
discipline for schema or live-server verification doesn't apply here.

`git status --short` after all changes: only `internal/skill/{convert,parser}.go` and their
`_test.go` files modified, plus the new `internal/skillinstall/` directory added — no other files
touched, no stray writes.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
