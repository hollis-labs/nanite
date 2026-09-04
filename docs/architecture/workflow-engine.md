# Agent Workflows engine boundary

Nanite embeds `github.com/hollis-labs/go-workflow` at the exact version in
`go.mod`. That library is the only Agent Workflows sequencer. It owns graph and
plan compilation, validation, deterministic transitions and events, waits,
retries, fan-out, dynamic calls, compensation, verification, memoization, and
the conformance contract.

Nanite remains the product host. It owns workflow authoring DTOs, HTTP and SSE
compatibility, authentication and policy, Agent/Turn/tool/approval/Loop/Team and
external-framework StepKinds, SQLite adapters, go-scheduler activations, and
artifact storage. A StepKind may call a Nanite service, but it may not choose the
next workflow node or maintain a second workflow state machine.

## Durable identity

Every new run records the engine identity
`go_workflow_v0.1.0@v0.1.0`, its immutable plan material, source and plan
digests, the exact StepKind and verifier catalogs, and the host-contract digest.
Resume loads that persisted identity and material; it never resolves a mutable
"latest" definition. Runs created during the Hadron pilot retain
`hadron_v0.5.0-beta.2@v0.5.0-beta.2` and use their frozen nine-kind catalog for
recovery only.

Definition publication is plan-first. Nanite stores immutable authored bytes in
`workflow_definition_revisions`, then moves `workflow_definition_heads` with a
generation compare-and-swap. A run remains tied to the plan it started with even
after the definition head advances.

## Product execution paths

Named workflow launches, direct API source launches, scheduler dispatch, A2A,
Loops, Teams, approvals, cancellation, and authenticated external callbacks all
enter the same durable host. The host invokes Nanite work through StepKind
adapters:

- LLM, Turn, Worker, and Reviewer call the bounded `StepExecutor` turn loop.
- Tool calls use the normal Nanite tool and permission boundary.
- Approval and callback steps create durable, authenticated waits.
- Loop and Team steps delegate product behavior through narrow host interfaces
  and persist their correlation before returning.
- LangGraph, CrewAI, Google ADK, AutoGen, and LangChain integrations are one
  external-framework StepKind. They are not alternative workflow sequencers.

`internal/agentworkflow` is consequently a product translation package: DTOs,
YAML decoding, required-input declarations, registry lookup, and execution
requests. It must not contain a scheduler, DAG executor, retry loop, or runtime
state machine. `internal/workflowhost` is the sole Nanite adapter to the shared
library. Architecture tests enforce the exact module version and reject the old
Hadron application dependency and retired local runtime.

## Compatibility

The HTTP Agent Workflows surface defaults to the shared host. The query alias
`engine=hadron` remains accepted for pilot clients and addresses both exact
pilot and current shared rows. `engine=legacy` is read-only: terminal historical
rows remain queryable, while legacy execution returns HTTP 410.

SSE preserves the existing event payload and also emits stable named event
types with monotonic cursor IDs. Clients may resume with `Last-Event-ID` or the
`after` query parameter. New UI clients listen for the named events; the
unnamed-message listener remains for compatibility.

## Playbooks

Playbooks are parameterized session-context templates. An LLM interprets their
rendered text; they do not sequence Agent Workflow nodes and are not an
execution engine. A Playbook can instruct an agent to launch a named workflow,
but the resulting run still executes exclusively through the shared host.
