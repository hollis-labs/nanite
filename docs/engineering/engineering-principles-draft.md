# Nanite / Hollis Agentic Engineering Principles — Draft

## Core principles

### Prefer dimensions of composition over feature-specific mechanisms
Add reusable orthogonal primitives when they expand the system's composition space. Teams are the canonical example: existing agents, scopes, messaging, lifecycle, skills/SOPs, reflexes, permissions, workflows/gates plus small membership/routing semantics.

### Abstract the intent, not the capability away
Stable application vocabulary expresses semantic intent; adapters/providers implement mechanics. Preserve capability discovery and extensions so richer providers are not flattened to a lowest common denominator.

### Abstract around volatility you understand, not futures you can imagine
Provider/model/harness/transport/memory churn are known volatility axes. Avoid abstraction merely because another implementation is imaginable.

### Keep canonical semantics centralized; materialize runtime-specific representations at the boundary
Keep roles, policies, skills, context, tools, agent configuration, and project instructions canonical. Plant/render `CLAUDE.md`, `AGENTS.md`, MCP/config files, boot prompts, provider settings, etc. at runtime boundaries.

### Materialize assets at runtime
Treat composition as compilation:
```text
canonical config + scopes + grants + runtime inputs
                 -> concrete executable environment
```
Prefer deterministic preparation before spending an LLM turn when possible.

### Separate policy from mechanism
Infrastructure owns enforcement/interception mechanisms; product/domain layers own policy. Routing, authority, gates, and approvals are distinct concepts.

### Hints for fluid behavior; deterministic enforcement for invariants
Use nudges/reflexes/feedback for adaptive behavior. Use workflow gates, permissions, sandbox restrictions, cancellation, or process termination for guarantees.

### Preserve a safety/control ladder
Keep message/remind, steer, safe halt, cooperative cancel, hard kill, revoke, and recovery semantics distinct. Advertise actual provider capabilities.

### Identity is not execution
Durable identity, session, process/runtime, team membership, and run/objective lifetime are orthogonal. Durable does not mean always on.

### Organization is not execution
Teams describe organization/topology/authority/routing. Workflows prescribe execution. Fluid coordination can reuse workflow substrate without a second orchestration engine.

### Reuse existing state before creating parallel state
Prefer derived views over messages, event logs, reflex firings, scopes, and workflow runs. Persist only genuinely new facts.

### One core, several doors
GUI, headless/API, MCP/A2A/ACP surfaces consume one execution model. Access method and launch method should remain orthogonal.

### Normalize semantics above transports
Product code reacts to normalized operations/events, not provider wire formats. Keep protocol and transport distinct; retain raw events for diagnostics.

### Host local execution; do not let the Host become the product
Host owns process/environment mechanics. It knows **how to run an agent**, not **what the agent means** to the calling product.

### Product apps remain independently useful
Put common infrastructure in libraries/hosts rather than forcing whole-app dependencies. Requiring another app for a product's core function is a boundary smell.

### Plugins declare; host validates and grants
Hooks/filters are extension behavior. Manifests declare surface. Distinguish declared/granted/used capabilities. Prefer narrow host APIs and transactional registration.

### Standard-compatible core; explicit additive extensions
Keep Agent Skills, ACP, and other standards valid/predictable. Nanite parameters, composition, security, materialization, and richer features are explicit additions.

### Treat executable content as code, not prose
Executable skill nodes are explicit, structurally parsed, provenance-aware, policy-controlled, sandboxed, and hash-aware. Default to read/compute/materialize; side effects require opt-in.

### Conversation recovery and filesystem recovery are separate axes
Logical session/event recovery and physical filesystem snapshots are independent mechanisms.

### Durable delivery targets identity, not merely a running process
Delivery and wake policy are separate. Running state is incidental to durable messaging.

### Deterministic routing does not require deterministic workflow
Routing can be predictable while agents self-organize inside the permitted topology.

### Prefer bounded autonomy
Define who may exist, spawn, communicate, delegate, approve, access resources, and terminate. Let agents adapt inside that envelope.

### Emit -> react is a recurring primitive
Meaningful signal -> deterministic/advisory reaction appears across Reflexes, Teams, runtime activity, plugins, and policy. Do not prematurely turn it into a universal event bus.

### Prefer explicit provenance
Track why an action/context/result exists: user, agent, role/team, skill chain, plugin, reflex, provider, workflow/run, policy decision, materializer.

### Fail closed where authority matters; fail usefully where reasoning can recover
Never silently elevate permissions. Where safe, return model-visible correction/alternatives instead of context-free denial.

### Contract-as-tests for pluggable systems
Adapters, providers, plugins, telemetry backends, and extension points should have conformance suites where practical.

### Architecture rules should be enforceable
Use package boundaries, types, CI/static tests, schemas, and conformance tests to make important architectural rules executable rather than aspirational.

### Do not generalize until real pressure exists
Recognize broader abstractions early, keep current structures compatible with them, but extract only when multiple real use cases create duplication/friction.

## Recurring boundary sketches

```text
Product semantics
    -> AgentRuntime
        -> Direct API | Agent Host
            -> ACP/native protocol
                -> provider/model
```

```text
Team = organization
Workflow = prescribed execution
Reflex = detect/react
Skill/SOP = reusable knowledge/process
Harness = runtime mechanics
```

```text
Routing  = where?
Authority = may?
Gate      = what must be true?
Approval  = who/what satisfies it?
```

```text
Canonical semantics
    -> resolver/composer
        -> materializer
            -> runtime-specific artifact
```

## Review questions for new features
1. Is this a genuinely new fact/mechanism or a composition of existing primitives?
2. Is the abstraction isolating a known volatility axis?
3. Are semantic intent and provider mechanics separated?
4. Is policy separated from enforcement mechanism?
5. Can the rule be enforced/tested?
6. Are we duplicating state already represented elsewhere?
7. Does this preserve provider-specific capabilities?
8. Does it keep products independently useful?
9. Is runtime-specific configuration materialized from canonical semantics?
10. Is hard enforcement used only where a real invariant requires it?
11. Is provenance sufficient to explain/security-audit the behavior?
12. Are recovery axes and lifecycle identities being accidentally conflated?
