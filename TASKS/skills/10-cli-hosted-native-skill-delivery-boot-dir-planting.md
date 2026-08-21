# CLI-hosted delivery — plant vendored skill packages into each provider's native boot-dir location

**Phase:** 6 — Delivery (`TASKS/skills`)
**Status:** not-started
**Depends on:** `02`, `03`
**Touches:** new file `internal/runtime/agent/skill_plant.go` (a shared helper feeding all three
providers), `internal/runtime/agent/bootdir_claude.go`/`bootdir_codex.go`/`bootdir_opencode.go`
(each provider's own `<provider>PlantSpec` function — add the skill-files contribution to each),
`internal/service/chat_boot_drive.go` (wherever assigned-skill lookup needs to happen before
planting — likely near where `agent_context_resolvers` rows are already fetched for boot, per
the existing pattern).

## Context

`docs/engineering/architecture/20-skills.md`'s "Delivery" section: *"CLI-hosted agents
(Claude/Codex/OpenCode, launched by Nanite): the vendored package is copied or symlinked into the
agent's own native skill location inside its boot dir (e.g. a Claude-launched agent gets it
staged at `.claude/skills/<slug>/`). The agent then uses its own native skill mechanism —
unmodified, no Nanite-specific rendering, no Nanite self-tool required. Nanite's responsibility
stops at planting real files at the path the runtime already expects."*

**The reusable primitive, confirmed this planning session, real and already in production use for
a conceptually identical operation**: `internal/runtime/agent/bootdir_plant.go`'s
`writePlantedFile(bootDir, relPath, content string, mode os.FileMode) error` (lines 80-95) and
`plantSpec` (lines 137-178) — the shared per-provider write routine each provider's own
`<provider>PlantSpec` function (e.g. `claudeLayout.claudePlantSpec`, `bootdir_claude.go:67-91`)
assembles a `map[string][]byte` (`spec.Files`) for, then hands to `plantSpec` for atomic,
path-validated writing. Path-safety validation (`agentlaunch.ValidateBootDirRelPath`) rejects
absolute paths, `..` segments, and a short reserved-prefix list — it does **not** whitelist
specific paths, so an arbitrary nested relative key like `.claude/skills/<slug>/SKILL.md` is
already a valid `Files` map key today, confirmed. **There is no existing directory-copy helper**
— the model is "build the whole `map[relPath][]byte]` in memory, then write it." This task builds
that map from a skill's vendored content (task `03`'s store) and feeds it into each provider's
existing plant-spec assembly.

**Per-provider native locations differ** — Claude Code's is `.claude/skills/<slug>/`; confirm
the real equivalent conventions for Codex and OpenCode before assuming they're identical (they
may not have an equivalent native-skill mechanism at all — if a provider genuinely has none,
document that explicitly rather than guessing at a path; that provider simply gets no skill
planting, which is a legitimate outcome, not a gap to paper over).

## What to do

1. Determine which skills are "assigned+granted" to a given agent (the set this task plants) —
   via `agent_known_skills`' extended grant-state columns from task `02` (a row with a valid,
   currently-matching `approved_content_hash` is plantable; a stale-hash or ungranted row is
   not — mirror task `09`'s same trust-validity check, since planting an unapproved/stale skill
   into a boot dir would be a real trust-boundary bypass of exactly the mechanism task `09`
   builds for the API-direct path).
2. Build `internal/runtime/agent/skill_plant.go`'s core function: given an agent's plantable
   skill set, read each skill's vendored file tree from task `03`'s store, and produce a
   `map[relPath][]byte]` keyed at each provider's native convention (e.g.
   `.claude/skills/<slug>/<original-relative-path>` for Claude).
3. Wire this into each provider's `<provider>PlantSpec` function — confirm the real native
   convention for Codex and OpenCode first (per Context above) rather than assuming
   `.claude/skills/`-style paths apply universally; if a provider has no native skill mechanism,
   skip planting for it and note that explicitly.
4. Wire the planting call into the actual boot sequence (where `driveBootSession` or its
   equivalent already assembles other boot-dir content) and into the existing mid-session
   regeneration path (`regenerateBootDirSlots`, `internal/service/chat_boot_drive.go:633-675`) so
   that a newly-granted skill gets planted without requiring a full session restart, matching how
   other boot-dir content already regenerates on change.
5. Confirm this task does **not** touch `composeSystemPrompt`/`ResolveSystemPrompt`
   (`internal/runtime/agent/prompt.go`) or the skill-catalog-teaser rendering path — per
   `20-skills.md`'s explicit instruction, CLI-hosted delivery needs "no Nanite-specific
   rendering" — planting real files is the entire mechanism, nothing gets added to the system
   prompt text itself for this delivery path.

## Done means

- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- A real dogfeed (a live Claude-launched CLI session against a scratch agent with at least one
  granted skill) confirms the skill's `SKILL.md` (and any `scripts/`/`references/`/`assets/`
  files) are actually present on disk at `.claude/skills/<slug>/` inside the real boot dir after
  boot — not just that a function returned the right map in a unit test.
- A skill whose grant hash no longer matches its current vendored hash is **not** planted —
  verified by re-installing a granted test skill with changed content and confirming a
  subsequent boot omits it (or plants the old approved version only if that's the design you
  choose — document explicitly which).
- Granting a new skill mid-session results in it appearing in the boot dir without a full session
  restart, via the existing mid-session regen path.
- For any provider confirmed to have no native skill mechanism, this is documented explicitly in
  the Work Log, not silently unhandled.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
