# Optional: shared `plugin.LoadEmbeddedManifest`/`plugin.BasePlugin` helper for the 4 CLI-ecosystem adapter plugins

**Phase:** Wave 6 — Semantic duplication / migration drift
**Status:** not-started
**Depends on:** none
**Touches:** `internal/plugin/builtin/adapter-claude`, `internal/plugin/builtin/adapter-codex`, `internal/plugin/builtin/adapter-gemini`, `internal/plugin/builtin/adapter-opencode`. Explicitly **not** `internal/plugin/builtin/adapter-nanite-native` (see Non-goals).

`requires_architect_decision: false` — optional cleanup, no design ambiguity, low priority.

## Context

### Findings addressed
- `GO-PLUGIN-007` — severity low, confidence high. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.6; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-PLUGIN-007`.

### Root cause

The 4 CLI-ecosystem adapter plugins — `adapter-claude`, `adapter-codex`, `adapter-gemini`, `adapter-opencode` — duplicate ~35-45 lines each of manifest-loading/registration/lifecycle boilerplate, **byte-for-byte identical aside from names/strings** (plugin name, description, and similar per-adapter identifying data). `adapter-nanite-native`, a fifth plugin in the same directory, was explicitly checked by the audit and found to genuinely diverge with real additional logic — it is **correctly excluded** from this finding and should stay excluded from this task's scope. Classification **(1) textual-only boilerplate**.

### Current behavior

The audit's evidence array for this finding is empty beyond the four directory paths; there is no file:line citation to a specific duplicated block. **Read all four adapters' plugin-registration files in full before starting** (the exact filename within each adapter directory — likely `plugin.go`, `main.go`, or similar; confirm the actual layout via `ls internal/plugin/builtin/adapter-claude/` etc.) to identify precisely which ~35-45 lines are duplicated and which (if any) genuinely differ beyond names/strings.

### Desired invariant

The manifest-loading/registration/lifecycle boilerplate shared by all 4 CLI-ecosystem adapters exists in one place (a shared helper or base type); each adapter's own file contains only its genuinely adapter-specific data (name, description, whatever else legitimately varies) and calls into the shared mechanism for everything else.

## What to do

### Scope
- The 4 adapter plugin directories named above — specifically their manifest-loading, registration, and `init()`-based self-registration lifecycle code.
- A new (or existing, if one already exists elsewhere in `internal/plugin` and is simply unused by these 4 adapters — check first) shared helper: `plugin.LoadEmbeddedManifest` and/or `plugin.BasePlugin`, per the audit's own naming suggestion. Confirm whether either already exists in `internal/plugin`'s public API before creating a new one — the audit's phrasing ("could remove most of it") suggests this is a proposed new mechanism, not a reference to something that already exists and is merely unused; verify this assumption against current source.

### Proposed direction

Per the audit's own recommendation: a shared `plugin.LoadEmbeddedManifest`/`plugin.BasePlugin` helper could remove most of the duplicated boilerplate **without touching per-adapter logic** — i.e., this is meant to be a pure extraction, not a redesign of how any individual adapter behaves. The audit also notes explicitly: **"some boilerplate is structurally unavoidable given the `init()`-based self-registration model"** — do not force-fit every line into the shared helper if the `init()`-registration pattern genuinely requires some boilerplate to remain per-file (Go's `init()` functions can't be parameterized or shared across packages the way ordinary functions can); extract what can cleanly be shared, and leave what can't with a clear note explaining why it stays duplicated.

### Non-goals
- **Not** touching `adapter-nanite-native` — the audit explicitly confirmed it has real additional logic beyond the shared boilerplate shape and correctly excluded it; this task should leave it alone.
- Not a broader redesign of the `internal/plugin/builtin` adapter architecture — this is a narrow boilerplate-extraction task.
- Not required to eliminate 100% of the duplication if the `init()`-based self-registration model genuinely prevents it — a partial reduction that removes the genuinely-shareable boilerplate while leaving the structurally-unavoidable parts is an acceptable, complete outcome for this task.

## Tests required

- Existing tests for all 4 adapters (plugin registration, manifest loading, lifecycle) must pass unchanged after the extraction — this is a pure refactor, no intended behavior change.
- If a shared helper is introduced, a small dedicated test for it (given a representative manifest, confirm correct loading/registration behavior) is good practice, though the primary regression guard is the existing per-adapter test suite.

## Prevention

Not a defect-prevention concern in the usual sense — this is boilerplate-reduction cleanup. Its main value is reducing the chance a future 5th CLI-ecosystem adapter copies the same ~35-45 lines a 6th time rather than using the now-shared helper.

## Verification

```bash
go build ./internal/plugin/builtin/...
go vet ./internal/plugin/builtin/...
go test ./internal/plugin/builtin/... -v
```

Observable behavior required for PASS: all 4 adapters build and pass their existing tests unchanged after extraction; `adapter-nanite-native` is untouched; the shared helper (if introduced) is used by all 4 adapters, not just some of them.

## Risk / rollback

Low risk — a pure boilerplate extraction across 4 files with existing test coverage as the regression guard. The main risk is inadvertently touching `adapter-nanite-native` or changing any adapter's genuinely-distinct behavior (name, description, or other per-adapter data) while extracting the shared shape — mitigate by diffing each adapter's file before and after to confirm only the intended boilerplate lines moved. Rollback is a per-file revert.

## Done means

- [ ] Shared boilerplate identified precisely across all 4 adapters (read all 4 files in full before extracting).
- [ ] `plugin.LoadEmbeddedManifest`/`plugin.BasePlugin` (or equivalent) helper introduced, confirmed not to already exist unused.
- [ ] All 4 adapters migrated to use the shared helper for their shareable boilerplate; structurally-unavoidable `init()`-registration boilerplate left in place with a note explaining why.
- [ ] `adapter-nanite-native` untouched.
- [ ] `go build`, `go vet`, `go test ./internal/plugin/builtin/...` all pass.

## Work log

<!-- Worker fills this in. -->

## Review notes

<!-- Reviewer fills this in. -->
