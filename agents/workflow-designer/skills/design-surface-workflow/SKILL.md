---
name: design-surface-workflow
description: Design or revise an interactive Surface module spanning user decisions, agent work, and system state. Use for workflow briefs and module implementation plans, including views, tools, slots, cards, and review handoffs.
---

# Design a Surface workflow

Use the accompanying `context/surface-module-contract.md`, `context/surface-workflow-design.md`, and `context/surface-module-ui.md` in the agent directory. This pack is a contract guide, not an agent identity or runtime configuration. Its source revision and compatibility are recorded in `context/surface-module-pack.md`; check the target checkout's contract before implementation.

Start from the user's desired outcome and the object whose state changes. Describe the sequence of user, agent, and system actions, including who moves next, what evidence is available, and what happens on cancellation, failure, or a stale answer. Design the views around that sequence.

Produce a reviewable brief with:

- The outcome, actors, durable records, source of truth, states and transitions.
- A mapping from each transition to a user action, server command/tool and its level, validation, resulting event, and visible result.
- Views and focus context; slots and bounded live knowledge; the specialist's role and limits.
- Questions or approvals that stop work, what answer resumes it, and how outdated proposals are rejected.
- Cite references, marks, settings and data ownership; empty, waiting, failed and completed experiences.
- A concrete walkthrough and acceptance checks, followed by the files to create or change.

Use existing module capabilities before adding a new host contract. Read `module/` for signatures, `modules/ops` for an owner-controlled work lifecycle and `modules/workbench` for authoring/validation/version/eval/review. Their domain restrictions are examples, not automatically permissions for a new module. Coordinate visual review through the app's existing design workflow when requested; a design brief does not authorize external sends or deployment.

Keep the server's tools usable headlessly. A successful walkthrough must show the same state after a user action and the corresponding agent tool, and show the view refreshing from an event. Include the owner typing while an agent proposes a change, and a repeated or outdated response when the workflow has asynchronous decisions.

Before delivery, check the seam, manifest/web entry names, generated app wiring, registry consistency, and relevant behavior tests. Use a disposable app data directory. Distinguish a proposed workflow, implemented behavior, and a live smoke-tested specialist in the report.
