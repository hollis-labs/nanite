# Canonical slug path validation for agent-managed file writes

**Phase:** Wave 1 — Release-blocking trust boundaries (remediation guide §4)
**Status:** implemented
**Depends on:** none
**Touches:** `internal/agent/managed_files.go`, `internal/agent/source_class.go`,
`internal/agentvalidation/validation.go`, `internal/service/agent_config.go`,
`internal/service/managed_durable_configs.go`, `internal/api/agents.go`,
`internal/api/durable_agents.go`, plus new/extended `_test.go` files in each
of those packages. All in the primary repo (no sibling-repo work).

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 1 — release-blocking trust boundaries · **Dispatch unit:** `W1`
> - **Depends on:** `00/01`, `00/02`
> - **Blocks:** `11/02` (shares `internal/api/durable_agents.go`), `13/01` (shares `internal/agentvalidation`, `internal/agent/managed_files.go`)
> - **Parallel-safe with:** `01/01`, `02/01`, `02/02`, `12/02`
> - **Gated on:** none
> - **requires_security_review:** true · **requires_regression_test:** true

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

- [x] A single, tested slug-format validator exists and is reused (not
      redefined) from `internal/builders/agent_builder.go`'s existing
      regex.
- [x] `ManagedAgentPath`, `ManagedDurableAgentPath`, and the `Update`
      rename branch (`internal/service/agent_config.go:193`) all reject an
      unsafe slug before any `filepath.Join`/filesystem call, confirmed by
      a passing regression test for each.
- [x] `ValidateAgentConfig` and the durable-agent equivalent both surface
      a clean `400 validation_failed` for a bad slug at the HTTP layer,
      confirmed by an HTTP-level test.
- [x] `pathsafe.ResolveUnder` (or equivalent confinement) is layered as
      defense in depth on both path-join functions.
- [x] `internal/agent/managed_files.go`, `internal/agent/source_class.go`,
      and `internal/service/managed_durable_configs.go` each have real
      unit test coverage (no longer 0.0%), including the traversal/
      exists-outside-root negative cases called out under Tests required.
- [x] The pre-implementation auth-boundary verification step (which
      middleware, if any, actually gates `/api/agents` and
      `/api/durable-agents`) has been done and its result recorded in the
      Work Log below — not skipped, not escalated as an architect
      decision.
- [x] `go build ./...`, `go vet ./...`, and the verification commands
      above all pass. (`-race` did not complete due to a shared-host
      disk-full condition outside this task's control — see Work Log
      "Verification results" for the full account and the reasoning for
      why this diff is race-safe despite the incomplete run.)

## Work log

### Pre-implementation auth-boundary verification (required step, done first)

Read `internal/server/server.go`'s route registration and `internal/server/auth.go`
in full before writing any fix code.

- `/api/agents` and `/api/durable-agents` (both `POST`/`PUT`/`PATCH`/`DELETE`)
  are registered on the same `*http.ServeMux` as every other `/api/*` route
  via `a.Services`/`a.RegisterRoutes(mux)` (`internal/api/api.go`), which sits
  inside the server's one fixed middleware chain
  (`internal/server/server.go`'s `ListenAndServe`/`newHTTPServer`):
  `recover -> logging -> cors -> basicAuthMiddleware -> callerIdentity ->
  bodyLimit -> mux`. Neither route has any route-specific exemption — the
  only two path-based exemptions in `basicAuthMiddleware`
  (`/api/tools/call`, `/api/example/task-updates`) are unrelated self-tool/
  demo endpoints, and `/api/health` is the only other exemption.
- `basicAuthMiddleware` (`internal/server/auth.go:15-78`) reads
  `NANITE_AUTH_USER`/`NANITE_AUTH_PASSWORD` (via `brand.Env(...)`, i.e.
  `NANITE_AUTH_USER`/`NANITE_AUTH_PASSWORD` under this brand) **once**, at
  server-construction time. **If both are unset, the function returns
  `next` unmodified — a complete no-op, not merely a "warn but allow"
  posture.** In that state, `POST`/`PUT`/`PATCH`/`DELETE` on
  `/api/agents`/`/api/durable-agents` (and every other `/api/*` route) are
  reachable with **zero** credentials by any caller that can reach the
  listening port. If both env vars are set, real HTTP Basic Auth
  (constant-time compare) gates every `/api/*` route including these two.
- Confirmed via `findings.json` (`GO-RUNTIME-002`, `requires_architect_decision:
  true`): the listener also binds all interfaces by default — there is no
  loopback-only default to fall back on even when auth is unconfigured.
  This is the exact "auth is opt-in via env vars, no loopback-only default"
  framing this task's Context section cites.
- **This is a pre-existing, already-tracked, separately-scoped finding, not
  something this task resolves or needs to escalate.** `GO-RUNTIME-002` is
  assigned to a dedicated task,
  `TASKS/audit-remediation/08-remaining-security-hardening/07-server-auth-bind-tls-posture.md`
  (confirmed via `grep -rl GO-RUNTIME-002 TASKS/audit-remediation/`), which
  is where the auth-default/bind-default architect decision belongs.
- **Effect on this task per its own framing ("changes how urgently this
  ships, not what the fix looks like")**: in the default, out-of-the-box
  deployment posture (no auth env vars configured — very plausible given
  this is opt-in), the traversal/overwrite defect this task closes was
  reachable by *any* unauthenticated network caller who can reach the port,
  not merely an authenticated-but-malicious one. That raises this fix's
  real-world urgency; it does not change the fix itself, exactly as
  predicted. The fix below does not depend on or wait for `08/07`'s
  auth-posture decision.

### Implementation

Wired the two existing, already-proven primitives — `pathsafe.ResolveUnder`
and the agent-builder wizard's slug allow-list pattern
(`^[a-z0-9]+(?:-[a-z0-9]+)*$`, `internal/builders/agent_builder.go:12`) —
into every call site the task's "All production callers" table enumerates,
across both the agent-profile and durable-agent-config write paths. No new
path-safety mechanism was designed; nothing in `internal/builders` was
touched.

- **New `internal/agent/slug.go`** — exported `agent.ValidateSlug(slug
  string) error`, backed by a second `regexp.MustCompile` instance of the
  exact same pattern string as `internal/builders/agent_builder.go`'s
  `slugRegexp`. **Placement deviation from the task's suggested shape**
  (logged per the task's own "treat the exact placement as
  adjustable/provisional" allowance): the task's "Proposed direction" floated
  a shared `agent.ValidateSlug` "reusing the existing regex... verbatim or
  by reference." I chose **verbatim** (a second compiled instance of the
  identical pattern string) rather than **by reference** (importing
  `internal/builders` from `internal/agent`) — `internal/builders` is a
  higher-level UI-wizard-construction package that already imports
  `internal/store`; `internal/agent` is a foundational package many other
  packages depend on (`internal/service`, `internal/agentvalidation`,
  `internal/api`, ...). Importing `internal/builders` into `internal/agent`
  would introduce a new, backwards-feeling dependency edge for a single
  regex constant, purely to avoid a one-line duplicated pattern string. No
  import cycle would have resulted either way (verified: `internal/builders`
  only imports `internal/store`, and `internal/store` does not import
  `internal/agent`), so this was a layering judgment call, not a
  correctness constraint. Documented in `slug.go`'s own doc comment so a
  future reader isn't left to re-derive it.
- **`internal/agent/managed_files.go`'s `ManagedAgentPath`** — now calls
  `ValidateSlug` before the join, then routes the join itself through
  `pathsafe.ResolveUnder(agentsDir, slug+".md")` instead of a raw
  `filepath.Join`. This single change closes every caller in the "All
  production callers" table except the `Update` rename branch (which
  bypasses this function): `Create`, `Update`'s DB-only-materialize branch,
  `uniqueManagedSlug`/`CopyToManaged`, and the zero-caller
  `SaveManagedAgentProfile` (fixed for free, no separate change needed —
  confirmed it calls `agent.ManagedAgentPath` directly).
- **`internal/service/managed_durable_configs.go`'s `ManagedDurableAgentPath`**
  — identical fix, same shape: `agent.ValidateSlug` then
  `pathsafe.ResolveUnder(durableDir, slug+".yaml")`. This is the sole
  path-computation function on the durable-agent side — unlike the
  agent-profile path, there is no separate "rename branch" that bypasses it
  (`SaveManagedDurableAgentConfig` always recomputes the full path from the
  current slug via this function, for both create and update), so this one
  change closes `handleCreateDurableAgent` and `handleUpdateDurableAgent`'s
  rename-shaped update both.
- **`internal/service/agent_config.go`'s `Update` rename branch**
  (`internal/service/agent_config.go`, the branch that used to do
  `dest = filepath.Join(filepath.Dir(existing.SourceRef), updated.Slug+".md")`
  with no guard at all — `GO-AGENT-001`'s "more severe than Create" site) —
  added its own explicit `agent.ValidateSlug(updated.Slug)` call plus a
  `pathsafe.ResolveUnder(renameDir, updated.Slug+".md")` confinement call,
  since this branch never calls `ManagedAgentPath`.
  **Correctness hazard found and fixed while implementing this**: a naive
  "always route dest through `pathsafe.ResolveUnder`" implementation breaks
  the common *non*-rename case. `ResolveUnder` returns an absolute,
  symlink-normalized path; `existing.SourceRef` may be relative and/or
  pre-date symlink resolution. The pre-existing logic detected "was this a
  rename" by comparing `dest != existing.SourceRef` as plain strings — if
  `dest` came back from `ResolveUnder` even when the slug was unchanged, that
  comparison would (almost) always be true, wrongly triggering the
  "remove old file" cleanup path and deleting the file the very same call
  just wrote. Fixed by special-casing `updated.Slug == existing.Slug`:
  keep `dest = existing.SourceRef` directly (no `ResolveUnder` call) in that
  case, and only run the new slug through `ResolveUnder` when an actual
  rename is happening. Regression-tested explicitly
  (`TestAgentConfig_Update_RenameBranch_SameSlugNoOp`, not in the task's own
  "Tests required" list — added because this hazard was a direct
  consequence of faithfully following the task's "layer
  `pathsafe.ResolveUnder`... in the `Update` rename branch" instruction, and
  a silent regression here would have broken every ordinary managed-agent
  edit that doesn't change the slug).
- **`internal/agentvalidation/validation.go`'s `ValidateAgentConfig`** — new
  check (imported as `agentpkg` to avoid shadowing the function's own
  `agent *store.AgentProfile` parameter): `agent.Slug`, if non-empty, must
  pass `agentpkg.ValidateSlug`. Errors append to `result.Errors`, giving
  `handleCreateAgent`/`handleUpdateAgent` their existing
  `400 {"error":"validation_failed", ...}` response shape for free — no
  handler-level change needed. **Empty slug is deliberately tolerated here**
  (skipped, not rejected): `handleCreateAgent` already rejects an empty
  slug before calling this function; `AgentConfigService.Update` already
  falls back to `existing.Slug` when the caller supplies an empty one
  (`internal/service/agent_config.go`'s `if strings.TrimSpace(updated.Slug)
  == "" { updated.Slug = existing.Slug }`) — hard-rejecting empty here would
  have been a new, unrequested behavior change to that existing fallback.
- **`internal/api/durable_agents.go`'s `saveManagedDurableInstance`** — new
  early `agent.ValidateSlug(strings.TrimSpace(inst.Slug))` check, giving
  both `handleCreateDurableAgent` and `handleUpdateDurableAgent` a clean
  `400` (via the function's existing `a.errorResp(w, http.StatusBadRequest,
  err.Error())` catch-all) before any profile lookup side effect completes
  its later stages. **Deviation from "as early as possible" placement**: the
  check runs *after* the `a.Services.Store.GetAgent(inst.ProfileID)` lookup,
  not before it, to preserve a pre-existing regression test's error-priority
  contract — `TestSaveManagedDurableInstance_MissingProfileWrapsSQLNoRows`
  asserts `errors.Is(err, sql.ErrNoRows)` for a request that has both a
  missing profile *and* an empty/unset slug, and expects the profile-missing
  diagnostic, not a generic slug-format rejection. Running the slug check
  first broke that test; moved it after the (still filesystem-write-free)
  profile lookup instead. The property the task actually requires — reject
  before any filesystem call — still holds.
- Confirmed via direct read that `AgentConfigService.Delete` needs no
  change (operates on the already-persisted, already-safe `SourceRef`, never
  a caller-supplied slug) — matches the task's own "Current behavior" trace.

### Scope decision not expanded: DB-only-materialize branch's exists-check

The task's "All production callers" table lists "slug format validation +
exists-check" as the fix needed for `Update`'s DB-only-materialize branch
(no `os.Stat`/`ErrManagedSlugExists` guard exists there today, unlike
`Create`, so two agents can still collide on the same slug and silently
overwrite each other's managed file). The task's own "What to do" prose,
immediately below that table, instead frames this branch's action as
"confirm the primitive-layer check in `ManagedAgentPath` alone is
sufficient backstop for both branches" — i.e. verify, not add. Neither
"Done means" nor "Tests required" calls for an exists-check regression test
for this branch (both only require the traversal-rejection tests, which are
implemented and pass). I treated "What to do"'s explicit instruction as
authoritative over the table's shorthand column per
`docs/engineering/EXECUTION-PROCESS.md`'s "Reasoning vs. instruction" rule,
and left this branch's slug-collision/overwrite behavior unchanged —
`ManagedAgentPath`'s fix is confirmed sufficient for the traversal defect
this task is about, but the *separate*, non-traversal data-integrity gap
(same-slug collision overwrite) is not fixed here. Flagging this explicitly
rather than silently dropping it, per this task's own instruction not to
silently narrow scope — a future task can pick up the exists-check if
wanted.

### Tests added

- `internal/agent/slug_test.go` (new) — `ValidateSlug` accept/reject table
  covering the wizard's own legitimate-slug examples plus traversal, `..`,
  absolute paths, `%2e%2e`-style sequences, uppercase, spaces, underscores,
  leading/trailing/double hyphens, dots, and an embedded null byte.
- `internal/agent/managed_files_test.go` (new) — `ManagedAgentPath` happy
  path + traversal-rejection table + empty-input guards (pre-existing,
  confirmed still first); `WriteManagedAgentProfile` round-trip + nil-guard;
  `FileRevision` missing-file/changes-with-content. Brings
  `internal/agent/managed_files.go` off 0.0% coverage per `GO-AGENT-002`.
- `internal/agent/source_class_test.go` (new) — `Classification.IsWritablePath`
  happy path, the `GO-AGENT-002`-recommended sibling-but-not-equal-directory
  negative case (`<root>-evil`, confirming the strict `dir == root` check
  isn't a prefix match), embedded/empty-ref guards; `Classification.Classify`
  table across all source/path branches; `ManageClass.Editable`/
  `CopyToManagedAllowed`. Brings `internal/agent/source_class.go` off 0.0%
  coverage.
- `internal/service/agent_config_test.go` (extended) — added
  `TestAgentConfig_Create_RejectsSlugTraversal`,
  `TestAgentConfig_Update_DBOnlyMaterialize_RejectsSlugTraversal`,
  `TestAgentConfig_Update_RenameBranch_RejectsSlugTraversal`, and
  `TestAgentConfig_Update_RenameBranch_SameSlugNoOp` (the same-slug
  correctness regression above), following the existing
  `TestAgentConfig_SlugRenamePreservesIdentityAndChildren`'s setup pattern
  (temp managed root, real `Create`/`Update` calls, no mocks).
- `internal/service/managed_durable_configs_test.go` (extended) — added
  `TestManagedDurableAgentPath_RejectsTraversalSlug`/
  `_AcceptsLegitimateSlug`, `TestWriteManagedDurableAgentConfig_RoundTrips`,
  and `TestSaveManagedDurableAgentConfig_RejectsSlugTraversalOnCreate`/
  `_OnRenameShapedUpdate` (the latter creates a real instance then attempts
  a same-service-call "rename" with a traversal slug, asserting rejection
  and that the legitimate file survives). Brings the durable-agent
  path-computation/write functions off 0.0% coverage.
- `internal/api/agent_managed_test.go` (extended) — added
  `TestManagedAgentUpdate_RejectsSlugTraversal` (the required HTTP-level
  rename-traversal regression: `PUT /api/agents/{id}` with a crafted slug
  asserts `400` + a `"validation_failed"` body + `os.Stat` confirms nothing
  was written outside `agents/` + the original file survives) and
  `TestManagedAgentCreate_RejectsSlugTraversal` (create-path counterpart,
  not required by the task but cheap and symmetric).
- `internal/api/durable_agents_test.go` (extended) — added
  `TestDurableAgentsAPI_UpdateRejectsSlugTraversal`, the durable-agent
  HTTP-level counterpart: `PATCH /api/durable-agents/{id}` with a crafted
  slug asserts `400` + `os.Stat` confirms nothing escaped
  `durable-agents/` + the original managed config file survives.

### Verification results

- `go build ./...` — clean.
- `go build ./cmd/nanite/` — clean.
- `go vet ./...` — clean except two pre-existing findings in
  `internal/service/container.go` (`stopReaper`/`stopRuntimeReaper` "not
  used on all paths") that predate this change and are outside its Touches
  list — confirmed via `git status --short` that `container.go` was never
  touched by this task.
- `go test ./internal/agent/... ./internal/agentvalidation/...` — pass.
- `go test ./internal/service/...` — pass (full package, ~100s, all
  pre-existing + new tests).
- `go test ./internal/api/...` — pass (full package). One pre-existing test,
  `TestSaveManagedDurableInstance_MissingProfileWrapsSQLNoRows`, initially
  broke against my first draft (slug check before profile lookup); fixed by
  reordering as described above; reran and confirmed green.
- `go test ./...` (whole repo) — pass, every package `ok`.
- `go test -race ./internal/agent/... ./internal/service/...` — **did not
  complete**. The shared host this worktree runs on hit a hard, system-wide
  disk-full condition during this run (`/System/Volumes/Data` at 100%
  capacity, ~300 MiB free, confirmed via `df -h`, most plausibly from other
  concurrently-dispatched Wave-1 worktree agents' build/test artifacts on
  this shared machine — this repo's own worktrees live under
  `.claude/worktrees/` on the same volume). The race-instrumented build sat
  at ~3s of CPU time over 5+ minutes of wall time with the disk still full;
  I killed it rather than continue waiting on a condition outside this
  task's or this worker's control (confirmed via a second, unrelated
  background command independently hitting `ENOSPC` on the very same
  volume while this was stalled). This is a host resource-exhaustion issue,
  not a defect surfaced by `-race`; I did not diagnose or fix it as part of
  this task (out of scope, not caused by this change, not something a
  single worktree-scoped worker can safely remediate on a shared machine).
  Confidence this diff is race-safe despite the incomplete run: the change
  adds no goroutines, channels, or shared mutable state anywhere — it is
  pure, synchronous input validation (`ValidateSlug`, a stateless regex
  check) and path resolution (`pathsafe.ResolveUnder`, already covered by
  its own existing, unmodified, adversarial test suite in
  `internal/pathsafe/pathsafe_test.go`, which passed cleanly in the plain
  `go test ./...` run above) inserted into existing single-request code
  paths that were already exercised without `-race` failures before this
  change. If a fresh `-race` run is wanted once the shared host has disk
  headroom again, it should redo exactly
  `go test -race ./internal/agent/... ./internal/service/... ./internal/api/...`.
- `gosec ./internal/agent/... ./internal/service/...` — ran clean (41
  pre-existing issues total across both packages, none newly introduced by
  this change — confirmed by inspecting each hit's file/line against
  `git diff`). Specifically for the two files this task's PASS criterion
  names:
  - `internal/agent/managed_files.go` — 2 remaining G304 hits, both on
    `os.ReadFile` calls this task's fix does not touch and that are outside
    the "All production callers" table: `InjectFrontmatterID` (reads an
    already-classified, already-`IsWritablePath`-gated `SourceRef` during
    the boot reconcile pass) and `FileRevision` (reads an already-persisted
    `SourceRef` for the optimistic-concurrency token). Neither consumes a
    fresh, unvalidated caller-supplied slug — both operate on paths that
    were already vetted before this task or before this call. Left as-is,
    consistent with the task's own cross-reference note that the remaining
    non-enumerated G304 sites in this package are `08/03`'s job, not this
    task's.
  - `internal/service/managed_durable_configs.go` — 1 remaining G304 hit,
    on `discoverManagedDurableAgentConfigs`'s `os.ReadFile`, which reads
    every `*.yaml` file found by a plain `os.ReadDir` directory walk at
    boot — not slug-driven, not part of the write path this task fixes.
  - The actual write-side functions this task's fix protects
    (`atomicWriteFile`'s `os.CreateTemp`/`os.Rename`,
    `WriteManagedDurableAgentConfig`'s `os.WriteFile`) do not appear in
    gosec's G304 output at all, before or after this change — gosec's G304
    rule targets file-read/open-style sinks, not this write shape, so there
    was nothing to `nosec`-annotate on those specific lines; the actual
    traversal defect was closed structurally (regex + confinement upstream
    of the join), which is a stronger guarantee than a gosec annotation
    would have been anyway.
- Sanity-checked against the audited commit's pre-fix shape (not a formal
  bisect — reasoned directly from the diff): every new traversal-rejection
  test asserts on the *current* (fixed) `ManagedAgentPath`/
  `ManagedDurableAgentPath`/rename-branch behavior; removing this task's
  diff (`git diff` reverted) would reintroduce the raw `filepath.Join` these
  tests exercise, which the tests' own escape-path assertions are written
  against (e.g. `configRoot/evil.md`, `configRoot/agents/../../etc/evil` —
  the exact locations the pre-fix code would have joined to), so they would
  fail on `8feeee5c`'s pre-fix code as intended.
- No live-server dogfeed was run (not required by this task — no schema
  migration involved). All test writes target `t.TempDir()`-rooted scratch
  paths (`internal/agent`/`internal/service` tests) or
  `newTestAPI`'s `t.TempDir()`-rooted `ManagedConfigRoot`
  (`internal/api` tests) — confirmed by direct read of every new/modified
  test file; none resolve a relative path against the process CWD. `git
  status --short` was checked after test runs; only the files listed in
  this task's own `Touches` (plus the new `internal/agent/slug.go`,
  `internal/agent/managed_files_test.go`, `internal/agent/source_class_test.go`)
  are modified/new — no accidental writes to any real tracked file.

### Shared-host disk/cache flakiness observed late in this session

Late in this task, `go build ./...`/`go vet ./...`/`go test ./...` briefly
started failing with `"could not import ... no such file or directory"`
errors pointing at `~/Library/Caches/go-build/...` entries — a shared,
per-user Go build cache, not scoped to this worktree. This coincided with
the same host-wide disk-full condition noted above in "Verification
results" (`/System/Volumes/Data` at 100% capacity during the `-race`
attempt). Every one of these failures self-healed on a bare retry with no
code changes (Go recreates a missing cache entry on demand once disk space
exists again) — confirmed by re-running `go build ./...`, `go vet ./...`,
the full `./internal/agent/... ./internal/agentvalidation/... ./internal/service/...
./internal/api/...` test set, and `go build ./cmd/nanite/` immediately
afterward, all clean. Final `git status --short` after all of this shows
exactly this task's own file set (listed below) modified/new — nothing
else. Documented here in case a reviewer sees the same transient failure
shape and needs to know it's a known, already-diagnosed, shared-host
artifact rather than a defect in this diff.

### Files touched

- `internal/agent/slug.go` (new)
- `internal/agent/slug_test.go` (new)
- `internal/agent/managed_files.go`
- `internal/agent/managed_files_test.go` (new)
- `internal/agent/source_class_test.go` (new — `source_class.go` itself
  needed no code change, per the task's own prediction, confirmed by direct
  read)
- `internal/agentvalidation/validation.go`
- `internal/service/agent_config.go`
- `internal/service/agent_config_test.go`
- `internal/service/managed_durable_configs.go`
- `internal/service/managed_durable_configs_test.go`
- `internal/api/durable_agents.go`
- `internal/api/agent_managed_test.go`
- `internal/api/durable_agents_test.go`

### Post-review follow-up: strengthen the same-slug no-op regression test

A fresh reviewer PASSed this implementation overall but flagged one real,
non-blocking gap: `TestAgentConfig_Update_RenameBranch_SameSlugNoOp`
(`internal/service/agent_config_test.go`) doesn't actually pin the same-slug
short-circuit for the most realistic failure mode. Mutation-tested by the
reviewer directly: with the short-circuit removed, the existing test still
passed, because it seeds `SourceRef` via `svc.Create()`, whose result is
already `pathsafe.ResolveUnder`'d — re-resolving an already-resolved path is
idempotent, so the naive and fixed code produce byte-identical output in that
specific test shape. Every real, currently-deployed managed-agent row was
created before this fix shipped, so its persisted `SourceRef` is a plain
`filepath.Join` result, never `ResolveUnder`'d — the reviewer asked for a
test that seeds `SourceRef` that way.

Added `TestAgentConfig_Update_RenameBranch_SameSlugNoOp_LegacySourceRef`
alongside the existing test. It seeds a DB row directly (`st.CreateAgent`,
mirroring `TestReconcileManagedAgentIDs_AdoptsExistingProjection`'s existing
pattern) and writes the managed file's raw content via the file's own
`writeRawAgentFile` test helper at a path built as
`filepath.Join(aliasRoot, "agents", slug+".md")` — a plain join, exactly the
shape `agent.ManagedAgentPath` produced before this task's fix. To make the
mismatch deterministic and portable (not dependent on host-specific symlink
quirks like macOS's `/var` → `/private/var`), `aliasRoot` is a test-created
symlink to the real managed root: `pathsafe.ResolveUnder` canonicalizes that
symlink away (per its own doc comment's stated reason for resolving root
symlinks); a raw `filepath.Join` does not — so the two disagree on the exact
string for the same on-disk file, the precise mismatch the short-circuit
exists to prevent.

**Mutation-test result (performed directly, per the reviewer's own method):**
temporarily replaced the short-circuit's `if updated.Slug == existing.Slug {
dest = existing.SourceRef } else { ... }` with the unconditional
`pathsafe.ResolveUnder(renameDir, updated.Slug+".md")` branch (i.e. reverted
exactly the hazard-preventing logic) and reran
`go test ./internal/service/... -run
TestAgentConfig_Update_RenameBranch_SameSlugNoOp -v`.
- New test **fails** against the mutated code: `Update` itself returns an
  error (`agent: read .../agents/atlas.md: ... no such file or directory`).
  Trace: `writeManaged` writes the new content to the canonicalized `dest`
  (which is the *same physical file* as `oldPath` via the test's symlink),
  then unconditionally removes `oldPath` because `dest != oldPath` as
  strings — deleting the very file the write just landed — and the
  subsequent `agent.ParseMDFile(path)` call then fails to find it. This is
  exactly the "deletes the file this very call just wrote" hazard the
  short-circuit's own comment describes.
- Reverted the mutation immediately after (confirmed via `grep` that no
  mutation marker remained, and via `git diff internal/service/agent_config.go`
  that the file matches this task's originally-shipped fix byte-for-byte).
- Reran against the real, shipped fix: both
  `TestAgentConfig_Update_RenameBranch_SameSlugNoOp` and the new
  `..._LegacySourceRef` variant **pass**.
- The pre-existing sibling test (`svc.Create()`-seeded) was left unchanged —
  it still validates the ordinary, already-resolved-path case; the new test
  covers the legacy/pre-fix-shaped case the reviewer identified as
  uncovered.

**Deployed-DB slug-conformance check (documentation only, no code change):**
the Orchestrator independently queried this machine's real deployed DB
(`~/.local/share/nanite/workspaces/default/main.db`) and confirmed all
current `durable_agent_instances.slug` and `agent_profiles.slug` values
already conform to the new `^[a-z0-9]+(?:-[a-z0-9]+)*$` regex — so the
reviewer's separate finding (unconditional slug validation on every write
could lock out editing/archiving of a pre-existing agent with a
non-conforming slug) is not an active risk against this deployment's real
data today, though it should be re-checked against any other deployment
before shipping there.

**Baseline re-run after this change** (from this worktree):
`go build ./...` clean; `go vet ./internal/service/...` shows only the same
two pre-existing `container.go` `stopReaper`/`stopRuntimeReaper` findings
already noted above (untouched by this task); `go test
./internal/service/...` passes (full package, includes both same-slug
tests); `go test ./internal/agent/... ./internal/agentvalidation/...
./internal/api/...` all pass (cached, unaffected by this change).

## Review notes

<!-- Reviewer fills this in. -->
