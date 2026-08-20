# Teams

Design produced by a dedicated architecture-alignment session (2026-08-20): an external proposal (reviewed against Nanite's real code, not taken at face value) plus operator sign-off on the two load-bearing forks it raised. **Design only — no code or schema changed in this session.** Implementation is deferred to follow-up work, not yet filed.

## The problem this solves

Dispatch today is a hardcoded three-role model: `internal/dispatch/role.go`'s `AssignRole` maps `(ScopeTier, ExecutionPattern)` to exactly Chat/Worker/Planner (plus `RoleWorkflow`, a named-workflow escape hatch). There is no way to express "this objective needs an architect, N engineers, and a reviewer, with these communication and approval rules" without hand-building a one-off workflow definition per case. Teams generalizes that fixed mapping into a configurable, reusable N-slot organizational shape — without inventing a second orchestration engine to run it.

## The core split

```
Agent Workflow  = prescribed execution        (internal/agentworkflow, unchanged)
Team            = prescribed organization      (new: slots, routing, authority)
TeamRun         = a WorkflowRun                (not a new run engine — see below)
```

This is the same distinction the codebase already draws between a `Role` (template) and an `Agent` (a specific composition/binding of one) — Team is that same pattern one level up, applied to *groups*, not individuals. It is deliberately **not** inserted into the existing `role → agent → task` cascade (`01-agent-construction.md`). A Team Slot sits beside that cascade and resolves *into* it:

```
Role → Agent → Task/Invocation
         ▲
         │ instantiated into
      Team Slot
```

## Decision 1 — Gates are literal `agentworkflow` gate steps, not a new hard-block mechanism

`00-overview.md`'s guiding principles record, as an observed failure mode and not a style preference: *"Steering nudges; it doesn't gate with a deterministic pre-decision layer. Hard-gating experiments in this codebase produced dead-end conversations."* A Team-native hard-gate mechanism (`gates: merge: requires: reviewer`, enforced by Team itself) would reintroduce exactly that shape at a new layer.

`agentworkflow` already has a real, deliberate exception to "hints not control": `StepKindGate`, a genuine human/role-in-the-loop pause that blocks a branch of the DAG until externally resolved. **A Team's deterministic invariant compiles to a `StepKindGate` step, full stop — no second gate mechanism.** Fluid coordination (agents messaging, delegating, self-organizing) stays purely on reflexes and messaging, which remain advisory. A Team is not required to declare any gates at all — a fully fluid team is a legitimate, gate-free shape.

Operator's framing, worth preserving verbatim: reflexes have "largely failed" so far mostly because their implementation was never finished (the shared decision engine, halt-must-actually-halt, unified telemetry — all deferred by `10-reflex-action-taxonomy.md`), not because "hints not control" is the wrong philosophy for the fluid zones. Teams' fluid-coordination quality inherits that debt; it doesn't need to pay it down itself, but it also won't be better than reflexes currently are until that follow-up work lands.

## Decision 2 — TeamRun IS a WorkflowRun

Given gates are real gate steps, a Team Run's overall shape — gated stretches alternating with fluid self-organizing stretches — is itself a DAG. Rather than Team owning a second run lifecycle that calls into `agentworkflow` only at gate boundaries, **a Team definition's phase sequence compiles to a `WorkflowDefinition` at launch, and TeamRun *is* the resulting `WorkflowRun`** — one run engine, one persistence path (`workflow_runs`/`workflow_run_steps`), consistent with `WorkflowLauncher`'s existing framing that "a workflow run IS a template-class durable agent instance" reusing `durable_agent_instances`' event-log machinery rather than a second tracking system. Team-specific state stays additive, not parallel (see "New vs. reused" below).

This requires one real new step kind, `StepKindFlex` (see naming note below) — the only piece of this design that touches the workflow *engine* itself rather than just adding Team-layer configuration on top of what exists. Its config names which Team slots are active participants during that stretch and an exit trigger; execution during a flex step is N slot-members self-organizing via messaging/reflexes, not one prescribed actor per step the way `llm`/`tool` steps are today.

**A flex step is a phase boundary, not a single execution action — say this explicitly so implementation doesn't assume a step is always "handler runs, handler returns."** This isn't new engine behavior, though: `BuiltinWorkflowEngine` already pauses a run at `RunStatusWaiting` for a gate and resumes it later via a real `Resume(ctx, runID, wf, exec)` call (A2A's gate-resolution path, `resumeWorkflowRun`, already does exactly this after an operator approves). A flex step reuses that identical pause/external-resolve/`Resume` shape — it returns the waiting status when it starts, and something external (the exit trigger firing) is what calls `Resume`, not a step executor returning synchronously. The only new thing is *what happens during the wait* (active agents doing real work, not an idle pause) and *what resolves it* (an exit-trigger condition, not an operator approval) — the pause/resume plumbing itself is not new.

### Illustrative shape

A Team definition (using the SME scenario from the reviewed proposal — a genuinely good canonical test case, see "Validating this design" below):

```
Team: Feature Development

slots:
  architect:
    role: architecture-sme
    resolution: durable        # wakes an existing identity, not spawned fresh
    agent_id: nanite-architect
    required: false            # normally dormant

  orchestrator:
    role: orchestrator
    resolution: fresh
    activation_mode: singleton
    required: true

  engineer:
    role: engineer
    resolution: fresh
    activation_mode: concurrent
    min: 1
    max: 4

  reviewer:
    role: code-reviewer
    resolution: fresh
    activation_mode: singleton
    required: true

authority:
  orchestrator.may_spawn: [engineer, reviewer]
  engineer.may_message: [architect, orchestrator, engineer]
  reviewer.may_message: [engineer, architect]
  reviewer.may_not_review: self

routing:
  architecture_question -> architect
  otherwise -> orchestrator
```

`resolution: durable | fresh` and `activation_mode: singleton | fresh-per-wake | concurrent` are not new vocabulary — they're `agents.durable`/`agents.activation_mode` as they exist today, referenced by a slot rather than redefined by one.

Compiles, at TeamRun launch, to a `WorkflowDefinition` roughly like:

```
steps:
  - id: scope_work
    kind: flex
    config: { active_slots: [orchestrator, engineer, architect], exit_trigger: {self_tool: mark_ready_for_review} }

  - id: review_gate
    kind: gate
    depends_on: [scope_work]
    config: { approver_slot: reviewer }

  - id: address_feedback
    kind: flex
    depends_on: [review_gate]
    config: { active_slots: [engineer, reviewer, architect], exit_trigger: {event: review_approved} }

  - id: merge_gate
    kind: gate
    depends_on: [address_feedback]
    config: { approver_slot: operator }   # human, not a team slot
```

A flex step's `exit_trigger` reuses the reflex system's existing predicate/event/interval trigger-spec AST (`internal/agent/reflexes`) rather than inventing a second condition language — "this step ends when X" is the same shape as "this reflex fires when X."

**Trigger truth and permission to produce the trigger are separate concerns** — the same policy/mechanism split this design already draws between routing (where), authority (who may), and gates (what must be true). `exit_trigger: {self_tool: mark_ready_for_review}` says *what* ends the phase; it does not by itself say *who* may call that self-tool, or whether one active member marking ready ends the phase while others are still working. That authorization question resolves through the same slot-level authority mechanism described below (see "Authority"), not a separate exit-trigger permission system — but it must be resolved explicitly, not left implicit in "whoever happens to call the tool first."

## Slot resolution and the one genuinely new persistence table

At TeamRun launch, each slot resolves to a concrete `(agent_id, session_id)` tuple — the same tuple `internal/messaging` already addresses `agent_messages` by. Durable slots wake an existing identity (`DurableAgentService`, the same primitive `WorkflowLauncher`/A2A `CancelTask` already call); fresh/template slots spawn through the ordinary Agent Construction cascade. `min`/`max`/`required` on a slot gate whether resolution is mandatory-at-launch, deferred-until-needed (e.g. the architect SME, "normally dormant"), or elastic (spawn engineer #2 mid-run).

The one real new table this design needs is the resolution record itself — `team_run_members` (`workflow_run_id`, `slot_name`, `agent_id`, `session_id`, `resolved_at`, `status`), the same shape as the existing `session_agents` junction table, scoped to a run instead of a session. Everything else a naive reading of the reviewed proposal would turn into new tables (`team_run_routes`/`_messages`/`_tasks`/`_decisions`) should **not** be built — those are filtered views over `agent_messages`, `event_log`, and `agent_reflexes` firings by `workflow_run_id`, not new parallel state. This codebase has already paid once for a duplicate messaging system (`internal/messaging/gomsg`, cut in full, never constructed anywhere) — Teams should not reintroduce that mistake one layer up.

**`team_run_members` is one-to-one only for singleton slots.** A slot with `min`/`max` > 1 (e.g. `engineer` resolving to three concrete members) makes `@engineer` ambiguous — route to one, broadcast to all, or address a specific concrete member — and the doc does not resolve this. Left as an open question deliberately (see "What this session did not decide") rather than guessed at now, since the right shape depends on real routing/messaging usage patterns not yet observed.

## Routing: real reuse, and one real gap

`dispatch_to_agent` is already a live reflex action kind doing "route to that agent when a condition fires," with a resolution engine (`10-reflex-action-taxonomy.md`'s Facet 2 combining algorithms — `first_applicable` for `dispatch_to_agent` specifically) that Team routing should reuse wholesale rather than reimplement:

- **Explicit addressing** (`send @architect ...`) — a thin resolver from slot name to the `team_run_members` tuple, then the existing `send_message` self-tool machinery. No new transport.
- **Semantic/capability routing** (`architecture_question -> architect`) — a `dispatch_to_agent` reflex row, `first_applicable`, scoped to the run.
- **Coordinator fallback** (`* -> orchestrator`) — the lowest-priority row in the same scoped set. `first_applicable`'s existing priority tie-break covers this without new mechanism.

The real gap: `agent_reflexes.agent_id` today is only `NULL` (class-bound/global) or a specific agent (`agent_profiles.id`) — there is no third scoping dimension for "this reflex exists only for the lifetime of this run, and its candidate set is this run's resolved members." That's genuine new schema/engine work (likely a nullable `workflow_run_id` column and a run-scoped candidate-set lookup), not configuration over what exists. Called out explicitly so it isn't discovered as a surprise mid-implementation.

**Keep three things terminologically distinct, so "why did this message go to Architect?" stays answerable:**

```
Team routing rule    -- configuration (a dispatch_to_agent reflex row this Team declares)
Routing decision      -- a runtime event (one reflex firing, at one moment, for one message)
Message delivery       -- the transport record (an agent_messages row)
```

None of these need a new persistence layer — they're already exactly what the "one new table" claim above depends on. A provenance trace for "why did this go to Architect" walks: the delivered message (`agent_messages`) → the reflex firing that produced it → the resolved slot → the concrete `(agent_id, session_id)` in `team_run_members`. The middle step isn't hypothetical: `10-reflex-action-taxonomy.md` already documents `attemptReflexDispatch`'s `alternatives_considered` list (every candidate evaluated for a `dispatch_to_agent` firing, and whether it fired) as the pattern the taxonomy work wants generalized to every kind under `first_applicable`/`deny_overrides`. Team routing provenance is that same telemetry, filtered by `workflow_run_id` — derived/observability, not new persistent Team state.

## Authority: generalize, don't invent

`internal/dispatch`'s `agent_parent_dispatch_allowlist` (migration 060) already governs "which *tools* may a dispatching parent authorize a spawned child to use." A Team's `may_spawn`/`may_message`/`may_not_review` is the same shape one level up — *role*-level rather than *tool*-level authorization between a spawning slot and a spawned slot. This should generalize that existing mechanism (same join-table pattern, new dimension) rather than be built as an unrelated Team-native permission system. Where authority intersects approval-bypass (a slot dispatching without human confirmation), it should compose with the existing `dispatch.TrustTier`/`ErrUntrustedRole` enforcement point, not add a second bypass check next to it.

**`may_message` is one illustrative verb, not the root authority primitive — don't let it become that by default.** "May exchange messages at all" (transport access) and "may direct/command/approve" (semantic authority) are different questions that happen to look the same in the example above only because the example didn't need to distinguish them yet. An engineer asking the architect a question and an engineer trying to cancel the architect's current work are not the same grant, even though both are "messages." The transport (`internal/messaging`) can and should stay generic — authority should eventually attach to the semantic operation (`may_delegate`, `may_approve`, `may_signal`, alongside `may_spawn`/`may_message`), not to raw message-send access alone. No verb set is locked by this session; this is a warning against letting the one convenient example ossify into the whole mechanism.

## Runtime overrides follow the existing cascade

Team defaults → saved Team configuration → TeamRun invocation overrides is the same "closest wins" cascade already standardized for Agent Construction (`role → agent → task`) and permission resolution elsewhere in this codebase — not a new pattern Teams introduces. A caller (Loom, or any Nanite consumer) should be able to launch against a saved Team by name with invocation-time overrides (`engineers.max: 3`) without creating a new persistent Team definition per call, matching how `WorkflowLaunchRequest.Params` already works today.

## Naming decisions made this session

Per this repo's own Glossary discipline (a documented, repeated history of naming collisions causing real confusion):

- **`StepKindFlex`**, not `StepKindCoordination` — `internal/coordination` already exists (a Badger-backed ephemeral KV store for locks/heartbeats, unrelated to this). Reusing "coordination" for the new step kind would reintroduce exactly the kind of same-word-two-meanings collision this Glossary exists to prevent.
- **Not "Scope"** for a slot's "what does this team need" facet — `Scope` is already claimed twice: the Agent-construction sense (project/data-source reference) and `permission.Scope`'s grant-duration sense (`once`/`session`/`project`). If a TeamRun-lifetime permission grant is ever needed, it's a fourth value added to the existing `permission.Scope` enum, not a new concept.
- **Not "Action"** for a routing/authority outcome — reserved for Reflexes' own `action_kind`/`AppliedAction`/`Executor.Apply` vocabulary, same reasoning the harness-reactive self-tools design already applied when it chose "reaction" over "action" for its own adjacent mechanism.
- **"Gate" stays "Gate"** only because it's the literal same mechanism (Decision 1) — if a future variant of Team gating ever diverges from `StepKindGate`'s actual semantics, it needs to stop calling itself that.

## New mechanism vs. reuse — explicit ledger

So this isn't oversold as "100% configuration over existing systems," which it isn't quite:

| Piece | New or reused |
|---|---|
| Slot → `(agent_id, session_id)` resolution | Reused addressing (`internal/messaging`'s tuple); **new** table (`team_run_members`) |
| Durable vs. fresh member resolution | Fully reused (`agents.durable`, `agents.activation_mode`, `DurableAgentService`) |
| Gates | Fully reused (`StepKindGate`, unchanged) |
| Fluid coordination stretches | **New** step kind (`StepKindFlex`) — real engine-level work |
| Flex-step exit conditions | Reused (reflex trigger-spec AST) |
| Explicit/semantic/fallback routing | Reused (`dispatch_to_agent`, its combining algorithms) |
| Run-scoped routing candidate set | **New** — `agent_reflexes` has no run-scoping dimension today |
| Authority (`may_spawn`/`may_message`) | Generalizes an existing mechanism (`agent_parent_dispatch_allowlist`), new join table |
| Runtime override cascade | Fully reused pattern (role→agent→task shape) |
| TeamRun persistence | Fully reused (`workflow_runs`/`workflow_run_steps`, `durable_agent_instances`) |

## Guardrail

The net effect of every decision above is that Team is a **compiler/configuration layer over existing runtime primitives**, not a new multi-agent runtime sitting beside them:

```
Team definition
    ↓ resolve slots
    ↓ install run-scoped routing/reflex policy
    ↓ compile phase/gate sequence into a WorkflowDefinition
    ↓ launch an ordinary WorkflowRun
    ↓ existing agents + messaging + lifecycle do the actual work
```

Protect that framing during implementation. If building this starts to require a `TeamExecutionEngine`, `TeamMessageBus`, `TeamScheduler`, `TeamMemory`, or `TeamGateManager` — anything that duplicates a subsystem this doc already named as reused — that's a signal the design has drifted from what's written here, not a natural extension of it.

## Validating this design

The reviewed proposal's stress-test approach is worth keeping as the follow-up validation method, not just a one-off illustration: take the Feature Development SME example above and check the design survives (not necessarily gracefully on the first pass, but without requiring a new top-level mechanism) messy realities like: an engineer asks the wrong slot; a reviewer discovers an architecture issue mid-run and needs to reach the architect directly; the architect SME wakes mid-run; an engineer instance dies and needs replacing; the orchestrator adds a second engineer dynamically; the SME and reviewer disagree; a human injects a new requirement mid-run; **a flex step's exit trigger fires while other required members are still actively working (phase-closure race)**; **a message routes to a slot whose durable member is unavailable or whose fresh member fails to instantiate (routing-target resolution failure)**. The last two are likely the messiest runtime edges of the whole design — closure and failure, not the happy path — and deserve concrete answers before implementation starts, not just a passing mention here. If a scenario forces a new top-level table or engine concept rather than composing the pieces above, that's a real signal this design missed something — not just a corner case to special-case away.

## What this session did not decide

- Exact table/column DDL for `team_run_members`, the authority join table, or `agent_reflexes`' run-scoping column — this doc is architecture-level agreement, not migration-ready schema.
- Whether a flex step's `active_slots` and exit trigger can vary per-step within one TeamRun, or whether a Team's routing/authority config applies uniformly across every flex step in a run. Real scoping question, not resolved here.
- Whether Team (organization) and Workflow (execution shape) should eventually become two independently-reusable, separately-authored definitions that combine at launch (so one Team can run against multiple different phase-sequence shapes — the same topology running "deep research" vs. "quick answer" phase sequences, for example) — the SME example above keeps them fused (a Team's own phase sequence compiles 1:1 to its WorkflowDefinition) because that's the smaller surface area for a first pass, but it's a plausible later extension, not ruled out. Implementation should keep the compile-Team-phase-sequence-into-WorkflowDefinition step as one clean function/module boundary with the phase sequence treated as its own addressable sub-structure within the Team definition, even though authored together for v1 — so a later split is "accept a phase sequence from a second source" rather than a rewrite of the compiler itself.
- The specific set of authority verbs beyond `may_spawn`/`may_message`/`may_not_review` (e.g. `may_delegate`/`may_approve`/`may_signal`, or per-slot tool-surface restriction beyond what `agent_parent_dispatch_allowlist` already covers) — flagged above as a real gap, verbs not locked here.
- Multi-member slot addressing (`@engineer` when the slot resolved to three concrete members) — route-to-one, broadcast, or address-a-specific-member are all plausible; no syntax or default is chosen here, deliberately, since real usage should inform it.
- Exit-trigger authority in the general case: whether it's always `orchestrator`-only, `any(active_slots)`, a specific slot, or varies per flex step — the separation (trigger truth vs. permission to produce it) is decided; the default policy is not.
- Whether `min`/`max`/elastic slot resolution (spawning engineer #2 mid-run) needs a Team-level self-tool, or reuses `nanite_execute_task`'s existing dispatch surface with a slot-name argument.
- Any implementation sequencing/task breakdown — none of this is scheduled or filed yet.

## Status

Design discussed and aligned with the operator 2026-08-20, including explicit resolution of both load-bearing forks (gate enforcement, TeamRun substrate), followed by a second independent review pass (same day) that tightened flex-step lifecycle semantics, separated exit-trigger truth from exit-trigger authority, flagged multi-member slot addressing and `may_message` verb-granularity as open questions rather than settled ones, sharpened routing-provenance terminology, and added two failure-mode stress tests (phase-closure race, routing-target resolution failure). No code or schema changed. Implementation is deferred to follow-up work, not yet filed.
