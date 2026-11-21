# Designing user / agent / system workflows

A module is a unit of work: primitives, data, and agent behavior assembled around an outcome. A form may capture an input; a view may show an artifact. Neither alone specifies how work proceeds.

For each state transition record the trigger, actor, required evidence or revision, permission boundary, mutation, published event, visible feedback, and next actor. Keep suggested transitions separate from applied transitions. Persist enough state to resume after reconnect or restart; decide how pending decisions are reconciled with their source records.

## Reference workflows

**Operations** (`modules/ops/ops.go`, `tools.go`, `store.go`, `cite.go`, `web/`): workstreams move through owner-controlled stages; queue order is explicit. Commands open the board or a keyed workstream. Tools operate on the same records, updates use revisions, slots describe cached state, and `ops.changed` refreshes views. The module never launches or claims work itself. Its cached external read-back loop is not a reason to poll from a page. Build local view updates on events.

**Workbench** (`modules/workbench/workbench.go`, `tools.go`, `library.go`, `runner.go`, `web/index.tsx`): create/import an agent, edit and validate files, save versions, run requested evals under budgets, inspect failures, review proposed changes, then accept/reject and compare. `workbench.changed`, `workbench.run`, and `workbench.proposal` notify views. Gateway calls and paid evals are explicit owner actions; accepting proposals is owner-only. An agent library entry being edited is distinct from the specialist currently assisting the owner.

For a new workflow, borrow these mechanics and specify its own authority and source of truth. Keep authoring, proposal, approval and execution visibly separate when they are different operations. Explain unavailable configuration and recoverable failures with a next action, rather than a silent empty view.

## Cards, questions and errands

`Host.Cards()` owns card state. `Show` returns an ID; `Update` changes an open card; `End` closes it; `Await` waits until it closes or the context ends. `Respond` records an outcome once under the core's lifecycle rules. Read the returned status and outcome; a canceled, expired, superseded or failed card is not approval. A stable source/kind/subject lets a new card supersede an open one about the same work.

Prefer built-in kinds: info, error, confirm, prompt, choice, form, approval, progress, plan. `Blocking` means the sender awaits an answer. Outcomes separate free-form `Data`, question `Answers`, and item `Decisions`. Core handling of late answers is specifically for abandoned/expired agent questions; do not assume a module's expired approval can be applied later. Revalidate the affected record and proposal revision before mutation.

A custom kind belongs in the module web export's `cards` map, keyed by kind (or kind@version). Its `CardProps` provide card, respond(outcome), and module api. A blocking custom card participates in attention. Use existing registry renderers when their semantics fit; inspect current payload types rather than guessing JSON.

`Host.Agent().Ask(ctx, module.Ask{Title, Text})` is a different operation: a module asks the embedded agent to do an errand on the shared conversation. It returns a run ID without waiting and may return `ErrAgentBusy` or `ErrNoAgent`. Describe the request as the module, never impersonate the owner; keep pending state if retry is required. It is not a human approval.

`Manifest.Around` contributes a short cached summary to every turn (bounded by the core to 1.5 seconds and 1500 characters). Use it for outstanding decisions or arrived results; avoid network work there.
