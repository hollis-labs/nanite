<!--
Imported into the repo 2026-08-21 by the audit-remediation planning session.

PROVENANCE: this is a verbatim copy of the advisor-authored remediation
planning guide that originally lived at
`~/dev/chrispian/inbox/nanite-audit-triage-remediation-planning-guide.md`
— outside this repository, and therefore unreadable by a context-free
Orchestrator/worker session. It is vendored here because
`TASKS/audit-remediation/` treats it as a primary source of truth (its §4
wave structure is the batch's grouping scheme, its §6 template is the task
files' content shape, its §5 is the dependency-field scheme, and its §9 is
the architect-decision queue).

Do not edit the body below to reflect decisions this project subsequently
makes — it is a frozen input document. Decisions belong in
`TASKS/audit-remediation/ARCHITECT-DECISIONS.md`; sequencing belongs in
`TASKS/audit-remediation/README.md`.
-->

# Nanite Audit Triage & Remediation Planning Guide

> **Purpose:** Turn the completed Nanite Go quality/architecture audit
> into a prioritized, dependency-aware remediation plan.
>
> This is a **planning task**, not a remediation task. Do not modify
> production code while creating the plan. The audit is the source of
> findings; current source is the source of truth when validating
> dependencies, blast radius, or remediation ordering.

## 1. Goal

Create a remediation plan that:

1.  addresses release-blocking security/correctness issues first;
2.  groups related findings by **root cause**, not merely finding ID;
3.  identifies changes that can eliminate an entire recurring defect
    class;
4.  separates mechanical cleanup from changes requiring architectural
    judgment;
5.  preserves existing behavior unless a finding explicitly identifies
    incorrect behavior;
6.  adds regression/prevention checks wherever practical;
7.  establishes a quality baseline that can be ratcheted down over time;
8.  explicitly decides the fate of fully-built but
    production-unreachable "islands";
9.  avoids broad rewrites unless they are clearly cheaper/safer than
    incremental remediation;
10. leaves Nanite with enforceable standards that reduce recurrence in
    future work.

The desired output is not a flat backlog. It is a **sequenced
remediation program**.

## 2. Inputs and evidence rules

Use the completed audit, its JSON catalog when available, current
source, current engineering standards/architecture docs, and existing
task records where they explain intentional work.

Before planning fixes, compare current HEAD with audited commit
`8feeee5c`. Some findings may already be resolved because development
continued after the audit.

Do not rewrite historical findings. Update their disposition:

-   confirmed-open
-   already-resolved
-   superseded
-   accepted
-   needs-revalidation

Do not trust old task notes or comments over current source.

## 3. Planning principles

### Root causes over occurrence counts

Do not create one remediation task per raw analyzer occurrence. Collapse
occurrences into underlying causes where evidence supports it.

### Security and correctness before cleanup

Prioritize:

1.  critical trust-boundary/security failures;
2.  high security/correctness failures;
3.  lifecycle, concurrency, and data-integrity defects;
4.  production-reachability decisions;
5.  architecture and duplicated semantics;
6.  testing gaps preventing verification;
7.  enforceable quality standards;
8.  dead code/comments/naming;
9.  cosmetic/mechanical cleanup.

### Fix every sibling path

The audit repeatedly found a newer/correct implementation beside an
older stale implementation.

For every security/correctness remediation:

1.  identify the primitive/invariant being corrected;
2.  enumerate **all production callers**;
3.  locate parallel implementations;
4.  migrate or explicitly exempt each caller;
5.  add a test/rule making future divergence visible.

**Standard:** A security/correctness fix is incomplete until every
production caller of the affected semantic boundary has been checked.

### Production reachability is part of done

Require this proof for feature completion:

``` text
production entry point
    ↓
construction / registration / wiring
    ↓
feature invocation
    ↓
observable behavior
```

For every "island," explicitly choose:

-   **wire** --- intended feature; connect and test the real production
    path;
-   **defer** --- intentionally staged; record trigger/owner and ensure
    docs don't claim it is live;
-   **retire** --- remove implementation/tests/docs if no longer
    architectural intent.

### Preserve essential complexity

Classify complexity findings as:

-   essential;
-   accidental;
-   mixed.

Do not split code merely to reduce a metric. Prefer capability/phase
extraction where it reduces responsibility or state coupling.

### Remediation includes prevention

For each meaningful finding ask:

> What regression test, automated check, architectural rule, or
> engineering standard would have prevented this?

Recurring patterns should normally produce a prevention mechanism.

------------------------------------------------------------------------

# 4. Proposed remediation waves

## Wave 0 --- Revalidate the baseline

Before scheduling work:

-   compare current HEAD with audited commit;
-   mark recently completed islands/fixes already landed;
-   verify critical/high findings remain open;
-   preserve original audit state in the JSON catalog;
-   create the working remediation view from current state.

Do this quickly; do not re-audit the whole repository.

## Wave 1 --- Release-blocking trust boundaries

### Plugin installation convergence

Group:

-   `GO-PLUGIN-001`
-   `GO-PLUGIN-002`
-   `GO-PLUGIN-003`

Goal: **one authoritative plugin-install security pipeline shared by CLI
and GUI/API.**

Verify the plan covers:

-   fail-closed signature policy in production;
-   explicit checksum/signature policy;
-   release builds cannot use dev signing bypass;
-   final target path uses approved confinement;
-   catalog-controlled names cannot escape plugin root;
-   every CLI/GUI/API entry point converges on the same pipeline;
-   weaker legacy pipeline is removed or unreachable;
-   regression tests traverse the real Plugin Manager/API path.

### Linux sandbox fail-open

Primary: `GO-SEC4-001`.

Related: `GO-SEC4-002`, `GO-SEC4-006`.

Goal: **Nanite must not report sandboxed execution as successfully
isolated when required OS isolation is absent.**

Architect decision:

-   fail closed without `bwrap`; or
-   require explicit, highly visible opt-in to degraded/no-OS-isolation
    execution.

Add tests for Linux with/without `bwrap` and observable sandbox status.

### Agent slug traversal

Group:

-   `GO-AGENT-001`
-   `GO-AGENT-002`

Goal: **one canonical validation contract for identifiers that become
filesystem paths.**

Use the existing slug validator/regex where appropriate, apply it to
direct CRUD paths, retain path confinement as defense in depth,
enumerate every slug→path caller, and add traversal/overwrite regression
tests.

## Wave 2 --- Correctness, lifecycle, concurrency

### Container/reaper lifecycle

Group:

-   `GO-LIFE-001`
-   `GO-TEST-001`
-   relevant `GO-SVCCORE-*` lifecycle findings.

Keep distinct:

1.  constructor partial-failure cleanup;
2.  API tests that create Containers without shutdown;
3.  `internal/service` race timeout, whose cause was explicitly found to
    be different.

Success criteria:

-   every started resource has ownership;
-   partial construction cleans up;
-   test-created containers are torn down;
-   obtain a real `-race` verdict for API/service;
-   independently investigate service timeout if it persists.

### Subagent execution ordering

Group:

-   `GO-EXEC-001`
-   `GO-EXEC-002`

Verify approval obeys the same concurrency limit as spawn and operator
cancellation can stop a run while it is still queued. Add regression
tests for both.

### Store correctness

Prioritize `GO-STORE-003`: distinguish true not-found from real DB
errors in `DeleteAgentByID`.

Then triage `GO-STORE-004/005/006`.

Do not turn this into a repository-wide Store abstraction rewrite.

### Runtime correctness/lifecycle

Review together:

-   `GO-RUNTIME-001`
-   `GO-RUNTIME-003`
-   `GO-RUNTIME-004`
-   `GO-RUNTIME-005`
-   `GO-RUNTIME-007`

Prioritize observable behavioral defects such as wrong orphan branch
cleanup and unbounded retained job state over informational
idempotency/comment issues.

## Wave 3 --- Remaining security hardening

Review:

-   `GO-SVCCORE-004` --- A2A callback SSRF;
-   `GO-MCPTOOL-008` --- symlink/TOCTOU escape;
-   `GO-SEC-003` --- remaining variable-path sites;
-   `GO-SEC4-003/004/007/008`;
-   `GO-RUNTIME-002` --- auth/bind/TLS/warning policy;
-   dependency/toolchain vulnerability updates.

For filesystem/network findings classify source trust:

``` text
external unauthenticated
external authenticated
agent-controlled
plugin/catalog-controlled
operator CLI-controlled
OS-derived/internal
```

Severity and remediation should follow the actual trust boundary, not
raw analyzer severity.

## Wave 4 --- Production islands

Create an architect decision table.

Seed candidates:

  -----------------------------------------------------------------------
  Feature                 Finding                 Decision
  ----------------------- ----------------------- -----------------------
  Grounding memory recall GO-MEM-001              wire / defer / retire

  Hadron context gate     GO-MEM-002              wire / defer / retire

  Team semantic routing   GO-SVCEXEC-003          wire / defer / retire

  Tool builder/YAML       GO-MCPTOOL-001          wire / defer / retire
  architecture                                    

  Reasoning-augmented     GO-MCPTOOL-002          wire / defer / retire
  tool selection                                  

  Curated tool knowledge  GO-MCPTOOL-003          wire / defer / retire
  matcher                                         
  -----------------------------------------------------------------------

Check current source first; some were completed after the audited
commit.

Anything deferred must not look production-live in docs and should not
impose unnecessary boot/runtime cost.

## Wave 5 --- Architectural concentration

### `chatServiceImpl` / `generateResponse`

Group:

-   `GO-SVCEXEC-001`
-   `GO-SVCEXEC-002`

First create a responsibility map:

``` text
capability
fields owned
methods owned
shared mutable state
dependencies
callers
candidate extraction boundary
```

For `generateResponse`, identify named phases and mutation boundaries.

Preferred approach:

1.  lock behavior with characterization/regression tests;
2.  improve coverage of provider-error, compaction-recovery, and
    plugin-cancel branches;
3.  identify 3--6 coherent phases/capabilities;
4.  extract one at a time;
5.  keep outer state-machine flow recognizable;
6.  rerun behavior/race/complexity reports after each extraction;
7.  consider a rewrite only if it is clearly safer/cleaner than
    incremental extraction.

Use `StreamManager` as an in-repo precedent.

### `SelfToolsTransport`

Primary: `GO-MCPTOOL-006`.

Map its capability domains and decide whether the transport should
dispatch into narrower capability owners rather than implement all
domains directly.

Do not split solely to reduce field/method counts.

### `Container`

Do **not** schedule a god-object refactor merely because it has 60
fields. The audit found it wiring-only with two methods.

Only improve construction/lifecycle/grouping if it provides concrete
value.

### `internal/store`

Treat as a gravitational-package review, not a mandatory split. Add
narrow consumer-defined interfaces only where they solve demonstrated
coupling/testability problems.

## Wave 6 --- Semantic duplication / migration drift

Build:

``` markdown
| Concept | Old implementation | New/shared implementation | Live callers | Decision |
```

Seed with:

-   GUI/API vs CLI plugin install;
-   subagent completion policy vs message wake policy;
-   duplicated SSRF CIDR lists;
-   Harness-v1 vs native durable-agent handlers;
-   StructuredMessage unwrap duplication;
-   traffic-light calculation;
-   MCP result-processing tail;
-   provider streaming divergence where semantics should match.

Classify:

1.  textual-only boilerplate;
2.  same semantics/stable;
3.  same semantics/divergent behavior;
4.  migration drift;
5.  intentionally independent.

Prioritize semantic divergence and migration drift over LOC reduction.

## Wave 7 --- Quality ratchet

After meaningful backlog reduction, establish enforcement.

### Fast developer gate

Keep fast:

-   formatting;
-   build;
-   vet;
-   approved staticcheck/golangci subset;
-   changed-code lint;
-   targeted tests.

### Full-repo scheduled/merge gate

Add an enforcement point for:

-   uncapped lint;
-   `govulncheck`;
-   `gosec` with known-noise policy;
-   module verification;
-   dead-code report;
-   race suite where runtime permits.

Do not require historical low-value debt to hit zero before introducing
a ratchet. Baseline and reject regressions/new actionable findings.

### Standards to add/confirm

**Production Reachability**

> A feature is not done until its production entry point, wiring,
> invocation, and observable behavior are proven.

**Security/Correctness Migration Completeness**

> When a security or correctness fix replaces a primitive or pipeline,
> enumerate and verify every production caller of the superseded
> implementation.

**Semantic Duplication**

> Duplicating syntax is a maintainability concern. Duplicating a
> semantic rule is a correctness concern.

**Lifecycle Ownership**

> Every goroutine/background worker/resource has an explicit owner and
> shutdown path; partial construction cleans up already-started
> resources.

**Trust-Boundary Paths**

> Values influenced by external callers, agents, plugins, catalogs, or
> persisted untrusted state must not become filesystem paths without
> canonical validation/confinement.

**Silent Security Degradation**

> A required security boundary must not silently degrade while reporting
> success.

## Wave 8 --- Mechanical cleanup

After architect-sensitive work:

-   misspell findings;
-   staticcheck quickfixes;
-   confirmed dead code;
-   stale history/task comments;
-   stale package docs;
-   gofmt backlog;
-   trivial naming issues;
-   obsolete agent artifacts;
-   deprecated APIs;
-   accepted/noisy linter exclusions.

Batch mechanical changes separately from semantic changes.

------------------------------------------------------------------------

# 5. Dependency ordering

Each task should record:

``` yaml
depends_on:
blocks:
can_parallelize_with:
requires_architect_decision:
requires_security_review:
requires_regression_test:
```

Default flow:

``` text
critical security
      ↓
correctness/lifecycle
      ↓
trustworthy race/test baseline
      ↓
island decisions
      ↓
architecture refactors
      ↓
semantic duplication cleanup
      ↓
quality enforcement
      ↓
mechanical hygiene
```

Independent dependency upgrades and isolated tests can run in parallel.

------------------------------------------------------------------------

# 6. Remediation task template

``` markdown
## <Task ID> <Title>

### Findings addressed
- GO-...

### Root cause
Underlying defect pattern, not just symptom.

### Current behavior
What source does today, with pointers.

### Desired invariant
What must always be true after remediation.

### Scope
Expected packages/files/symbols.

### All production callers
Mandatory for shared primitive/security/correctness changes.

### Proposed direction
Architecture-level approach.

### Non-goals
Prevent opportunistic refactoring.

### Dependencies
- ...

### Tests required
- regression reproducing original defect;
- real production-path/integration test where applicable;
- race/security tests where applicable.

### Prevention
What standard/check/test prevents recurrence?

### Verification
Commands and observable behavior required for PASS.

### Risk / rollback
Likely regression surface and rollback approach.

### Done means
Concrete completion checklist.
```

------------------------------------------------------------------------

# 7. Plan output format

Produce four artifacts:

## A. Executive triage

Short architect-facing summary:

-   release blockers;
-   high-priority correctness;
-   architecture decisions;
-   islands;
-   quality-system improvements;
-   expected parallel workstreams.

## B. Remediation waves

Sequenced tasks with dependencies and parallelization.

## C. Finding disposition table

Every audit finding must end up with a disposition:

``` markdown
| Finding | Severity | Root-cause group | Disposition | Task | Notes |
```

Allowed dispositions:

-   remediate
-   already resolved
-   accepted risk
-   false positive
-   superseded
-   defer
-   retire feature
-   needs architect decision
-   needs more evidence

No finding should disappear merely because it was grouped.

## D. Prevention/rules table

``` markdown
| Defect class | Findings demonstrating it | Prevention | Enforcement point |
```

This is the bridge from Nanite cleanup to improved future software
production.

------------------------------------------------------------------------

# 8. Prioritization scoring

Severity is primary. Within equal severity, score qualitatively on:

-   externally reachable?
-   security/trust boundary?
-   data loss/corruption?
-   silent failure?
-   affects multiple callers?
-   recurring pattern?
-   blocks reliable testing?
-   cheap/high-confidence fix?
-   prerequisite for other work?

Do not use a numerical score to override obvious architectural judgment.

------------------------------------------------------------------------

# 9. Architect decision queue

Keep decisions separate from implementation work.

Likely architect decisions from this audit include:

1.  Linux sandbox behavior when `bwrap` is absent.
2.  Linux network allowlist enforcement level.
3.  Default auth/bind/TLS/warning posture.
4.  Fate of each production island.
5.  `chatServiceImpl` decomposition boundaries.
6.  `SelfToolsTransport` decomposition boundaries.
7.  Whether/how far to narrow Store dependencies.
8.  File/directory permission policy.
9.  Which historical lint classes become blocking.
10. Which duplicated semantics should share implementation vs parity
    tests.

Do not let implementation agents silently make these decisions.

------------------------------------------------------------------------

# 10. Verification after each wave

After each wave:

1.  rerun affected regression tests;
2.  run relevant package `-race` tests;
3.  rerun security tools for affected boundaries;
4.  update JSON catalog statuses;
5.  rerun complexity/dependency reports only if architecture changed;
6.  record new findings separately rather than rewriting baseline;
7.  verify no previously accepted production path became an island.

After final remediation:

``` bash
go build ./...
go vet ./...
go test ./...
go test -race ./...
staticcheck ./...
errcheck ./...
golangci-lint run --max-issues-per-linter=0 --max-same-issues=0
gosec ./...
govulncheck ./...
go mod verify
go mod tidy -diff
deadcode -test ./...
```

Use project-specific timeouts/configuration where the baseline showed
the default is insufficient.

------------------------------------------------------------------------

# 11. Guardrails for the planning agent

Do not:

-   modify production code;
-   create one task per lint occurrence;
-   prioritize cosmetic debt over security/correctness;
-   rewrite large code solely because metrics are high;
-   split healthy composition roots;
-   create repository interfaces everywhere because Store has high
    fan-in;
-   delete islands without checking current intent/current source;
-   assume an old audit finding is still open;
-   mark a feature done based only on unit tests;
-   fix one caller of a shared security boundary without enumerating
    siblings;
-   silently accept a finding;
-   mix large mechanical cleanup into semantic remediation tasks.

Do:

-   validate current source;
-   group by root cause;
-   preserve every finding's disposition;
-   enumerate production callers;
-   identify dependencies and parallel work;
-   distinguish architect decisions from implementation;
-   require regression tests for real defects;
-   require production-reachability proof for features;
-   propose prevention for recurring defect classes;
-   keep the plan incremental and reviewable.

------------------------------------------------------------------------

# 12. Definition of a successful triage plan

The plan is complete when:

-   every critical/high finding has an explicit disposition;
-   every audit finding is mapped to a disposition/root-cause group;
-   release blockers are isolated into the earliest wave;
-   shared-root-cause findings are consolidated;
-   all known islands have wire/defer/retire decisions queued;
-   architecture refactors have bounded scopes and behavior-preservation
    plans;
-   mechanical cleanup is separated from semantic work;
-   dependencies/parallelization are explicit;
-   recurring defect classes have proposed prevention mechanisms;
-   verification steps are defined;
-   the JSON catalog can track the plan from baseline → remediation →
    verified closure.

The result should let an architect review the **decisions and
sequencing** rather than rediscover the audit.
