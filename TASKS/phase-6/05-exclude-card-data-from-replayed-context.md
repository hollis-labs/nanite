# Exclude Card data from replayed conversation history (the highest-leverage item in this phase)

**Phase:** 6
**Status:** implemented
**Depends on:** none
**Touches:** `internal/chat/context_client.go:256-267` (`AssembleSlotSources`'s message-history build — **the actual fix site, not `internal/context/compaction.go`**, see Context), `internal/service/chat_generate.go:1801,1868-1972,2004-2008` (the emit/persist path, read-only reference — confirms the shape of what needs filtering, not itself the fix site), `internal/chat/structured.go` (`StructuredMessage`, `EnvelopeRef`, `MarshalContent`)

## Context

Architecture doc `08-cards.md`: *"Tool-auto-emitted card data is already excluded from the *current* turn's tool-result content the agent sees. But the full data still gets appended into the assistant's own persisted response text, which *is* replayed into every subsequent turn's conversation history... This is the concrete mechanism that actually makes the harness-injects-rich-data-agent-context-stays-light vision hold at session scale."* Architecture doc `06-session-lifecycle-and-recovery.md` names the same gap. TASKS.md's own wording ("Card data excluded from replayed conversation history") makes this Phase 5's job, explicitly flagged as the single highest-leverage item in the phase.

### The full pipeline, traced end to end — corrects the architecture doc's implied fix location

1. **Emit**: `chat_generate.go:1801` appends a raw ```` ```nanite-envelope\n{...}\n``` ```` fence block to `responseContent` for pending envelopes (fence tag confirmed post-Phase-0-#33: only `nanite-envelope` recognized, `volon-envelope`/`fragments-envelope` gone). `chat.ParseEnvelopes(responseContent)` (`internal/chat/envelope.go:228`) then extracts the fence into `cleanContent` — the raw fenced text does **not** survive into persisted message text. This part already works correctly.
2. **But**: `chat_generate.go:1868-1972` builds `envRefs []chat.EnvelopeRef`, and `EnvelopeRef.Data` (`internal/chat/structured.go:60`) is `json.RawMessage` — the **full, untruncated card payload**, not a pointer or summary. `chat.WrapResponse(cleanContent, tier, toolCalls, envRefs, ...)` builds a `StructuredMessage{Text: cleanContent, Envelopes: envRefs, ...}`, and `structured.MarshalContent()` — the entire JSON, **envelopes-with-full-data included** — becomes `store.Message.Content` (`chat_generate.go:2004-2008`).
3. **The actual replay mechanism, and the real fix site**: `internal/chat/context_client.go:256-267` — `ListMessages(session.ID, 200)`, then `chatMessages[i] = llmtypes.ChatMessage{Role: role, Content: m.Content}`. **`m.Content` is used verbatim** — the full `StructuredMessage` JSON, every historical envelope's complete `data` payload included, becomes the literal text of that turn in every subsequent turn's LLM-facing history. `StructuredMessage.Envelopes` has **zero readers anywhere else** in `internal/chat`/`internal/service`/`internal/context` (verified by grep) — it is write-only except for this one blind pass-through.
4. **None of the four compaction stages reach this** — `internal/context/compaction.go`'s `stageDropEnrichment`/`stageSummarizeOldest`/`stageStripToolBlocks`/`stageDedupeToolResults` all operate on `m.ContentBlocks` (typed `tool_use`/`tool_result` blocks); a message built via the plain-`Content` path at `context_client.go:266` has `len(m.ContentBlocks) == 0`, so every stage's guard clause skips it entirely — **compaction stages structurally cannot reach this data, confirmed by direct inspection of `stageStripToolBlocks`'s guard clause.** This is also why the fix does not belong in `compaction.go`: compaction only fires when a session is over budget, but this problem exists on **every single turn**, regardless of budget.

## What to do

1. At `internal/chat/context_client.go:266` (or wherever the equivalent history-build now lives), change the message-content construction: parse `m.Content` as `StructuredMessage` and use only `.Text` for history replay — discard `.Envelopes` entirely from what reaches the LLM. Preserve a fallback for non-JSON legacy message content (older rows may not be `StructuredMessage`-shaped).
2. **Verify before assuming Text-only is safe for every case**: check whether the model needs visibility into its own just-emitted envelope somewhere else in the current turn's loop (e.g. to reason about what it just showed the user) — if so, confirm that visibility comes from a different, already-correct path (the current-turn tool-result exclusion the architecture doc says already works) rather than needing envelope data to survive in history replay.
3. Confirm this fix doesn't interact badly with `StructuredMessage.Envelopes`' write path — the field can keep being written (it may have other future consumers, or be needed for exact reconstruction/audit purposes), this task only changes what gets *read* for LLM-facing history replay.
4. Measure the real context-size impact: pick a long session with several historical cards (e.g. a table-card with substantial row data) and confirm token usage for a late-session turn drops meaningfully once envelope data is excluded from replay.

## Done means

- Replayed conversation history sent to the LLM contains only a message's `Text`, never its historical `Envelopes` payload data.
- A real, long session with multiple historical cards shows a measurable context-size reduction on later turns (report the before/after numbers in this file's Work Log).
- No regression in the agent's ability to reason about the current turn's own just-emitted card (verified the current-turn exclusion path, already correct per the architecture doc, remains intact and is what's actually relied on, not accidentally broken by this change).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log

Implemented exactly at the fix site the task identified — `internal/chat/context_client.go`'s `AssembleSlotSources` message-history build — plus a small support helper in `internal/chat/structured.go`, which is the same file `StructuredMessage`/`EnvelopeRef`/`MarshalContent` already live in.

**Mechanism changed:**

1. Added `replayContent(content string) string` to `internal/chat/structured.go`. It trims the input, and if it doesn't start with `{`, returns it unchanged (covers plain user text and `envelope_response` rows, which are formatted by `FormatEnvelopeResponseContent` as `"[envelope:type status:...] {...}"` — never `{`-prefixed). If it does start with `{`, it unmarshals into `StructuredMessage`; on unmarshal failure or `Version == 0` (non-`StructuredMessage`-shaped legacy JSON), it also returns the input unchanged. Otherwise it returns only `sm.Text`, discarding `sm.Envelopes` and `sm.ToolCalls` entirely. Same fallback shape as the existing precedent in `internal/recovery/pack/pack.go`'s `MessagePlainText` (which unwraps the same JSON shape for a different purpose — Recovery Pack replay text — confirming this is an established, safe pattern in this codebase, not a new one).
2. Changed exactly one line in `context_client.go`'s `AssembleSlotSources` (was line 265, in the `messages, err := cb.Store.ListMessages(...)` loop): `chatMessages[i] = llmtypes.ChatMessage{Role: role, Content: m.Content}` → `chatMessages[i] = llmtypes.ChatMessage{Role: role, Content: replayContent(m.Content)}`. This is the only call site of `ListMessages` inside `AssembleSlotSources`, and `AssembleSlotSources` has exactly one caller (`internal/service/context.go:204`), confirmed by grep — this is the single, real path that turns persisted message rows into LLM-facing conversation history for a turn.

No other file was touched. `chat_generate.go`'s emit/persist path (`WrapResponse` → `MarshalContent` → `store.Message.Content`) is completely unchanged — `StructuredMessage.Envelopes` still gets written in full on every turn, exactly as before. Only what gets *read back* for history replay on a later turn changed, per the task's item 3.

**Verification that Card rendering and the response round trip are unaffected (task item 2 / "no regression"):**

- Confirmed via grep that the persisted, page-reload FE rendering path reads the separate `store.Message.Envelope` column (`internal/api/envelopes.go`), populated independently from `envelopeJSON` in `chat_generate.go:1793-1798`/`1938` — not `StructuredMessage.Envelopes` inside `Content`. `replayContent` only touches `Content`; `Envelope` is untouched. Persisted-card rendering on reload is unaffected.
- Confirmed via grep (`grep -rn "StructuredMessage\|\.Envelopes\b" internal --include="*.go"`) that `StructuredMessage.Envelopes` has zero readers anywhere in the codebase other than the one line changed here — matches the task's own claim.
- Confirmed the "current turn's own just-emitted card" visibility the model needs mid-turn comes from a structurally separate mechanism: `chat.ExtractEnvelopeMarker`/`captureEnvelopeData` (`internal/service/chat_generate.go` ~3041-3063) strips/extracts the `ENVELOPE_DATA` marker from *live* tool-result text within the in-flight turn, before that turn's own message is ever persisted. `AssembleSlotSources` only ever loads *already-persisted* rows (turns 1..N-1) when assembling context for turn N — the card the model is about to emit in turn N doesn't exist in `messages` yet when `AssembleSlotSources` runs, so there is no overlap/conflict between the two mechanisms, and this fix cannot regress it.
- Confirmed the envelope-response round trip is untouched: `FormatEnvelopeResponseContent` output (`"[envelope:type status:...] {...}"`) never starts with `{`, so `replayContent` returns it verbatim — response payloads a user submits via `POST /api/envelopes/{id}/respond` still replay into history exactly as before.
- Added `TestReplayContent_FallsBackForNonStructuredContent` covering plain user text, legacy non-`StructuredMessage` JSON, an actual `FormatEnvelopeResponseContent`-shaped `envelope_response` row, and empty content — all pass through byte-for-byte unchanged.

**Context-size measurement (task item 4 / Done-means bullet — real session, before/after numbers):**

Used the real backed-up database at `~/.local/share/nanite/workspaces/default/backups/main.db.pre-execution-backup-20260818-132726` (copied to the scratchpad to avoid touching the live file). Queried for the session with the most assistant turns carrying persisted card data:

- Session `782cb1e1-3db7-4280-a39b-ccedeb762257`: 36 total messages, 6 historical assistant turns carrying real card data (`report-card` x5, `list-card`, `todo-list` — mix, one turn carries two).
- Added a temporary, env-gated test (`internal/chat/zzz_measure_temp_test.go`, deleted after capturing this measurement — never committed) that ran `s.ListMessages(sessionID, 200)` (the exact call `AssembleSlotSources` makes) and summed `len(m.Content)` / `EstimateTokens(m.Content)` before vs. after `replayContent`, i.e. exactly what a late-session turn's replayed history looks like pre- and post-fix.
- **Before:** 76,936 chars / ~19,222 estimated tokens of replayed message history.
- **After:** 42,716 chars / ~10,669 estimated tokens.
- **Reduction:** 34,220 chars / ~8,553 estimated tokens — **44.5%** — on a session whose card payloads happened to make up a large share of its total content. This is exactly the mechanism the architecture doc describes: a card shown once stops costing full-payload context on every later turn.

**Deviation from plan:** none of substance. The fix landed exactly where the task said it would (`context_client.go:266`, now `replayContent(m.Content)` — the surrounding code shifted by a few lines during earlier phases but the target statement was still the same one). Added a small, well-precedented helper (`replayContent`) in `structured.go` rather than inlining the unmarshal in `context_client.go`, since `StructuredMessage` and its JSON shape already live there and `internal/recovery/pack` establishes the same "unwrap `StructuredMessage`, fall back to raw content" pattern as a first-class helper rather than inline logic. Also ran `gofmt -w` on `structured.go`, which fixed a pre-existing (unrelated) struct-tag alignment issue in the same file I was already editing — noted as a drive-by fix, not part of the task's substance.

**Baseline checks:**
- `go build ./cmd/nanite/` — clean, no errors.
- `go vet ./...` — one pre-existing failure in `internal/service/container.go` (`stopReaper`/`stopRuntimeReaper` "not used on all paths" possible-leak lint), confirmed via `git stash` to exist identically on the unmodified baseline before this task's changes — unrelated to this task's fix site, not touched or introduced by this work.
- `go test ./...` — all 91 packages pass, zero failures, including new tests: `TestReplayContent_StripsEnvelopeData`, `TestReplayContent_FallsBackForNonStructuredContent` (4 subtests), `TestReplayContent_PreservesTextOnlyStructuredMessage` (`internal/chat/structured_test.go`), and `TestAssembleSlotSources_ExcludesEnvelopeDataFromReplayedHistory` (`internal/chat/context_client_test.go`), an end-to-end test that persists a real `store.Message` row shaped exactly like `chat_generate.go` produces (200-row synthetic table-card via `WrapResponse`/`MarshalContent`) and asserts `AssembleSlotSources`'s `Messages` slot contains only the turn's `Text`, while the underlying stored row (verified via a direct `ListMessages` call) still retains the full envelope payload untouched.

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
