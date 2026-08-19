# Start a test harness — adversarial suite for the anti-hallucination mechanisms

**Phase:** 8 (Test, Review, Verify)
**Status:** not-started
**Depends on:** none
**Touches:** new test-harness scaffolding (location TBD by whoever picks this up — likely a new `internal/testharness/` or `scripts/adversarial/` package, doesn't exist yet), `internal/service/chat_generate_failure_footer.go`, `internal/service/chat_tool_executor.go` (`card_show` sources gate, `:373`), `internal/service/chat_generate.go` (`:1861`, harness-side failure-footer fallback).

## Context

Operator, 2026-08-19: this is the deliberate start of a proper test suite/harness for the app — not the whole thing, just the first real piece, beginning with adversarial testing. Prompted by a separate architecture-review agent's suggestion: *"Run a deliberate adversarial pass against the two anti-hallucination fixes you already have, not just organic incident-driven ones. The failure-footer and sources-gate mechanisms were both built reactively to specific real bugs — good, but that means you found them by hitting them, not by looking for the class. Worth an explicit pass: starve the model of a tool it expects, feed it ambiguous/missing sources, malformed citations — see what else breaks the same way before a user finds it."*

### The two existing mechanisms this suite targets first

- **Failure-footer enrichment** (`CW-20260501-0013`, `internal/service/chat_generate_failure_footer.go`) — inlines failure context next to a tool name so the model doesn't fabricate a result for a tool call that actually failed. Toggleable via `NANITE_HARNESS_FAILURE_FOOTER` (default ON, `chat_generate_failure_footer.go:32`). Harness-side fallback path at `chat_generate.go:1861` (`CW-20260429-0026`).
- **`card_show` sources gate** (`chat_tool_executor.go:373`) — rejects fabricated `tool_use_id` values so a card can't cite a tool call that never happened.

Both were built reactively, after a specific real bug was hit — neither has had a deliberate pass looking for the broader failure *class* they're each meant to guard against.

## What to do

Scope this narrowly — this is the seed of a larger test-harness effort, not the whole thing. Don't build generic test-harness infrastructure beyond what this suite actually needs.

1. Design a small number of adversarial scenarios per mechanism, run against a real (or realistically mocked) chat session:
   - **Failure-footer**: starve the model of a tool it expects to succeed (deny/error a tool call it depends on), and confirm the footer actually appears and the model doesn't narrate a fabricated success. Try this across a few different tool-failure shapes (denied, timed out, malformed args, empty result) — not just one.
   - **Sources gate**: feed the model ambiguous or missing source data, malformed citation IDs, a `tool_use_id` from a *different* turn or session, and confirm the gate actually rejects each case rather than passing it through.
2. Record what breaks, if anything — a clean pass on all scenarios is a real, useful finding too (confirms the guard classes are solid, not just anecdotally not-yet-broken).
3. Land the scenarios as real, re-runnable tests (Go tests colocated with the mechanisms they target are the default choice, since both mechanisms already live in `internal/service/*_test.go`-covered files) — not a one-off manual exercise whose findings evaporate after this task closes.
4. Write up findings — for anything that breaks, file it as a normal bug fix task, not a scope-creep fix inside this task.

## Out of scope

- Building a general-purpose test-harness framework, load-testing tools, or anything not directly needed to exercise these two mechanisms adversarially. This task is explicitly scoped to be the *first* piece — later work can build on whatever scaffolding this produces, but this task shouldn't pre-guess what that later work needs.
- Fixing anything found — file a separate task per real finding.

## Done means

- Adversarial test scenarios exist and are re-runnable (real Go tests, not manual notes) for both the failure-footer and sources-gate mechanisms.
- Each scenario's outcome (passed / found a real gap) is recorded in this file's Work Log.
- Any real gap found is filed as its own follow-up task, referenced here, not fixed inline.

## Work log
<Worker fills this in as it goes: scenarios run, what broke, what didn't, follow-up tasks filed.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
