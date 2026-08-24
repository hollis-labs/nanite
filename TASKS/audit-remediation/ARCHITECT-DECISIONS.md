# Architect decision queue — audit remediation

The remediation guide is explicit (§9): *"Keep decisions separate from
implementation work… Do not let implementation agents silently make these
decisions."* This file is that separation. It is the batch's single
authoritative list of calls that must be made **by the operator/architect**,
not by a worker mid-task.

**29 decisions currently in this queue**, all decided.
Most are grounded in the **44
findings** carrying `requires_architect_decision: true` in `findings.json`,
plus the guide's own §9 list; AD-23 and AD-24 are process decisions surfaced by
the planning pass.

**Five were found missing after the fact, by pre-flight verification** —
AD-25 (Wave 1), AD-26 (Wave 2), AD-27/AD-28 (Wave 3), and AD-29
(Wave 6). In each case a
finding carried `requires_architect_decision: true` with no entry in this
queue. That is the failure mode this file exists to prevent, and it has now
been caught four waves running by the same mechanism: **a kickoff author cross-checking
`findings.json`'s flag against this queue before dispatch.** Keep doing that
check when writing each wave's kickoff.

**AD-01 through AD-04 were moved from Wave 1 to Wave 0** by operator direction
on 2026-08-21 — see the note below the queue table.

## How this file is used

- A task file that is gated on a decision names its `AD-NN` in its
  `**Gated on:**` header line. **An Orchestrator must not dispatch a gated
  task while its decision's status is `open`.** This is the mechanism that
  keeps a worker from quietly inventing an answer to a question the audit
  deliberately escalated.
- When a decision is made, edit its section below: set **Status** to
  `decided`, add **Decided (`<DATE>`)** with the call *and the reasoning*, and
  update `findings.json`'s `disposition` for the affected findings if the
  decision changes it (a `retire-feature` call on an island, most obviously).
- The reasoning is not optional garnish. Six of these decisions (AD-06 through
  AD-11) permanently delete or permanently keep working features; a future
  reader needs to know why, and the guide's §4 warns specifically against
  *"delet[ing] islands without checking current intent."*

**Status values:** `open` | `decided` | `deferred` (explicitly postponed, with
a named trigger) | `moot` (Wave 0 revalidation removed the question).

## The queue

| ID | Decision | Gates | Findings | Wave | Status |
|---|---|---|---|---|---|
| AD-01 | Linux sandbox behavior when `bwrap` is absent | `02/01` | GO-SEC4-001, GO-SEC4-002, GO-SEC4-006 | **0** | **decided** |
| AD-02 | Linux network allowlist enforcement level | `02/01` | GO-SEC4-002 | **0** | **decided** |
| AD-03 | macOS seatbelt read-boundary: disclose, narrow, or accept | `02/02` | GO-SEC4-005 | **0** | **decided** |
| AD-04 | Plugin-install convergence: concrete integration shape | `01/01` | GO-PLUGIN-001/002/003 | **0** | **decided** |
| AD-05 | Default/official catalog source: provision a real signing key? | `01/01` follow-up | GO-PLUGIN-001 | 1 | **decided** |
| AD-06 | Island: grounding memory recall — wire / defer / retire | `09/01` | GO-MEM-001 | 4 | **decided** |
| AD-07 | Island: Hadron context gate — wire / defer / retire | `09/02` | GO-MEM-002 | 4 | **decided** |
| AD-08 | Island: team semantic routing — wire / defer / retire | `09/03` | GO-SVCEXEC-003 | 4 | **decided** |
| AD-09 | Island: tool builder / YAML architecture — wire / defer / retire | `09/04` | GO-MCPTOOL-001 | 4 | **decided** |
| AD-10 | Island: reasoning-augmented tool selection — wire / defer / retire | `09/05` | GO-MCPTOOL-002 | 4 | **decided** |
| AD-11 | Island: curated tool-knowledge matcher — wire / defer / retire | `09/06` | GO-MCPTOOL-003 | 4 | **decided** |
| AD-12 | `chatServiceImpl` / `generateResponse` decomposition boundaries | `10/01`, `10/04` | GO-SVCEXEC-001/002 | 5 | **decided** |
| AD-13 | `SelfToolsTransport` decomposition boundaries | `10/02`, `10/05` | GO-MCPTOOL-006 | 5 | **decided** |
| AD-14 | How far to narrow `internal/store` dependencies | `10/03`, `06/02`, **`06/03`** | GO-DEP-002, GO-STORE-001, GO-STORE-005 | 5 | **decided** |
| AD-15 | Default auth / bind / TLS / startup-warning posture | `08/07` | GO-RUNTIME-002 | 3 | **decided** |
| AD-16 | `permission.Engine` `ModeDefault`: does it prompt for writes? | `08/04` | GO-SEC4-003 | 3 | **decided** |
| AD-17 | `cmdServe` fatal-path cleanup: direction | `07/02` | GO-RUNTIME-001 | 2b | **decided** |
| AD-18 | Background job registry: retention policy | `07/03` | GO-RUNTIME-004 | 2b | **decided** |
| AD-19 | Duplicated semantics: share implementation vs. parity tests | Wave 6a (all) | GO-SVCEXEC-004, GO-API-007, GO-CHAT-002, GO-INFRA-004 | 6a | **decided** |
| AD-20 | `internal/config` naming collision: rename direction | `11/08` | GO-INFRA-001 | 6a | **decided** |
| AD-21 | Which historical lint classes become blocking | `12/01` | GO-HYG-001 | 7 | **decided** |
| AD-22 | Repo-wide `gofmt` sweep: now, never, or ratchet-only | `13/03` | GO-HYG-001, GO-CHAT-007 | 8 | **decided** |
| AD-23 | Accept ~8 MB of audit evidence into the repo | `00/02` step 1 | — (process) | 0 | **decided** |
| AD-24 | Dev-freeze scope and exit criteria | **every batch in the repo** | — (process) | 0 | **decided** |
| AD-26 | Untracked `safego.Go` spawns: adopt an owner, or accept fire-and-forget | `04/04` (Part B) | GO-SVCCORE-002 | 2a | **decided** |
| AD-27 | Autocomplete `repo_path` enumeration: constrain, or accept the local-operator trust model | `08/09` | GO-API-001 | 3 | **decided** |
| AD-28 | Catalog archive fetch: host/scheme restriction — **and** CIDR-denylist consolidation | `08/09` | GO-API-003, **GO-SEC4-007** | 3 | **decided** |
| AD-33 | Dead skill-mode/source functions: retire or wire | `13/01` | GO-STORE-008 | 8 | **decided** |
| AD-30 | Task-ID citations in permanent production comments: trim or keep | `13/02` | GO-STORE-009 | 8 | **decided** |
| AD-31 | `internal/chat` vocabulary-vs-wiring split | `13/05` | GO-CHAT-008 | 8 | **decided** |
| AD-32 | Context pipeline: relevance contract and token-estimator convergence | `13/05` | GO-MEM-004, GO-MEM-005 | 8 | **decided** |
| AD-25 | `allow_unsigned_plugins` devmode bypass: wire or retire | `01/02` | GO-PLUGIN-008 | 1 | **decided** |
| AD-29 | Client-side elicitation dead code: build or delete | `11/09` | GO-MCPTOOL-004 | 6a | **decided** |

### A gap worth naming

Two of the six islands — **AD-07** (`GO-MEM-002`) and **AD-11**
(`GO-MCPTOOL-003`) — are **not** flagged `requires_architect_decision: true`
in `findings.json`, even though the remediation guide's §4 Wave 4 table lists
all six as requiring a wire/defer/retire call. They are in this queue anyway,
because the guide's requirement is the stronger authority: *"For every
'island,' explicitly choose: wire / defer / retire."* Wave 0 should set both
findings to `disposition: needs-architect-decision` to bring the catalog into
line.

### AD-01 through AD-04 are Wave 0 decisions, resolved before Wave 1 dispatches

**Operator direction, 2026-08-21.** These four were originally filed as Wave 1
decisions, to be made when Wave 1 was scheduled. They are now **Wave 0
work**, running as a parallel operator-owned track alongside `00/01` and
`00/02`, for two reasons:

1. **Nothing about them depends on Wave 0's tooling output.** They are design
   calls. Holding them until Wave 1 would stall the four release-blocking
   tasks behind a revalidation that was never going to answer them.
2. **They *do* depend on Wave 0's revalidation of their own findings** —
   `GO-PLUGIN-001/002/003` and `GO-SEC4-001/002/005/006`, which are the 3
   critical and 3 of the 8 high findings. `00/01` processes critical-then-high
   **first**, by design, precisely so these decisions can be made against
   revalidated current-source evidence rather than audit-era evidence that is
   40 commits stale.

So the ordering inside Wave 0 is: `00/01` revalidates the critical and high
findings → the operator decides AD-01 through AD-04 against that evidence →
the rest of Wave 0 finishes → Wave 1 becomes dispatchable. AD-05 stays a
follow-up and does not gate anything.

Practical consequence: **`00/01`'s worker must report the critical/high
revalidation results as soon as they exist, not hold them until the full
113-finding sweep is done.** That interim report is what the operator decides
from. This is written into `00/01`'s own instructions.

---

## Wave 0 decisions

### AD-01 — Linux sandbox behavior when `bwrap` is absent

**Status:** decided · **Gates:** `02/01` · **Findings:** GO-SEC4-001 (critical),
GO-SEC4-002 (high), GO-SEC4-006

> **Decided (2026-08-22): fail closed, with an explicit config opt-in to
> degrade.**
>
> Absent `bwrap`, `AgentExec`/`UserExec` return an error rather than executing.
> A new config knob — none exists today, confirmed by grep — lets an operator
> accept unisolated execution deliberately. When that knob is set, **every**
> degraded execution logs at warn.
>
> **The `sync.Once` goes.** `os_linux.go:14`/`os_other.go:11` currently gate the
> warning behind a `sync.Once`, so it fires once per *process lifetime* and
> every subsequent unsandboxed exec is completely silent. That is materially
> worse than "logs a warning" and is the concrete mechanism of the "silent
> degradation" this decision closes.
>
> **The return type must carry an isolation verdict, and that part was never
> optional.** `applyOSSandbox` returns `(cleanup func(), err error)` with no
> third state, so both call sites (`exec.go:158`, `exec.go:203`) genuinely
> cannot distinguish "isolated" from "not isolated". Every candidate answer
> required this change; only what to *do* with the verdict was in question.
>
> **Scope: the class is `os_linux.go` + `os_other.go`.** Both have the same
> fail-open shape and the same `sync.Once`. macOS is unaffected —
> `sandbox-exec` is built into the OS and always present.
>
> **Known consequence, accepted:** on a Linux host without bubblewrap this
> disables agent shell (`internal/mcp/dev_tools.go`), code execution
> (`internal/mcp/code_exec_tools.go`), workflow shell steps
> (`internal/workflow/handlers.go`), and API shell (`internal/api/shell.go`)
> until an operator either installs `bwrap` or sets the opt-in. This is the
> intended behaviour, not a regression — but note it is untestable on the
> primary dev platform (darwin), so `02/01` must exercise the Linux path
> deliberately rather than relying on the default test run.
>
> **Knock-on: this makes `GO-SEC4-006` load-bearing.** That finding (bypassable
> literal-substring command denylist, low severity in isolation) is described
> by the audit as becoming "the ONLY remaining control on Linux without bwrap."
> Fail-closed removes that scenario by default — but the opt-in re-creates it
> exactly. Anyone who sets the knob is relying on the denylist as their entire
> security boundary. `GO-SEC4-006` should be re-weighted accordingly and its
> task cross-referenced from `02/01`.

The single most consequential decision in Wave 1. Today the Linux sandbox
silently falls back to unisolated execution when `bwrap` is unavailable while
still reporting success — the guide's named "silent security degradation"
class. The guide (§4 Wave 1) frames the choice as:

- **(a) Fail closed** — no `bwrap`, no sandboxed execution. Safest; breaks
  any Linux deployment without bubblewrap installed, possibly loudly and at a
  bad time.
- **(b) Explicit, highly visible opt-in** to degraded execution — runs
  anywhere, but only after an operator affirmatively accepts the downgrade,
  and with the sandbox's *reported status* telling the truth.

Note `02/01`'s Touches includes `internal/sandbox/os_other.go`, which the task
file flags as having "the same silent-fallback shape." Whichever way this
goes, it applies to both files — decide once, for the class.

### AD-02 — Linux network allowlist enforcement level

**Status:** decided · **Gates:** `02/01` · **Findings:** GO-SEC4-002 (high)

> **Decided (2026-08-22): fix it properly — move the proxy inside the sandbox
> netns.**
>
> Implement the plan the code already carries as
> `TODO(network-isolation)` at `os_linux.go:141-145`: a socket-passing handoff
> (or a proxy pre-bound to a socket inherited across `unshare`) so the
> allowlist proxy runs co-located with the sandboxed process, making
> `--unshare-net` unconditional.
>
> **What this fixes is an inversion, not a gap.** `os_linux.go:146` applies
> `--unshare-net` only `if len(networkAllow) == 0`. So configuring an allowlist
> *removes* network-namespace isolation and falls back to `HTTP(S)_PROXY`
> convention — meaning **the operator who configures an allowlist gets a
> strictly weaker sandbox than one who configures nothing**, and enforcement is
> bypassed by any process that ignores `HTTP_PROXY` (a raw socket in Go or
> Python, `curl --noproxy`). After this change, an allowlist is strictly
> stronger than no allowlist, which is what operators already assume.
>
> Chosen over disclosure-only because renaming the feature to admit it is
> convention-level would leave the inversion in place, and over fail-closed
> because that disables the allowlist feature on Linux outright while the same
> engineering work is required either way.
>
> `findings.json`'s `GO-SEC4-002` moves `needs-architect-decision` →
> `remediate`: the open question was whether to enforce at namespace level or
> accept proxy-convention, and it is now answered in favour of enforcement.
>
> **Sequencing note:** this is real engineering, not a flag flip, and it is
> larger than AD-01's change. `02/01` should be re-scoped to say so — the two
> land together in the same task, and the task's own estimate predates this
> decision.

Guide §9 item 2, distinct from AD-01: even with isolation present, how
strictly is the network allowlist enforced, and what happens when it can't
be? Decide alongside AD-01 — the same task implements both, and an
inconsistent pair (fail-closed filesystem, best-effort network) is worse than
either coherent answer.

### AD-03 — macOS seatbelt read-boundary: disclose, narrow, or accept

**Status:** decided · **Gates:** `02/02` · **Findings:** GO-SEC4-005 (low)

> **Decided (2026-08-22): disclose. Do not narrow the boundary.**
>
> The unrestricted file reads, mach-IPC, and process inspection permitted by
> `internal/sandbox/os_darwin.go`'s seatbelt profile are an accepted,
> intentional tradeoff. **The code does not change.**
>
> The remediation is the *disclosure* gap. `docs/hardening-phase-plan.md`'s
> Tier 2 description was confirmed by Wave 0 to describe protections the
> shipped code does not actually provide — that inaccuracy is the live harm
> here, because it tells an operator they have a boundary they do not have.
> Correct it to match reality.
>
> `findings.json`'s `GO-SEC4-005` is `remediate`, not `accepted-risk`: real
> work remains. What was accepted is the *code posture*, not the finding as a
> whole. `02/02` is already scoped for exactly this and needs no re-scope.

`02/02` is written as a documentation task on the assumption the tradeoff is
intentional and merely undisclosed — it explicitly says
`internal/sandbox/os_darwin.go` "is not expected to change code-wise unless
the architect decision concludes the tradeoff itself should be narrowed."
That conditional is this decision. Also in scope: `docs/hardening-phase-plan.md`'s
Tier 2 description, which the task file identifies as **inaccurate** today.

### AD-04 — Plugin-install convergence: concrete integration shape

**Status:** decided · **Gates:** `01/01` · **Findings:** GO-PLUGIN-001 (critical),
GO-PLUGIN-002 (critical), GO-PLUGIN-003 (high)

> **Decided (2026-08-22).** Converge `handleCatalogInstall` onto the CLI's
> `install.Installer` pipeline, with these six sub-questions resolved. Four
> were settled by reading current source rather than by preference; two were
> real calls.
>
> **1. Confinement (a real call): `validatePluginID` via `DirStaging`.** Route
> the API path through `install.DirStaging.Commit` and inherit its
> `^[a-z][a-z0-9-]{1,62}$` allowlist plus the atomic backup-then-rename. Chosen
> over `pathsafe.ResolveUnder` because confinement then comes *with* the
> pipeline rather than being a separate call the next handler author can
> forget, and because allowlist validation rejects a bad name outright instead
> of neutralising it. Note this does **not** relieve `08/09` or `12/01` of
> widening the `forbidigo` `ResolveUnder` rule to `internal/api/` — that rule
> guards the handlers this decision does *not* re-plumb.
>
> **2. Archive formats (a real call, and a gap neither the audit nor `01/01`
> caught): keep both, via a format-dispatching `Extractor`.**
> `internal/api/catalog.go:388-391` dispatches on `.zip` vs `.tar.gz`; the CLI
> installer is wired with `&install.TarGzExtractor{}` only. Converging as-is
> would have **silently dropped zip support** and broken any catalog entry with
> a `.zip` archive URL — externally hosted, so possibly not enumerable. Add an
> `install.Extractor` that dispatches on format, reusing the API's existing
> `extractZip`/`extractTarGz`, which the audit already reviewed as soundly
> defended against traversal.
>
> **3. Loader — settled by the code.** `pms.runPluginLoadIntoHost`
> (`internal/api/plugins.go:839`) already exists and is used by all four API
> handlers. A thin `install.Loader` adapter around it. The CLI's `noopLoader`
> does not apply because the CLI runs out-of-process.
>
> **4. Source — settled by the code.** `catalogArchiveSource`
> (`cmd/nanite/plugin_install_flow.go:66-89`) already carries
> sha256/signature/signerKey into `install.Handle` and fits directly. Its only
> couplings are `install.HTTPDownloader` and CLI-side progress emission; the
> API path emits to `pluginHost.EmitPluginInstallProgress` instead, so expect
> to parameterise the emitter rather than fork the type.
>
> **5. Legacy retirement — settled by the code, and narrower than the audit
> implied.** `VerifyChecksum` and `VerifySignature` have exactly **one caller
> each**, both inside `handleCatalogInstall` (`catalog.go:358`, `:367`); they go
> fully dead on migration. But `CatalogFetcher` **must survive** — `cs.fetcher`
> is load-bearing for browse and refresh at `catalog.go:97, 138, 148, 205, 242,
> 281`. Retire `internal/plugin/signature.go`'s two functions; keep
> `internal/plugin/catalog.go`'s fetcher. The audit's "retire the old
> CatalogFetcher" recommendation is wrong on this point.
>
> **6. Wiring shape.** Extract a shared constructor usable from both
> `cmd/nanite` and `internal/api` rather than hand-rolling a second
> `Installer` wiring site — that second site is how the original divergence
> happened. CLI behaviour must not change as a side effect; its existing tests
> pass unmodified.
>
> **AD-05 remains open and separate** — it does not gate `01/01`.

The *direction* is not in doubt — converge the GUI/API path onto the CLI's
already-fail-closed pipeline. The blast radius is why this needs sign-off
before a worker starts rather than discovery mid-implementation. `01/01`'s
"Proposed direction" section already enumerates the six sub-questions in
detail; they are not restated here. In brief: shared constructor vs. second
wiring site; which `install.Source` adapter; which `install.Loader` for
in-process hot-load; **which path-confinement mechanism becomes canonical**
(`pathsafe.ResolveUnder`, used by 3 of 4 API handlers, vs. `validatePluginID`,
used by the CLI's `DirStaging`); whether the other three handlers get
re-plumbed too; and how much of the legacy `CatalogFetcher` survives for the
browse path.

The confinement sub-question is the one most likely to be answered by
accident: pick one and write it down, because `08/09` also hardens paths in
the same file and will otherwise pick differently.

---

## Wave 1 decisions

### AD-05 — DECIDED: remove the default seeded catalog source

> **Decided (2026-08-22): remove it. Do not provision a key.**
>
> After AD-04's fail-closed convergence, a seeded source with no `public_key`
> produces a default that **rejects every install out of the box**. A shipped
> default that always fails is worse than no default — it teaches operators the
> feature is broken rather than that it needs configuring.
>
> Operators add and key their own catalog sources deliberately. Provisioning an
> official key was rejected as real external work with no owner: keypair
> generation, secure private-key custody, a rotation and revocation policy, and
> a signing step in whatever publishes the catalog — none of which exists.
>
> Consequence to state plainly in the implementing task: **no out-of-box
> catalog browsing.** `handleBrowseCatalog`/`handleRefreshCatalog` will have no
> source to list until one is configured. That is intended, and the UI should
> say so rather than render an empty list that looks like a failure.
>
> This has no task file — it was filed as a `01/01` follow-up. Needs one, in
> whichever wave the operator prefers; it touches the seed path, not the
> install pipeline, so it is independent of Wave 3's remaining work.

### AD-05 — Default/official catalog source: provision a real signing key?

**Status:** open · **Gates:** `01/01` (as a follow-up, not a blocker)
· **Findings:** GO-PLUGIN-001 (critical)

`01/01` traced the root of the critical bypass: the default seeded "official"
catalog source's `INSERT` never sets `public_key`, so `sourcePublicKey == ""`
out of the box and verification is skipped with no log line. Failing closed
fixes the *bypass*, but leaves the intended common case failing rather than
verifying. Provisioning a real key is potentially external work (key
generation, distribution, rotation policy) — hence a separate decision.
`01/01` is instructed not to silently scope this in or out.

### AD-26 — Untracked `safego.Go` spawns: adopt an owner, or accept fire-and-forget

**Status:** decided · **Gates:** `04/04` Part B · **Findings:** GO-SVCCORE-002

> **Decided (2026-08-22): selective lifecycle tracking, plus gate hardening.**
>
> `safego.Go` is allowed in `internal/service` only for bounded, best-effort
> external telemetry that may be abandoned without affecting Nanite state,
> and for child goroutines synchronously joined by their caller. Asynchronous
> work that mutates state, drives recovery or worker execution, or invokes
> the plugin extension contract must have a lifecycle owner and observe that
> owner's shutdown context where its API permits.
>
> The current-HEAD audit found **34** sites across seven files, not the task
> file's stale ~18–20 estimate: track 23 asynchronous sites (17 plugin-event
> dispatches, 2 auto-title/tag jobs, 2 recovery jobs, and 2 delegation/worker
> jobs); explicitly retain 10 bounded `ActivityEmitter` HTTP telemetry sends
> as fire-and-forget; and leave the single concurrent tool-batch child
> unchanged because its caller synchronously joins it and its parent is
> already lifecycle-tracked.
>
> Part B also hardens `lifecycle.Manager.Go`'s spawn-vs-shutdown gate. Its
> current separate `closed` check and `WaitGroup.Add` allow a goroutine to
> pass the check, pause while `Shutdown` observes zero work and returns, then
> add and spawn afterward. The closed transition and admission/Add operation
> must be serialized and covered by a race regression test; otherwise moving
> call sites onto the manager does not establish the promised drain boundary.
>
> Direct chat work is owned by `chatServiceImpl.lifecycle`. Composite plugin
> dispatch receives that same manager through composition-root wiring,
> following the existing wake-reactor precedent. The implementation must
> verify plugin-host shutdown ordering; `Container.Shutdown` currently does
> not call `Plugins.Shutdown`, so any remaining host-lifecycle gap is either
> closed in the smallest safe way needed for the drain invariant or recorded
> as an explicit follow-up rather than hidden inside the migration.

**Found by the Wave 2 kickoff author, 2026-08-21** — the same class of gap as
AD-25: `GO-SVCCORE-002` carries `requires_architect_decision: true` in
`findings.json` and had **no corresponding entry in this queue**. Recorded here
so the decision lives in the queue rather than only inside a kickoff prompt,
which is precisely the hole AD-25 exposed.

The finding: ~18 `safego.Go` call sites are bare fire-and-forget spawns with no
owner that `Container.Shutdown()` can drain, unlike one wake-reactor spawn
already migrated to a tracked pattern. Part A of `04/04` (`GO-SVCCORE-001`,
`DelegateAndAggregate`) is unaffected and needs no decision.

The call: adopt the tracked-owner pattern across all ~18 sites, adopt it
selectively where shutdown-drain actually matters, or accept fire-and-forget as
the intended semantics and close the finding as `accepted-risk`. Worth deciding
against a real answer to "what breaks today if one of these is still running at
shutdown?" rather than on principle — the guide's Lifecycle Ownership standard
argues for owners, but 18 mechanical migrations that drain nothing real is the
kind of ceremony it also warns against.

`04/04`'s Part B must not be dispatched until this is decided. The Wave 2
kickoff gates on it.

### AD-27 — Autocomplete `repo_path` enumeration: constrain, or accept the local-operator trust model

**Status:** decided · **Gates:** `08/09` · **Findings:** GO-API-001 (low)

> **Decided (2026-08-22): validate `repo_path` at write time, not at read
> time.**
>
> Constrain where a project's `repo_path` may point when it is *set* — in
> `handleCreateProject`/`handleUpdateProject`, which `08/09` already touches —
> rather than confining every consumer that later reads it. Fixes the class at
> its source, leaves the autocomplete walk untouched, and holds regardless of
> how AD-15's bind option is configured.
>
> **Suggested policy, for `08/09` to confirm rather than assume:** reject the
> filesystem root, the user's home directory *itself* (subdirectories are the
> normal case and must stay allowed), and system directories (`/etc`, `/usr`,
> `/var`, `/System`); require the path to exist and be a directory. That
> directly covers the finding's own stated attack — *"point a project at `/` or
> `$HOME`"* — without inventing new configuration. A stricter alternative, a
> configured projects-root that all `repo_path`s must live under, is cleaner in
> principle but needs new config and a migration for existing rows; take it
> only if the denylist proves leaky in practice.
>
> **Existing rows are not validated retroactively by this change.** Decide in
> `08/09` whether to validate on next update only (simplest, and consistent
> with write-time framing) or to sweep existing `projects` rows. Say which in
> the Work log either way.
>
> Rejected: accept-as-is (AD-15's loopback default makes it defensible, but it
> leaves a remote authenticated caller able to enumerate `$HOME` whenever the
> bind-wide opt-in is used); confining the walk (leaves `repo_path`
> unconstrained for every other consumer); and a bind-conditional constraint
> (mode-dependent security behaviour drifts silently, and the safe path would
> be the one nobody exercises in development).

Found by the Wave 3 pre-flight cross-check, 2026-08-22 — the third instance of
the AD-25/AD-26 pattern. `GO-API-001` carries `disposition:
needs-architect-decision`, meaning Wave 0 deferred the disposition *itself* to
a decision that was never created. Verified still open against current source:
`resolveRoot` (`internal/api/autocomplete.go:136-154`) returns `p.RepoPath`
straight from the store with no validation, so any authenticated caller can
point a project at `/` or `$HOME` and enumerate filenames, sizes, and mtimes
(metadata only, no contents) to depth 8.

**The question is whether that is a vulnerability or the product working as
designed.** The audit's own `false_positive_considerations` says it plainly:
*"in a single-operator local deployment, authenticated caller and the person
who set `repo_path` are the same person."* Wave 0 judged that *"a genuine
disposition-level question, not just an implementation detail."*

**This decision is downstream of AD-15.** AD-15 sets the default auth/bind/TLS
posture. If it lands on "single-user local app" — which
`internal/server/caller_identity.go:14-22` currently documents as the
intentional tradeoff — then AD-27 is plausibly `accepted-risk` and `08/09`
sheds this finding. If AD-15 moves toward auth-by-default or non-loopback bind,
"authenticated caller" stops meaning "the operator" and this becomes real.
**Decide AD-15 first, then AD-27 in its light.** Deciding them independently
risks a permissive bind with an unconstrained walk behind it.

Options: constrain (validate `repo_path` against an allowlist or confine the
walk under a configured root); accept and close as `accepted-risk`, recording
the trust assumption; or defer with AD-15 named as the trigger.

### AD-28 — Catalog archive fetch: add a host/scheme allowlist, or accept operator-configured sources

**Status:** decided · **Gates:** `08/09` · **Findings:** GO-API-003 (low),
GO-SEC4-007 (medium)

> **Decided (2026-08-22): block private, loopback, and link-local destinations
> — and consolidate the CIDR denylist rather than adding a third copy.**
>
> Reject archive URLs resolving into private/RFC1918, loopback, or link-local
> ranges (including cloud IMDS at `169.254.169.254`). This targets the actual
> residual — using the server as a probe into its own network — without
> constraining which *public* hosts a catalog may legitimately vend from.
> Rejected a same-host allowlist: it breaks the common real pattern of a
> catalog on one host pointing at releases on another (GitHub releases, a CDN)
> and would need an escape hatch immediately.
>
> ### This decision also closes `GO-SEC4-007`, which had no implementing task
>
> The denylist already exists **twice** — `internal/mcp/general_tools.go:61-68`
> and `internal/sandbox/proxy.go:37+` — identical today, with the sandbox
> copy's own comment stating the intent is parity *"so both network egress
> paths enforce the same policy"*, and nothing enforcing it. That is
> `GO-SEC4-007`.
>
> **A naive implementation of this decision would add a third copy**, making
> the finding worse while closing a different one. So: extract the CIDR set
> into one shared location that all three consumers import — the sandbox proxy,
> `callWebFetch`, and the new catalog-download guard. The audit's own
> recommendation for `GO-SEC4-007` is exactly this (*"extract the shared CIDR
> set into one place both packages import, or add a test that asserts the two
> lists are identical"*); prefer extraction over a parity test, since this
> decision adds a third consumer and parity tests scale worse than a shared
> constant.
>
> **Tracking correction.** `GO-SEC4-007`'s `task_file` pointed at
> `11-semantic-duplication-migration-drift/07-ssrf-cidr-denylist-duplication.md`,
> which explicitly disclaims being an implementation task and says the real work
> lives in `08-remaining-security-hardening/` — where no such task was ever
> written, because that folder was empty when `11/07` was authored. The finding
> was therefore an orphan: flagged, dispositioned `needs-architect-decision`,
> and implemented by nothing. Reassigned to `08/09`.

Same pre-flight cross-check, same missing-entry pattern. **But this finding is
now half-resolved, and by work that landed after Wave 0 measured it** — worth
knowing before deciding, because it narrows the question considerably.

`GO-API-003` was *"`http.DefaultClient` with no explicit timeout and no
host/scheme restriction."* `01/01`'s convergence (AD-04) removed
`http.DefaultClient` from `internal/api/catalog.go` entirely; the download now
runs through `install.HTTPDownloader` (`catalog.go:339`), whose `Download`
applies `DefaultDownloadTimeout` (2 minutes) and `DefaultMaxArchiveBytes`
whenever the zero value is left in place — so **the timeout half is fixed, and
a size cap the finding never asked for came with it.**

What remains is only the host/scheme allowlist: nothing in
`internal/plugin/install/download.go` restricts the target beyond http/https.
Wave 0's own note anticipated this split — *"the timeout half is
uncontroversial but the disposition as a whole (accept current trust model vs.
add restriction) needs architect judgment."* The uncontroversial half is done;
only the judgment call is left.

The case for accepting: per the audit's own
`false_positive_considerations`, *"adding a catalog source is itself an
explicit, privileged, operator-initiated action"* — an allowlist constrains
someone who already had to be trusted to add the source. The case against: this
is the standard package-manager-registry SSRF shape, and a compromised or
malicious catalog source controls `entry.ArchiveURL` without further operator
involvement.

**Related, not duplicate:** AD-05 (provision a real signing key for the default
seeded catalog source) addresses whether catalog *content* is trustworthy;
AD-28 addresses where the fetch may *go*. Signature verification failing closed
(AD-04, landed) already blunts the payload risk, so what an allowlist adds is
protection against the *request itself* as a probe — internal network
enumeration from the server's vantage point. Weigh it on that, not on payload
trust, which is already handled.

### AD-33 — Dead skill-mode/source functions: retire or wire

**Status:** decided · **Gates:** `13/01` · **Findings:** GO-STORE-008 (low)

> **Decided (2026-08-24): retire. Delete all four.**
>
> `ParseSkillModeIDs`, `MarshalSkillModeIDs`, `SkillMatchesMode`
> (`internal/store/skill_mode_filter.go`) and `ClassifySkillSource`
> (`internal/store/skills_source.go`) — verified at HEAD with **zero external
> non-test callers**, still dead after six waves.
>
> The decisive evidence is circumstantial but strong: they sat **untouched
> while the skills-index redesign** (migrations `136`/`137`, `TASKS/skills/02`)
> reworked the entire skills subsystem around them. A subsystem rewrite that
> steps over four functions is telling you they are not part of it.
>
> Same wire-vs-retire shape as AD-06 through AD-11, decided the same way five
> of those six were. `ClassifySkillSource`'s described FE gating was considered
> for wiring and rejected: building a feature to justify existing code is
> backwards, and nobody has asked for that gating.
>
> Deleting also disposes of a doc comment asserting a live caller in
> `internal/service/ingest.go` that does not exist — the `failure-modes.md`
> "documents assert false things" class, and the specific false claim the audit
> called out.
>
> **Delete the tests with the functions.** Tests for retired code are not
> coverage; they are maintenance on something nothing calls.

### AD-30 — Task-ID citations in permanent production comments

**Status:** decided · **Gates:** `13/02` · **Findings:** GO-STORE-009 (informational)

> **Decided (2026-08-24): trim pure citations, keep rationale.**
>
> Remove comments whose entire content is a task-ID (`CW-YYYYMMDD-NNNN`) or a
> `TASKS/*.md` pointer. **Keep** any comment that explains an invariant or a
> *why*, even where it also cites a task.
>
> This is the `failure-modes.md` "documents assert false things" class: a
> comment pointing at a task file that may no longer exist sends a reader
> hunting for context that is gone, and the pointer looks authoritative while
> it does so. Roughly 40 task IDs and 23 `TASKS/*.md` paths across 25+ files.
>
> **This needs judgment per comment, which is why it was a decision and not a
> `sed`.** A worker who bulk-deletes every line matching the pattern will
> destroy load-bearing rationale. If a comment would still be useful with the
> citation stripped, strip the citation and keep the comment.

### AD-31 — `internal/chat` vocabulary-vs-wiring split

**Status:** decided · **Gates:** `13/05` · **Findings:** GO-CHAT-008 (informational)

> **Decided (2026-08-24): accept for this batch, and file a follow-up for
> after audit remediation completes.**
>
> The observation is real — `internal/chat` mixes broadly-consumed
> wire-vocabulary types with subsystem-specific response-handler wiring, so a
> caller wanting only `Envelope`/`ResponseV1` transitively pulls an
> 11-package graph. But splitting it is a Wave-5-sized architecture refactor,
> and this batch already ran its architecture wave with deliberately bounded
> scope. Doing it inside mechanical cleanup would be exactly the
> "mix large mechanical cleanup into semantic remediation" the guide warns
> against.
>
> **Not urgent, schedulable whenever resources allow.** Recorded in
> `14-followups/README.md`'s post-remediation backlog, not the in-batch
> candidate register — this is deliberately *after* the batch, not pending
> within it.

### AD-32 — Context pipeline: relevance contract and token estimators

**Status:** decided · **Gates:** `13/05` · **Findings:** GO-MEM-004,
GO-MEM-005 (both informational)

> **Decided (2026-08-24): fix the relevance contract now; accept the estimator
> divergence, with a post-remediation follow-up to converge both properly.**
>
> **`GO-MEM-004` is correctness-adjacent despite its severity label.** Three
> `contextbroker` sources (`source_pcc.go`, `source_memory.go`,
> `source_conduit.go`) each compute `ContextItem.Relevance` by an unrelated
> method, and the results are merged and cross-compared on one global scale.
> Ranking across differently-scaled inputs is arbitrary by construction — the
> same defect class as the unbounded-score bug AD-07 disposed of by deleting
> the Hadron gate. Document and enforce a shared 0–1 normalization contract.
>
> **`GO-MEM-005` is accepted for now.** `internal/context/tokens.go` uses
> `len/4` (floors); `internal/contextbroker/broker.go` uses `(len+3)/4`
> (rounds up). Confirmed at HEAD. The divergence is a few tokens, and
> converging properly needs a new shared lower-level package to respect
> `contextbroker`'s deliberate one-way dependency away from `internal/context`
> — structure disproportionate to a rounding difference *inside this batch*.
>
> **The follow-up covers both**, so the eventual fix is one coherent pass over
> the context pipeline's measurement contracts rather than two disconnected
> edits. Filed in `14-followups/README.md`'s post-remediation backlog.

### AD-25 — `allow_unsigned_plugins` devmode bypass: wire or retire

**Status:** decided · **Gates:** `01/02` · **Findings:** GO-PLUGIN-008 (low)

> **Decided (2026-08-22): wire it.**
>
> `buildInstaller` (or its post-AD-04 successor, once `01/01`'s shared
> constructor lands) reads `user_settings.allow_unsigned_plugins` and sets it
> on the constructed `SignatureVerifier.AllowUnsigned`, making the setting's
> documented effect real on a `devmode`-tagged build. Production (`!devmode`)
> builds are unaffected either way — `devmode.HostDevSigningBypass` compiles
> to `false` outside `devmode` builds, so `AllowUnsigned`'s value is dead-code-
> eliminated there regardless of this decision.
>
> **Why wire rather than retire, given the rest of this wave is hardening the
> same trust boundary elsewhere:** the tension `01/02`'s own file raises is
> real but not disqualifying — `01/01`/`02/01`/`03/01` all close paths where
> an *untrusted, external* input (a catalog entry, a missing sandbox tool, an
> agent slug) reaches a security-relevant sink without validation. This
> setting is the opposite shape: a developer explicitly opts in, on a build
> that is never shipped to production, to skip signature checks on plugins
> they are installing on their own machine. Wave 0's own revalidation of this
> finding (`findings.json`'s `GO-PLUGIN-008` entry) already concluded
> `requires_architect_decision: false` and "no architect input needed" for
> the same reason — this decision formalizes that call as a real `AD-NN`
> record rather than leaving it resting on the task file's own contradictory
> header, which is the gap this decision closes.
>
> **Scope stays as `01/02`'s file already specifies:** thread the setting
> through, do not remove `user_settings.allow_unsigned_plugins`'s storage/API
> surface (that's a separate, larger follow-up if ever wanted), and correct
> `verify.go`/`devmode_on.go`/`devmode_off.go`'s doc comments to match
> whatever the final construction site looks like once `01/01` lands.
>
> `findings.json`'s `GO-PLUGIN-008` disposition (`remediate`) is unchanged —
> this decision doesn't move it, it resolves the task file's own
> `requires_architect_decision: true` header against the finding record's
> `false`, in favor of proceeding.

`01/02`'s own header claimed `requires_architect_decision: true` with no
matching `AD-NN` entry anywhere in this file — a real planning-pass gap, not
a decision anyone had made (see the W1 kickoff's item D). Found and closed
during Wave 1 dispatch prep, 2026-08-22, by direct operator confirmation
rather than a default guess.

---

## Wave 2 decisions

### AD-17 — `cmdServe` fatal-path cleanup: direction

**Status:** decided · **Gates:** `07/02` · **Findings:** GO-RUNTIME-001 (low)

> **Decided (2026-08-22): `cmdServe` returns `error`; `main()` does the exit.**
>
> Change `cmdServe`'s signature to return `error` and let `main()`'s
> `case "serve":` branch perform the `os.Exit`. No new mechanism, no cleanup
> registry — the deferred cleanups already exist and are already correctly
> placed; they simply never fire today. Rejected the `slogx` cleanup-hook
> alternative because it introduces a process-global exit hook nobody owns and
> a second shutdown path competing with the defers.
>
> **This finding's "low" severity undersells it.** `slogx.Fatal` is
> `slog.Error` + `os.Exit(1)` (`internal/slogx/slogx.go:138-141`), and
> `os.Exit` runs **no deferred functions**. `cmdServe` registers four:
> `logCloser.Close()` (`main.go:143`), `otelShutdown(otelCtx)` (`:174`),
> `s.Close()` (`:182`, the store), and `coordStore.Close()` (`:331`). There are
> seven `slogx.Fatal` sites (`:180 :186 :189 :199 :406 :433 :824`), and
> **`:824` is the terminal statement of `cmdServe`** —
> `if err := srv.ListenAndServe(); err != nil { slogx.Fatal(...) }`. So *every*
> abnormal server exit drops telemetry spans, leaves the log buffer unflushed,
> and skips both SQLite closes. Fix all seven sites, not just the terminal one.

`slogx.Fatal` in `cmdServe` bypasses cleanup. Two shapes, per the task file:
change `cmdServe`'s signature (and `main()`'s `case "serve":` branch) to
return errors instead of exiting, or add a cleanup-hook mechanism to
`internal/slogx`. The first is more honest and more invasive; the second is
narrower and risks becoming a general-purpose exit hook nobody owns.

Low severity, but it gates `08/07` and `11/10`, which both edit the same file
later — so decide it early rather than letting it drift to the back.

### AD-18 — Background job registry: retention policy

**Status:** decided · **Gates:** `07/03` · **Findings:** GO-RUNTIME-004 (medium)

> **Decided (2026-08-22): TTL plus a hard count cap, and evicted must not read
> as unknown.**
>
> Evict completed jobs on a TTL with a ceiling on retained records.
> **`Status`/`Result` must return a distinct "expired" state, not not-found** —
> silently turning "job succeeded" into "job unknown" is a different bug, not a
> fix. Rejected count-only LRU (a burst can evict a result before its owner
> reads it, with no time guarantee) and persist-to-store (needs a migration
> this batch otherwise claims none of, and turns a correctness task into a
> storage feature).
>
> **The leak has a concrete rate.** The only `delete(svc.jobs, jobID)` in
> `internal/background/service.go` is on the immediate `backend.Start` failure
> path (`:126`). Every job that actually *starts* is retained for the process
> lifetime, each holding up to `DefaultMaxOutputBytes` (1 MiB) of captured
> output. A long-running service leaks roughly 1 MiB per background job,
> indefinitely.
>
> The doc-comment fix — `PTYBackend.Status` falsely claims completed jobs are
> "reaped" — is required regardless and is no longer gated on anything.

`internal/background`'s job registry grows unbounded. The doc-comment half of
`07/03` is required regardless; the retention half needs a policy — TTL, max
count, completion-based eviction, or persist-and-evict. The relevant tradeoff
is what `Status`/`Result` callers are entitled to expect after eviction; a
policy that silently turns "job succeeded" into "job unknown" is a different
bug, not a fix.

### AD-14 — How far to narrow `internal/store` dependencies

**Status:** decided · **Gates:** `10/03`, `06/02`, `06/03` · **Findings:**
GO-DEP-002, GO-STORE-001, GO-STORE-005

> **Decided (2026-08-22), in two halves.**
>
> **Context propagation (`GO-STORE-005`): full sweep — all 371 methods.**
> Executed as a **standalone mechanical task, `06/03`, outside the wave
> structure**, handed to an external session so it stays out of the main
> workstream. Measured scope: 67 non-test files, 505 exported `*Store` methods
> of which 371 lack `ctx`, 368 non-context `database/sql` calls, callers across
> 32 packages.
>
> **Isolation is the binding constraint, not a preference.** This sweep is
> structurally incompatible with concurrent work — same hazard class as the
> repo-wide `gofmt` sweep (AD-22): it must run alone from a clean `main` and
> land in one merge, because a half-swept package does not compile. It
> conflicts directly with `06/01` (which edits `agents.go`, one of the two
> zero-adoption hot tables), `06/02`, `11/13`, `13/01`, `13/02`, and in
> practice any task touching a caller package — `internal/service` alone holds
> 76 direct `*store.Store` references.
>
> `GO-STORE-005`'s `task_file` moves from `06/02` to `06/03`, and **`06/02` is
> re-scoped to its remaining two findings** (`GO-STORE-004`, `GO-STORE-006`),
> which shrinks it considerably and removes its architect gate.
>
> **Narrow interfaces (`GO-STORE-001`, `GO-DEP-002`): none. Accepted as-is.**
> Following the guide's own directive — *"gravitational-package review, not a
> mandatory split… narrow consumer-defined interfaces only where they solve
> demonstrated coupling/testability problems"* — and its explicit guardrail
> against *"creat[ing] repository interfaces everywhere because Store has high
> fan-in."* Both findings move to `accepted-risk`. The two existing
> counter-examples (`dispatch.TrustResolver`, `grounding.ConsultationLogger`)
> stand as the pattern for when one *is* warranted. `10/03` remains
> review-note-only with no code changes.
>
> *(This half was not explicitly stated in the operator's answer, which
> addressed the sweep. It applies the guide's documented default and is
> recorded here so it is visible and correctable rather than silently assumed.)*

> **Supplemental `10/03` operator disposition (2026-08-23).** The operator
> expressly approved treating the remaining three shape findings separately:
>
> - **`GO-DEP-001` / `Container`: confirmed false-positive as a decomposition
>   target.** Its current size is accepted because it remains a wiring and
>   lifecycle composition root; no decomposition is scheduled.
> - **`GO-STORE-002` / `Store` method breadth: no blanket split.** The
>   no-new-interface decision above remains binding. A consumer-defined narrow
>   interface is justified only when a specific named consumer demonstrates
>   concrete coupling or testability friction.
> - **`GO-PLUGIN-006` / plugin `Host`: selective, pressure-driven
>   decomposition only.** The existing `GO-PLUGIN-004` `UnloadPlugin` work is
>   the concrete growing pain and the coordination point. Its already-scoped
>   teardown-helper extraction proceeds; a later sub-registry extraction is
>   justified only for a named registration category whose ownership,
>   teardown, locking, or tests remain materially clearer after that step. An
>   all-category migration sweep is explicitly rejected.
>
> The operator's governing rationale is to address growing pains as they
> appear, rather than decompose for the sake of lowering field or method
> counts. This is the approved balance for closing `10/03`.

The guide is unusually directive here and the decision should not relitigate
it: *"Treat as a gravitational-package review, not a mandatory split. Add
narrow consumer-defined interfaces only where they solve demonstrated
coupling/testability problems."* Its guardrail list also forbids *"creat[ing]
repository interfaces everywhere because Store has high fan-in."*

The live question is narrower: `06/02` proposes package-wide method-signature
changes for `GO-STORE-005` (context propagation), which is a large mechanical
diff across a high-fan-in package. Is that in scope now, deferred, or
accepted? Listed in Wave 5's grouping but gates a Wave 2b task — decide it by
Wave 2b.

---

## Wave 3 decisions

### AD-15 — Default auth / bind / TLS / startup-warning posture

**Status:** decided · **Gates:** `08/07`, and informs AD-27 · **Findings:**
GO-RUNTIME-002 (high)

> **Decided (2026-08-22): the audit's literal recommendation — bind loopback by
> default, require an explicit opt-in to bind wide, and always log auth status
> at startup.**
>
> Three concrete changes:
> 1. `server.go:163` — `addr := fmt.Sprintf(":%d", s.port)` becomes
>    `127.0.0.1:<port>` unless an explicit bind-all/bind-address option is set.
>    That option does not exist today and must be added (`internal/config`).
> 2. `server.go:164` — the startup line reports `addr` and `dev` but never auth
>    status. It must always state whether auth is enabled, and warn when it is
>    not. This is the same silent-degradation class AD-01 closed: the service
>    must not be quiet about running unauthenticated.
> 3. `auth.go:15-22` — `basicAuthMiddleware` returning `next` unmodified when
>    both env vars are unset stays as-is behaviourally, but is no longer
>    *silent* after (2).
>
> **TLS is explicitly out of scope.** It is ceremony on loopback, and a reverse
> proxy is the right answer for the wide case. `08/07` should not add it.
>
> ### ⚠ This is a breaking change for existing deployments — treat it as one
>
> Anything currently relying on the bind-all default stops working on upgrade:
> Docker port mapping, LAN access, remote dev, any reverse proxy pointed at a
> non-loopback interface. There is no deprecation window, and the failure mode
> is silent from the operator's side — the service starts fine and simply
> stops being reachable.
>
> `08/07` must therefore: name the new opt-in option in its Done-means, and
> produce operator-facing release-note text stating the change, the symptom
> ("service starts but is no longer reachable from other hosts"), and the
> one-line fix. A migration note is part of this task, not a follow-up.
>
> Rejected: warning-only (leaves the substantive half of the batch's last
> high-severity finding open) and accept-as-is (would close a high finding with
> no observable change). Also considered and not taken: coupling auth to bind
> width rather than defaulting to loopback — more surgical, but it lets a
> misconfiguration stay reachable, and the operator chose the stronger default.
>
> **Downstream:** AD-27 (`GO-API-001`, unconstrained autocomplete walk) was
> deliberately sequenced after this. A loopback default means an authenticated
> caller is on the same machine, which strengthens the accept case for AD-27 —
> but the bind-wide opt-in is exactly where that reasoning stops holding.
> Decide AD-27 with that conditional in view.

The highest-severity finding in Wave 3. What does Nanite bind to by default,
does it require auth by default, is TLS expected/optional/absent, and what
does it say at startup when the posture is permissive? This is a product
decision as much as a security one — a local-first dev tool and a shared
service want different defaults, and `08/07` touches `internal/config` and
`cmd/nanite`'s composition root either way.

### AD-16 — `permission.Engine` `ModeDefault`: does it prompt for writes?

**Status:** decided · **Gates:** `08/04` · **Findings:** GO-SEC4-003 (medium)

> **Title corrected 2026-08-22.** This decision was originally filed as
> "file/directory permission policy (default mode)" from the guide's §9 item 8.
> That is not what `GO-SEC4-003` is about — "default mode" here means
> `permission.Mode`'s `ModeDefault`, not filesystem mode bits. No filesystem
> permission question is in this batch.
>
> **Decided (2026-08-22): `PathGrants` governs writability. The code is right
> and the comment is stale — fix the comment.**
>
> `ModeDefault`'s const comment (`engine.go:25`) promises *"prompt for
> destructive/write operations"*, but the switch (`engine.go:171-178`) asks on
> destructive, allows on read-only, and falls through to Allow otherwise —
> so `dev_write`/`dev_edit`, which match neither name heuristic, are allowed
> silently. `ModeAcceptEdits` has the missing `{false,false}` branch right next
> to it, which is what makes the omission look like a bug.
>
> It is not. Writes are governed by `PathGrants`
> (`internal/permission/path_grants.go`) — session-scoped, explicit-mention
> grants with a documented "no nag-again" philosophy. Per-call prompting for
> every write would contradict that design, not complete it. Rejected adding
> the Ask branch (re-introduces nagging, changes default UX for every write in
> the product) and the PathGrants-consulting hybrid (largest change, and
> unnecessary if the architecture is already as intended).
>
> ### The decision has a falsifiable premise — `08/04` must test it, not assume it
>
> This rests on `Check()` actually consulting `PathGrants` on the write path.
> `Check()` (`engine.go:117-122`) reads `e.sessionGrants[sessionID]`, which is
> **not obviously the same thing** as `PathGrants`. `08/04`'s first step is to
> trace it and confirm.
>
> **If `PathGrants` does not in fact gate writes reached through `ModeDefault`,
> this decision's premise is false and the finding is a live gap, not a stale
> comment.** In that case `08/04` must stop and re-open AD-16 rather than
> fixing the comment to describe a guarantee nothing provides — which would
> convert a code bug into a documentation lie. Make that verification an
> explicit Done-means item.

Guide §9 item 8. `08/04` covers the permission engine's default-mode write
gap; the underlying question is what the project's default file/directory
mode policy *is*, stated once, so the engine can enforce something written
down rather than a value chosen at one call site.

---

## Wave 4 decisions — the six production islands

> ## ✅ ALL SIX DECIDED (2026-08-22) — five retire, one wire
>
> | | Island | Call | Scale removed / added |
> |---|---|---|---|
> | AD-06 | Grounding memory recall | **retire** | −534 prod / −361 test |
> | AD-07 | Hadron context gate | **retire** | −302 prod / −337 test |
> | AD-08 | Team semantic routing | **wire** | +1 call site |
> | AD-09 | Tool builder / YAML | **retire except `cache.go`** | −~1.8k prod / −~1.7k test |
> | AD-10 | Reasoning-augmented selection | **retire** | −342 prod / −210 test |
> | AD-11 | Curated tool-knowledge matcher | **retire** | −405 prod / −162 test |
>
> Roughly **3,400 production lines and 2,600 test lines deleted**, against one
> added call. Wave 4 is therefore mostly a deletion wave — mechanically
> low-risk, but every retire must confirm nothing outside the island imports
> what it removes. Decided against `00/01`'s reachability evidence, which
> re-confirmed all six still unwired at frozen HEAD.
>
> **AD-07 — the operator's reasoning, recorded because it generalises:** the
> Hadron gate is *"code in core that's specific to another application."*
> That is an architectural boundary argument, not a reachability one — it would
> hold even if the gate were wired. Apply the same test to anything similar
> that surfaces later. It also disposes of the latent unbounded-relevance-score
> bug (additive scoring with no clamp against a documented 0.0–1.0 contract)
> that `GO-MEM-002` warned would corrupt cross-source ranking if revived
> unfixed — deleting the gate removes the hazard rather than deferring it.
>
> **AD-08 — why this one is wired when five are retired:** `team_routing.go`
> carries **927 lines of tests against 771 of implementation**, and
> `TASKS/teams/HANDOFF.md` named the missing wiring as a known risk rather than
> leaving it accidental. That is a feature that ran out of runway one call
> short, not an abandoned experiment. Wiring is `InstallTeamRunRouting` from
> `handleLaunchTeam`, plus the four-step reachability proof.
>
> **AD-09 — verified live surface, because the audit's claim was worth
> checking:** only `cache.go`'s `NewResultCache` / `ResultCache` /
> `ResultCacheConfig` are used outside the package, by
> `internal/service/chat.go` and `container.go`. Delete `adapt.go`,
> `builder.go`, `register.go`, `tool.go`, `yaml_loader.go` and their tests; the
> package survives as `cache.go` alone. **`tool.go` holds the package doc that
> falsely calls this "the primary way to construct tools in Go code" — that doc
> must not be carried over to `cache.go`.** Write a new one describing a
> result-cache package.
>
> **AD-10 + AD-11 — decided as one question**, since both answer "what tools
> match this intent" alongside the live `intent.go` (119 lines). Three
> mechanisms, two dead. Retiring both leaves one. Also closes 2 of the audit's
> previously-unjudged complexity outliers, which `ranking.go` accounts for.
> `ranking.go` was orphaned when an *earlier task in this project* removed its
> debug-SQL consumer — worth noting as a reminder that removals create islands.
>
> ### Deletion coupling — three tasks inherit scope changes
>
> - **AD-06 reaches into `internal/selftools`.** Retiring grounding must also
>   remove `self_tools_transport.go:219-231` (both fields and their comments)
>   and `self_tools_dispatch.go:109-118` (the E2 pre-strategy recall block).
>   That shrinks `10/02`'s decomposition surface and touches a file `11/11`
>   also edits.
> - **AD-07 and AD-09 shrink `13/01`.** Dead code those tasks delete is dead
>   code `13/01` no longer has to. Re-derive `13/01`'s list after Wave 4 rather
>   than trusting its authored version.
> - **AD-10/AD-11 shrink `11/12`**, which touches `internal/toolclient/broker.go`.

For each, choose exactly one:

- **wire** — intended feature; connect the real production path and prove it
  end to end: `production entry point → construction/registration/wiring →
  feature invocation → observable behavior`. Unit tests alone do not close a
  wire decision (guide §11: *"mark a feature done based only on unit tests"* is
  on the do-not list).
- **defer** — intentionally staged. Record a **trigger** and an **owner**, and
  ensure no doc claims it is live. Deferred code must not impose boot/runtime
  cost.
- **retire** — remove implementation, tests, and docs.

**Check current source first.** The guide warns some islands may have been
completed after the audited commit, and this batch has 40 commits of drift —
Wave 0 (`00/01`) should report each island's current reachability *before*
these decisions are made. Do not decide AD-06…AD-11 ahead of `00/01`.

| ID | Island | Finding | Task | Notes |
|---|---|---|---|---|
| AD-06 | Grounding memory recall | GO-MEM-001 | `09/01` | Wiring point is `internal/service/container.go`; also touches `selftools` transport fields and the E2 pre-strategy recall block |
| AD-07 | Hadron context gate | GO-MEM-002 | `09/02` | Wiring point is the composition root's `sources` slice (4 live `ContextSource`s today). Not flagged in `findings.json` — see "A gap worth naming" |
| AD-08 | Team semantic routing | GO-SVCEXEC-003 | `09/03` | Relates to the completed `TASKS/teams/` batch — check that batch's HANDOFF for original intent before retiring |
| AD-09 | Tool builder / YAML architecture | GO-MCPTOOL-001 | `09/04` | 5 dead files in `internal/tool/`; `result_cache.go` is the package's one live export and must survive any retire |
| AD-10 | Reasoning-augmented tool selection | GO-MCPTOOL-002 | `09/05` | `internal/toolclient/ranking.go` |
| AD-11 | Curated tool-knowledge matcher | GO-MCPTOOL-003 | `09/06` | Self-contained (`tool_knowledge.go` + its test). Not flagged in `findings.json` — see "A gap worth naming" |

A note on sequencing that is easy to miss: **AD-09's outcome changes `13/01`'s
scope.** If the tool-builder island is retired, its files become dead-code
removal; if wired, they must not be touched by the mechanical cleanup wave.
The same coupling exists between AD-07 and `13/01`'s `internal/contextbroker`
entries.

---

## Wave 5 decisions

### AD-12 — `chatServiceImpl` / `generateResponse` decomposition boundaries

**Status:** decided · **Gates:** `10/01`, implementation task `10/04` ·
**Findings:** GO-SVCEXEC-001 (high), GO-SVCEXEC-002 (high)

> **Decided and expressly approved by the operator (2026-08-23): implement the
> reviewed six-phase action pipeline incrementally.** `generateResponse`
> remains the coordinator: it owns control flow and routing, including the
> provider/tool loop, retry-versus-finish decisions, cancellation, and terminal
> cleanup. Each extracted action owns one step's local logic and returns an
> explicit result to the coordinator; actions do not call the next action or
> hide the state machine behind callbacks.
>
> The approved phase boundaries are: (1) resolve and assemble the turn,
> (2) open the stream and initialize the run, (3) govern an iteration and make
> the provider request, (4) consume and normalize one provider stream,
> (5) settle tools and decide continuation, and (6) finalize, persist, and
> close the run. Begin with named methods and explicit state/outcome structs
> such as `turnSetup`, `runState`, `providerAttempt`, and `turnResult`; do not
> create six independent service objects merely to reduce method counts.
>
> **Reasoning.** This matches the familiar action/pipeline pattern: the outer
> workflow remains readable as a sequence of transitions while each step owns
> its own policy and mechanics. It reduces accidental complexity without
> dispersing the essential orchestration state or obscuring loop semantics.
> The characterization suite from `10/01` already protects the production
> door, error recovery, compaction, and cancellation paths, making incremental
> extraction safer than a rewrite. Extract one phase at a time and rerun the
> focused behavior, race, and complexity checks after every phase.
>
> **Scope fence.** This decision does not authorize a wholesale
> `chatServiceImpl` split. The runtime-session lifecycle cluster remains a
> possible later type extraction only if the action work exposes concrete
> ownership or testability pressure. A full rewrite is rejected for this
> phase.

### AD-13 — `SelfToolsTransport` decomposition boundaries

**Status:** decided · **Gates:** `10/02`, implementation task `10/05` ·
**Findings:** GO-MCPTOOL-006 (medium)

> **Decided and expressly approved by the operator (2026-08-23): implement
> selective delegation beginning with the four boundaries recommended by the
> reviewed capability map.** Keep `SelfToolsTransport` as the MCP catalog and
> dispatch adapter. Move real behavior and its cohesive dependencies into:
> (1) messaging plus session handoff, (2) todo/plan work tracking, (3) agent
> profile management plus source resolution, and (4) combined card/panel
> presentation.
>
> **Reasoning.** These four groups have real internal cohesion, shared policy,
> or exclusive dependencies. The other domains are either already delegated,
> too small/stateless to justify another layer, or cross load-bearing seams
> that should stay singular. The combined presentation boundary is especially
> important: card render-target resolution and panel operations must continue
> to share one trust/access gate. This is a deliberate balance between one
> overgrown transport and decomposition for its own sake; further delegation
> should happen only when growing pains demonstrate ownership, coupling, or
> testability value.
>
> Each move must transfer meaningful logic and use narrow dependencies where
> practical; hollow wrappers and duplicated policy are out of scope. Preserve
> static tool definitions, `ListTools`, `CallTool` routing, request context,
> response behavior, and tool names as the transport contract. Add direct
> behavioral coverage for the moved handlers, with particular attention to
> the messaging operations that `10/02` found thinly covered.

---

## Wave 6 decisions

### AD-19 — Duplicated semantics: share implementation vs. parity tests

**Status:** decided · **Gates:** all of Wave 6a · **Findings:** GO-SVCEXEC-004,
GO-API-007, GO-CHAT-002, GO-INFRA-004 (+ the rest of `11/`)

> **Decided (2026-08-23): resolve per instance, favoring shared implementation
> for migration drift and boilerplate, and parity/sync tests for deliberately
> independent paths.**
>
> - `11/01` — migration drift. Migrate `resolveSubagentCompletionPolicy` onto
>   the same `override.Resolve` cascade primitive used by message wake policy;
>   preserve separate default-policy configuration and add a shared cascade
>   regression test.
> - `11/02` — ambiguous Harness v1/native handler duplication. Consolidate
>   the durable-agent start/resume/wake handler implementation unless the
>   required caller enumeration proves Harness v1 is a deliberate,
>   version-stable external contract. If it is deliberate, keep separate
>   implementations but document the boundary and add a parity/contract test.
> - `11/05` — textual boilerplate. Extract the duplicated post-`CallTool`
>   result-processing tail into one private helper; preserve both public entry
>   points.
> - `11/06` — confirmed semantic divergence. Unify malformed streamed
>   tool-call JSON behavior on Anthropic-style graceful degradation, with
>   regression coverage and downstream consumer verification for
>   `EventError`-without-`EventDone`.
> - `11/07` — no implementation here. Confirm the cross-reference remains
>   correct: `GO-SEC4-007` was reassigned to `08/09` under AD-28.
> - `11/09` / `GO-CHAT-004` — resolve after AD-29. If AD-29 deletes the dead
>   client-side elicitation path and `routeClientElicitation` disappears, close
>   the duplication as a side effect. If anything survives, unify the surviving
>   elicitation paths onto one explicit `context.Canceled` contract.
> - `11/10` — plumbing duplication. Replace the three independent envelope
>   registry setter/copy sites with one shared holder or one shared wiring
>   call; do not build hot reload/test-isolation features in this task.
> - `11/11` — intentionally independent. Do not merge the two
>   `dispatch_to_agent` evaluators; add a sync/parity test for their shared
>   reflex-row interpretation only.
>
> `GO-MCPTOOL-004` does not fit AD-19's share-vs-parity shape. It is a
> wire/delete decision and is recorded separately as AD-29.

### AD-20 — `internal/config` naming collision: rename direction

**Status:** decided · **Gates:** `11/08` · **Findings:** GO-INFRA-001 (low)

> **Decided (2026-08-22): give both types names that say what they are.**
> `Config` inside a package named `config` carries no information; renaming
> only one leaves the asymmetry.
>
> **The task file's scope warning is wrong and should not be trusted.** `11/08`
> says the fix "may reach every caller of `config.Config` and
> `config.AppConfig` across the tree." Measured at HEAD: **11 references
> total** — `config.Config` ×5, `config.AppConfig` ×6 — across
> `cmd/nanite/main.go` and three files in `internal/service`
> (`chat.go`, `container.go`, `slot_stash.go`). This is a bounded rename, and
> `11/08` no longer needs to run alone.

### AD-29 — Client-side elicitation dead code: build or delete

**Status:** decided · **Gates:** `11/09` · **Findings:** GO-MCPTOOL-004 (low)

> **Decided (2026-08-23): delete.**
>
> The standing rule on dead code applies: do not push dead code forward into a
> newly-built feature just because stale docs describe it. Git history is the
> recovery path if the project later wants this direction. `11/09` should
> re-confirm that `ClientElicitMiddleware` still does not exist and that the
> client-side functions are still dead, then remove the dead client-side
> elicitation functions and correct the package doc to describe only live
> code.
>
> This decision runs before `GO-CHAT-004` inside the same task. If deleting
> `routeClientElicitation` removes the duplicate half, `GO-CHAT-004` is closed
> as a side effect and the worker must record that explicitly. If any
> client-side path survives for a concrete reason, its cancellation contract
> must be unified or documented per AD-19.

---

## Wave 7–8 decisions

### AD-21 — DECIDED: regression-gate everything, zero-tolerance on the correctness three

> **Decided (2026-08-22): baseline every linter and fail on any increase —
> plus require `errcheck`, `errorlint`, and `nilerr` to reach and stay at
> zero.**
>
> The regression half is the guide's own instruction and is immediately
> actionable. The measured drift justifies it empirically: across 40 commits of
> ordinary development, gosec +35, cyclop +25, gocyclo +25, gocognit +16,
> errcheck +10. Nothing was watching.
>
> The correctness three are singled out on evidence, not taste: **`nilerr` is
> the linter that already caught `GO-STORE-003`** (high severity) and was
> ignored because the fast gate runs `--new` and cannot see pre-existing
> findings in untouched code.
>
> **Implementation-time update (2026-08-23):** the current correctness-three
> backlog is errcheck 281 + errorlint 48 + nilerr 24 = **353**, and task
> `14/02` now owns reducing all three to zero before Stage 2 activates. This
> supersedes the earlier measurement and unowned-paydown statements below;
> the two-stage decision itself is unchanged.
>
> ### ⚠ The zero-tolerance half has a 365-finding prerequisite that no task owns
>
> Current counts: **errcheck 294, errorlint 49, nilerr 22 = 365**. `13/04`
> (low-risk error-handling batch) touches five files — it is nowhere near this.
> **No task in the batch owns that paydown.**
>
> So `12/01` must ship in two stages, and its Done-means should say so:
> 1. **Now:** the regression gate over all linters. Fully actionable today.
> 2. **On a named trigger:** zero-tolerance on the correctness three, activated
>    once the backlog reaches zero — not before, because a gate that fails on
>    every merge from day one gets disabled within a week.
>
> Whether the paydown becomes a new task in this batch or later work is the
> operator's call; staging it this way means `12/01` is not blocked either way.
> Do **not** let an implementer quietly interpret "zero-tolerance" as
> "regression-gate these three too" — that is a different and weaker decision.

### AD-21 — Historical question (superseded by the decided block above)

**Status:** superseded · **Gates:** none · **Findings:** GO-HYG-001

Guide §9 item 9, with a clear constraint from §4 Wave 7: *"Do not require
historical low-value debt to hit zero before introducing a ratchet. Baseline
and reject regressions/new actionable findings."* So the decision is not
"which do we fix" but **which classes block a merge going forward**, with
history baselined. The audit's own totals (errcheck 284, etc. — `REPORT.md:290`)
are the input; `00/02` refreshes them at the frozen HEAD.

### AD-22 — DECIDED: ratchet now, single sweep as the batch's final act

> **Decided (2026-08-22): enforce `gofmt` on new and changed code immediately,
> and land one repo-wide sweep as the last commit of the batch, alone, with no
> other worktree open.**
>
> Backlog measured at HEAD: **105 files** — down from 122 at the audited commit
> and 130 at frozen HEAD, because Waves 1–3 formatted what they touched. The
> ratchet is already working organically; the sweep exists to close the
> remainder definitively so the gate can be simple rather than
> baseline-aware.
>
> **Ordering is absolute and unchanged**: `13/03` is the last thing that lands
> in the batch. Sweeping before Wave 4 was rejected — Wave 4 deletes ~3,400
> production lines, and formatting files that are about to be deleted is waste
> with conflict risk attached.
>
> Re-measure immediately before the sweep. The count has moved three times
> already (122 → 130 → 105) and will move again as Waves 4–7 land.

### AD-22 — Repo-wide `gofmt` sweep: now, never, or ratchet-only

**Status:** open · **Gates:** `13/03` · **Findings:** GO-HYG-001, GO-CHAT-007

122 files failed `gofmt -l` at the audited commit. `13/03` is written to cover
either 12 files (the `GO-CHAT-007` subset) or all 122. This is a real
sequencing hazard, not a style preference: a 122-file format sweep conflicts
with **every** other open branch in the repo. If it happens at all it must be
the last thing that lands, alone. Options: sweep now (as the batch's final
act), never (baseline and enforce on changed files only), or ratchet-only
(`12/01` enforces `gofmt` on new/changed code and the backlog decays).

---

## Process decisions

### AD-23 — Accept ~8 MB of audit evidence into the repo

**Status:** decided · **Gates:** `00/02` step 1 (now satisfied)

> **Decided (2026-08-21): accept.** The operator copied the evidence to
> `docs/audits/2026-08-21-go-quality/raw/` (29 files, 8.0 MB), and the
> planning session added the narrow `.gitignore` negation
> (`.gitignore:88-91` — `!docs/audits/**/raw*/*.log` and `*.out`) that the
> global `*.log`/`*.out` rules would otherwise have applied. Verified: all 29
> files now stage, where 21 of 29 would previously have been silently skipped,
> and all 14 `raw/` paths cited by `REPORT.md` and the task files resolve.
> **Still uncommitted** — the rescue is not complete until it is in history.

The original question, retained for the record — written before the decision,
so its present tense and its "decide this before the freeze" urging describe
the situation as it stood on the morning of 2026-08-21, not now:

`REPORT.md:8` says *"Raw tool output backing every finding below lives in
`raw/`."* That directory is **not on `main`** — it exists only in the
untracked, gitignored
`.claude/worktrees/go-quality-audit/docs/audits/2026-08-21-go-quality/raw/`
(8.0 MB, 29 files — the whole directory, not just the cited subset). The
global `*.log` rule at `/Users/chrispian/.gitignore:11` is why the logs never
staged.

If that worktree is pruned, the evidence behind all 113 findings is gone and
cannot be regenerated — it was measured against a working tree that no longer
exists. **Decide this before the freeze, not during Wave 0.** The alternative
to accepting the repo weight is a durable external copy whose absolute path is
recorded in `REPORT.md` itself; an unrecorded copy on one machine is not an
acceptable outcome.

### AD-24 — Dev-freeze scope and exit criteria

**Status:** decided · **Gates:** every batch in the repo, not just this one

> **Decided (2026-08-21) by the operator, directly:**
>
> - **Scope: ALL tasks freeze.** Every batch, every phase, every `TASKS/`
>   folder. **Not** scoped to audited packages, not scoped to this batch's
>   dependencies. The six planned-but-undispatched sibling batches (Plugin
>   System, Loops, Turn vs. Run, Feedback-Carrying Denial, Code Mode,
>   Filesystem Snapshots) are frozen along with everything else.
> - **Priority: `TASKS/audit-remediation/` is #1** and is the only work
>   authorized to proceed.
> - **Exceptions require explicit operator authorization, case by case.** The
>   operator has stated an exception is unlikely. An agent must never grant
>   itself one, and must not treat size, risk, or "it's only docs" as
>   qualifying. If work seems to need an exception, stop and ask.
> - **Exit: the operator is the gate.** Resumption is **not** automatic on any
>   condition — not a wave boundary, not "all critical/high closed," not a
>   green test run, not `TASKS/INDEX.md` showing a batch complete. There is no
>   derived trigger. Work resumes when the operator says so.
> - **In-flight work finishes.** The two batches running at the time of the
>   freeze were in their home stretch and complete; nothing new starts.
>
> Mirrored as a banner at the top of `TASKS/INDEX.md`, since the freeze
> governs every batch tracked there and an Orchestrator booting against any
> section needs to hit it before that section's own "ready to dispatch"
> language.

**Migration numbering during the freeze** — unchanged and still worth
tracking: migration `135` remains unclaimed by Plugin System (per the root
`HANDOFF.md`). If frozen batches resume later, their provisional claims need
re-checking against whatever this batch lands. This batch claims **no**
migration numbers (see the batch README).
