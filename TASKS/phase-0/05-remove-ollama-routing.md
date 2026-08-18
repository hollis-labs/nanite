# Remove Ollama routing entirely

**Phase:** 0
**Status:** implemented
**Depends on:** none
**Touches:** `internal/chat/engine.go` (`InferProvider`, ~lines 324-349 — remove the `"ollama"` fallback branch), `internal/store/seed.go` (~line 210 — read-only reference, the historical removal comment this task completes), `pkg/models/registry.go` (`ProviderDefaults` map, `allModels` — confirm no dangling Ollama entries), any other remaining "ollama" reference found during implementation

## Context

**Operator decision, 2026-08-18 — final, supersedes this task's original scope.** The original planning pass (task file `05-build-ollama-provider.md`, now replaced by this file) took the decision log's framing ("a real, wanted local-model provider that's currently broken... actually run locally today") at face value and scoped a full new-provider build. Independent verification during planning found the opposite: `internal/llm/ollama` doesn't exist, no reusable Ollama client exists anywhere in the sibling monorepo either, and `internal/store/seed.go`/`pkg/models/registry.go` both carry explicit comments documenting a **prior, deliberate removal** ("Step 6.5 (SP-20260508-0001) reduced the API-provider catalog to Anthropic + OpenAI... ollama API rows have been removed"). This was flagged to the operator as `TASKS/ESCALATIONS.md`'s Ollama entry. **Operator resolution: invert the task. Don't build Ollama support — finish removing it.** `chat.InferProvider`'s `"ollama"` routing branch is a dangling reference to a provider that was intentionally removed elsewhere; per the sharpened escalation rule in `docs/engineering/EXECUTION-PROCESS.md` (a decision-log rationale not holding up against the code is a correction to log, not grounds to reopen the decision — and default-to-cut applies to genuinely undocumented/contradicted cases), the right move is to finish the removal that was already started, not build new code against a premise the code itself contradicts.

**Verified against real code (2026-08-18):**

`internal/chat/engine.go`'s `InferProvider` (line 324) maps a model name to a provider name when the session has no explicit provider set. Its Ollama-shaped fallback branch (line 342-346):
```go
case strings.HasPrefix(model, "llama"),
	strings.HasPrefix(model, "gemma"),
	strings.HasPrefix(model, "mistral-7b"),
	strings.Contains(model, ":"): // "model:tag" is an ollama-ism
	return "ollama"
```
resolves to a provider name (`"ollama"`) that is never registered anywhere in `initProviders` (`cmd/nanite/main.go`) — a chat request whose model matches this heuristic today hits a registry-lookup miss (an unregistered-provider error), not a working local model. This is dead, actively-misleading routing logic: it looks like Ollama support exists, but any request that reaches this branch fails.

`internal/store/seed.go` (line ~210) and `pkg/models/registry.go` (its top-of-file `ProviderDefaults` comments) both confirm the removal was deliberate, not an oversight — the DB catalog seed row for Ollama was pruned as part of a broader provider-catalog reduction to Anthropic + OpenAI only.

## What to do

1. Remove the `"ollama"`-returning case branch from `internal/chat/engine.go`'s `InferProvider` (lines ~342-346). Decide what the fallback should do instead for a model name matching those patterns (`llama*`, `gemma*`, `mistral-7b*`, or anything containing `:`) — most likely: fall through to whatever `InferProvider`'s existing default/unknown-model behavior already is (check the function's full body for what happens when no case matches), rather than inventing new behavior for this now-orphaned pattern set.
2. Grep the whole repo (Go and TypeScript) for `"ollama"`/`Ollama` (case-insensitive) to find any other remaining references beyond `engine.go` — check `pkg/models/registry.go`'s `ProviderDefaults`/`allModels` for any dangling Ollama entries the prior removal (SP-20260508-0001) might have missed, any frontend provider-picker UI that still lists Ollama as an option, and any config/YAML fixture referencing it.
3. Remove whatever is found in step 2 that's a genuine dead reference (matching the same "provider that was intentionally removed" pattern as `engine.go`'s routing branch). If anything found looks like it might be a *different*, still-live Ollama-adjacent feature rather than a dangling reference to the removed provider, note it in the Work Log rather than assuming — this task's scope is finishing the already-decided removal, not making a new removal decision about something unrelated.
4. Run `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...`, and `cd ui && npm run build` if any frontend file was touched.

## Done means

- `chat.InferProvider` no longer has an `"ollama"`-returning branch; a model name matching the old pattern set falls through to the function's existing default/unknown-model behavior.
- No remaining references to Ollama as a supported provider anywhere in the codebase (backend or frontend) — grep confirms this, or every remaining hit is explicitly logged in the Work Log with a reason it was left.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass; frontend build passes if touched.
- The Work Log records that this task inverted the original planning-pass scope (build → remove) per direct operator instruction, so a future reader isn't confused by the mismatch between this file's content and `TASKS.md` item 5's original "build" framing.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
