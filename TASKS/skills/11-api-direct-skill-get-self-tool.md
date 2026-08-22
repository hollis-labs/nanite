# API-direct delivery — `skill_get` self-tool

**Phase:** 6 — Delivery (`TASKS/skills`)
**Status:** reviewed
**Depends on:** `06`, `07`, `08`, `09` (the full Resolver → Materializer → Policy/Sandbox
pipeline this self-tool is the entry point into)
**Touches:** `internal/selftools/self_tools.go` (new `skill_get` `InputSchema`),
`internal/selftools/self_tools_transport.go` (new dispatch case), `internal/chat/context.go`
(`buildSkillListForSession` — update rendering only, not selection logic, to match task `02`'s
new index columns).

## Context

`docs/engineering/architecture/20-skills.md`'s "The invocation gap" section: *"The most
load-bearing gap found in this session: no skill's body content has ever reached a model, in any
runtime, through any live mechanism... API-direct agents: a new self-tool (`skill_invoke` or
equivalent naming) is the real entry point into the Resolver → Materializer → Policy/Sandbox →
Materialized Skill pipeline. An agent calls it with a slug plus parameters, gets back fully
materialized content... as a tool result. The catalog block stays a lightweight teaser —
progressive disclosure."*

**Naming: `skill_get`, not `skill_invoke`** — a deliberate departure from the architecture doc's
own placeholder phrasing, decided during this batch's planning (see
`TASKS/skills/README.md`'s corrections section and `TASKS/ESCALATIONS.md`'s 2026-08-21 entry).
`docs/tool-naming-convention.md`'s verb table already anticipates a `get` verb ("Fetch a single
resource by ID"), and `docs/tool-naming-audit.md`'s verdict table already renamed this exact
self-tool family (`skill_create`→cut in task `01`, `skill_list`, `skill_update`→cut in task `01`,
`skill_delete`) to the bare, prefix-free `skill_*` shape — `skill_get` matches that established,
already-renamed family directly. Confirm no other `skill_get` reference exists before locking
this in — the only prior hit found this planning session
(`internal/service/ingest_test.go:350,370-371`) is an explicit test-fixture placeholder for a
*non-existent* tool name (comment: `"skill_get"], // not real either`) proving the name was free,
not a real prior implementation to reconcile with; update or remove that test comment once this
tool is real.

**Confirmed this planning session: no `skill_get`/`skill_invoke`/any content-retrieval self-tool
exists anywhere today** — the four current `skill_*` self-tools (`self_tools.go:66,85,102,122`)
are pure CRUD-metadata operations; none has a `prompt` field in either input or (implicitly)
output. This task is genuinely new, not an extension of an existing tool.

**The catalog teaser stays exactly as it is, selection-wise** — `buildSkillListForSession`
(`internal/chat/context.go:251-287`) renders `- name: description [tools: ...]` with no mode
filter, no scoring, a flat `SkillEssentialCap=25` cap, `ORDER BY sk.name`. Per `20-skills.md`:
"progressive disclosure, matching the Agent-Skills-spec's own pattern, not a departure from it."
This task only needs to update what fields get rendered (task `02` dropped `Prompt`/`ToolBindings`
from `store.Skill` — confirm the teaser never referenced `Prompt` anyway, per this planning
session's research, so likely no change needed there at all beyond `ToolBindings`' removal) —
**do not** add scoring, mode-filtering, or any selection logic here; that's explicitly out of
scope per `TASKS/skills/README.md`'s scope fences.

## What to do

1. Add `skill_get`'s `InputSchema` to `internal/selftools/self_tools.go`: `slug` (required,
   string), `params` (optional, JSON object — static invocation arguments per task `06`'s
   Resolver). Output: the fully materialized skill content (task `06`-`08`'s pipeline result,
   with `inline` dependencies spliced and `fork` dependencies delegated-and-folded per task `07`).
2. Add the dispatch case in `self_tools_transport.go`: look up the skill by slug, confirm the
   invoking agent has a valid grant (task `09`'s same trust-validity check — an agent calling
   `skill_get` for a skill it isn't granted, or whose approved hash is stale, gets a clear
   "not granted"/"re-approval required" error, not silently-empty or unauthorized-but-succeeding
   content), then run the full Resolver → Materializer → Policy/Sandbox pipeline
   (tasks `06`-`09`) and return the result as the tool result.
3. Update `buildSkillListForSession`'s rendering only (drop any now-removed-column references) —
   verify against task `02`'s actual final `Skill` struct shape once that task lands.
4. Update `internal/service/ingest_test.go:350,370-371`'s stale `"skill_get" // not real either`
   comment/test now that the tool is real (confirm what that test was actually asserting — likely
   "unknown tool references get logged" using `skill_get` as one example of several nonexistent
   names; if so, swap in a different, still-nonexistent placeholder name rather than just deleting
   the assertion).

## Done means

- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- A real dogfeed (API-direct agent, e.g. `anthropic` provider, real chat turn) confirms an agent
  calling `skill_get` with a granted skill's slug receives real materialized content back as a
  tool result — verified against the actual message/turn transcript, not just a unit test of the
  handler.
- Calling `skill_get` for an ungranted skill, or a skill whose approved hash is stale, returns a
  clear denial — verified the same way task `09`'s equivalent test does, reused/adapted rather
  than reinvented.
- The catalog teaser (`buildSkillListForSession`) still renders with the same selection
  behavior (cap, ordering, no scoring/mode-filter) as before this task — only the rendered field
  set changed.
- `internal/service/ingest_test.go`'s stale placeholder comment/assertion is updated to reflect
  that `skill_get` is now real.

## Work log

**Read before wiring, as instructed:** `internal/skill/resolver.go`, `compose.go`, `exec.go`,
`gate.go` in full, plus `docs/engineering/architecture/20-skills.md`, `EXECUTION-PROCESS.md`,
`TASKS/skills/README.md`, and `docs/engineering/GLOSSARY.md` (confirmed no `skill_get` glossary
entry exists and none is needed — the glossary catalogs architectural concepts like "Skill
Resolver"/"Skill Materializer"/"Skill capability gate", not individual self-tool names; `skill_list`/
`skill_delete` aren't glossary entries either). Confirmed via grep that the only prior `skill_get`
hit repo-wide (outside `TASKS/skills/`) was the flagged `internal/service/ingest_test.go` test
placeholder — no real prior implementation, no naming collision. (Two unrelated near-misses exist
and are correctly out of scope: `mux_skill_get`, a distinct cross-app Mux-namespaced tool in
`internal/agent/builtin/profiles/system-architect.md`; and a `.nanite/agents/agent-builder.md`
reference, which is this *assistant's own* developer-persona boot config, a namespace the
glossary's own "Agent" entry explicitly documents as unrelated to Nanite's runtime tool registry.
Neither touched.)

**Pipeline ordering finding (load-bearing, not obvious from the task brief alone):**
`internal/skill.MaterializeSkill` (task 07) does **not** itself call `ResolveInlineMarkers`
(task 08) — confirmed directly by reading `compose.go`'s `materializeOne`, which only resolves
parameters, substitutes `{{name}}` placeholders, and splices/delegates dependencies. Marker
execution is a separate step this task's own caller must invoke afterward, against the *final
composed* output — `exec.go`'s own doc comment anticipates exactly this ("a future end-to-end
Materializer may call this after task 07's compose.go has already spliced inline dependencies
in"). `callSkillGet` therefore calls `MaterializeSkill` then `ResolveInlineMarkers` as two
sequential steps, not one combined call.

**1. Tool definition** (`internal/selftools/self_tools.go`): added `skill_get`'s `mcp.Tool` entry
— `slug` (required), `params` (optional object, static invocation args), `fork_role` (optional,
no default — see design-latitude note below), `fork_timeout_seconds` (optional). Registered in
`selfToolDefinitions()` right after `skill_delete`.

**2. Dispatch case** (`internal/selftools/self_tools_transport.go` + new
`self_tools_skill_get.go`): added `case "skill_get": return st.callSkillGet(ctx, args)` and a new
`SkillVendor *skillvendor.Store` field on `SelfToolsTransport` (nil-safe, wired from
`cmd/nanite/main.go`'s `selfTools.SkillVendor = container.SkillVendor`, alongside the existing
`selfTools.Subagent` line). `callSkillGet` assembles the full pipeline in order:
  - Resolves `slug` (required arg) and `agentID` (`mcp.CallerProfileFromContext(ctx)` —
    ctx-authoritative, no arg-based override, matching `procedure_get`'s identical precedent:
    "a forged/guessed agent ID must not be able to read a skill granted to a different agent").
  - Looks up the skill by slug (`GetSkillBySlug`); rejects not-found / disabled / never-vendored
    (`ContentHash == ""`) with a specific message for each.
  - **Runs an unconditional top-level grant check** (`skill.NewGate(st.Store,
    st.Store).Authorize(ctx, slug, agentID)`) *before* any resolution/materialization — this is
    load-bearing independent of marker execution: a skill with no `` !`cmd` `` markers or
    scripts/ (plain instructional content, almost certainly the common case) never reaches the
    Gate any other way, since `ResolveInlineMarkers` is a documented no-op on a marker-free body.
    Without this explicit check, an ungranted or stale-approval agent would receive full skill
    content with zero enforcement for every marker-free skill — exactly the "unauthorized-but-
    succeeding content" failure mode the task file calls out as the specific thing this batch's
    reviewers have been finding real gaps in.
  - Loads the root `skill.Definition` live from the vendored store (`loadRootSkillDefinition` —
    mirrors `compose.go`'s own unexported `loadDependencyDefinition`, applied to the root instead
    of a nested dependency, since `MaterializeSkill`'s entry point takes an already-loaded
    `Definition` rather than an address).
  - Calls `skill.MaterializeSkill` (Resolver + inline/fork composition), then
    `skill.ResolveInlineMarkers` against the final composed content (Gate-routed marker
    execution), attributed to the *root* skill's slug for every marker found — including one that
    originated inside a spliced-in inline dependency's own body. Documented in code as a
    deliberate composition-model property, not a task-11-specific narrowing: once inline-spliced,
    nested content is indistinguishable from the parent's own body, and a package wanting its own
    markers under its own, separately-granted identity uses `fork` instead (a real, separate trust
    boundary via `subagent.SpawnRequest`).
  - Fork-composition materialization failures get a distinguished message
    (`skillGetMaterializeErrorResult`): a `*skill.ForkPendingApprovalError` anywhere in the error
    chain (the common, by-design outcome under `SubagentApprovalRequired=true`, per `compose.go`'s
    own doc comment — not a rare edge case) surfaces the pending `run_id`/`envelope_instance_id`/
    `status` so the caller can poll `subagent_status` and retry, rather than an opaque
    "materialization failed."

**3. `internal/skill/gate.go` — one small, minimal extension, not listed in the task's own
"Touches" but required to avoid duplicating trust-check logic.** The task instructs "confirm the
invoking agent has a valid grant (task 09's same trust-validity check)" for a **content-only**
fetch that may never reach `ExecuteGated` at all (see above). `Gate.authorize` (the actual
decision logic) is an unexported method scoped to an `ExecRequest`; duplicating its two-branch
grant/hash-mismatch check as a second, hand-rolled copy in `internal/selftools` would have created
exactly the "same decision made twice, in two places" pattern this project's own review criteria
flags as a regression risk (`EXECUTION-PROCESS.md`'s "hardcoded value duplicated across multiple
call sites instead of one typed source of truth"). Instead, added `Gate.Authorize(ctx, skillSlug,
agentID) (Capabilities, error)` — a thin, exported wrapper that builds a minimal `ExecRequest`
(only `SkillSlug`/`AgentID`, the only two fields `authorize` reads) and calls the same unexported
`authorize` + `logDecision`, then rewired `ExecuteGated` to call `Authorize` internally instead of
inlining the same two lines. Verified `authorize`'s body reads only `req.SkillSlug`/`req.AgentID`
(confirmed by direct read) before making this change, so `ExecuteGated`'s external behavior —
including every existing `gate_test.go` assertion — is byte-for-byte unchanged; ran the full
`internal/skill` suite after the change to confirm (`go test ./internal/skill/...` — all pass,
unmodified). This is the *only* file outside this task's own listed "Touches" that was edited.

**4. `buildSkillListForSession` (item 3) — confirmed already fully handled by task `02`, no
further change needed.** Read `internal/chat/context.go:230-308` directly: task `02`'s own landed
commit already dropped the `ToolBindings` rendering (its comment there cites `TASKS/skills/02`
by name and confirms `sk.Prompt` was never rendered either). No `store.Skill` field this task's
column set removed is still referenced. Per the task's own scope fence ("do NOT add scoring,
mode-filtering, or any selection logic here") and the README's explicit instruction that this
function's selection behavior (cap, ordering) must stay byte-identical, deliberately did **not**
add the skill's `slug` to the essentials render (which would make an already-visible skill
callable via `skill_get` without a `skill_list` round-trip) — that's a real, plausible usability
improvement, but it's a field-set expansion beyond "match task 02's dropped columns," and the
task's own instruction was narrowly literal here ("This task only needs to update what fields get
rendered... so likely no change needed there at all"). Flagging it here as a candidate follow-up
rather than making the call unilaterally.

**5. `internal/service/ingest_test.go`'s stale placeholder (item 4).** Confirmed the test
(`TestAutoIngestAgents_UnknownToolNameIsLoud`) asserts exactly what the task predicted: "unknown
tool references get logged," using `skill_get` as one of two illustrative nonexistent names
(`bash_run`, `skill_get`) against a `knownTools` allowlist that only contains `dev_read`/
`dev_bash`. Swapped `skill_get` → `skill_frobnicate` (an obviously fabricated verb, not in
`docs/tool-naming-convention.md`'s verb table, so it stays reliably nonexistent) in both the
`Tools` slice and the log-content assertion. Ran `gofmt -w` on this file afterward — the longer
replacement string shifted the struct literal's trailing-comment alignment column.

**6. Design-latitude note, flagged rather than resolved silently (per the task's own explicit
instruction): `ForkRole` has no invented default.** `compose.go`'s own doc comment states "the
caller ... is the only party that knows what role should execute the delegated skill," and no
task in this batch defines a standing default fork role anywhere. Resolution: exposed `fork_role`
as `skill_get`'s own optional argument — the calling agent supplies it when it knows a
fork-composed dependency is in play; omitting it when a fork dependency is actually encountered
surfaces `compose.go`'s own clear, named error ("fork composition requires a ForkRole (no default
role is assumed)") rather than a silently-guessed value. Covered by
`TestCallSkillGet_ForkDependencyWithoutForkRole_ClearError`. Separately, `MaterializeInput.Workdir`
is left empty for every `skill_get` call — an API-direct invocation has no session boot dir or
project working directory to thread through the way `chat_boot_drive.go`'s boot-time resolution
does (per `20-skills.md`'s own "Delivery" section: API-direct agents have no boot dir at all);
this only matters for a `cmd`-kind `agent_context_resolvers` row that itself omits a CWD, which
falls back to this value. Neither of these is an open question needing an answer before this task
can be called done — both are documented, deliberate, narrow design calls within what the task
file itself flagged as genuinely open latitude, not a stop-and-escalate case.

**7. Regression tests** (`internal/selftools/self_tools_skill_get_test.go`, 9 tests) — explicitly
"reused/adapted rather than reinvented" from `internal/skill/gate_test.go`'s own fixture
conventions (`writeSkillFixture`/`installGateFixture`/`makeGateTestAgent`, duplicated locally
since they're unexported and this is a different package — the same cross-package duplication
precedent `gate_test.go` itself documents against `resolver_test.go`):
  - `TestCallSkillGet_GrantedSkill_ReturnsMaterializedContentWithMarkerExecuted` — a real,
    unmocked sandboxed marker execution (skipped, not failed, when `sandbox-exec`/`bwrap` is
    absent, matching `gate_test.go`'s own `requireSandboxTool` convention) proving parameter
    substitution *and* real Gate/sandbox marker execution both work end-to-end through the actual
    `CallTool` dispatch path.
  - `TestCallSkillGet_NoGrantRow_Refused`, `_BareAssignmentGrant_Refused`,
    `_HashMismatch_ReapprovalRequired` — directly mirror `gate_test.go`'s own three equivalent
    tests, against `skill_get`'s tool-result text instead of a raw `ExecuteGated` error.
  - `_NoCallerInContext_Refused`, `_UnknownSlug_ClearError`, `_DisabledSkill_Refused`,
    `_MissingRequiredParameter`, `_NilStoreOrVendor_ClearError`,
    `_ForkDependencyWithoutForkRole_ClearError` — edge cases specific to this self-tool's own
    guard clauses, not covered by `gate_test.go` (which only exercises the Gate itself, not a
    skill_get-shaped caller sitting in front of it).
  All 9 pass; ran individually via `go test ./internal/selftools/... -run TestCallSkillGet -v`.

**8. Golden example fixture** (not called out in the task's own "What to do," but required by an
existing, already-passing repo-wide invariant — `go test ./...` first failed on
`TestNaniteToolDescribe_AllSelfToolsHaveExamples: tool skill_get has no golden examples on file`).
Added `internal/selftools/examples/skill_get.json` (3 examples: plain fetch, static-param fetch,
fork-composed fetch) following the exact same house style as `procedure_get.json`/`skill_list.json`.

**9. Live dogfeed — a real, unmocked, API-direct chat turn against the real Anthropic provider**
(satisfying the task's Done-means literally, not the softer `/api/tools/call`-only bar a sibling
task in this batch used for a caller-identity-free tool):
  - Built the binary to an absolute scratch path
    (`/private/tmp/.../scratchpad/skill11-dogfeed/nanite`), never `./nanite` in the repo root.
  - Isolated **every** on-disk location the scratch server could touch, not just the DB: set
    `XDG_DATA_HOME`/`XDG_STATE_HOME`/`XDG_CACHE_HOME` env vars (confirmed via direct source read of
    `github.com/hollis-labs/go-apppaths@v0.1.0/paths/paths.go` that these are honored) so the
    `coordination/`/`worktrees/` state dirs `config.ResolveLayout()` unconditionally creates at
    boot — anchored on `StateDir`, independent of any `-db` override, per `main.go`'s own comment
    — never touched the real, shared `~/.local/state/nanite/...`/`~/.local/share/nanite/...` this
    machine's actual Cerberus-run `nanite-api-service` uses. This is a real, previously-undocumented
    footgun class beyond the two already logged in `TASKS/ESCALATIONS.md` (a relative `-db`/
    `source_ref` path) — the app-level relative config defaults (`data/artifacts`,
    `data/skills/vendor`, loaded from `config/nanite.yaml` relative to CWD) resolve independently
    of `-db` entirely, so ran every scratch invocation with CWD pinned to the scratch dir as the
    second half of the mitigation. Did *not* set `XDG_CONFIG_HOME` — the sandbox correctly flagged
    that as touching git's own config-resolution env var, and it wasn't needed for this app's own
    config loading anyway.
  - Installed a real skill package (`nanite skill install ./pkgsrc`, the actual product CLI, not a
    hand-rolled fixture) — one required parameter (`who`) plus a real, non-fenced
    `` !`echo hello-from-sandboxed-marker` `` inline marker — confirmed the vendored bytes and DB
    row landed under the scratch dir only (`find` before touching anything else).
  - Seeded one agent profile (`DefaultProvider: "anthropic"`, `DefaultModel: "claude-sonnet-4-5"`,
    `Tools: ["*"]` so the boot-time `BackfillAgentToolsFromLegacyColumns` grants it every tool
    including `skill_get`) and one real, approved `agent_known_skills` grant row
    (`ApprovedContentHash` = the just-installed skill's real content hash) via a throwaway,
    never-committed `_test.go` file in `internal/store` (deleted immediately after running once;
    confirmed via `git status --short` before and after that nothing was left behind) — there is
    no REST path for setting `approved_content_hash`/`capabilities_granted` yet (that's task `12`,
    not yet landed), so direct DB seeding via the real store API is the only way to construct an
    *approved* grant today, matching what an actual future task-`12` REST caller would eventually
    do to the same columns.
  - **Sourced the real, already-configured Anthropic credential from the local OS keychain** —
    the exact mechanism `cmd/nanite/main.go`'s `initProviders` already uses in production
    (`secrets.Get(secrets.ProviderKeyName("anthropic-001"))`), so the scratch server's own
    "anthropic" provider registration is byte-identical to a real deployment's, not a stub. Before
    relying on this, explicitly verified (via a separate, bounded-timeout, throwaway
    `internal/secrets` test, deleted immediately after) that an ad-hoc-built local binary reading
    this keychain item does **not** trigger a blocking macOS Keychain access-confirmation GUI
    prompt (which would have hung indefinitely in this non-interactive session) — confirmed
    instant, non-empty read, no prompt, before ever trusting the real scratch-server boot to it.
    Never printed, logged, or persisted the actual key value anywhere in any tool output.
  - Booted the scratch server (`nanite serve -port 8299 -db <scratch>/scratch.db -dev`), confirmed
    `GET /api/health` responded and boot logs showed `skill_get` auto-discovered as an MCP tool.
  - Drove one real turn via `nanite chat -agent <seeded-agent-id>`, message: "Please run the
    dogfeed check." **Real, unscripted model behavior — the agent decided on its own to call
    `skill_get(slug="dogfeed-test-skill", params={"who":"Dogfeed"})`.** Transcript (verbatim CLI
    output):
    ```
    > I'll run the dogfeed check by calling the skill_get tool with the dogfeed-test-skill.
    [tool] skill_get
    [tool ok] Hello Dogfeed! Marker output: hello-from-sandboxed-marker
    Perfect! The dogfeed check completed successfully. Here's the exact text returned:
    **Hello Dogfeed! Marker output: hello-from-sandboxed-marker**
    ```
    This confirms, in one real call: parameter substitution (`{{who}}` → `Dogfeed`), real gate
    authorization for a real approved grant, and real sandboxed marker execution — all three
    pipeline stages, end to end, through the real production dispatch path (`mcp.CallerProfileFromContext`
    stamped by the real chat-turn tool executor, not a hand-stamped test ctx).
  - **Verified against the actual persisted message/turn transcript**, not just the CLI's own
    stdout rendering, per the Done-means wording: queried the scratch DB directly —
    `messages.content` for the assistant row is a real, structured `{"v":1,...,"tool_calls":
    [{"id":"toolu_013fNmbV7eLL3wjAWVZsTDCC","name":"skill_get","status":"success"}],...}` JSON
    blob, with `text` quoting the exact materialized content back verbatim. This is a real
    Anthropic `tool_use` id (`toolu_...`), not a synthetic one.
  - Did not additionally live-dogfeed the denial paths — the task's own wording only requires the
    granted-success case be "verified against the actual message/turn transcript," and explicitly
    asks the denial paths be verified "the same way task 09's equivalent test does" (i.e. real,
    unmocked Go tests, which item 7 above already provides).
  - Killed the scratch server (confirmed port released), deleted the entire scratch directory, and
    ran `git status --short` in the worktree immediately after — clean, exactly the intended
    source-file changes, no stray write to any tracked file at any point during the whole exercise.

**10. Baseline checks**, run repeatedly through the session and finally once more at the end:
`go build ./cmd/nanite/`, `go build ./...`, `go vet ./...` (same single pre-existing, unrelated
`internal/service/container.go` `stopReaper`/`stopRuntimeReaper` finding this batch's other tasks
have already noted and left alone — confirmed via `git diff --stat -- internal/service/container.go`
that this task's own diff never touches that file, so the finding predates and is unrelated to
this work), and `go test ./...` (full suite, twice — once mid-session, once final — both clean).

**Deviations from the plan, summarized:** (a) added `Gate.Authorize` to `internal/skill/gate.go`,
one file outside the task's own "Touches" list, to avoid duplicating trust-check decision logic
across two call sites — a minimal, behavior-preserving extraction, not a design change; (b) added
a golden-example JSON fixture the task didn't explicitly call out, required by an existing
repo-wide test invariant; (c) did not add the skill's slug to `buildSkillListForSession`'s
essentials render despite it being a plausible usability improvement for this exact tool — flagged
as a candidate follow-up rather than a unilateral scope expansion. None of these change what the
task asked for.

## Review notes

**PASS.** Fresh reviewer with no shared context independently verified against `main` commit
`ba481a77`, not just against the Work Log's own claims.

- **Order of operations** traced directly in `self_tools_skill_get.go`: slug/arg validation →
  ctx-authoritative `agentID` (no arg override, matching `procedure_get`'s precedent) → catalog
  lookup (metadata-only errors, no body content) → unconditional `Gate.Authorize` → only then
  `loadRootSkillDefinition` reads vendored bytes → `MaterializeSkill` → `ResolveInlineMarkers`.
  Confirmed the grant check genuinely gates all content, including the marker-free-skill case
  that would otherwise never reach `ExecuteGated` at all.
- **`Gate.Authorize` extraction** in `internal/skill/gate.go` diffed directly against the
  pre-task version: confirmed byte-for-byte behavior-preserving (`ExecuteGated` now calls
  `Authorize` internally, which does exactly the same `authorize`+`logDecision` calls as before).
  `gate_test.go` untouched and all `TestExecuteGated_*` assertions still pass.
- **`fork_role`/`fork_timeout_seconds` privilege-escalation question** chased directly: confirmed
  `AgentProfileID` is sourced from the *calling* agent's own ctx-derived identity, consistent with
  the same established convention already used by `self_tools_workflow_run.go` and
  `self_tools_dispatch.go` (both real production callers of the identical trust-resolution
  mechanism) — not a deviation or new escalation path. One observation surfaced for awareness,
  not a task-11 bug: any agent with `skill_get` plus a grant on a `fork`-composed skill can
  trigger a real `subagent.Service.Spawn`, independent of whether `subagent_spawn` is separately
  on that agent's own tool allowlist — this is an inherited property of task 07's `compose.go`
  design, not something this task introduced. Logged in `TASKS/ESCALATIONS.md` as an observation
  for the batch's broader review, not a blocker.
- **Denial paths** — read `self_tools_skill_get_test.go`'s actual assertions directly: they check
  specific substrings matching `gate.go`'s typed `GrantRequiredError`/`ReapprovalRequiredError`
  text (no-grant, bare-assignment, hash-mismatch), not generic `err != nil`. Ran independently
  with `-count=1`: all pass.
- **`ingest_test.go` placeholder swap** confirmed consistent on both sides (Tools slice + log
  assertion) and `skill_frobnicate` confirmed via grep to not collide with any real tool name.
- **Golden example fixture** confirmed to satisfy the real repo-wide invariant
  (`TestNaniteToolDescribe_AllSelfToolsHaveExamples`), not just inert JSON.
- **Fork composition sourcing** confirmed real (`ParentSessionID`/`ParentAgentID` from the actual
  production tool-call path, not test-only), and the no-default-`ForkRole` design gap confirmed
  honestly flagged rather than silently guessed.
- **Build/vet/test**: `go build ./cmd/nanite/` clean; `go vet ./...` shows only the pre-existing,
  unrelated `internal/service/container.go` finding (confirmed untouched by this diff);
  `go test -count=1 ./internal/selftools/... ./internal/skill/... ./internal/service/...` all pass.
- **Wiring reachability** confirmed real: `cmd/nanite/main.go`'s `SkillVendor` wiring connects to
  a genuinely live, non-nil store under normal boot (nil only on an actual storage-init failure,
  an existing documented degrade-not-crash pattern) — this is real, reachable production code.

No bugs found. Task closed.
