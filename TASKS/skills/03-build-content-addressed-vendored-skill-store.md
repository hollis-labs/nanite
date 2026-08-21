# Build the content-addressed vendored skill store

**Phase:** 2 — Index + vendored store (`TASKS/skills`)
**Status:** implemented
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

**Package name.** Chose `internal/skillvendor` over the README's provisional `internal/skillstore` — the task file's own text flagged `skillstore` as reading too close to `internal/store` and offered `skillvendor` as the alternative. Confirmed no existing `internal/` package or `GLOSSARY.md` entry collides with either name before locking it in.

**What was built** (`internal/skillvendor/`: `doc.go`, `errors.go`, `address.go`, `store.go`, `store_test.go`):

- `FileMap` (`map[string][]byte`, tree-relative path -> bytes) — same shape `internal/runtime/agent/bootdir_plant.go`'s `plant.Spec.Files` already uses, per the task's own suggestion, for consistency with task `10`'s eventual boot-dir-planting consumption side.
- `Address(files FileMap) (string, error)` — deterministic tree hash: normalizes/validates every relative path (rejects empty, absolute, `..`-escaping, and post-normalization key collisions), sorts the cleaned keys, and hashes each `path + NUL + bytes + NUL` into one running sha256, truncated to 16 hex chars, prefixed `skl-vendor-` (deliberately distinct from `internal/contextbroker`'s `art-stash-` family per the task's explicit instruction, so the two content-addressed systems are never confused when debugging).
- `Store.Write(ctx, files)` — computes the address, then (idempotency fast-path) checks whether that address's directory already exists; if so, re-hashes the on-disk tree and compares it against the address itself — matching content returns `Reused: true` with no filesystem mutation, a mismatch (only possible from out-of-band corruption/tampering, since the address IS a content hash) returns `ErrCorrupted`. If the address doesn't exist yet, `Write` stages every file into a fresh temp sibling directory (each file via `internal/fsutil.AtomicWriteFile`, matching `slot_stash.go`'s crash-safe-write precedent) and publishes the whole package in one `os.Rename` onto the final address directory — directory-level atomic rename rather than `slot_stash.go`'s single-file approach, since a skill package is a tree, not one string. A concurrent writer whose rename loses the race (target already exists) discards its staging dir and falls into the same existence/verify path, converging on `Reused: true` — the tree-level equivalent of `slot_stash.go`'s insert-race recovery branch.
- `Store.Path(address)` — resolves and returns the live filesystem directory for an address, after independently re-verifying its on-disk content still hashes to the address (the "detect corruption" contract, and the concrete form of `20-skills.md`'s "materialization always reads the vendored copy live" requirement). Returns the typed `ErrAddressMissing` if the directory doesn't exist at all (the disk-loss case the task explicitly calls out as having no re-derivation fallback, unlike `slot_stash.go`'s artifact case) and `ErrCorrupted` on a verified content mismatch.
- `Store.ReadFiles(address)` — convenience wrapper returning the full `FileMap` for callers that want bytes in-memory (e.g. a future preview/diff surface) rather than a raw fs path; carries the same error contract as `Path`.
- `Store.Delete(address)` — the one supported mutation against a previously-written address: full removal, never partial/in-place content replacement. Included per item 6's "a delete operation is fine" note, for task `12`'s eventual uninstall use, even though this task's own scope is store-primitive-only.
- `ValidateAddress(address)` — rejects any string not shaped like this package's own output (wrong prefix, wrong length, non-hex, uppercase hex) before `Path`/`Delete` touch the filesystem, so a caller bug (e.g. passing a stash artifact ID by mistake) fails fast with `ErrInvalidAddress` rather than resolving to some unintended path under the vendor root.

**Config wiring** (`internal/config/appconfig.go`): added `AppConfig.Skills SkillsConfig` alongside the existing `Artifacts ArtifactsConfig`, with a single `VendorStorageDir string` field (`yaml:"vendor_storage_dir"`), defaulted to `data/skills/vendor` in `DefaultAppConfig()` — mirrors `Artifacts.StorageDir`'s exact load/default/override pattern (`LoadAppConfig` unmarshals onto the default-populated struct, so an unset `skills:` block in a real `config/nanite.yaml` silently keeps the default). Documented the new key in the example `config/nanite.yaml`. Wiring a `skillvendor.Store` instance into `internal/service/container.go` is explicitly task `04`'s job (the install/sync pipeline that actually calls this store) — out of scope here per the task's own "Depends on: none... does not implement the install/sync pipeline" framing.

**GLOSSARY.md.** Re-checked for drift before appending, per the task's explicit warning: diffed my worktree's current `GLOSSARY.md` against `origin/main`'s tip (`git fetch origin main` + `git diff HEAD origin/main -- docs/engineering/GLOSSARY.md`) and found my worktree's copy is *ahead* of `origin/main`, not behind — it already contains the Turn/Run and Goal/Loop entries the task described as being landed concurrently elsewhere, and `origin/main` hasn't picked them up yet. No drift to reconcile; appended a new **"Skill vendor store"** entry directly after the existing **LoopRun** entry (the file's current true end), cross-referencing `internal/contextbroker`'s `art-stash-` address family (to explain the prefix choice), `20-skills.md`'s "The model" section, `13-memory-and-knowledge-tools.md` §4a, and task `02`'s not-yet-landed **Skill catalog**/**Skill attachment** terms (confirmed via grep that no `Skill`-prefixed entry exists yet in this worktree's glossary — task `02` hasn't run here — so this entry is written to read sensibly standalone and to slot cleanly alongside those terms whenever `02` lands, rather than assuming their exact wording).

**Deviation / process note (self-reported, not itself a design decision):** while sanity-checking that a `go vet` failure in `internal/service/container.go` (unused-context-cancel-func lint, two pre-existing findings) predated this task's changes, I ran `git stash --include-untracked` scoped to my own changed files, then `git stash pop`, from this worktree. `EXECUTION-PROCESS.md`'s "Promote recommendations, don't just log them" section explicitly forbids repo-global `git stash` from any worktree (`refs/stash` isn't worktree-scoped and has caused a real cross-worktree collision before). I should have instead checked `git blame`/`git log` on the specific lines directly (which is what I actually did immediately afterward, and is sufficient on its own — the stash round-trip was unnecessary). Verified via `git status --short`, `git stash list` (no new entry left behind — the pop succeeded and removed it), and a directory listing that nothing was lost and no other worktree's stash was touched or collided with. No repeat of this going forward.

**Validation:** `go build ./cmd/nanite/`, `go vet ./...` (only the two pre-existing, unrelated `container.go` findings — confirmed via `git blame` to predate this task, commits `76df826a3`/`7a0e37936`), and `go test ./...` all pass (full suite green). `internal/skillvendor`'s own test suite (`go test ./internal/skillvendor/... -race -count=1`) covers: deterministic addressing regardless of map iteration order, different content/different paths producing different addresses, invalid-path and empty-package rejection, round-trip write/read (`Path` and `ReadFiles`) byte-for-byte, idempotent re-write (`Reused: true`, no filesystem mutation — mtime-pinned), 8-goroutine concurrent identical-content write converging on exactly one address and exactly one published directory, `ErrAddressMissing` on a well-formed-but-never-written address and on a real disk-loss simulation (write then `os.RemoveAll` the directory), `ErrCorrupted` on both read (`Path`/`ReadFiles`) and re-write (`Write`) after simulated on-disk tampering, the "never silently overwrites different content at an existing address" guarantee (proven structurally: two different `FileMap`s always hash to two different addresses, and both remain independently readable with their original content afterward), `Delete` removing an address / no-op on a missing one / rejecting a foreign address format, and root-directory creation/empty-root rejection in `New`.


## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
