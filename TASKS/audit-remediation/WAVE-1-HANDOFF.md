# Wave 1 handoff — for Wave 2's kickoff author

**Audience: a fresh session with zero memory of this one.** This is not a
restatement of the six task files' original plans — it's what actually
happened, verified against the Work Logs, the architect-decision record, the
escalation log, and `git log`, not against what was intended when the tasks
were written.

Wave 1 ("release-blocking trust boundaries") is **complete**. All six tasks
are `reviewed` in `TASKS/INDEX.md`: `01/01`, `01/02`, `02/01`, `02/02`,
`03/01`, `12/02`.

---

## 1. What shipped

### `01/01` — Plugin-install convergence (GO-PLUGIN-001/002/003, critical×2 + high)

`handleCatalogInstall` (`POST /api/plugins/catalog/install`, the GUI "Plugin
Manager" install path) previously bypassed signature verification by default
and wrote to an unconfined `filepath.Join(cs.pluginsDir, entry.Name)` — a
critical path-traversal write plus a critical signature-bypass, both fed by
one weak legacy pipeline (`internal/plugin/catalog.go`/`signature.go`) that
existed in parallel with a strong, already-fail-closed CLI pipeline
(`internal/plugin/install/`). Per AD-04's six decided sub-questions, the
handler now converges onto `install.Installer` via a new shared constructor,
`install.NewInstaller`/`BuildOptions` (`internal/plugin/install/build.go`) —
confinement via `install.ValidatePluginID` + `DirStaging.Commit` (not
`pathsafe.ResolveUnder`), a format-dispatching `catalogExtractor` supporting
both `.zip` and `.tar.gz` (AD-04 caught this as a real gap the audit and the
original task file both missed — a naive convergence would have silently
dropped zip support), a `hostLoader` thin adapter around the existing
`pms.runPluginLoadIntoHost`, and the CLI's own `catalogArchiveSource`
relocated/exported as `install.CatalogArchiveSource` so both callers share
it. `internal/plugin/signature.go`'s `VerifyChecksum`/`VerifySignature` are
fully removed (zero remaining callers); `CatalogFetcher` was kept, per AD-04,
because it's still load-bearing for browse/refresh. Verified by 7 new HTTP-
level regression tests in `internal/api/catalog_install_test.go`
(`TestHandleCatalogInstall_RejectsUnsignedEntry`, `_PathTraversal`,
`_Success_TarGz`/`_Zip`, `_WrongSignature`, `_AlreadyInstalled`, `_NotFound`)
plus the CLI's own unmodified test suite still passing. Commit `02c7f0e9`.

A same-day follow-up (still inside this commit's Work Log, operator-directed
after independent re-review) restored a real pre-existing behavior the
convergence had silently dropped: the old handler tolerated a "plugin.yaml
inside exactly one wrapper subdirectory" archive shape (what a plain GitHub
"Download ZIP" produces); the new pipeline's `install.Installer` state
machine has no hook for that, so it was fixed entirely inside
`catalogExtractor` (a new `resolveCatalogPluginRoot`/`flattenCatalogPluginRoot`
pair) without touching `install.go`/`staging.go`/`extract.go`. New test:
`TestHandleCatalogInstall_Success_WrapperDirectory`.

### `01/02` — Wire `allow_unsigned_plugins` (GO-PLUGIN-008, low)

`user_settings.allow_unsigned_plugins` was stored and API-exposed but never
actually read into the CLI's `SignatureVerifier.AllowUnsigned` field — a
documented dev-workflow opt-in that silently did nothing. AD-25 (decided
mid-wave — see §2 below) said wire it, not retire it. Because `01/01` landed
first, this task found the construction site had changed shape from what its
own file assumed: one shared `install.NewInstaller` constructor with **two**
production callers (`cmd/nanite/plugin_install_flow.go`'s `buildInstaller`
and `internal/api/catalog.go`'s `handleCatalogInstall`), both needing the
same one-line wiring fix, not the single CLI-only site the task file
originally described. Both now read the setting (via a new
`resolveAllowUnsignedPlugins` helper on the CLI side, and a direct
`cs.store.GetUserSettings()` read on the API side) and fail safe to `false`
on any read error. Production (`!devmode`) builds are unaffected either way
— `devmode.HostDevSigningBypass` compiles to `false` outside `devmode`
builds. 5 new tests across `cmd/nanite/plugin_install_flow_devmode_test.go`
and `internal/api/catalog_install_devmode_test.go`, all under `//go:build
devmode`. Commit `a9d843a5`.

### `02/01` — Linux sandbox fail-open (GO-SEC4-001 critical, GO-SEC4-002 high, GO-SEC4-006)

Three findings closed in one task, per AD-01 and AD-02:

- **AD-01 (fail closed):** `applyOSSandbox` on Linux/`os_other.go` used to
  silently fall back to unisolated execution when `bwrap` was absent, warned
  once per process lifetime (`sync.Once`), and returned a success-shaped
  `(cleanup, nil)` indistinguishable from real isolation. Now: the
  `sync.Once` is gone (every degraded exec logs at warn), `applyOSSandbox`'s
  signature carries a real three-state isolation verdict
  `(cleanup, isolated bool, err error)`, and absent `bwrap` returns a real
  error unless the operator has set a new env var,
  `NANITE_ALLOW_UNSANDBOXED_AGENT_EXEC=1`. `ExecResult` gained a
  `SandboxIsolated bool` field, and all four production callers
  (`dev_bash`, `code_execute`, `ShellStep`, `handleShellExec`) now surface a
  degraded-mode warning visibly (not just server-side), not merely log it.
- **AD-02 (fix the network inversion for real):** previously, configuring a
  `NetworkAllow` allowlist made the sandbox *weaker* than configuring
  nothing (`--unshare-net` was applied only when the allowlist was empty).
  Fixed with new infrastructure, not a flag flip: `--unshare-net` is now
  unconditional, and a new "netns bridge"
  (`internal/sandbox/netns_bridge_linux.go`) relays the sandboxed process's
  loopback traffic across the network-namespace boundary via an AF_UNIX
  socket to the still-host-netns `Proxy`, so a configured allowlist is now
  strictly *stronger* than no allowlist, matching what operators already
  assumed.
- **GO-SEC4-006 (denylist):** reframed as advisory/defense-in-depth in a
  doc comment rather than rewritten as a real tokenizer — a locked-in test
  (`TestCheckDenylist_KnownBypassClasses`) records exactly which bypass
  shapes are accepted-uncaught.

Verified on **real Linux** (colima+docker, not darwin — darwin never
exercises this code path): `bwrap`-absent Alpine container confirmed the
fail-closed error and the opt-in degrade path; `bwrap`-present container
(`apk add bubblewrap`) confirmed real namespace isolation, the netns-bridge
end-to-end forward, and `-race` clean. Three real bugs were found and fixed
only because real-Linux verification was actually run (see §4). Commit
`494c3187`; review notes (a genuine independent PASS, with real-Linux
re-verification, not a rubber stamp) recorded in commit `a53327a6`.

### `02/02` — macOS seatbelt disclosure (GO-SEC4-005, low)

AD-03 decided: disclose, don't narrow — `internal/sandbox/os_darwin.go`'s
`(allow default)` seatbelt profile (unrestricted file reads, mach-IPC,
process inspection) is an accepted, intentional beta-stage tradeoff, and the
code does not change (confirmed via `git status --short`: only two `.md`
files touched, zero Go files). The actual work was correcting a real,
independently-found disclosure gap: `docs/hardening-phase-plan.md`'s Tier 2
description claimed guarantees the shipped code doesn't provide ("Read/write
within sandbox CWD only", "No process inspection") — corrected with a
struck-through-and-annotated block citing `os_darwin.go`, the 2026-04-10
audit, and AD-03. A **second**, previously-unknown disclosure gap was found
and fixed in the same pass: `docs/programmatic-tool-calling-safety.md`
claimed the OS sandbox "blocks most file reads" and "blocks outbound
network" for `python_run` — traced and confirmed **false**: `python_run`'s
subprocess doesn't route through `sandbox.AgentExec`/`applyOSSandbox` at all,
on either platform. Both docs corrected. No `SECURITY.md` or genuinely
end-user-facing security doc was created — AD-03 explicitly scoped the
remedy to the internal-doc correction only. Commit `b50d35b8`.

### `03/01` — Agent-slug path traversal (GO-AGENT-001 high, GO-AGENT-002 high)

An agent `slug` (and a durable-agent `slug`) was never validated for
path-safety before being joined into a managed-config file path, on both
create and update — the `Update` rename branch in particular
(`internal/service/agent_config.go`) bypassed the shared path-computation
function entirely and had **no guard at all**. `requires_architect_decision`
was `false` here (a clear-direction fix, not a design call), but the task's
own real scope grew beyond its cited findings: verifying "all production
callers" surfaced a second, fully parallel write path with the identical
defect — durable-agent configs
(`internal/service/managed_durable_configs.go`) — that neither
`GO-AGENT-001` nor `GO-AGENT-002`'s evidence lists mentioned at all. Fixed:
a new exported `agent.ValidateSlug` (a second compiled instance of the
agent-builder wizard's existing regex, not an import of `internal/builders`
into the lower-level `internal/agent` — a deliberate layering call, logged
in `slug.go`'s own doc comment) plus `pathsafe.ResolveUnder` as defense in
depth, wired into `ManagedAgentPath`, `ManagedDurableAgentPath`, the
`Update` rename branch's own explicit call, `ValidateAgentConfig`, and
`saveManagedDurableInstance`. Confirmed via direct trace: no auth middleware
gates these routes when `NANITE_AUTH_USER`/`NANITE_AUTH_PASSWORD` are unset
(the default out-of-the-box posture) — a pre-existing, separately-tracked
finding (`GO-RUNTIME-002`, task `08/07`), not something this task needed to
fix, but it raises this fix's real-world urgency. A real correctness hazard
was found and fixed mid-implementation: naively routing the rename branch's
`dest` through `pathsafe.ResolveUnder` unconditionally broke the ordinary
same-slug-edit case (deleting the file the same call just wrote), fixed with
an explicit same-slug short-circuit and a regression test. A subsequent
independent review mutation-tested that short-circuit and found the first
version of the test didn't actually pin the realistic failure mode (it only
covered an already-`ResolveUnder`'d `SourceRef`, not a legacy plain-`Join`
one); a second test, `..._LegacySourceRef`, was added and the reviewer's own
mutation-test method (temporarily reverting the fix, confirming the new test
fails, reverting the mutation) is recorded in the Work Log. Commit
`5670c8a3`.

One thing this task deliberately left unfixed and flagged, not silently
dropped: `Update`'s DB-only-materialize branch still has no exists-check
(same-slug collision between two agents can silently overwrite one's
managed file) — the task file's own "What to do" prose said "confirm the
primitive-layer check is sufficient," not "add an exists-check," and neither
Done-means nor Tests-required asked for one. Traversal is fixed; that
separate, non-traversal data-integrity gap is not.

### `12/02` — Six engineering standards codified

Pulled forward from Wave 7 to Wave 1 (doc-only, collides with nothing) so
the six standards are citable by the rest of this batch instead of written
down after the work they were meant to govern. Added a new section to
`docs/engineering/standards/coding-standards.md` — Production Reachability,
Security/Correctness Migration Completeness, Semantic Duplication,
Lifecycle Ownership, Trust-Boundary Paths, Silent Security Degradation —
each with the guide's exact verbatim wording plus a short traceability note
(sourced from `PREVENTION.md`'s per-class material, not the task file's own
condensed prose) pointing at the real `TASKS/audit-remediation/` sub-folder
the pattern was found in. Pre-existing stub content and "Not yet
documented" section left in place, per the task's own non-goals. Commit
`f68acac1`.

**Worth noting for anyone citing this doc going forward:** `01/01` and
`03/01` are now the concrete, in-repo examples of "Security/Correctness
Migration Completeness" (a fix landed for one caller of a shared boundary
without checking every other caller) — cite them directly if this pattern
recurs.

---

## 2. What changed from the original plan, and why

- **AD-04 (plugin-install convergence shape)** was decided with six
  sub-answers, four "settled by the code" and two real calls (confinement
  mechanism = `DirStaging`/`validatePluginID`, not `pathsafe.ResolveUnder`;
  legacy retirement is narrower than the audit's own recommendation —
  `CatalogFetcher` survives). Full text: `ARCHITECT-DECISIONS.md`'s AD-04
  section.
- **AD-01/AD-02 (Linux sandbox)** — AD-01 chose fail-closed-with-opt-in over
  a silent degrade; AD-02 chose to actually fix the network-namespace
  inversion (real engineering — the netns bridge) rather than
  document-only or fail-closed-the-whole-feature. The task file's own
  effort framing predates both decisions and undersold AD-02's real size —
  AD-02's own text says so explicitly ("this is real engineering... larger
  than AD-01's change... the task's own estimate predates this decision").
- **AD-03 (macOS seatbelt)** — resolved as disclose-only; `os_darwin.go`
  does not change. The real work ended up being two documentation
  corrections, one of which (`programmatic-tool-calling-safety.md`) wasn't
  named anywhere in the original task file — found by the worker's own
  repo-wide grep, not anticipated.
- **AD-25 was not a pre-existing decision — it was a gap found and closed
  mid-wave.** `01/02`'s own header claimed
  `requires_architect_decision: true` with no matching `AD-NN` entry
  anywhere in `ARCHITECT-DECISIONS.md` at all — a real planning-pass hole,
  not a decision anyone had actually made. Found and closed during Wave 1
  dispatch prep via direct operator confirmation on 2026-08-22 (wire, not
  retire). If Wave 2's task files have similar `requires_architect_decision:
  true` headers, **check `ARCHITECT-DECISIONS.md` has a matching `AD-NN`
  entry before dispatch** — this is exactly the failure mode that produced
  AD-25.
- **`01/01`'s wrapper-subdirectory extraction tolerance** was initially
  dropped by the convergence (the old handler's "plugin.yaml at root OR one
  subdirectory" tolerance had no equivalent hook in `install.Installer`'s
  state machine) and then restored same-day after independent review
  flagged it as a real, if minor, regression for anyone installing from a
  plain GitHub "Download ZIP"-shaped archive. Fixed entirely inside the new
  `catalogExtractor` type, per the reviewer's own correctly-identified seam
  — no changes to `install.go`/`staging.go`/`extract.go`.
- **A newly-found leftover-archive-file gap was NOT fixed in this wave** —
  see §3.

---

## 3. Gotchas for Wave 2 to know about

1. **`install.NewInstaller`/`BuildOptions` now has two production callers.**
   Before `01/01`, there was exactly one `SignatureVerifier` construction
   site (`cmd/nanite/plugin_install_flow.go`'s `buildInstaller`). After
   `01/01`, it's a shared constructor (`internal/plugin/install/build.go`)
   called from both `cmd/nanite` and `internal/api/catalog.go`. **Any future
   task touching plugin installs must re-check both call sites, not assume
   the CLI is the only one** — this is exactly the failure mode `01/02`
   itself hit (its own task file's "one caller" premise went stale the
   moment `01/01` landed, and the task correctly re-verified before
   proceeding rather than trusting its own stale text).

2. **A real, unfixed hygiene gap: leftover downloaded archive files.** Found
   during `01/01`'s follow-up re-review (`TASKS/ESCALATIONS.md`,
   2026-08-22 entry): `install.HTTPDownloader.Download` writes the
   downloaded archive *inside* the same `targetDir` that
   `catalogExtractor`/`TarGzExtractor` also extract into, and nothing in
   the pipeline (`install.go`, `staging.go`, `download.go`,
   `catalog_install.go`) ever deletes it after a successful install.
   Confirmed reproducible via both the GUI/API path and the CLI path (they
   share the same `HTTPDownloader`). Every catalog- or CLI-installed plugin
   today ships with an extra multi-hundred-KB-to-MB archive file sitting
   inside its own install directory. This is correctness/hygiene, not a
   security regression — it doesn't touch verification or confinement —
   and it predates `01/01` (it's latent in the CLI pipeline `01/01`
   converged onto, just never surfaced before because nobody had test
   coverage checking install-directory *contents*, only correctness of the
   installed plugin itself). **Not fixed in Wave 1** — flagged as a
   fast-follow candidate for whoever next touches
   `internal/plugin/install/`. Two fix shapes are already sketched in the
   escalation entry: have `HTTPDownloader.Download` write outside
   `stagingDir`/`targetDir` (restoring the pre-convergence handler's
   original design), or have `Installer.Install` delete the archive
   post-extraction.

3. **Real-Linux verification technique that worked: colima + docker.**
   `02/01`'s worker had no local Docker/DigitalOcean credentials available
   and, per the task's own explicit instruction not to declare the Linux
   path unverifiable, installed `colima`+`docker` CLI via Homebrew
   (lightweight, no GUI/kernel extension) and ran real `golang:1.25-alpine`
   containers — one with `bwrap` absent, one with it installed via `apk add
   bubblewrap --privileged` (a plain `--cap-add SYS_ADMIN` was insufficient
   for the nested procfs mount in this specific Docker-in-VM setup). This
   surfaced three real bugs that darwin testing structurally cannot catch
   (see §4). **Reusable pattern for any future task in this batch that
   needs genuine Linux-only verification** rather than a `GOOS=linux`
   cross-compile that never actually executes.

4. **`go.mod`/Dockerfile Go-version drift.** `go.mod` declares `go 1.26.2`;
   the repo's own `Dockerfile` `go-build` stage is pinned to
   `golang:1.25-alpine` (the exact base image `02/01`'s worker used for
   real-Linux verification, deliberately matching the Dockerfile). Worth a
   look before anyone next touches the Dockerfile or bumps `go.mod`'s
   version — not something Wave 1 fixed or was in scope to fix, just
   flagged here as directly observed while assembling this handoff.

5. **`02/01`'s test-robustness observation (non-blocking, left as-is):**
   `TestNetnsBridge_HostArbitraryPortStillBlocked`
   (`internal/sandbox/os_linux_test.go`) can pass "for the wrong reason" in
   an unprivileged/restricted container — a bwrap namespace-construction
   failure and a correctly-blocked port both produce `runErr != nil`, and
   the assertion doesn't distinguish them. Its sibling positive test has no
   such ambiguity. Cheap follow-up candidate whenever `internal/sandbox`'s
   test suite is next touched.

6. **Review-note completeness gap — found by this doc-writer pass, closed same day.** At the time this doc-writer session first drafted this handoff, `TASKS/INDEX.md` marked all six Wave 1 tasks `reviewed`, but only `02/01`'s task file had a substantive `## Review notes` write-up — `01/01`/`03/01`'s real review activity lived only in their Work Logs' "Follow-up" subsections, and `01/02`/`02/02`/`12/02` had empty placeholder `## Review notes` sections with no reviewer content anywhere. The underlying reviews were all real (independent fresh-context reviewer dispatches happened for all six tasks — this was never a fabricated-attestation issue), but the durable record didn't reflect it for five of six. **Resolved same day, commit `b4e2bc12`**: the Orchestrator backfilled all five task files' `## Review notes` sections with the actual verdicts, criteria checked, and findings from each task's real review round(s). All six Wave 1 task files now carry a complete, checkable independent-review record in their own `## Review notes` section — safe to cite "every Wave 1 task got a fresh independent review" as fully documented.

---

## 4. Verification steps to independently re-confirm this wave's deliverables

Do not just trust the status column. Concrete checks, in rough order of
value:

**`01/01` — plugin-install convergence:**
- `grep -n "SignatureVerifier{" internal/plugin/install/build.go` — should
  show exactly one struct literal, inside `NewInstaller`.
- `grep -rn "install.NewInstaller(" --include="*.go" .` (excluding tests) —
  should show exactly two production callers:
  `cmd/nanite/plugin_install_flow.go` (`buildInstaller`) and
  `internal/api/catalog.go` (`handleCatalogInstall`).
- `grep -n "VerifyChecksum\|VerifySignature" internal/plugin/*.go` — should
  show zero remaining definitions (both fully removed).
- `go test ./internal/api/... -run TestHandleCatalogInstall -v` — 8 tests
  should pass, including `_RejectsUnsignedEntry`, `_PathTraversal`,
  `_Success_Zip`, `_Success_WrapperDirectory`.
- Manual spot-check: `POST /api/plugins/catalog/install` with an unsigned
  catalog entry against a production (`!devmode`) build should return
  non-2xx and write nothing under `pluginsDir`.

**`01/02` — allow_unsigned_plugins wiring:**
- `grep -n "resolveAllowUnsignedPlugins" cmd/nanite/plugin_install_flow.go`
  and `grep -n "AllowUnsignedPlugins" internal/api/catalog.go` — both call
  sites should populate `BuildOptions.AllowUnsigned`.
- `go test -tags devmode ./cmd/nanite/... ./internal/api/... -run Install -v`
  — should pass, including the devmode-gated tests confirming the setting
  has effect under `-tags devmode` and none under a plain build.

**`02/01` — Linux sandbox fail-closed + network fix:**
- `grep -n "sync.Once\|bwrapWarnOnce\|osWarnOnce" internal/sandbox/os_linux.go internal/sandbox/os_other.go`
  — should return nothing (both deleted).
- `grep -n "func applyOSSandbox" internal/sandbox/os_linux.go` — signature
  should return three values including an `isolated bool`.
- `grep -n "SandboxIsolated" internal/sandbox/exec.go` — should exist on
  `ExecResult`.
- `test -f internal/sandbox/netns_bridge_linux.go` — should exist (AD-02's
  new relay mechanism).
- `grep -n "unshare-net" internal/sandbox/os_linux.go` — should be
  unconditional, not gated on `len(networkAllow) == 0`.
- Real verification (not `GOOS=linux go build` alone) requires an actual
  Linux environment — see §3 item 3 for the colima+docker technique if
  re-confirming end-to-end.

**`02/02` — seatbelt disclosure:**
- `git diff --stat <pre-wave-1-commit> -- internal/sandbox/os_darwin.go` —
  should be empty (no code change, per AD-03).
- `grep -n "AD-03\|2026-04-10" docs/hardening-phase-plan.md
  docs/programmatic-tool-calling-safety.md` — both should show corrections
  present.

**`03/01` — agent-slug traversal:**
- `test -f internal/agent/slug.go` and `grep -n "func ValidateSlug"
  internal/agent/slug.go` — should exist.
- `grep -n "ValidateSlug" internal/agent/managed_files.go
  internal/service/managed_durable_configs.go internal/service/agent_config.go
  internal/agentvalidation/validation.go internal/api/durable_agents.go` —
  should show a call site in each.
- `go test ./internal/agent/... ./internal/service/... ./internal/api/... -run 'Slug|Traversal' -v`
  — should pass, including
  `TestManagedAgentUpdate_RejectsSlugTraversal`,
  `TestDurableAgentsAPI_UpdateRejectsSlugTraversal`, and
  `TestAgentConfig_Update_RenameBranch_SameSlugNoOp_LegacySourceRef`.

**`12/02` — engineering standards:**
- `grep -c "Production Reachability\|Security/Correctness Migration Completeness\|Semantic Duplication\|Lifecycle Ownership\|Trust-Boundary Paths\|Silent Security Degradation" docs/engineering/standards/coding-standards.md`
  — should return `6`.

**Baseline, all six:**
- `go build ./...`, `go vet ./...`, `go build -tags devmode ./...` clean.
- `go vet ./...` should show only the pre-existing, unrelated
  `internal/service/container.go` `stopReaper`/`stopRuntimeReaper` findings
  (tracked separately as `GO-LIFE-001`, `04/01`-adjacent) — every Wave 1
  Work Log independently confirmed these predate and are untouched by this
  wave's diffs.

## Tracking correction applied after Wave 1 closed (2026-08-22)

Two bookkeeping gaps were found post-wave and fixed in a follow-up commit. Both
are the same class as the missing-`## Review notes` gap Wave 1 self-reported —
substantive work genuinely complete, tracking state not advanced to match.
**Wave 2 should close both loops as part of its own wrap-up, not after it.**

1. **All six task files still read `**Status:** implemented`** despite real,
   independent reviews with PASS verdicts recorded in their `## Review notes`.
   Advanced to `reviewed`. Wave 0's precedent is the one to copy — commit
   `e3980e9b` explicitly marked its tasks `reviewed` when the review passed.

2. **`findings.json`'s `task_status` was `not-started` for all 113 findings**,
   including the ten Wave 1 closed. Synced to `reviewed` for
   `GO-PLUGIN-001/002/003/008`, `GO-SEC4-001/002/005/006`, and
   `GO-AGENT-001/002`. (`12/02` is guide-derived and maps to no finding.)

The second matters more than it looks. Per the batch README and `findings.json`'s
own docs, that field exists so anyone can *"answer 'how much of the audit is
actually closed' by reading one JSON file instead of opening 61 task files,"* and
so a future re-audit can *"diff cleanly against a known remediation state."*
Left unsynced, the catalog reported 0% of the audit closed while ten findings
were done — which is exactly what a Wave 2 kickoff author or a re-audit would
have read.

Current state: **10 of 113 findings `reviewed`, 103 `not-started`.**

### Also worth knowing for Wave 2

`go vet ./...` is **not** clean — it reports 4 findings, all in
`internal/service/container.go` (`stopReaper`/`stopRuntimeReaper` not used on
all paths). This is **`GO-LIFE-001`, catalogued and assigned to `04/01`**, which
is Wave 2a's own first task. It is pre-existing, not a Wave 1 regression, and
Wave 0 already corrected its line-number citations (audit: 1162/1182 and
1242/1246 → current: 1213/1233 and 1291-1293/1295-1297). Do not treat a
non-green `go vet` at Wave 2 start as a blocker — closing it *is* `04/01`.
