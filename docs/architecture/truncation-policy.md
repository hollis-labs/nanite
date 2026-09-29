# Truncation policy

How Nanite shortens text, and which idiom a call site uses. Citations are
`symbol` in `path`; the "Verify" commands at the end re-find them.

## The rule

Text shown to a model, a user or a client, or interpolated into a prompt, is cut
on a UTF-8 rune boundary. A raw `s[:n]` can end inside a multi-byte sequence: a
model reads that as garbage and a JSON encoder rewrites it to U+FFFD.

Three idioms exist. Pick by what the cut is for:

| Need | Use | Why |
|---|---|---|
| A hard byte cap on text | `UTF8Cut` / `UTF8Head` in `internal/truncate/utf8.go` | Largest prefix ≤ n that ends on a rune start. Works on `string` and `[]byte`. |
| A bounded reading view of a large result | `previewResult` in `internal/tool/preview.go` | Spends the budget on both ends, prefers line boundaries, states the omitted range, is JSON-pointer aware. |
| A model-facing tool result with a saved copy | `Output` / `OutputForModel` in `internal/truncate/truncate.go`, or `ResultCache.PresentResult` in `internal/tool/cache.go` | Sizes the cut from the model's window and leaves a recovery pointer. Their fallback cuts also use `UTF8Head`. |

## Where a raw cut is still fine

A raw byte cut is acceptable only when the bytes are never rendered as text:

- **Equality-only comparison.** `head1KB` in `internal/agent/reflexes/evaluator.go`
  cuts both sides identically before comparing.
- **Log and diagnostic lines.** `truncateForLog` in `internal/mcp/validate.go`,
  `trimForLog` in `internal/recover/repair.go`, the recovered-panic stack cap in
  `internal/server/server.go`, the eval scorer's `truncate`.
- **Slicing a list of items** (`tools[:limit]`, `items[:limit]`) rather than text.

Any other site that does `s[:n]` on text belongs on `UTF8Head`.

## Call sites on the safe idiom

Model- or user-facing caps that use `UTF8Head` / `UTF8Cut`: `capOutput` in
`internal/mcp/dev_tools.go`, the `web_fetch` body and exposed caps in
`internal/mcp/general_tools.go`, `boundedRuntimeString` in
`internal/service/host_runtime_feed.go`, the instruction-file cap in
`internal/workspace/walkup.go`, `truncateForPrompt` in
`internal/memory/extraction.go`, `TruncateStr` in `internal/chat/engine.go`, the
tool-warning and `tool_result` summary caps in
`internal/service/chat_tool_executor.go`. Context slots cut in
`truncateSlot` in `internal/context/window.go` with its own rune walk-back.

## Verify

```bash
# raw text cuts that remain: each hit should be in the "still fine" list
grep -rnE '\[:(max|cap|limit|n|maxLen|maxBytes)\]' --include='*.go' internal cmd | grep -v _test
go test ./internal/truncate -run UTF8
```
