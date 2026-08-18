# Build a real HTTP-provider recovery retry path

**Phase:** 0
**Status:** not-started
**Depends on:** none
**Touches:** `internal/runtime/agent/recovery/broker.go` (`DispatchRetry`, ~line 327-347), `internal/runtime/agent/recovery/deps.go` (`Dependencies`, `AgentBoot` interface), `internal/runtime/agent/recovery/types.go` (`MaxBrokerRetries`, ~line 323-333), `internal/service/chat_http_broker_notify.go` (the whole file — especially `notifyRecoveryBrokerForHTTPStreamError`, ~line 142-222), `internal/runtime/agent/bootdir.go` (`HasBootdirLayout`, ~line 82-89 — read-only reference), `internal/service/agent_deps.go` (~line 253, broker wiring), `internal/store/recovery.go` (`nanite_recovery_breadcrumbs` read/write — read-only unless schema needs extending), `internal/service/chat_generate.go` (StreamChat call pattern reference, ~line 1100 and ~line 3251 — read-only reference)

**IMPORTANT — sequencing note for whoever schedules parallel work:** `internal/runtime/agent/recovery` is also the target of a separate, independently-written task file — `32-rename-recovery-namespace.md` — which regroups the Recovery Broker, Orphan Sweep, Recovery Pack, and interrupted-turn detection under a new shared `internal/recovery/*` package prefix (TASKS.md Phase 0 "Renames" item 26 / decision log §25). **Task 32 must run AFTER this task, not in parallel with it.** This task adds real logic (a new retry path, new broker plumbing) inside the current `internal/runtime/agent/recovery` package; task 32 is a mechanical package move. Running them concurrently risks either a merge collision on the same files or task 32 moving code this task is mid-edit on. Sequence: 04 first, then 32 (which will simply carry this task's new code along with everything else in the move).

## Context

TASKS.md Phase 0 item 4: "Build a real HTTP-provider recovery retry path, with flagging and backoff." Decision log §19 ("HTTP-provider recovery — build a real retry path") and architecture doc `docs/engineering/architecture/06-session-lifecycle-and-recovery.md`'s "HTTP-provider recovery" section give the reasoning:

> Confirmed with much stronger evidence than yesterday's initial flag: 46 of 50 (92%) of all permanent-outcome recovery breadcrumbs are sessions that were correctly classified transient by the Recovery Broker, then escalated to a user-visible permanent error purely because `DispatchRetry` structurally can't succeed for HTTP-streamed sessions (it always calls the CLI bootdir-setup path). **Decision: build a real HTTP-provider retry path** (retry the API call directly, no bootdir involved), with explicit flagging/telemetry and a backoff strategy — not naive immediate retry, matching the existing `MaxBrokerRetries`/remediation-timeout discipline already used for the CLI path.

**Verified: the structural gap is real.** `internal/runtime/agent/recovery/broker.go`'s `DispatchRetry` (line 327) has exactly one code path — every call, regardless of provider, ends in:
```go
return b.deps.AgentBoot.Boot(ctx, agent.Options{...})
```
`AgentBoot.Boot` (the interface in `deps.go`) is documented as "dispatches a replacement session" via `agent.Boot` — the CLI-based subprocess bootdir-setup path (see `docs/engineering/architecture/02-agent-launching.md`: "Every CLI launch spawns with `cwd` set to a Nanite-owned boot directory... only the CLAUDE.md/AGENTS.md loaded from the process's own boot-dir cwd survives Claude Code's context compaction"). For a plain HTTP API provider (anthropic, openai, and anything else without a real bootdir `Layout`), this call is guaranteed to fail — there is no HTTP-provider branch anywhere in `DispatchRetry` or in `Dependencies`.

**A important correction/nuance the decision log and TASKS.md do NOT mention — read before starting, this changes what "build the retry path" actually means today:** `internal/service/chat_http_broker_notify.go` already contains a workaround for the exact symptom the 92%-breadcrumb evidence describes, added in commit `da7e1c7` ("Core-dispatch batch: stale model IDs, reply-delivery UNIQUE bug, workflow_run input discoverability, recovery-broker noise", 2026-08-15, tag `CW-20260815-0024`) — **two days before** the 2026-08-17 decision log entry that cites the 92% figure. The relevant code, in `notifyRecoveryBrokerForHTTPStreamError` (line 151-168):

```go
// CW-20260815-0024: the recovery broker's retry path (DispatchRetry)
// always attempts a fresh agent.Boot — which for a provider with no
// implemented bootdir Layout (every plain HTTP API provider: anthropic,
// openai, gemini-api, openrouter, ...) fails 100% of the time with
// "bootdir for provider %q is not yet implemented", regardless of the
// underlying stream error's classification. Notifying the broker for
// those sessions produced nothing but a guaranteed-permanent-failure
// breadcrumb and, worse, a misleading "retrying..." envelope that was
// never going to succeed — so skip the notification entirely rather
// than let it fail structurally on every single HTTP-provider stream
// error. CLI/PTY-backed sessions (claude, codex, opencode) still go
// through unchanged — this is the case the mechanism was built for.
if !runtimeagent.HasBootdirLayout(providerName) {
	slog.Info("recovery: http chat stream error — skipping broker notify (no bootdir layout for this provider, recovery would be a guaranteed no-op)", ...)
	return
}
```

In other words: as of `da7e1c7`, an HTTP-streamed session's stream error **no longer reaches the recovery broker at all** — the notification is skipped outright before `OnSessionExit`/`DispatchRetry` is ever called. The 92% figure in the decision log is real historical evidence (breadcrumbs written before this guard existed, from `nanite_recovery_breadcrumbs`), and it's exactly why the guard was added — but it also means that **today**, HTTP-provider errors write zero breadcrumbs and get zero recovery attention of any kind (not even a doomed one). This guard is a stopgap that stopped the misleading "retrying..." UX and the guaranteed-permanent-failure breadcrumb spam; it is NOT the fix decision log §19 asks for. It is the thing this task's real retry path needs to grow past.

Concretely: **building the real HTTP-provider retry path means the `HasBootdirLayout` skip-guard in `chat_http_broker_notify.go` has to change too** — once a real HTTP-provider retry mechanism exists, HTTP-streamed sessions should notify the broker again (so they get classified, breadcrumbed, and retried through the new path), rather than being unconditionally skipped. Leaving the skip-guard as-is after adding a retry path would mean the new path is unreachable from the one place that currently feeds the broker HTTP-stream failures. Don't remove the guard blindly, though — its underlying purpose (never let an HTTP-provider session dispatch a doomed CLI-boot retry) must be preserved; the fix is to route HTTP-provider sessions to the new HTTP retry path instead of skipping notification, not to delete the check.

**The `MaxBrokerRetries`/remediation-timeout discipline to mirror** (from `internal/runtime/agent/recovery/types.go` line 323-333 and `broker.go` line 52-96):
- `MaxBrokerRetries = 3` — the broker-level hard cap on replacement sessions dispatched per chat session before escalating to `ClassPermanent`. `WithMaxRetries(n)` overrides it at construction.
- A `remediationTimeout` (10s default) bounds each remediation action, overridable via `WithRemediationTimeout`.
- Both are `Broker` construction-time options (`NewBroker(deps, opts...)`), not new invented mechanisms — a real HTTP retry path should respect the same `maxRetries` ceiling (an HTTP session shouldn't get unlimited retries any more than a CLI one does) and use an equivalent bounded-timeout discipline, but decision log §19 explicitly asks for **backoff**, which the current CLI path (a fresh `agent.Boot` per attempt) does not implement today — that's new work, not a mirror of something that already exists.

**Table for evidence-gathering reference:** `nanite_recovery_breadcrumbs` — confirmed via `internal/store/recovery.go` (`INSERT INTO nanite_recovery_breadcrumbs`, line 43; `SELECT ... FROM nanite_recovery_breadcrumbs`, line 91). This is where postmortem retry outcomes get written; any new HTTP-retry path should write through the same table via `BrokerStore.WriteBreadcrumb`, not a parallel mechanism, so postmortem queries stay unified.

**What "retry the API call directly" means in this codebase**: the chat HTTP path calls `prov.StreamChat(ctx, llmtypes.ChatRequest{...})` where `prov` is an `llmcontracts.Provider` (see `internal/service/chat_generate.go` line 1100 for the primary call, and line 3251 for an existing same-turn retry example — `retryEnvelopeCorrection` re-invokes `prov.StreamChat` with a fresh `retryCtx` after a malformed-envelope error. That's a different scenario (same-turn correction, not broker-driven post-exit recovery) but shows the shape of calling a provider directly without going through `agent.Boot`/bootdir at all — useful as a reference for what a bootdir-free retry call looks like in this codebase, not as code to reuse verbatim.

## What to do

1. Design and add an HTTP-provider retry path to the recovery broker that does NOT go through `AgentBoot.Boot`/bootdir setup. This likely means:
   - A new `Dependencies` field (parallel to `AgentBoot`) — e.g. an `HTTPRetry` interface the broker can call for HTTP-streamed sessions, implemented in `internal/service` where the chat service already has access to `llmcontracts.Provider` instances and the session's resolved provider/model. Keep the interface as narrow as `AgentBoot`'s is today (it only needs what the broker needs, not the chat service's full surface).
   - `DispatchRetry` (or a new sibling method, if branching inside `DispatchRetry` gets unwieldy) needs to decide CLI-boot vs. HTTP-retry based on the session's provider — `runtimeagent.HasBootdirLayout(providerName)` is the existing, already-correct predicate for this (true → CLI/bootdir path as today; false → the new HTTP retry path). Don't duplicate that logic; reuse the function.
   - Real backoff (not naive immediate retry) — decision log §19 is explicit about this. A simple exponential or fixed-with-jitter backoff bounded by `remediationTimeout`/`maxRetries` is reasonable; if there's genuine ambiguity about the right backoff shape/parameters, escalate rather than inventing numbers with no basis.
   - Real breadcrumbing via the existing `BrokerStore.WriteBreadcrumb` → `nanite_recovery_breadcrumbs`, so an HTTP retry's outcome is queryable the same way a CLI retry's is today.
2. Update `notifyRecoveryBrokerForHTTPStreamError` in `chat_http_broker_notify.go` so HTTP-provider stream errors reach the broker again once the new retry path exists — replace the current unconditional skip with a path that still avoids ever calling `AgentBoot.Boot` for a no-bootdir-layout provider (the original bug this guard fixed), but now lets the broker's classify → HTTP-retry-dispatch flow run instead of returning early. Preserve the existing classification logic (`classifyHTTPStreamError`, the `causeHTTPStream*` constants, the meta bag) — that machinery is provider-agnostic and doesn't need to change, only what happens after classification for an HTTP-shaped session.
3. Wire the new `Dependencies` field at the broker's production construction site (`internal/service/agent_deps.go`, ~line 253, `recovery.NewBroker(brokerDeps)`).
4. If real ambiguity comes up about where exactly the HTTP retry call should live (inside `internal/runtime/agent/recovery` vs. a thin adapter in `internal/service` that implements a broker-defined interface, mirroring how `AgentBoot` is a broker-defined interface implemented elsewhere) — resolve it the way `AgentBoot`/`BootDirOps`/`MCPControl`/`CredentialOps` already resolve it in `deps.go`: the broker package defines a narrow interface, the composition root (`internal/service`) supplies the real implementation. Don't have the recovery package import `internal/service` or vice versa in a way that creates a cycle.

## Done means

- An HTTP-streamed session (anthropic/openai, no bootdir `Layout`) that hits a transient stream error gets a real retry attempt that calls the provider directly (`prov.StreamChat` or equivalent), not a doomed `agent.Boot` call.
- The retry path respects a real backoff strategy and a bounded max-attempts ceiling (mirroring `MaxBrokerRetries`'s intent, even if the concrete number differs for HTTP), not an immediate/unlimited retry loop.
- `notifyRecoveryBrokerForHTTPStreamError`'s `HasBootdirLayout` guard no longer unconditionally skips HTTP-provider sessions — it routes them to the new path instead, while still never calling `AgentBoot.Boot` for a provider without a real bootdir `Layout`.
- New breadcrumbs for HTTP-provider retry outcomes land in `nanite_recovery_breadcrumbs` via the existing `BrokerStore.WriteBreadcrumb` mechanism.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Real validation per `EXECUTION-PROCESS.md`'s checkpoint guidance: exercise an actual HTTP-provider transient failure (e.g. inject a timeout/5xx against a test double provider) and confirm a retry is dispatched, succeeds or exhausts correctly, and a breadcrumb is written — a green build alone does not prove this path is reachable or correct, since the whole point of this task is that the previous mechanism was structurally unreachable for this exact case.
- If any part of the "flagging/telemetry" half of decision log §19's ask (beyond the breadcrumb write) is ambiguous — e.g. whether a user-facing envelope should announce an HTTP retry the way `EnvelopeSink`/`info-card` does for CLI retries — check `EnvelopeSink`/`Envelope` in `deps.go` first (it's already provider-agnostic — "Kind is one of info-card/error-report/chat-loop-terminated... no new envelope kinds") before assuming new UI surface is needed.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
