# Plugin pre-hook contract: Reason (and, for builtins, Suggestion)

**Phase:** 2 — The four extension points (`TASKS/feedback-carrying-denial`)
**Status:** not-started
**Depends on:** `01-shared-kind-taxonomy-and-auto-repair-gate.md`; sequence after
`03-human-reject-feedback.md` (shares `chat_tool_executor.go` — a different, non-overlapping
block from `03`'s, but coordinate merge order rather than dispatching as true concurrent
commits, since three tasks in this batch touch that one file)
**Touches:** `internal/plugin/events.go` (`Host.EmitPreHook`), `internal/plugin/subprocess/plugin.go`
(`subprocessEventHook.Handle`), `internal/service/chat_tool_executor.go` (`tool.executing`
call site), `internal/service/chat_generate.go` (`message.sending` call site),
`internal/service/events_composite.go` (`context.pre_compact` call site — compile-compat
change only, see Context).

## Context

`docs/engineering/architecture/23-feedback-carrying-denial.md`: "extend `EmitPreHook`'s
return from bare `bool` to `{Allow, Reason, Suggestion}` (or equivalent) so a policy plugin
can supply its own reasoning. Today's generic hardcoded template... becomes the fallback
only when a plugin returns a bare bool." Doc 23 flags as genuinely unresolved: "whether the
plugin pre-hook contract change is backward-compatible enough to land without a plugin-SDK
version bump."

**This resolves cleanly, and asymmetrically, once traced end-to-end — the single most
load-bearing finding of this planning pass.**

- `Host.EmitPreHook` (`internal/plugin/events.go:593-637`) loops over `hooks, exists :=
  h.eventHooks[eventType]` — each element is an `eventHookEntry{hook plugin.EventHook,
  pluginID string}` (`internal/plugin/host.go:84-87`). The loop already has `hook.pluginID`
  in scope (currently only used at registration/unregistration time) — **free, no-extra-
  plumbing provenance** for the plugin-prehook `Source` tag.
- For a **subprocess** plugin, the hook is `subprocessEventHook` (`internal/plugin/subprocess/plugin.go`),
  whose `Handle` (line 549) calls `CallResult[EventHandleResult](h.transport, ctx,
  MethodEventHandle, params)` and, on `result.Cancel == true`, returns `plugin.ErrCancelled`
  (line 617). `EventHandleResult` is a type alias
  (`internal/plugin/subprocess/protocol.go:51`: `EventHandleResult = sdksub.EventHandleResult`)
  to `github.com/hollis-labs/plugin-sdk/subprocess.EventHandleResult` — **pinned at v0.3.0
  in `go.mod`, with no local `replace` directive** (confirmed — unlike `go-envelopes`/
  `go-modelsdev`, which *are* locally replaced under `libs/`; `plugin-sdk` is consumed as a
  real published module here). That struct, **in the version already pinned**, is:
  ```go
  // plugin-sdk@v0.3.0/subprocess/types.go:127-131
  type EventHandleResult struct {
      Cancel    bool                 `json:"cancel,omitempty"`
      Reason    string               `json:"reason,omitempty"`
      Envelopes []plugin.EnvelopeOut `json:"envelopes,omitempty"`
  }
  ```
  **`Reason` is already on the wire**, populated by the SDK's own server-side dispatch
  (`plugin-sdk@v0.3.0/subprocess/server.go:293`: `s.writeResult(req.ID, EventHandleResult{Cancel:
  res.Cancel, Reason: res.Reason, Envelopes: res.Envelopes})`). Nanite's host-side
  `subprocessEventHook.Handle` (line 594-619) reads `result.Cancel` but **never reads
  `result.Reason` at all** — it is silently discarded today. Landing `Reason` end-to-end for
  subprocess plugins needs **zero plugin-SDK version bump** — only a host-side code change to
  stop throwing the field away.
  There is **no `Suggestion` field anywhere in `EventHandleResult`/`EventRequest`/
  `EventResponse` at v0.3.0** — a subprocess plugin genuinely cannot send a distinct
  "suggestion" value over the wire today. Adding one *would* require a real plugin-sdk
  version bump (a new optional field — additive, not breaking, but still a cross-repo
  release). Decided for this batch: **not this task's job** — see "What this batch does NOT
  do" below.
- For a **builtin** (in-process) hook, `plugin.EventHook.Handle(ctx, event) error` is a plain
  Go method — no wire protocol at all. `event.Data` is a `map[string]interface{}`
  (reference type), passed by value into `Handle` but sharing the same underlying map with
  the caller's `event` in `EmitPreHook` — confirmed by the **already-existing "legacy" cancel
  convention** at `events.go:632-635`: `if cancelled, ok := event.Data["cancel"].(bool); ok &&
  cancelled { return true }`, checked *after* the hook loop, reading whatever a hook wrote
  into the shared map. A builtin hook can set `event.Data["reason"]`/`event.Data["suggestion"]`
  the same way, and `EmitPreHook` can read both back after the loop — **zero SDK involvement,
  full Reason *and* Suggestion for builtins**, reusing a pattern the codebase already
  established rather than inventing a new one. Confirmed this pattern is real and live today
  (not hypothetical): `internal/memory/extraction.go`'s `perTurnHook`/`postCompactHook`
  (lines 79-156) implement `plugin.EventHook.Handle` directly in Go and are registered via
  `RegisterEventHook` — on post-hook event types today, but the interface conformance and
  `event.Data` sharing mechanics are identical for a pre-hook-registered builtin.

**This batch's decision**: land `Reason` end-to-end (subprocess: read the already-wired wire
field; builtin: read the existing Data-map convention, extended). Land `Suggestion` for
builtins only (same Data-map mechanism, no SDK dependency). Subprocess plugins fold "try this
instead" guidance into the `Reason` string they already send (one prose field, not two, until
a real SDK bump exists) — document this asymmetry plainly in code comments so a future reader
doesn't mistake it for an oversight.

**A tangential, pre-existing gap — not this task's job, flagged so it isn't mistaken for a
missed spot**: `internal/service/events_composite.go:157-164`'s `EmitPreCompact` calls
`c.plugin.EmitPreHook("context.pre_compact", ...)` **inside a `safego.Go` goroutine, and
discards the returned value entirely** — it was never wired for cancellation despite calling
the pre-hook API, and `subprocessEventHook.isPreHookEvent` (`plugin.go:623-629`) doesn't even
list `"context.pre_compact"` as cancel-eligible, so a subprocess plugin's `Cancel` response to
this event type is structurally ignored on both ends already. This task only needs a
compile-compatibility touch here (the call site must still type-check against
`EmitPreHook`'s new return shape) — do not attempt to make this event type actually
cancellable or reason-carrying as part of this task; that's a separate, real gap doc 23 never
asked this batch to close.

## What to do

1. **`internal/plugin/events.go`** — change `Host.EmitPreHook`'s return type from `bool` to a
   small result type, e.g.:
   ```go
   type PreHookResult struct {
       Cancelled  bool
       Reason     string
       Suggestion string
       Source     string // which plugin cancelled, e.g. "plugin:<plugin_id>"
   }
   func (h *Host) EmitPreHook(eventType, sessionID string, data map[string]interface{}) PreHookResult
   ```
   Inside the loop (lines 615-630): when a hook returns `plugin.ErrCancelled` (or wraps it —
   `errors.Is` still applies), read `event.Data["reason"]`/`event.Data["suggestion"]` (string,
   best-effort type-assert, empty if absent or wrong type) and set `Source: "plugin:" +
   hook.pluginID` (the field already in scope on the loop's `eventHookEntry`, per Context).
   The legacy map-based cancel check (lines 632-635) folds into the same result — it already
   reads `event.Data["cancel"]`; extend it to also read `reason`/`suggestion` the same way for
   consistency (a builtin hook using only the legacy convention, never returning
   `ErrCancelled` at all, still gets full Reason/Suggestion support).
2. **`internal/plugin/subprocess/plugin.go`'s `subprocessEventHook.Handle`** (line 549-620) —
   at the `result.Cancel == true` branch (line 616-618), before returning
   `plugin.ErrCancelled`, write `result.Reason` into the shared map: `if result.Reason != "" {
   event.Data["reason"] = result.Reason }`. This is the one-line fix that stops discarding
   the already-wired wire field. No `event.Data["suggestion"]` write here — the wire has
   nothing to put there yet (see Context).
3. **`internal/service/chat_tool_executor.go`**, the `tool.executing` block (lines 255-277) —
   `cancelled := s.pluginHost.EmitPreHook(...)` becomes `result :=
   s.pluginHost.EmitPreHook(...)`; `if cancelled` becomes `if result.Cancelled`. Replace the
   flat `blockMsg` construction (line 263) with a `*recover.RecoverableError{Kind:
   recover.KindPolicyRefused, ToolName: tu.Name, ErrorReason: result.Reason, Suggestion:
   result.Suggestion, Source: result.Source}` rendered via `buildAgentErrorEnvelope`. When
   `result.Reason == ""` (a plugin that only returned bare `ErrCancelled`, the
   backward-compat case doc 23 names explicitly), fall back to the existing generic template
   text as `ErrorReason` — `"Tool %q was refused by a policy plugin for this input..."` — so
   every plugin, old or new, still produces a real envelope, just a less specific one.
4. **`internal/service/chat_generate.go`**, the `message.sending` block (lines 909-930) — same
   `cancelled` → `result.Cancelled` rename. Replace the hardcoded `blockMsg := "Message
   blocked by plugin policy."` (line 923) with `result.Reason` when present, else that same
   fallback string (this surface streams `Content`/`fullContent`/`finalContent` as plain text,
   not a `tool_result` JSON block — do not force the JSON envelope shape here, this is a
   different rendering context from the tool-call surfaces; a plain, reason-aware sentence is
   the right fit, e.g. `fmt.Sprintf("Message blocked by plugin policy: %s", result.Reason)`
   when non-empty).
5. **`internal/service/events_composite.go`**, `EmitPreCompact` (lines 157-164) — the call
   site's return value is already discarded inside `safego.Go`; just confirm it still
   compiles against the new `PreHookResult` return type (it should — the call statement
   doesn't capture the return value at all today). No behavior change here, per Context.
6. Tests: `internal/plugin/prehook_test.go` and `internal/plugin/subprocess/event_hook_test.go`
   both need coverage for the new `Reason`/`Source` propagation (subprocess: a fake transport
   returning `EventHandleResult{Cancel: true, Reason: "..."}`, asserting `event.Data["reason"]`
   is set and `EmitPreHook`'s result carries it; builtin: a test hook setting
   `event.Data["reason"]`/`["suggestion"]` directly via the legacy convention, asserting both
   surface in `PreHookResult`). Extend whatever tests cover the three call sites in item
   3/4/5 for the new envelope/fallback text.

## Done means

- `Host.EmitPreHook` returns a `PreHookResult{Cancelled, Reason, Suggestion, Source}` (or
  equivalently named/shaped type — document your choice); all three call sites compile and
  behave correctly against it.
- A subprocess plugin that returns `EventHandleResult{Cancel: true, Reason: "..."}` today
  (already valid at the pinned plugin-sdk v0.3.0) has its `Reason` reach the agent-facing
  envelope — verified by a test using a fake/mock transport, not just a code read.
- A builtin/in-process hook can supply both `reason` and `suggestion` via `event.Data`,
  verified by a test.
- A plugin returning only bare `ErrCancelled` (old-style, no reason) still produces a real
  envelope via the existing generic-template fallback — no plugin is broken by this change.
- `context.pre_compact`'s call site compiles against the new type with no behavior change
  (still fire-and-forget, as documented in Context — not a regression, a pre-existing state
  this task doesn't touch).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why,
anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
