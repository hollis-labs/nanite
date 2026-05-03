# Recoverable stream events

> **What this is:** the chat-service stream-event taxonomy for non-fatal pauses
> that previously surfaced as `internal_error` and ended the SSE turn. Glass-6
> (CW-20260502-0013, SP-20260502-0001) introduces the first such event,
> `rate_budget_pause`. As more error codes graduate from "session-killing" to
> "recoverable," they land here.

## Principle

An error that stops the conversation should not be possible. The chat session
is the user's working state — the SSE stream for a single turn ending is
fine, but the *session* must stay alive for the next message. Failure modes
that the user can resolve (compact further, split message, wait, change
provider) belong on a recoverable event channel, not on the fatal
`error` / `internal_error` envelope path that the FE treats as a session
kill.

This is the downstream counterpart to Glass-4's auto-handoff (the upstream
fix: don't lose context). When upstream cannot recover, downstream still
should not kill the session — emit a recoverable event and end the turn
cleanly.

## Naming convention

Recoverable events end in `_pause` (`notify_pause` from the trust-agent
middleware was the first such event; `rate_budget_pause` follows the same
shape). They flow through `chat.StreamEvent.Type` with a JSON payload on
`Data`. They are explicitly NOT `error` envelopes — clients should treat
them as informational signals, not failures.

## `rate_budget_pause`

Emitted when an estimated request size exceeds the per-minute rate-limit
window AND compaction-based recovery (the Glass-1 path) cannot reduce it
sufficiently.

**Payload (`StreamEvent.Data`, JSON):**

| field             | type      | meaning                                                                 |
| ----------------- | --------- | ----------------------------------------------------------------------- |
| `session_id`      | string    | Session identifier; same as the active SSE stream's session.            |
| `estimated_tokens`| int       | Token estimate that tripped the budget. Parsed from go-providers.       |
| `current_limit`   | int       | Per-minute rate-budget limit at the time of the pause.                  |
| `retry_after_ms`  | int64     | `RateTracker.WaitTime(estimated_tokens)`. Zero when waiting won't help. |
| `suggested_action`| string    | `auto_retry` (a single retry is in flight) or `user_action_needed`.     |
| `recovery_options`| []string  | Hints for the FE: subset of `wait`, `compact_more`, `user_split_message`.|
| `trigger_kind`    | string    | Always `rate_budget_exceeded` for this event.                           |

**Auto-retry policy.**

One transparent retry per turn. On the first pause for the active turn:

1. `rate_budget_pause` is emitted with `suggested_action="auto_retry"`.
2. Chat-service sleeps `retry_after_ms` (cancellable via the request ctx).
3. After the sleep, the rate tracker is rechecked. If
   `WaitTime(estimated_tokens) == 0` the chat loop re-runs the provider
   call — no further events, the turn proceeds normally.
4. If the recheck still shows non-zero wait, a second `rate_budget_pause`
   is emitted with `suggested_action="user_action_needed"` and the turn
   ends. The session stays active; the next user message starts a new
   turn against the same `session_id`.

`compactRecoverableAttempts` (the Glass-1-era counter) is independent of
the new `rateBudgetPauseAttempts`. Compaction recovery happens first;
Glass-6 only intervenes once compaction has either refused or been
exhausted.

**Frontend handling — out of scope.** A follow-up FE ticket is needed to
render the new event distinctly from `error` envelopes (no toast/error
icon; a quiet "rate-limited, will retry…" inline indicator that resolves
when either the auto-retry succeeds or the user_action_needed event
arrives). Until that lands, FE clients will receive the events as
unrecognized stream-event types and ignore them — which is still
strictly better than the prior fatal path.

## Future events

When other error codes (provider 5xx without retry, provider auth failure
that the user can fix in settings, model-not-available with fallback
suggestions) graduate to recoverable, they get their own
`<class>_pause` event with the same shape: an SSE stream event whose
payload includes the trigger kind, what the user can do, and any retry
hint. Updating this doc + the `chat_<class>_pause.go` helper file is the
expected pattern.

## Cross-references

- Implementation: `internal/service/chat_rate_budget_pause.go`,
  `internal/service/chat_generate.go` (rate-budget refused-recovery and
  failed-after-retry branches).
- Sentinel: `provider.ErrRequestExceedsRateBudget` from go-providers.
- Sibling pattern: `internal/service/chat_notify_pause.go` (notify_pause
  trust-agent middleware event).
- Upstream fix: Glass-4 SlotHandoff (CW-20260502-0015) — preserves intent
  across compaction so the recoverable pause has good state to resume to.
