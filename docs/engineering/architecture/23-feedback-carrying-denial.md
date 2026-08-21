# Feedback-Carrying Denial

A follow-up architecture topic from the harness audit: Nanite already produces useful failure prose in places; formalize it — a denial should carry a decision, a reason, and where possible a context-specific suggestion for what to try instead, with provenance, while keeping enforcement itself hard. This is the concrete implementation of an already-stated principle ([00-overview.md](00-overview.md): "Hints, not control") applied to the one place it wasn't yet: things the system refuses outright.

## The foundation already exists — for one narrow case

`internal/recover` implements exactly the right shape, today, for schema/argument-repair failures: `RecoverableError{Kind, ToolName, SentArgs, SchemaURI, ErrorPath, ErrorReason, Suggestion}`. `ErrorReason` and `Suggestion` are precisely "why" and "what to try" — this is the target shape, not a new invention. It's the deterministic classification layer (C1) of a self-healing-tool-surface design whose C2 layer anchors LLM-based auto-repair on the `Kind`.

Critically, `isUnrecoverableProse` deliberately excludes permission/auth/forbidden/service-unavailable failures (`KindNone`) from this envelope — the design's own "fail loudly" rule: no payload-shape repair can fix a policy denial, so C2 shouldn't burn a turn trying. That exclusion is correct for *auto-repair eligibility*. It is not a reason the *structured feedback* (reason + suggestion) has to be withheld too — those are two different questions this doc's target design keeps separate.

## What's actually reaching the model today: four inconsistent, mostly-flat surfaces

- **Tool permission denial** (`internal/permission/engine.go`'s `CheckResult{Decision, Reason string}`) — `Reason` is always a flat prose string (e.g. `"plan mode — write operations blocked"`), surfaced verbatim as `"PERMISSION DENIED: %s — %s"` (`chat_tool_executor.go`). No suggestion field. `internal/service/tool_execution_rules.go`'s backstop is a single static string with zero context on what *is* allowed.
- **Human-reject on an approval request** — the sharpest gap. `RespondApprovalRequest{Decision, Scope}` (`internal/api/types.go`) has no feedback field at all. A human rejecting an approval cannot supply free text; the model always receives a hardcoded `"user denied"` (`chat_tool_executor.go`), never anything the human actually typed.
- **Plugin pre-hook denial** (`tool.executing`, `EmitPreHook` returns `bool` only) — the richest *generic* message in the codebase today: `"Tool %q was refused by a policy plugin for this input... adjust the arguments, pick a different tool, or explain..."` (`chat_tool_executor.go`). Rich, but hardcoded and identical for every plugin/every denial — never the plugin's actual reasoning, because the hook contract has nowhere for the plugin to put it.
- **MCP trust-tier rejection** (`internal/mcp/validate.go`'s `ValidateResultSize`, `ValidationError{Reason}`) — falls through to a flat `"Error: %v"`. Real telemetry exists (`span.RecordError`), but it's operator-visible only; the model gets no guidance (e.g. "narrow the query / paginate") despite this being one of the more mechanically fixable failures in the list.
- **Reflex `halt_session`** — the model never sees this at all. It aborts the turn *before* the LLM is called (`chat_generate.go`); the reason reaches only the human/API/UI layer via an `ErrorEvent`. Provenance is unusually complete here (`AgentReflex.ProvenanceTier`, a structured `event_log` write) — it just never round-trips into the conversation.

## Target design

**Baked into core enforcement machinery, not reflex-routed.** This is a hard-enforcement layer that also happens to explain itself — not an advisory nudge. Reflexes stay the separate, model-never-decides steering primitive ([03-steering.md](03-steering.md)); this doc's mechanism lives in the permission engine, the tool executor, the plugin host, and the MCP validator directly.

**One shape, reused everywhere a denial can talk back to the model.** Extend `internal/recover`'s existing taxonomy with new `Kind` values for policy-class denials — `KindPermissionDenied`, `KindCapabilityForbidden`, `KindResultTooLarge`, `KindPolicyRefused` (naming illustrative, not final) — each still populating `ErrorReason`/`Suggestion`, each explicitly marked ineligible for C2's automatic LLM-repair loop. This preserves "fail loudly = no auto-retry" exactly as designed while closing the actual gap: the model still gets told why, and — where the denying subsystem can produce one — a context-specific suggestion, not a generic template.

Four concrete extension points:

1. **Permission engine** — `CheckResult.Reason` (flat string) becomes a structured envelope; the rule/scope that denied gets to attach a suggestion (e.g. which tool/scope *would* be allowed).
2. **Human-reject feedback** — add a `Feedback` field to `RespondApprovalRequest`. When present, it becomes the model-visible `Suggestion`/`ErrorReason` instead of the hardcoded `"user denied"`. This is the one gap with no existing partial coverage at all — pure addition.
3. **Plugin pre-hook contract** — extend `EmitPreHook`'s return from bare `bool` to `{Allow, Reason, Suggestion}` (or equivalent), so a policy plugin can supply its own context-specific reasoning. The current generic template becomes the fallback only when a plugin returns a bare bool (backward-compatible with plugins that don't opt in).
4. **MCP trust-tier rejection** — attach a `Suggestion` (narrow the query, paginate, request specific fields) alongside the existing `Reason`, closing the model-visible half of a rejection that already has real operator-side telemetry.

**Reflex `halt_session` is a genuine special case** — there is no same-turn tool-result to attach a suggestion to, because the turn never reaches the LLM. Target: the halt's reason/guidance rides the Recovery Pack's next-cold-boot context replay ([06-session-lifecycle-and-recovery.md](06-session-lifecycle-and-recovery.md)) so a resumed session isn't blind to why it was halted, rather than trying to force a same-turn message that structurally can't exist.

**Provenance** — each denial envelope carries a `Source`/origin tag (which policy rule, plugin, or subsystem produced it), consistent with "prefer explicit provenance." MCP trust-tier rejection already has span-level telemetry; this design adds the model-visible half without duplicating what already exists on the operator side.

**Approved for implementation, 2026-08-21** — the target design above (the four extension points, the shared Kind/Reason/Suggestion/Provenance envelope, no-auto-repair for policy kinds) is real, scoped, sequenced work, tracked under `TASKS/feedback-carrying-denial/` (see that folder's `README.md` for the task breakdown).

## What's genuinely still open

- Whether `internal/recover` itself is extended in place, or a structurally-identical sibling type is introduced and reused across these four surfaces — an implementation-time call. The target *shape* (Kind/Reason/Suggestion/Provenance, no-auto-repair for policy kinds) is decided; the package boundary is not.
- The exact new `Kind` names — illustrative above, not final.
- Whether the plugin pre-hook contract change is backward-compatible enough to land without a plugin-SDK version bump — real implementation detail, not resolved here.
