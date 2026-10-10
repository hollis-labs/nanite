# Code Mode Architecture

Status: Approved (2026-10-03, Tesseract design `01M4188548BRN1PTM3ER8BWESW`)

Code Mode isolates intermediate tool calling, reasoning, and data manipulation within an ephemeral fork of the user's session. The main agent sees only the final structured result of the task, hiding the underlying tool traffic from the primary interface while retaining it for audit.

## Goal

- **Result-oriented Main Session:** The main session should only display the request and the answer, not the intermediate steps (like reading files, executing scripts, etc.).
- **Durable Trace:** All intermediate tool calling, thinking and output happen in a "code mode" fork.
- **Approval Relay:** Any required user approvals inside the fork (e.g. nested tool asks) surface seamlessly in the parent session.

## Architecture

### 1. Execution Model (`code_task` Tool)
The main agent invokes a `code_task(goal, return_spec, inputs?, mode?)` tool. This operation:
1. Forks the session with a specific `fork_kind: code_mode` (pending implementation).
2. Launches the fork using a hardened code-mode profile.
3. Operates synchronously or asynchronously, returning a structured result: `{status, result, artifacts[], cost, fork_session_id}`.

### 2. Fork Session Context (Boundary Definitions)
The current `store.ForkSession` implementation (in `internal/store/sessions.go:694` and `service/session.go:37`) copies the full history or nothing (`copyMessages bool`). The approved Code Mode target restricts this:
- **Default Context:** The last N messages (configurable per profile) + goal, inputs, pinned handoff, and an explicitly passed scratchpad.
- **Opt-in Full History:** Full-history copy is relegated to an opt-in override.
- **History Search Tool:** A new tool provides the fork on-demand access to the parent's older messages.
- **Overrides:** The fork overrides tool grants, permission posture, and harness profile.

### 3. Nested `code_task` Refusal
To prevent runaway recursion and maintain predictable boundaries, a Code Mode fork cannot itself invoke a `code_task`. This refusal is enforced at the tool boundary.

### 4. Approval Relay
The fork seamlessly relays required approvals (like `Ask` checks for sensitive tools) back to the parent session. The parent attributes the approval request to the specific `code_task` execution, blocking only that fork until answered. If denied, a structured refusal is returned directly to the fork.

### 5. Verified Actor & Privilege Isolation
The fork operates under its own verified execution tier:
1. **Direct Tools**
2. **Hardened `python_run`:** Features typed stubs, `search_tools` / `describe` discovery inside the sandbox, strict network isolation, and persisted per-call args/results via the existing caching/redaction path.
3. **Blueprint Library:** Reusable, fork-authored routines saved as definition revisions. Exposed via a virtual tool MCP server and synced to Hadron.

### 6. Lifecycle
Code Mode forks are **ephemeral** by default. They spawn, process the task, yield the result, and exit. An opt-in **companion mode** allows a long-lived fork to be reused across multiple `code_task` calls within the same parent session.

## Implementation Gap: Missing Fork Primitives
The current mainline implementation lacks the required contextual fork primitives. `ForkOpts` (`internal/service/session.go`) and `ForkSession` (`internal/store/sessions.go`) currently map directly to an `IncludeMessages` boolean, lacking support for:
- `fork_kind` categorisation.
- "Last-N" message slicing during fork creation.
- Scratchpad and selective context injection.
- Policy/tool-grant overrides for the child session.

These primitives must be implemented before the `code_task` orchestrator can safely dispatch forks without leaking full history or granting overly permissive parent toolsets.
