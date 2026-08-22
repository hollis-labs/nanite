# Architect decision queue — audit remediation

The remediation guide is explicit (§9): *"Keep decisions separate from
implementation work… Do not let implementation agents silently make these
decisions."* This file is that separation. It is the batch's single
authoritative list of calls that must be made **by the operator/architect**,
not by a worker mid-task.

**24 decisions**, of which **2 are decided** (AD-23, AD-24) and 22 remain
open. 22 are grounded in one or more of the **44 findings** that carry
`requires_architect_decision: true` in `findings.json`, plus the guide's own
§9 list; 2 (AD-23, AD-24) were surfaced by this batch's planning pass and are
process decisions rather than finding-derived.

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
| AD-05 | Default/official catalog source: provision a real signing key? | `01/01` follow-up | GO-PLUGIN-001 | 1 | open |
| AD-06 | Island: grounding memory recall — wire / defer / retire | `09/01` | GO-MEM-001 | 4 | open |
| AD-07 | Island: Hadron context gate — wire / defer / retire | `09/02` | GO-MEM-002 | 4 | open |
| AD-08 | Island: team semantic routing — wire / defer / retire | `09/03` | GO-SVCEXEC-003 | 4 | open |
| AD-09 | Island: tool builder / YAML architecture — wire / defer / retire | `09/04` | GO-MCPTOOL-001 | 4 | open |
| AD-10 | Island: reasoning-augmented tool selection — wire / defer / retire | `09/05` | GO-MCPTOOL-002 | 4 | open |
| AD-11 | Island: curated tool-knowledge matcher — wire / defer / retire | `09/06` | GO-MCPTOOL-003 | 4 | open |
| AD-12 | `chatServiceImpl` / `generateResponse` decomposition boundaries | `10/01` | GO-SVCEXEC-001/002 | 5 | open |
| AD-13 | `SelfToolsTransport` decomposition boundaries | `10/02` | GO-MCPTOOL-006 | 5 | open |
| AD-14 | How far to narrow `internal/store` dependencies | `10/03`, `06/02` | GO-DEP-002, GO-STORE-001, GO-STORE-005 | 5 | open |
| AD-15 | Default auth / bind / TLS / startup-warning posture | `08/07` | GO-RUNTIME-002 | 3 | open |
| AD-16 | File/directory permission policy (default mode) | `08/04` | GO-SEC4-003 | 3 | open |
| AD-17 | `cmdServe` fatal-path cleanup: direction | `07/02` | GO-RUNTIME-001 | 2b | open |
| AD-18 | Background job registry: retention policy | `07/03` | GO-RUNTIME-004 | 2b | open |
| AD-19 | Duplicated semantics: share implementation vs. parity tests | Wave 6a (all) | GO-SVCEXEC-004, GO-API-007, GO-CHAT-002, GO-INFRA-004 | 6a | open |
| AD-20 | `internal/config` naming collision: rename direction | `11/08` | GO-INFRA-001 | 6a | open |
| AD-21 | Which historical lint classes become blocking | `12/01` | GO-HYG-001 | 7 | open |
| AD-22 | Repo-wide `gofmt` sweep: now, never, or ratchet-only | `13/03` | GO-HYG-001, GO-CHAT-007 | 8 | open |
| AD-23 | Accept ~8 MB of audit evidence into the repo | `00/02` step 1 | — (process) | 0 | **decided** |
| AD-24 | Dev-freeze scope and exit criteria | **every batch in the repo** | — (process) | 0 | **decided** |

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

---

## Wave 2 decisions

### AD-17 — `cmdServe` fatal-path cleanup: direction

**Status:** open · **Gates:** `07/02` · **Findings:** GO-RUNTIME-001 (low)

`slogx.Fatal` in `cmdServe` bypasses cleanup. Two shapes, per the task file:
change `cmdServe`'s signature (and `main()`'s `case "serve":` branch) to
return errors instead of exiting, or add a cleanup-hook mechanism to
`internal/slogx`. The first is more honest and more invasive; the second is
narrower and risks becoming a general-purpose exit hook nobody owns.

Low severity, but it gates `08/07` and `11/10`, which both edit the same file
later — so decide it early rather than letting it drift to the back.

### AD-18 — Background job registry: retention policy

**Status:** open · **Gates:** `07/03` (the retention half only)
· **Findings:** GO-RUNTIME-004 (medium)

`internal/background`'s job registry grows unbounded. The doc-comment half of
`07/03` is required regardless; the retention half needs a policy — TTL, max
count, completion-based eviction, or persist-and-evict. The relevant tradeoff
is what `Status`/`Result` callers are entitled to expect after eviction; a
policy that silently turns "job succeeded" into "job unknown" is a different
bug, not a fix.

### AD-14 — How far to narrow `internal/store` dependencies

**Status:** open · **Gates:** `10/03`, `06/02` · **Findings:** GO-DEP-002,
GO-STORE-001, GO-STORE-005

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

**Status:** open · **Gates:** `08/07` · **Findings:** GO-RUNTIME-002 (high)

The highest-severity finding in Wave 3. What does Nanite bind to by default,
does it require auth by default, is TLS expected/optional/absent, and what
does it say at startup when the posture is permissive? This is a product
decision as much as a security one — a local-first dev tool and a shared
service want different defaults, and `08/07` touches `internal/config` and
`cmd/nanite`'s composition root either way.

### AD-16 — File/directory permission policy (default mode)

**Status:** open · **Gates:** `08/04` · **Findings:** GO-SEC4-003 (medium)

Guide §9 item 8. `08/04` covers the permission engine's default-mode write
gap; the underlying question is what the project's default file/directory
mode policy *is*, stated once, so the engine can enforce something written
down rather than a value chosen at one call site.

---

## Wave 4 decisions — the six production islands

All six share one shape, so the guide's own proof obligation is stated once
here rather than six times. For each, choose exactly one:

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

**Status:** open · **Gates:** `10/01` · **Findings:** GO-SVCEXEC-001 (high),
GO-SVCEXEC-002 (high)

The largest refactor in the batch. The guide prescribes the *method* in
detail (§4 Wave 5) and it should be followed rather than re-derived:
responsibility map first, characterization tests to lock behavior, improve
coverage of the provider-error / compaction-recovery / plugin-cancel branches,
identify 3–6 coherent phases, **extract one at a time**, keep the outer state
machine recognizable, re-run behavior/race/complexity after each extraction.
`StreamManager` (`internal/service/stream.go`) is the named in-repo precedent.

What the architect actually decides: **which 3–6 boundaries**, and whether a
rewrite is on the table at all (the guide permits it only *"if it is clearly
safer/cleaner than incremental extraction"*). Decide after the responsibility
map exists, not before — this decision has a prerequisite deliverable.

### AD-13 — `SelfToolsTransport` decomposition boundaries

**Status:** open · **Gates:** `10/02` · **Findings:** GO-MCPTOOL-006 (medium)

81 methods across several files. The question per the guide: should the
transport *dispatch into narrower capability owners* rather than implement
every domain directly? Explicit constraint, worth quoting because it is the
failure mode: *"Do not split solely to reduce field/method counts."*

---

## Wave 6 decisions

### AD-19 — Duplicated semantics: share implementation vs. parity tests

**Status:** open · **Gates:** all of Wave 6a · **Findings:** GO-SVCEXEC-004,
GO-API-007, GO-CHAT-002, GO-INFRA-004 (+ the rest of `11/`)

Guide §9 item 10, and the decision that shapes the entire semantic-duplication
wave. For each duplicated *rule*, two legitimate outcomes: collapse to one
implementation, or keep both and add a **parity test** that fails when they
diverge. Sharing is not automatically right — `11/06` (provider streaming
error handling) covers two providers whose behavior *may* legitimately differ.

The guide's classification is the tool for this, and Wave 6a should produce
the table before the decision is made:

`textual-only boilerplate | same semantics/stable | same semantics/divergent
behavior | migration drift | intentionally independent`

Prioritize **semantic divergence** and **migration drift**; LOC reduction is
explicitly not the goal.

### AD-20 — `internal/config` naming collision: rename direction

**Status:** open · **Gates:** `11/08` · **Findings:** GO-INFRA-001 (low)

`config.Config` vs. `config.AppConfig` in one package. Low severity, but
`11/08`'s Touches warns the fix may reach *"every caller of `config.Config`
and `config.AppConfig` across the tree, depending on which direction the
architect chooses."* That range — a two-line rename or a tree-wide sweep — is
exactly why it needs deciding before dispatch and not during.

---

## Wave 7–8 decisions

### AD-21 — Which historical lint classes become blocking

**Status:** open · **Gates:** `12/01` · **Findings:** GO-HYG-001

Guide §9 item 9, with a clear constraint from §4 Wave 7: *"Do not require
historical low-value debt to hit zero before introducing a ratchet. Baseline
and reject regressions/new actionable findings."* So the decision is not
"which do we fix" but **which classes block a merge going forward**, with
history baselined. The audit's own totals (errcheck 284, etc. — `REPORT.md:290`)
are the input; `00/02` refreshes them at the frozen HEAD.

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
