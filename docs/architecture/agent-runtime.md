# Agent runtime ownership

Nanite compiles product inputs (agent/boot profile, work directory, child
environment, sandbox profile, recovery metadata, and UI sinks), then hands the
resolved launch to `go-agent-wrapper` v0.9.0. `wrapper.Wrapper` owns native and
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

When a canonical `runtimeevents.Sink` is injected, every normalized event
reaches it before Nanite projects the small legacy subset onto chat
SSE/provider callbacks. Process, lifecycle, interrupt, permission, raw-I/O,
and unknown future kinds are forwarded intact. There is deliberately no
default raw-event persistence: prompts, tool payloads, stdout, and stderr may
contain secrets, and an unredacted append-only workspace journal would be
unsafe and unbounded. An injected sink owns redaction, retention, bounds, and
lifecycle. A durable/redacted public feed is a separate CW-20260904-0129
contract; this change exposes no raw events through the API.

Native selection remains Nanite product policy: Claude uses streaming stdio;
Codex and OpenCode use subprocess-per-turn. The selection is expressed through
`adapters.Select` with Nanite's already-configured `provider.CLIAdapter`.
Per-session environment isolation uses `wrapper.ChildEnvironment` in replace
mode, so no shell or generated process wrapper is involved. ACP selection uses
the wrapper's shipped Claude, Codex, OpenCode, Copilot, and Pi ACP adapters.

User stop and same-session takeover cancel the Nanite generation context and
request `Wrapper.CancelTurn` against the exact wrapper and router token bound
when that generation admitted its prompt. No delayed cancellation or router
cleanup resolves a mutable session-ID binding. The request is bounded and does
not block the API caller; the generation remains a takeover barrier until the
provider request and predecessor terminal/cleanup boundary complete.
ACP uses its real turn-scoped cancel. Native runtimes honestly report that
turn cancellation is unsupported, so Nanite stops that exact wrapper and the
next turn cold-boots instead of pretending a wire-level cancel occurred.

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
live with the v0.9.0 wrapper API):

| ACP adapter | Measured provider behavior |
|---|---|
| Claude | One ordinary Bash call executed internally without asking; other operation classes are unmeasured. |
| Codex | One ordinary shell call executed internally without asking; other operation classes are unmeasured. |
| OpenCode | One shell shape executed internally without asking; this is not exhaustive. |
| Pi | No request was observed; `pi-acp` documents local filesystem and terminal execution. |
| Copilot | Copilot CLI 1.0.12 asked before one non-mutating `pwd`/`execute` shape; other shapes are unmeasured. |
