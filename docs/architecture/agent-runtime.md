# Agent runtime ownership

Nanite compiles product inputs (agent/boot profile, work directory, child
environment, sandbox profile, recovery metadata, and UI sinks), then hands the
resolved launch to `go-agent-wrapper` v0.9.1. `wrapper.Wrapper` owns native and
ACP start, resume/new, prompt, turn cancellation, close, provider session ID,
event draining, process exit, and cleanup. ACP wrappers share one
`acp.Manager`; Nanite's `SessionManager` is only the runtime-ID-to-wrapper
binding used by chat lookup, recovery replacement, orphan checks, and daemon
shutdown. It does not keep a second lifecycle state.

`SessionManager` admits a launch before `Wrapper.Run` begins. Shutdown closes
that admission boundary first, stops all de-duplicated wrappers concurrently,
and waits for ready, pre-ready, and recovery-pending Run tails under one shared
deadline, so a concurrent Boot cannot escape the stop set. Non-chat Boots
retire their exact pointer in the Run tail. Chat explicitly transfers that
retirement to its recovery observer, which may need to claim a same-ID recovery
lease through broker adoption. Stale observers neither clear successor state
nor dispatch recovery over it; runtime retirement never deletes turn-owned
slot, tool-partition, or router state.

Every normalized event reaches Nanite's canonical sink before the small legacy
subset is projected onto chat SSE/provider callbacks. The canonical sink now
also feeds the bounded, allowlisted [host runtime feed](host-runtime-feed.md).
The source event itself is never persisted: prompts, tool payloads, stdout,
stderr, and model deltas may contain secrets. The public feed stores metadata
and explicitly safe per-kind state only, with independent cursor, retention,
gap, redaction, and runtime-generation semantics.

Native selection remains Nanite product policy: Claude uses streaming stdio;
Codex and OpenCode use subprocess-per-turn. The selection is expressed through
`adapters.Select` with Nanite's already-configured `provider.CLIAdapter`.
Per-session environment isolation uses `wrapper.ChildEnvironment` in replace
mode, so no shell or generated process wrapper is involved. ACP selection uses
the wrapper's shipped Claude, Codex, OpenCode, Copilot, and Pi ACP adapters.

A turn sent while another is running on the same session queues behind it:
it starts once the running turn has finished, and the agent receives it as its
next turn. A mid-run message therefore steers at the next turn boundary; it
never interrupts the running turn or loses its reply.

Interrupting is explicit. User stop cancels the queued turns and the running
one, and requests `Wrapper.CancelTurn` against the exact wrapper and router
token bound when the running generation admitted its prompt. No delayed
cancellation or router cleanup resolves a mutable session-ID binding. The
request is bounded and does not block the API caller; a canceled generation
remains a barrier until the provider request and predecessor terminal/cleanup
boundary complete. The interrupted turn's output so far is saved, marked
`interrupted` in its metadata.
ACP uses its real turn-scoped cancel. Native runtimes honestly report that
turn cancellation is unsupported, so Nanite stops that exact wrapper and the
next turn cold-boots instead of pretending a wire-level cancel occurred.

## Subagent runtime

A subagent's runtime follows its parent. A CLI process's calls to Nanite's own
tools do not pass through the chat harness, so none of the result cache, the
truncation or the per-turn ceilings apply to them; a subagent run through the
chat harness gets all of it. From an API-driven parent — one whose provider has
no CLI adapter — a subagent therefore runs through the harness unless the user
or app opted into CLI subagents.

The opt-in is `subagent_runtime` (`api` or `cli`): the override on the **root**
session of the spawn tree, then the app default in user settings, then `api`.
The root, not the immediate parent, so the choice made where the tree started
holds at every depth: a CLI opt-in on the root reaches a grandchild spawned by
an API child, and an API root keeps API under a CLI app default. Session create
accepts the override on both `POST /api/sessions` and the harness v1 create. A
model-supplied `provider` argument and a role's `DefaultProvider` never count as
the opt-in. When either names a CLI provider without it, the child runs on the
parent's provider and model, the log records a warning, and the result the
parent reads opens with a note saying so. A parent session with no provider of
its own falls back to the app's default provider and model; if there is none, or
it is itself a CLI provider, the spawn fails saying so rather than running with
an empty provider. A CLI-driven parent keeps booting CLI children. The parent's
dispatch interface is the same either way.

`subagent_runtime` says how a *child* of an API-driven parent runs. It is
separate from an agent's `runtime_kind`, which says how that agent's own session
runs; the subagent path routes by the child's provider and does not read the
role's `runtime_kind`.

This is an interim home for the setting; it moves onto the launch-assignment
limits once those exist.

## ACP permission requests

Nanite configures `ACPBestEffortPermissionRequestResponder` only when both the
existing permission engine and the session SSE sink are available. When an ACP
provider sends `session/request_permission`, the bridge creates the same
`approval_request` consumed by the existing API/UI and waits for its
`allow|deny` plus `once|session` response. It returns only an exact option ID
that the provider offered:

- allow/once selects `allow_once`; it never widens to `allow_always`;
- allow/session prefers `allow_always`, then safely narrows to `allow_once`;
- deny/once selects `reject_once`; it never widens to `reject_always`;
- deny/session prefers `reject_always`, then narrows to `reject_once`.

If no semantically safe offered option exists, the bridge returns the zero
selection and the wrapper cancels the request. If Nanite's approval engine or
SSE sink is absent, the responder remains nil: wrapper's documented safe
default applies (Claude/Codex/OpenCode/Pi answer canceled; Copilot retains its
method-not-found fallback).

This is best-effort provider cooperation, not a general execution gate. It can
act only when the provider asks. Some ordinary provider operations may not send
an ACP permission request. Nanite's toolclient/RPC authorization remains the
authoritative pre-execution gate for host-owned tools and is not routed through
the wrapper policy-observer API.

The measured provider coverage is deliberately narrow (the canonical details
live with the v0.9.1 wrapper API):

| ACP adapter | Measured provider behavior |
|---|---|
| Claude | One ordinary Bash call executed internally without asking; other operation classes are unmeasured. |
| Codex | One ordinary shell call executed internally without asking; other operation classes are unmeasured. |
| OpenCode | One shell shape executed internally without asking; this is not exhaustive. |
| Pi | No request was observed; `pi-acp` documents local filesystem and terminal execution. |
| Copilot | Copilot CLI 1.0.12 asked before one non-mutating `pwd`/`execute` shape; other shapes are unmeasured. |
