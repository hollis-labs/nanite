# Canonical slug path validation for agent-managed file writes

**Phase:** Wave 1 — Release-blocking trust boundaries (remediation guide §4)
**Status:** not-started
**Depends on:** none
**Touches:** `internal/agent/managed_files.go`, `internal/agent/source_class.go`,
`internal/agentvalidation/validation.go`, `internal/service/agent_config.go`,
`internal/service/managed_durable_configs.go`, `internal/api/agents.go`,
`internal/api/durable_agents.go`, plus new/extended `_test.go` files in each
of those packages. All in the primary repo (no sibling-repo work).

## Context

### Findings addressed

- **`GO-AGENT-001`** (high, security, confidence high) — agent `slug` is
  never validated for path-safety before being joined into a managed-config
  file path, on both create and update.
- **`GO-AGENT-002`** (high, testing, confidence high) — every function in
  `internal/agent/managed_files.go` and `internal/agent/source_class.go`,
  including `Classification.IsWritablePath` (the codebase's own
  trust-boundary gate), is at 0.0% test coverage. Confirmed independently in
  this task-authoring pass: `find internal/agent -iname "*managed_files*test*"
  -o -iname "*source_class*test*"` returns nothing — no test file exists for
  either. A fix for `GO-AGENT-001` currently has no safety net.

Both are traced in full in REPORT.md §8.8 (`internal/agent`
(+builtin, +override, +reflexes), `internal/agentvalidation`,
`internal/agentworkflow`).

### Root cause

The slug-format allow-list regex that gates the agent-builder wizard
(`slugRegexp = ^[a-z0-9]+(?:-[a-z0-9]+)*$`, `internal/builders/agent_builder.go:12`,
enforced via the `Validator` on the wizard's `slug` step at
`internal/builders/agent_builder.go:63-72`) was never back-ported to the
direct agent-CRUD HTTP handlers. Those handlers copy the caller-supplied
slug verbatim into a `store.AgentProfile`, and the shared validation
function that runs before every write (`agentvalidation.ValidateAgentConfig`,
`internal/agentvalidation/validation.go:37-167`) never references `Slug`
anywhere in its body (confirmed by a full read of the function in this
pass — it validates `Tools`, `Directories`, `Constraints`, `Tags`,
`ParentDispatchAllowlist`, `Status`, and `Source`, and nothing else). The
slug then reaches a plain `filepath.Join` with no character allow-list and
no traversal rejection.

### Current behavior — full traced chain

**Agent-profile chain (`GO-AGENT-001`'s original evidence):**

1. `POST /api/agents` → `handleCreateAgent` (`internal/api/agents.go:61-172`)
   copies `req.Slug` verbatim into `agent.Slug` (line 90) after only an
   empty-string check shared with `Name`/`SystemPrompt` (line 67).
2. `agentvalidation.ValidateAgentConfig` runs (line 118) and never inspects
   `Slug`.
3. `a.Services.AgentConfig.Create(agent, nil)` (line 131) →
   `AgentConfigService.Create` (`internal/service/agent_config.go:134-151`)
   calls `agent.ManagedAgentPath(s.managedRoot, profile.Slug)` (line 141),
   which does a plain `filepath.Join(configRoot, "agents", slug+".md")`
   (`internal/agent/managed_files.go:87-95`) — no allow-list, no traversal
   rejection. `Create` does stat the resulting path first (line 145,
   `os.Stat` → `ErrManagedSlugExists` if it already exists) but that guard
   only blocks landing on an existing file inside the intended `agents/`
   directory — it does nothing to stop a slug like `../../.ssh/foo` from
   resolving *outside* `managedRoot` to a path that doesn't happen to exist
   yet.
4. `writeManaged` → `agent.WriteManagedAgentProfile` →
   `atomicWriteFile` (`internal/agent/managed_files.go:97-138, 205-233`)
   writes fully attacker-controlled YAML+markdown content to the resolved
   path.

5. `PUT/PATCH /api/agents/{id}` → `handleUpdateAgent`
   (`internal/api/agents.go:205-...`) sets `existing.Slug = *req.Slug`
   verbatim (lines 240-241) with **no check at all**, not even non-empty.
   That flows into `AgentConfigService.Update`
   (`internal/service/agent_config.go:158-203`), which has **two** distinct
   unguarded path-join branches, both worse than `Create`:
   - **DB-only-materialize branch** (`existing.SourceRef == ""`, line
     175-180): calls the same `agent.ManagedAgentPath(s.managedRoot,
     updated.Slug)` as `Create` — but, verified directly in this pass,
     **unlike `Create` this branch has no `os.Stat`/`ErrManagedSlugExists`
     guard at all** before falling into `writeManaged`. A crafted slug that
     collides with another agent's existing managed file silently overwrites
     it.
   - **Rename branch** (`existing.SourceRef != ""`, line 181-197): this is
     the specific site REPORT.md's `GO-AGENT-001` calls out as "more severe
     than Create." It bypasses `ManagedAgentPath` entirely and does its own
     raw join: `dest = filepath.Join(filepath.Dir(existing.SourceRef),
     updated.Slug+".md")` (line 193) — same unvalidated `updated.Slug`,
     and confirmed by reading the full function that **no exists-check on
     `dest` exists anywhere** before `writeManaged` unconditionally
     overwrites it (the only revision check present, lines 184-192, guards
     `existing.SourceRef`, i.e. the file being renamed *from*, not the
     target being renamed/overwritten *to*).
6. `Delete` (`internal/service/agent_config.go:207-224`) does **not**
   re-derive a path from a caller-supplied slug — it removes
   `profile.SourceRef`, the path already persisted from a prior Create/
   Update. Its exposure is entirely inherited, not independent: once
   Create/Update reject unsafe slugs before ever constructing a path,
   `SourceRef` can never contain an unsafe value and `Delete` needs no
   separate fix. (This directly answers this task's "check whether
   Delete/other agent-config operations share the same risk" instruction —
   verified by reading `internal/service/agent_config.go` in full.)
7. `AgentConfigService.uniqueManagedSlug` (line 309-330), used only by
   `CopyToManaged`, also calls `ManagedAgentPath` (line 314) — lower risk
   (input is `source.Slug + "-copy"`/`-N`, from an already-persisted
   profile, and its result is only used to pick a de-duplicated slug before
   delegating to `Create`, which independently re-validates existence) but
   it inherits whatever gap let `source.Slug` in in the first place, and
   should get the same fix as everything else for consistency.

The one mitigating factor already true today: the final path component is
always `.md`-suffixed, which constrains (does not eliminate) real-world
impact to `*.md` files the process's OS user can write.

### Scope note — this task's real scope is bigger than `GO-AGENT-001`/`GO-AGENT-002`'s stated evidence

`GO-AGENT-001`'s findings.json evidence list cites only
`internal/agent/managed_files.go`, `internal/agentvalidation/validation.go`,
`internal/service/agent_config.go`, and `internal/api/agents.go` — the
agent-**profile** write path. Verifying "all production callers" per this
task's own instruction (and per the remediation guide's §3 "Fix every
sibling path" standard: *"A security/correctness fix is incomplete until
every production caller of the affected semantic boundary has been
checked"*) surfaced a **second, fully parallel write path with the
identical defect shape**, not mentioned anywhere in `GO-AGENT-001`/
`GO-AGENT-002`'s evidence: the **durable-agent config** path.

- `internal/service/managed_durable_configs.go:82-90` —
  `ManagedDurableAgentPath(configRoot, slug)` does the exact same
  unguarded `filepath.Join(configRoot, "durable-agents", slug+".yaml")`
  as `agent.ManagedAgentPath`.
- `internal/service/managed_durable_configs.go:92-104` —
  `WriteManagedDurableAgentConfig` writes unconditionally via a plain
  `os.WriteFile` (not even the atomic-write helper `managed_files.go`
  uses for the profile path), with no exists-check.
- `internal/service/managed_durable_configs.go:407-421` —
  `SaveManagedDurableAgentConfig` calls both of the above with zero slug
  validation of any kind. There is no equivalent of
  `agentvalidation.ValidateAgentConfig` for `ManagedDurableAgentConfig` at
  all — durable-agent slugs get **no** validation, not even the
  incomplete kind agent profiles get.
- Reachable from `POST /api/durable-agents` →
  `handleCreateDurableAgent` (`internal/api/durable_agents.go:20-46`,
  slug from `req.Slug` at line 29) and from
  `PATCH/PUT /api/durable-agents/{id}` → `handleUpdateDurableAgent`
  (`internal/api/durable_agents.go:121-158`, slug **rename-settable** via
  `req.Slug` at lines 139-141 on an *existing* instance) — both funnel
  through `saveManagedDurableInstance`
  (`internal/api/durable_agents.go:48-81`) into
  `service.SaveManagedDurableAgentConfig`. `handleUpdateDurableAgent`'s
  rename shape is the exact overwrite risk `GO-AGENT-001` flags for
  `AgentConfigService.Update`'s rename branch, in a sibling function this
  audit never traced.
- A third exported function with the same shape,
  `internal/service/managed_durable_configs.go:384-405`
  (`SaveManagedAgentProfile` — note: distinct from the agent-profile
  `AgentConfigService`, an older-looking parallel implementation), was
  checked for production callers and has **none** (grep across all
  non-test `.go` files found zero call sites; only comments in
  `internal/service/ingest.go` and old `TASKS/phase-1/08-*.md` docs
  mention it by name). It is not part of the live risk chain today. Do not
  spend this task's budget hardening it — either fix it in the same pass
  if cheap (same primitive, same call shape as the two live functions), or
  leave it for whatever task eventually resolves `GO-AGENT-003`-style dead
  code; do not let it block the live fixes.

This expansion is explicitly in scope, not opportunistic addition: the
desired invariant below ("no identifier that becomes a filesystem path...
**or any similar identifier elsewhere**") already covers it, and it is the
same two existing primitives (regex + `pathsafe.ResolveUnder`) applied to
more call sites, not a new mechanism. If a future planner disagrees and
wants the durable-agent side split into its own task, that's a legitimate
re-scoping call — but do not silently drop it; it was found via direct
verification against current `HEAD`, not inferred.

### Desired invariant

No identifier that becomes a filesystem path (agent slug, durable-agent
slug, or any similar identifier elsewhere) reaches a path-join without
passing through one canonical, tested validation contract.

### Cross-reference — do not duplicate `GO-SEC-003`'s full site list here

The base report's `GO-SEC-003` (68 gosec G304 hits repo-wide, medium
severity, `requires_architect_decision: true`) explicitly names
`internal/agent/managed_files.go` and `internal/agent/managed_section.go`
as sites this task's fix should close (findings.json:
`"internal/agent/managed_* sites are the highest-priority subset (see
GO-AGENT-001 for a fully-traced example)"`). This task closes a meaningful
subset of `GO-SEC-003`'s site list — the write-side sites this trace
covers — but **not** all of it: `managed_section.go` and the remaining
non-`internal/agent` sites (`cmd/nanite/mcp_cmd.go`,
`cmd/nanite/plugin_cmd.go`, `cmd/nanite/plugin_logs.go`,
`cmd/nanite/serve_autostart.go`) are handled by a separate task at
`08-remaining-security-hardening/03-triage-remaining-gosec-g304-sites.md`.
Do not re-triage the full 68-site list here.

### Open question — pre-implementation verification step, not an architect decision

`requires_architect_decision: false` for this task (the guide frames this
as a clear-direction fix: apply an existing regex and an existing
confinement primitive to call sites that don't yet use them). There is one
real open question the audit flagged but did not resolve: **what auth
boundary currently gates `POST`/`PUT`/`PATCH` on `/api/agents` and
`/api/durable-agents`** — REPORT.md §8.8 states this was "not
independently verified (out of this cluster's scope) — reachable
population unknown." This is a **pre-implementation verification step**
for whoever picks up this task (read `internal/server`'s route
registration and whatever auth middleware wraps the agent-CRUD routes,
per `GO-RUNTIME-002`'s finding that auth is opt-in via env vars with no
loopback-only default), not a design decision to escalate — it changes how
urgently this ships, not what the fix looks like.

### Non-goals

Do not build a generic path-safety framework beyond applying the existing
`internal/pathsafe.ResolveUnder` (already adversarially tested per
REPORT.md §8.12 — dotdot-mid-path, dotdot-that-lands-back-inside,
symlink-escape, symlink-escape-subpath, null bytes) and the existing slug
regex (`internal/builders/agent_builder.go:12`) — both primitives already
exist and are proven; this task wires them into the call sites enumerated
above, it does not design new ones. Do not touch the agent-builder
wizard's own validator (`internal/builders/agent_builder.go:63-72`) — it
is already correct. Do not attempt to fix the rest of `GO-SEC-003`'s
68-site list (see cross-reference above). Do not fix `GO-AGENT-003`'s dead
code (`EnsureManagedDirs`, `UserManagedAgentPath`,
`ValidationResult.Error`) as part of this task.

## What to do

### Scope / files

- `internal/agent/managed_files.go` — add slug-format validation at the
  top of `ManagedAgentPath` (or a shared helper it calls), and switch its
  `filepath.Join` to route through `pathsafe.ResolveUnder` against the
  `agents/` subdirectory of `configRoot` as defense in depth.
- `internal/service/managed_durable_configs.go` — apply the identical
  fix to `ManagedDurableAgentPath` (same file, same shape). Consider
  factoring the slug validator into one shared place both `internal/agent`
  and `internal/service` can call (`internal/service` already imports
  `internal/agent`, so a shared `agent.ValidateSlug(slug string) error`
  exported helper, reusing the existing regex, is a reasonable shape —
  treat the exact placement as adjustable/provisional; log the actual call
  chosen in the Work Log rather than treating this paragraph as locked).
- `internal/agentvalidation/validation.go` — add a `Slug` check to
  `ValidateAgentConfig` using the same validator, so the HTTP layer gets a
  clean `400 validation_failed` response instead of a filesystem-level
  error, and so the check happens as early as possible in the
  agent-profile path.
- `internal/service/managed_durable_configs.go` (or
  `internal/api/durable_agents.go`'s `saveManagedDurableInstance`) — add
  an equivalent early slug check for `ManagedDurableAgentConfig`/
  `DurableAgentInstance`, since no validation function exists for that
  type today.
- `AgentConfigService.Create`/`Update` (`internal/service/agent_config.go`)
  — confirm the primitive-layer check in `ManagedAgentPath` alone is
  sufficient backstop for both branches (DB-only-materialize at line
  175-180, and the rename branch at line 181-197 which bypasses
  `ManagedAgentPath` entirely — this one specifically needs its own call
  into the shared validator/`pathsafe.ResolveUnder`, not just a fix inside
  `ManagedAgentPath`, since it never calls that function).
- `internal/agent/source_class.go` — no code change expected
  (`Classification.IsWritablePath` is already correct — see REPORT.md
  §8.8 and the direct read in this pass, `internal/agent/source_class.go:
  110-129`, which does a strict `dir == root` equality check, not a prefix
  match); this file's `GO-AGENT-002` gap is purely a testing gap, addressed
  below.

### All production callers (mandatory enumeration — see Current behavior above for full detail)

| Caller | File:line | Guard before this task | Fix needed |
|---|---|---|---|
| `handleCreateAgent` → `AgentConfigService.Create` | `internal/api/agents.go:90`, `internal/service/agent_config.go:141` | `os.Stat` exists-check only (line 145) | slug format validation before path-join |
| `handleUpdateAgent` → `AgentConfigService.Update`, DB-only-materialize branch | `internal/api/agents.go:240-241`, `internal/service/agent_config.go:176` | none | slug format validation + exists-check |
| `handleUpdateAgent` → `AgentConfigService.Update`, rename branch | `internal/api/agents.go:240-241`, `internal/service/agent_config.go:193` | none | slug format validation + confinement (bypasses `ManagedAgentPath`, needs its own call) |
| `AgentConfigService.Delete` | `internal/service/agent_config.go:207-224` | n/a — operates on persisted `SourceRef`, not a caller-supplied slug | none needed once Create/Update are fixed (verified, see Current behavior §6) |
| `AgentConfigService.uniqueManagedSlug` (via `CopyToManaged`) | `internal/service/agent_config.go:314` | delegates existence check to `Create` | inherits the `Create` fix; no independent change required beyond consistency |
| `handleCreateDurableAgent` → `SaveManagedDurableAgentConfig` | `internal/api/durable_agents.go:29`, `internal/service/managed_durable_configs.go:411` | none | slug format validation before path-join |
| `handleUpdateDurableAgent` → `SaveManagedDurableAgentConfig` (rename-shaped) | `internal/api/durable_agents.go:139-141`, `internal/service/managed_durable_configs.go:411` | none | slug format validation before path-join |
| `SaveManagedAgentProfile` (`internal/service/managed_durable_configs.go:384-405`) | — | none | zero production callers today (verified); fix opportunistically if cheap, do not block on it |

### Proposed direction

1. Add one exported, tested slug-format validator (reusing
   `internal/builders/agent_builder.go`'s regex verbatim or by reference —
   do not silently redefine a second regex with subtly different rules).
2. Call it inside `ManagedAgentPath` and `ManagedDurableAgentPath`
   themselves, before either function does its `filepath.Join` — this is
   the "one canonical contract" every enumerated caller already routes
   through, so fixing it there closes every caller in the table above in
   one place, except the `Update` rename branch which bypasses
   `ManagedAgentPath` and needs its own explicit call to the same
   validator (and to `pathsafe.ResolveUnder` for confinement) right before
   its `filepath.Join` at `internal/service/agent_config.go:193`.
3. Also call the validator early in `ValidateAgentConfig`
   (agent-profile path) and in `saveManagedDurableInstance` or
   `SaveManagedDurableAgentConfig` (durable-agent path) so callers get a
   clean `400` instead of discovering the rejection at the filesystem
   layer — this is UX/early-rejection, not a second contract; it calls the
   same validator.
4. Layer `pathsafe.ResolveUnder(agentsDir, slug+".md")` in place of the
   raw `filepath.Join` in `ManagedAgentPath`/`ManagedDurableAgentPath` (and
   in the `Update` rename branch) as defense in depth per the guide's
   explicit instruction to "retain path confinement as defense in depth" —
   format validation and confinement are complementary, not redundant:
   confinement also catches anything the regex might miss and gives a
   uniform `*EscapeError` shape to log/test against.

### Tests required

- Unit tests on `AgentConfigService.Create`/`Update` asserting a slug
  containing `/`, `..`, or a `%2e%2e`-style sequence is rejected **before
  any filesystem call** — add alongside the existing
  `TestAgentConfig_SlugRenamePreservesIdentityAndChildren`
  (`internal/service/agent_config_test.go:116`) so the new traversal test
  follows the same setup pattern (temp managed root, real `Create`/
  `Update` calls, no mocks). Cover both the DB-only-materialize branch and
  the rename branch explicitly — they are separate code paths with
  separate gaps.
- Equivalent test(s) for `saveManagedDurableInstance`/
  `SaveManagedDurableAgentConfig` in `internal/service/
  managed_durable_configs_test.go`, covering both create and the
  rename-shaped update.
- Direct unit tests for `ManagedAgentPath`/`WriteManagedAgentProfile` and
  `ManagedDurableAgentPath`/`WriteManagedDurableAgentConfig` (currently
  0.0% coverage per `GO-AGENT-002` — new `internal/agent/
  managed_files_test.go` and extended
  `internal/service/managed_durable_configs_test.go`), including a
  traversal-rejection case now that `GO-AGENT-001` is fixed, per
  `GO-AGENT-002`'s own recommendation.
- Direct unit tests for `Classification.Classify`/`IsWritablePath`
  (currently 0.0% coverage — new `internal/agent/source_class_test.go`),
  including a case confirming a path outside every configured root is
  correctly excluded (exercise the `dir == root` strict-equality logic at
  `internal/agent/source_class.go:122-127` with a sibling-but-not-equal
  directory, e.g. `<root>-evil`, to confirm it is *not* misclassified as
  inside `root`).
- HTTP-level regression test hitting `handleUpdateAgent`/
  `handleUpdateDurableAgent` with a crafted rename slug, asserting a `400`
  and asserting (via `os.Stat` on the would-be target) that no file was
  written or overwritten outside the managed root.

### Prevention

The validator becomes the single call site every current and future
slug→path caller must route through; a `go vet`-visible or code-review
convention ("any new `filepath.Join` involving a request-derived field
must route through the shared validator or `pathsafe.ResolveUnder`") is
the cheap, durable version of the guide's Wave 7 "Trust-Boundary Paths"
standard (*"Values influenced by external callers, agents, plugins,
catalogs, or persisted untrusted state must not become filesystem paths
without canonical validation/confinement"*) — this task is the first real
instance of that standard actually being enforced in this codebase's
agent-config surface, not just documented.

### Verification

```bash
go build ./...
go test ./internal/agent/... ./internal/agentvalidation/... ./internal/service/... ./internal/api/... -run . -v
go test -race ./internal/agent/... ./internal/service/... ./internal/api/...
gosec ./internal/agent/... ./internal/service/...
```

PASS means: every new test above passes; the crafted-slug traversal/
overwrite tests fail (i.e. correctly reject) against the pre-fix code if
run against a checkout at the audited commit `8feeee5c` (a quick sanity
check, not a required CI step); `gosec`'s G304 hits on
`internal/agent/managed_files.go` and
`internal/service/managed_durable_configs.go` are either resolved or
explicitly annotated as safe-by-construction now that the join is
confined.

### Risk / rollback

Regression surface is narrow and well-bounded: legitimate slugs
(`^[a-z0-9]+(?:-[a-z0-9]+)*$`) are unaffected: the wizard already enforces
this exact pattern today, so no legitimate agent created through the
wizard can regress. The only behavior change for API/GUI callers is that
previously-silently-accepted malformed slugs (containing `/`, spaces,
uppercase, etc.) now get a `400` instead of either succeeding unsafely or
(for uppercase/spaces) most likely already failing downstream in
unpredictable ways. Rollback is a straightforward revert — no schema or
data migration involved; existing on-disk managed files are untouched
(their filenames were already `slug + ".md"`/`slug + ".yaml"` under a real
managed root, or they wouldn't have been discovered/ingested in the first
place).

## Done means

- [ ] A single, tested slug-format validator exists and is reused (not
      redefined) from `internal/builders/agent_builder.go`'s existing
      regex.
- [ ] `ManagedAgentPath`, `ManagedDurableAgentPath`, and the `Update`
      rename branch (`internal/service/agent_config.go:193`) all reject an
      unsafe slug before any `filepath.Join`/filesystem call, confirmed by
      a passing regression test for each.
- [ ] `ValidateAgentConfig` and the durable-agent equivalent both surface
      a clean `400 validation_failed` for a bad slug at the HTTP layer,
      confirmed by an HTTP-level test.
- [ ] `pathsafe.ResolveUnder` (or equivalent confinement) is layered as
      defense in depth on both path-join functions.
- [ ] `internal/agent/managed_files.go`, `internal/agent/source_class.go`,
      and `internal/service/managed_durable_configs.go` each have real
      unit test coverage (no longer 0.0%), including the traversal/
      exists-outside-root negative cases called out under Tests required.
- [ ] The pre-implementation auth-boundary verification step (which
      middleware, if any, actually gates `/api/agents` and
      `/api/durable-agents`) has been done and its result recorded in the
      Work Log below — not skipped, not escalated as an architect
      decision.
- [ ] `go build ./...`, `go vet ./...`, and the verification commands
      above all pass.

## Work log

<!-- Worker fills this in as it goes. -->

## Review notes

<!-- Reviewer fills this in. -->
