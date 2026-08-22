# CLI-hosted delivery — plant vendored skill packages into each provider's native boot-dir location

**Phase:** 6 — Delivery (`TASKS/skills`)
**Status:** reviewed
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

**Implementation.**

- New file `internal/runtime/agent/skill_plant.go` — the shared helper feeding all three
  providers, per the task's own "Touches" list:
  - `SkillCatalogStore`/`SkillGrantStore`/`SkillStore` (combined) and `SkillVendorReader` — local,
    narrow interfaces (`*store.Store`/`*skillvendor.Store` satisfy them directly). Local rather
    than reused from `internal/skill/resolver.go`'s `SkillIndexStore` or `internal/skill/gate.go`'s
    `AgentKnownSkillStore` because `internal/skill` already imports
    `internal/runtime/agent` (for `ResolveContextBlocks`) — importing back would cycle.
  - `ResolvePlantableSkills(ctx, grants, catalog, agentID)` — mirrors `internal/skill/gate.go`'s
    `Gate.authorize` trust-validity check exactly (task 09), applied to planting instead of
    execution: a grant is plantable only when `ApprovedContentHash` is non-empty (excludes a bare
    `AssignSkillToAgent` row) **and** still matches the skill catalog row's current `ContentHash`
    (excludes a stale approval from before a since-superseded re-install/sync). Does **not**
    additionally gate on `store.Skill.Enabled` — `Gate.authorize` doesn't either, so the two checks
    stay in lockstep by design, per the task's own "mirror task 09" instruction.
  - `SkillPlantFiles(ctx, skills, vendor, destPrefixes)` — reads each plantable skill's vendored
    tree via `SkillVendorReader.ReadFiles(ContentHash)` and returns the combined
    `map[relPath][]byte`, nested under every prefix `destPrefixes(slug)` returns (a skill can be
    planted under more than one destination for the same boot dir).
  - `skillFilesForProvider(ctx, providerName, params)` — the per-provider dispatch: `claude` →
    `.claude/skills/<slug>/`; `opencode` → **both** `skills/<slug>/` and `.opencode/skills/<slug>/`
    (see "Per-provider native conventions" below); `codex` and anything unrecognized → `(nil, nil)`,
    no planting, not an error. Returns `(nil, nil)` whenever `SetupParams.Skills`/`.SkillVendor` is
    unset, so every pre-existing bootdir test/caller that doesn't know about skills is unaffected.
  - `PlantAgentSkillFiles(ctx, deps, bootDir, providerName, agentID)` — the exported entry point
    the mid-session regen path calls (see below); (re)plants into an *existing* boot dir via the
    already-production `writePlantedFile` primitive. Additive-only (documented explicitly in the
    file's own package doc — see "Known limitations" below).
- `SetupParams` (`bootdir.go`) gained `Skills SkillStore` / `SkillVendor SkillVendorReader`;
  `Dependencies` (`deps.go`) gained the same two fields; `composeBootdirParams` threads
  `deps.Skills`/`deps.SkillVendor` into `SetupParams` alongside the existing `CLIWritableRoots`
  threading.
- `claudePlantSpec` (`bootdir_claude.go`) and `opencodePlantSpec` (`bootdir_opencode.go`) each
  call `skillFilesForProvider` and merge the result into their `files` map, matching every other
  boot-dir content source (`sandboxFiles`, `mcpConfigBytes`) already merged there.
  `codexPlantSpec` (`bootdir_codex.go`) gets a doc-comment explaining the deliberate absence — see
  "Per-provider native conventions" below — and no functional change (What-to-do item 5 is
  satisfied structurally: `composeSystemPrompt`/`ResolveSystemPrompt`/the skill-catalog-teaser path
  are untouched by this whole task; nothing in this change adds anything to prompt text).
- Composition root: `AgentDepsConfig` (`internal/service/agent_deps.go`) gained `SkillVendor
  *skillvendor.Store`; `BuildAgentDependencies` sets `deps.Skills = cfg.Store` (satisfies
  `SkillStore` directly) and, **guarding against the classic Go "typed nil interface" trap**, only
  assigns `deps.SkillVendor = cfg.SkillVendor` when `cfg.SkillVendor != nil` — a nil
  `*skillvendor.Store` assigned unconditionally into the `SkillVendorReader` interface field would
  produce a non-nil interface wrapping a nil pointer, which `skillFilesForProvider`'s own `!= nil`
  guard would treat as "wired" and panic on the first `ReadFiles` call. `internal/service/
  container.go` threads the same `skillVendor` value (already constructed a few lines earlier for
  the install/sync pipeline, and already tolerant of a nil value on init failure) into
  `AgentDepsConfig.SkillVendor`.
- Mid-session regen (`internal/service/chat_boot_drive.go`'s `driveBootSession`): the prior
  `} else if s.slotsChangedFor(...) { regenerateBootDirSlots(...) }` branch is restructured into
  `} else { if s.slotsChangedFor(...) { regenerateBootDirSlots(...) }; runtimeagent.
  PlantAgentSkillFiles(ctx, s.agentDeps, sess.BootDir, sess.Provider, agent.ID) }` — see "Real
  design-latitude deviation from the task's literal wording" below for why skill (re)planting runs
  on *every* turn of an active session, independent of `slotsChangedFor`, rather than nested inside
  `regenerateBootDirSlots` and sharing its slot-hash gate.

**Per-provider native conventions (investigated for real, not assumed):**

- **Claude Code**: real, documented `.claude/skills/<slug>/SKILL.md` auto-discovery from cwd.
  Nanite's claude boot dir *is* cwd (`claudeLayout.SpawnWorkdir`), so `.claude/skills/<slug>/` is
  unambiguous.
- **OpenCode**: also has a real, documented native skill mechanism (confirmed via
  `opencode.ai/docs/skills/` and `opencode.ai/docs/config/`), but with genuine, unresolved
  published-doc ambiguity about which of two conventions Nanite's own `OPENCODE_CONFIG_DIR` env
  amendment (`opencodeLayout.AmendEnv`) actually reaches: project-local `.opencode/skills/<name>/`
  (relative to cwd), or a config-dir-relative `skills/<name>/` (the docs describe
  `OPENCODE_CONFIG_DIR` as searched "just like the standard `.opencode` directory... should follow
  the same structure" — which a separate doc section says includes plural `skills/` — but the same
  paragraph's own explicit enumeration of what gets searched there names only "agents, commands,
  modes, and plugins," not "skills"). Given real ambiguity and zero functional cost to covering
  both (inert extra files; nothing else in `opencodePlantSpec`'s `Files` map uses either top-level
  name), `opencodeSkillDestPrefixes` plants to **both** `skills/<slug>/` and
  `.opencode/skills/<slug>/`. This is a design-latitude call (task's own instruction: "confirm the
  real native convention... rather than assuming"), documented in `skill_plant.go`'s package doc
  rather than left silent.
- **Codex**: confirmed to have **no native skill mechanism at all** — checked directly against (1)
  the vendored `go-providers` module's `CodexAdapter.BootDirSpec` (no skills-related planted file
  or env amendment anywhere in it), (2) a live web fetch of the OpenAI Codex CLI's own
  `docs/config.md` reference (zero mentions of "skill"/"skills"), and (3) the CLI's GitHub README
  (same). `codexPlantSpec` has no skill-files contribution — this is the "provider genuinely has no
  equivalent native-skill mechanism" outcome the task's own Context explicitly names as a
  legitimate result, not a gap. **This satisfies the Done-means bullet "for any provider confirmed
  to have no native skill mechanism, this is documented explicitly" — Codex is that provider.**

**Real design-latitude deviation from the task's literal wording, with reasoning:**
`hashSlots`/`slotsChangedFor` (`chat_boot_drive.go`) only hash `SlotSystem`/`SlotAgent`/`SlotMode`/
`SlotRules` prompt-slot content. Per `20-skills.md`'s own explicit instruction (also this task's
What-to-do item 5), CLI-hosted skill delivery adds **nothing** to any prompt slot at all — so a
bare skill grant/revoke can never change the slot hash. Nesting the skill-replant call *inside*
`regenerateBootDirSlots` and letting it share that function's existing `slotsChangedFor` gate (the
most literal reading of "wire the planting call into... the existing mid-session regeneration
path") would make the Done-means bullet "granting a new skill mid-session results in it appearing
in the boot dir without a full session restart" essentially unreachable in the ordinary case (it
would only fire by coincidence, alongside an unrelated System/Agent/Mode/Rules slot change in the
same turn). Per `EXECUTION-PROCESS.md`'s "reasoning vs. instruction" guidance, What-to-do's actual
instruction is the acceptance criterion (a newly-granted skill appears without a full session
restart), not the literal call-site nesting — so `driveBootSession`'s active-session branch now
calls `runtimeagent.PlantAgentSkillFiles` unconditionally on every turn, alongside (not nested
inside) the still slot-hash-gated `regenerateBootDirSlots` call. This is cheap (a couple of DB
lookups plus a handful of small file writes, typically zero-to-few granted skills per agent) and
idempotent/additive, so re-checking every turn is the correct granularity. Confirmed live in the
dogfeed below: granting `late-grant-skill` mid-session (no slot content changed at all) and sending
one more ordinary chat turn planted it into the same, already-running boot dir.

**Live dogfeed (real Claude-launched CLI session, scratch DB/vendor-store/boot-dir throughout —
absolute paths under this session's `/private/tmp/claude-.../scratchpad/skill-dogfeed-10/`, never a
relative path resolved against CWD, never any real tracked `.nanite/`):**

1. Built `nanite-bin` from this worktree; installed two real skill packages
   (`demo-dogfeed-skill` with `SKILL.md` + `scripts/hello.sh`; `late-grant-skill` with just
   `SKILL.md`) into a scratch vendored store via `nanite skill install` against a scratch
   `NANITE_DB_PATH` and a scratch `config/nanite.yaml` (`skills.vendor_storage_dir` set to an
   absolute scratch path — the CWD-relative default, `data/skills/vendor`, is exactly the footgun
   this project's process doc warns about, avoided by an explicit absolute override).
2. Started `nanite-bin serve` against the same scratch DB (`NANITE_WORKSPACE=skills-dogfeed-10`,
   never `default`). Created a real agent via `POST /api/agents`, then set
   `default_provider='pty'`/`runtime_kind='cli'` directly via `sqlite3` against the scratch DB
   (the create/update REST payloads — `CreateAgentRequest`/`UpdateAgentRequest`,
   `internal/api/types.go` — have no `default_provider`/`runtime_kind` field at all; a genuine,
   separate pre-existing REST-surface gap, unrelated to this task, not fixed here). Granted
   `demo-dogfeed-skill` to the agent via a direct `agent_known_skills` insert with
   `approved_content_hash` set to the skill's real vendored address (task 02's grant-state columns
   have no REST-settable path yet either — also pre-existing, also out of scope).
3. Created a session bound to that agent, sent a real chat message through `POST /api/messages` —
   real `claude` CLI subprocess spawned, replied "hi." over real SSE (`stream_start`/`delta`/
   `stream_end`), confirming the whole harness path (not a stub).
4. Found the real boot dir under `$TMPDIR` (`nanite-boot-claude-<sessionID>-r0-*`) and confirmed
   `.claude/skills/demo-dogfeed-skill/SKILL.md` and `.claude/skills/demo-dogfeed-skill/scripts/
   hello.sh` present with exact expected content — **the Done-means dogfeed bullet, satisfied
   directly, not inferred from a unit test.**
5. Granted `late-grant-skill` mid-session (agent already booted, session already running) via a
   second direct `agent_known_skills` insert, then sent one more ordinary chat message (no slot
   content changed) — confirmed `.claude/skills/late-grant-skill/SKILL.md` appeared in the
   **same** boot dir (same directory name, no new boot dir created, no restart) after that turn —
   **the mid-session Done-means bullet, satisfied.**
6. Re-installed (`nanite skill sync`) `demo-dogfeed-skill` with changed body content, bumping its
   catalog `ContentHash` to a new vendored address, while the agent's existing grant's
   `approved_content_hash` stayed pointed at the old (now-stale) address. Created a **second**,
   fresh session for the same agent and sent a message — a brand-new boot dir was created, and its
   `.claude/skills/` contained **only** `late-grant-skill` (whose approval was still current) —
   `demo-dogfeed-skill` (stale approval) was correctly **omitted entirely** — **the stale-hash
   Done-means bullet, satisfied. Design choice: a stale-approval skill is omitted outright, never
   planted at its old approved content — there is no "plant the old approved version" fallback.**
7. Cleanup: killed the scratch `nanite-bin serve` process and its two spawned `nanite mcp`
   subprocesses (`pkill -f skill-dogfeed-10/nanite-bin`); confirmed via `git status --short` in
   this worktree that nothing leaked outside the scratch directory (the agent-create call's
   "managed agent" file write landed at `<scratch>/.nanite/agents/skill-dogfeed-agent-10.md`,
   never the real tracked `.nanite/agents/`, because the server's CWD was the scratch dir
   throughout).

**Unit tests** (`internal/runtime/agent/skill_plant_test.go`, all passing): grant-validity branch
coverage (approved+matching → plantable; ungranted/empty-hash, stale-hash, and dangling-catalog-row
→ excluded; nil-input tolerance); `SkillPlantFiles` multi-prefix fan-out; per-provider dispatch
(claude/opencode/codex/unknown); no-skill-wiring no-op tolerance (so every pre-existing bootdir
test stays unaffected); a real `claudeLayout{}.Setup(...)` call asserting the granted skill's files
land on disk and the stale-approval skill does not; `PlantAgentSkillFiles`'s mid-session
before/after-grant behavior; nil-`Dependencies` and no-wiring guards.

**Baseline checks:** `go build ./cmd/nanite/`, `go vet ./...` (two pre-existing, unrelated
`stopReaper`/`stopRuntimeReaper` warnings in `container.go` confirmed via `git diff` to predate
this change), and `go test ./...` all pass.

**Known limitations (documented, not Done-means gaps):**
- **Additive-only; no removal-on-revoke.** Every write in this codebase's boot-dir mechanism
  (`writePlantedFile`, `Populate`, `plantSpec`) is additive/idempotent-by-overwrite — none delete a
  previously-planted file that's no longer needed, and this task follows the same convention. A
  skill that stops being plantable (revoked, or a stale re-approval) simply stops being included in
  a future plant/replant call's map; its previously-planted files stay on disk in an
  already-running session's boot dir until that boot dir itself is torn down. This is a real,
  narrow residual-trust window (a CLI agent's own native skill mechanism re-reads its skill
  directory on each turn, not once at process start), logged here explicitly rather than left
  undiscoverable — not something the task's Done-means requires fixing (its own wording is "is not
  planted," satisfied by omission from future plant calls, and "appears... without a restart,"
  satisfied by the additive mid-session path).
- **OpenCode's dual-destination choice is a documented best-effort, not a verified-against-real-
  opencode-binary confirmation** — no live OpenCode dogfeed was run (the task's Done-means only
  requires a live Claude-launched dogfeed); the two-destination choice is deliberately safe
  (inert extra files either way) rather than a guess at a single path.
- **`default_provider`/`runtime_kind` and skill grant-state columns have no REST-settable path
  yet** — found during the dogfeed (item 2 above); a genuinely separate, pre-existing gap in
  `internal/api/types.go`'s `CreateAgentRequest`/`UpdateAgentRequest` and the known-skills REST
  handlers, unrelated to this task's own scope, not fixed here.

## Review notes

**FAIL.** Fresh reviewer with no shared context found one HIGH-severity bug and one MEDIUM
oversight. Full details logged in `TASKS/ESCALATIONS.md`'s 2026-08-22 entry. Summary:

1. **HIGH — path traversal via unvalidated skill slug.** `skill_plant.go`'s
   `claudeSkillDestPrefixes`/`opencodeSkillDestPrefixes` build each skill's destination via
   `path.Join(<providerPrefix>, slug)` with no validation of `slug`. `store.Skill.Slug` has no
   format validation anywhere in the codebase (confirmed across `internal/api/skills.go`,
   `internal/skill/parser.go`, `internal/skillinstall/validate.go`, and the migration's schema).
   `path.Clean` (called internally by `path.Join`) lets a `..`-laden slug cancel out the entire
   destination prefix — e.g. `path.Join(".claude/skills", "../..")` → `"."`, landing a vendored
   `CLAUDE.md` at the exact key used for the agent's real system prompt, silently overwritten
   with zero error. `ValidateBootDirRelPath` doesn't catch this because the canceled-clean path
   itself contains no remaining `..`. Fix: validate that each skill's cleaned destination
   genuinely stays under its own intended prefix before merging into the `Files` map (e.g.
   `strings.HasPrefix(cleaned, prefix+"/")`), or validate `slug` itself against an allow-list
   pattern before it's ever used in `path.Join`.
2. **MEDIUM — unconditional per-turn replant call doesn't guard ACP sessions.**
   `chat_boot_drive.go`'s new `PlantAgentSkillFiles` call in `driveBootSession`'s active-session
   branch runs on every turn with no `sess.BootDir != ""` guard. ACP-protocol sessions have a
   permanently empty `BootDir` by design (`internal/runtime/agent/agent_acp.go`), so every turn
   of every ACP-driven agent now logs a `slog.Warn` forever. Fix: guard the call with the same
   emptiness check (or protocol check) `regenerateBootDirSlots` already implicitly tolerates via
   its occasional-firing gate.

## Fix required

1. In `internal/runtime/agent/skill_plant.go`, after computing each skill's per-provider
   destination path(s), assert the cleaned result stays under the intended prefix
   (`.claude/skills/<slug>/`, `skills/<slug>/`, `.opencode/skills/<slug>/`) before adding any
   entry to the `Files` map. Reject (skip, with a logged warning — do not silently plant into a
   sanitized fallback path) any skill whose slug would escape its own prefix. Add a regression
   test with an adversarial slug (e.g. `"../.."`, `".."`) proving the escape is blocked and the
   skill is omitted rather than landing elsewhere.
2. In `internal/service/chat_boot_drive.go`, guard the new unconditional `PlantAgentSkillFiles`
   call in `driveBootSession`'s active-session branch with a check that the session actually has
   a boot dir (`sess.BootDir != ""`, or equivalent protocol-based check) before calling it. Add or
   extend a test confirming an ACP-style session (empty `BootDir`) does not trigger a per-turn
   warning log.
3. Re-run `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` — all must pass.
4. Update this file's own Work Log with what was actually fixed and how it was verified
   (including the adversarial-slug test's exact assertion), and set Status to `implemented`.

## Fix Work Log (2026-08-22)

**Bug 1 fixed — path traversal via unvalidated skill slug
(`internal/runtime/agent/skill_plant.go`).**

- Added `skillDestPrefixSafe(root, slug string) (dest string, ok bool)`: joins `root` and
  `slug` exactly the way `claudeSkillDestPrefixes`/`opencodeSkillDestPrefixes` already did
  (`path.Join(root, slug)`, which internally `path.Clean`s), then compares the result against
  the **literal, uncleaned** string concatenation `root + "/" + slug`. A slug with no
  `.`/`..` segments produces byte-identical strings on both sides; any slug that cancels part
  or all of `root` via `..` diverges the two (the cleaned join loses components the literal
  concatenation still has), which is treated as an escape attempt and rejected — `("", false)`.
  This is exactly the "compare against a literal, not-yet-cleaned prefix" technique the
  escalation's own fix guidance suggested, and it needs no path-component enumeration or
  `filepath.Rel` gymnastics: `path.Clean`'s own cancellation behavior is precisely what makes
  the literal-vs-cleaned strings diverge whenever `..` actually ate into `root`.
- `claudeSkillDestPrefixes`/`opencodeSkillDestPrefixes` now return `([]string, bool)` instead
  of a bare `[]string`; `false` means "this skill's destination(s) are unsafe, do not plant it
  anywhere." OpenCode's two-destination variant requires **both** candidate destinations to
  pass the check — a slug unsafe for the shorter `"skills"` root but coincidentally still safe
  for `.opencode/skills` (or vice versa) still gets the skill skipped entirely, never a partial
  plant to only the destination that happened to check out.
- `SkillPlantFiles`'s `destPrefixes` callback signature changed to
  `func(slug string) ([]string, bool)` to match; when a skill's callback returns `false`, that
  skill is `continue`d past entirely — never merged into the returned `Files` map, never
  redirected to any fallback/"sanitized" path, and (per the task's instruction not to plant a
  partial/mangled version) the vendored tree is never even read via
  `SkillVendorReader.ReadFiles` for that skill.
- `skillFilesForProvider` wraps the raw per-provider dispatch function in a closure that logs
  `slog.Warn("agent: skipping skill plant — destination would escape its own per-skill subtree
  (adversarial or malformed slug)", "agent_id", ..., "provider", ..., "skill_slug", ...)` on a
  `false` — this is where `agent_id`/`provider` context lives (the lower-level
  `SkillPlantFiles`/`skillDestPrefixSafe` functions only ever see the bare skill slug), so the
  warning identifies both the skill and the agent, per the fix's own requirement.
- Verified the actual invariant, not just the two literal reproduction slugs named in the
  finding: `TestSkillDestPrefixSafe` (new, `internal/runtime/agent/skill_plant_test.go`) is a
  table test covering a normal slug (accepted, destination == `root/slug`), `".."` against both
  a two-segment root (`.claude/skills`) and a one-segment root (`skills` — the OpenCode
  "shorter prefix" case), `"../.."`, `"../../.."`, an internal-cancellation slug
  (`"valid/../valid2"` — rejected even though its cleaned result technically stays under
  `root/`, since it doesn't match its own literal per-skill subtree), an embedded-mid-slug
  cancellation (`"a/../../b"`), and an empty slug — plus an explicit assertion that whenever the
  function reports `ok`, the destination is both `!= root` and has `root+"/"` as a genuine path
  prefix.
- `TestClaudeSkillDestPrefixes_AdversarialSlugBlocked` / `TestOpencodeSkillDestPrefixes_AdversarialSlugBlocked`
  pin the exact two reproduction cases from the escalation: `claudeSkillDestPrefixes("../..")`
  and `opencodeSkillDestPrefixes("..")` both return `(nil, false)`.
- `TestSkillPlantFiles_AdversarialSlugBlocked` is the task's own required regression test, run
  as a table over both provider shapes. For each case it (a) confirms `SkillPlantFiles` returns
  zero files for the adversarial-slug skill (the escape is blocked), (b) confirms the malicious
  skill's slug produces **no** entries in the returned `Files` map at all (omitted, not
  relocated), and (c) merges the (empty) result into a stand-in "already-planted" file map
  containing a sentinel at the exact collision key each case's canceled prefix would otherwise
  hit — `"CLAUDE.md"` for the claude `"../.."` case (the real key `claudePlantSpec` uses for the
  agent's system prompt) and `"agents.json"` for the opencode `".."` case (a stand-in for
  opencode's own real top-level config key) — and asserts the sentinel's content is unchanged
  after the merge, proving no clobber.
- `TestSkillFilesForProvider_AdversarialSlugSkippedButOthersPlanted` exercises the fix at the
  layer the reviewer's own finding traced the vulnerability to: `store.Skill.Slug` itself (not
  just the grant's skill name) carries the adversarial value (mirroring "REST-settable via
  `POST /api/skills`, no format validation"), granted alongside a second, legitimate skill for
  the same agent. Confirms the legitimate skill still plants normally
  (`.claude/skills/good/SKILL.md` present) while the adversarial one is fully omitted and no
  planted key falls outside `.claude/skills/` — proving the fix rejects only the offending
  skill, not the whole per-agent batch.
- Updated the existing `TestSkillPlantFiles_MultiplePrefixes`/`_EmptyInputs` call sites' inline
  closures to the new `([]string, bool)` return shape; no behavioral change to those tests.

**Bug 2 fixed — unconditional per-turn skill-replant call didn't guard ACP sessions
(`internal/service/chat_boot_drive.go`).**

- `driveBootSession`'s active-session branch now gates the `runtimeagent.PlantAgentSkillFiles`
  call on `sess.BootDir != ""` in addition to the existing `agent != nil && agent.ID != ""`
  check: `if agent != nil && agent.ID != "" && sess.BootDir != "" { ... }`. ACP-protocol
  sessions (`internal/runtime/agent/agent_acp.go`'s `bootACP`) leave `Session.BootDir`
  permanently empty by design (confirmed directly in that file — `BootDir: "",` with a comment
  explaining native CLI runtimes populate it, ACP does not), so this guard is a direct,
  structural match for "this session has a real boot dir to plant into," not an incidental
  side-effect of some other gate. Updated both the call site's own inline comment and the
  function's top-of-file doc comment to describe the guard and why it's needed (steady-state
  per-turn log noise otherwise, since `PlantAgentSkillFiles` itself errors on an empty
  `bootDir`) and to note `regenerateBootDirSlots` has the identical failure mode but is masked
  by only firing when `slotsChangedFor` is true — this call runs unconditionally every turn, so
  it needed its own explicit guard rather than inheriting that incidental tolerance.
- `TestDriveBootSession_ACPSessionSkipsSkillReplant` (new,
  `internal/service/chat_boot_drive_test.go`) drives the **real, unmocked**
  `driveBootSession`/`PlantAgentSkillFiles` code path end-to-end against a pre-registered
  `*runtimeagent.Session{Provider: "claude-acp", BootDir: ""}` (the exact shape `bootACP` leaves
  a session in) and an `agent.ID` set so the guard's other two conditions are already true. It
  captures `slog`'s default logger output into a goroutine-safe buffer (`syncBuffer`, new in the
  test file — a background `SendInput`-failure logger elsewhere in the same function makes a
  bare `bytes.Buffer` a race hazard under `-race`) and asserts the string
  `"driveBootSession: skill replant failed"` never appears in it. This exploits
  `PlantAgentSkillFiles`'s own deterministic "empty bootDir" error as the observable signal
  rather than mocking the function: without the guard, the call fires unconditionally, always
  errors for an empty `bootDir`, and the call site's own `slog.Warn` logs that exact string —
  so the test is a genuine, real-implementation proof that the call is skipped, not merely that
  no error surfaced. **Verified as a true regression test, not just a passing assertion**: with
  the guard's `&& sess.BootDir != ""` clause temporarily removed and the test re-run, it failed
  exactly as expected — `PlantAgentSkillFiles was invoked for an ACP-shaped session (empty
  BootDir) — guard did not skip it; log: ...msg="driveBootSession: skill replant failed"
  session_id=sess-acp-guard-1 err="agent: PlantAgentSkillFiles: empty bootDir"` — before the
  guard was restored and the suite re-confirmed green.
- Added `TestDriveBootSession_RealBootDirStillTriggersSkillReplant` as the positive control:
  same active-session path, but with a real `t.TempDir()` `BootDir` and a granted skill wired
  through `runtimeagent.Dependencies.Skills`/`SkillVendor` (two small package-local fakes,
  `fakeSkillStoreForBootDrive`/`fakeSkillVendorForBootDrive`, satisfying the exported
  `runtimeagent.SkillStore`/`SkillVendorReader` interfaces — `internal/runtime/agent`'s own test
  doubles are unexported and package-private, so this package needs its own). Confirms the
  skill's `SKILL.md` genuinely lands on disk at `.claude/skills/demo/SKILL.md` inside the real
  boot dir after `driveBootSession` returns — proving the new guard doesn't overzealously
  suppress the legitimate (non-ACP) case too.

**Baseline checks (post-fix):** `go build ./cmd/nanite/`, `go vet ./...` (same two
pre-existing, unrelated `stopReaper`/`stopRuntimeReaper` warnings in `container.go`,
reconfirmed via `git log`/`git diff` against this worktree's base commit to predate this whole
task, not just this fix), and a full `go test ./...` across every package in the module — all
pass.

**No scope creep.** Both fixes are confined to `internal/runtime/agent/skill_plant.go` and
`internal/service/chat_boot_drive.go` (plus their two test files) exactly as the Fix-required
section scoped them; no other file in the original task's "Touches" list was modified.

## Re-review notes (2026-08-22)

**PASS.** Fresh re-reviewer, no shared context, verified commit `671c1e7e` against `main` HEAD.

- **Bug 1 fix confirmed sound in general, not just against the two known reproduction cases.**
  Proved the invariant `skillDestPrefixSafe` enforces is `path.Clean(root+"/"+slug) ==
  root+"/"+slug` byte-for-byte — since `root` is always a hardcoded canonical literal, this holds
  iff every `/`-separated segment of `slug` is non-empty, non-`.`, non-`..`. Fuzzed ~25 adversarial
  slugs (`".."`, `"../.."`, leading `/`, `"."`, trailing slashes, embedded `"../.."`, empty string,
  `"\x00"`, `"~"`, URL-encoded `"..%2f.."`) against all three provider roots — every accepted slug
  keeps the root as a genuine prefix, every rejecting case is a real escape attempt. Confirmed via
  direct code read that a rejected skill is fully skipped before `vendor.ReadFiles` is even called
  — not a partial plant, not a redirect.
- **Mutation-tested both fixes**, matching this batch's established verification bar: stubbed
  `skillDestPrefixSafe` to always return safe — all five new/updated tests failed with the exact
  pre-fix symptom (vendored file landing at the boot-dir root); removed the ACP `BootDir` guard —
  the ACP test failed with the exact predicted "empty bootDir" warning. Both reverted and
  reconfirmed green.
- **Two non-blocking observations, not reasons to fail:**
  1. The Work Log's rationale for OpenCode's "both destinations must pass" check slightly
     overclaims — since the safety check's outcome depends only on `slug`'s own structure (not
     which hardcoded root it's appended to), the two checks are mathematically guaranteed to
     always agree; the code is still correct and safe, just marginally more defensive than the
     stated rationale requires. No code change needed.
  2. `TestSkillPlantFiles_AdversarialSlugBlocked`'s "no clobber" sub-assertion uses a sentinel
     filename (`"CLAUDE.md"`) that doesn't match the vendor fixture's actual filename
     (`"SKILL.md"`), so that one sub-assertion is trivially satisfied rather than independently
     exercising a real same-key collision. The core protection this test proves (zero files
     planted for a blocked skill) is sound and is what the mutation test actually falsified — a
     cosmetic test-fidelity gap, not a gap in the fix.
- **A pre-existing, unrelated flaky race was surfaced during verification**, not introduced by
  this diff: a `send on closed channel` panic in `driveBootSession`'s background `SendInput`
  failure-handling goroutine (`chat_boot_drive.go`, a TOCTOU race against a concurrent channel
  close), confirmed via `git blame` to predate this task by months. Logged separately in
  `TASKS/ESCALATIONS.md` as a follow-up candidate, not a blocker for this task.
- Build/vet/test all pass on `main` HEAD (`671c1e7e`); the one `go vet` finding is the
  already-confirmed pre-existing, unrelated `container.go` reaper warning.

**Task `10` is fully closed: implemented, validated, reviewed. Wave 7 (`10`, `11`) is complete.**
