# Provider abstractions audit — 2026-04-11

## Scope

**Scope string:** `provider-abstractions`

**Interpretation:** Subsystem review of `pkg/provider/` — the extracted `go-providers` library containing all LLM provider adapters (HTTP API and PTY/subprocess bridges), concurrency primitives (circuit breaker, rate limiter, retry), and supporting infrastructure (registry, cache hints, CLI adapter interface, event pipeline, scope guard).

**Packages read in full:**
- `pkg/provider/` — all 52 files (26 source, 26 test)

**Packages sampled:**
- `cmd/nanite/main.go` — provider registration and API key wiring (sampled, not full read)
- `internal/service/chat_generate.go` — consumer of provider interface (referenced from cross-audits, not re-read)

**Packages skipped:**
- `internal/chat/`, `internal/service/` — covered by `2026-04-10-chat-engine` audit
- `internal/mcp/` — covered by `2026-04-10-mcp-client-transport` audit

**Layout note:** The scope definition referenced `internal/provider/` — this path no longer exists. The provider code has been extracted to `pkg/provider/` (the `go-providers` local replace directive library). All findings reference `pkg/provider/` paths.

## Methodology

**Categories applied:**
- Security — API key handling, secret exposure in logs/spans/errors, URL injection, SSRF surface
- Concurrency correctness — PTY/subprocess goroutine lifecycle, circuit breaker mutex usage, rate limiter thread safety
- Memory & Resources — response body size limits, scanner buffer bounds, goroutine leak assessment
- Error Handling — retry logic, error propagation, error message content
- Idioms — Provider interface design, code organization, naming

**Categories deferred:**
- Standards & Tooling — `go vet`, `golangci-lint`, `go test -race` deferred to `whole-repo-tooling-and-tests-sweep` (already run 2026-04-11). This is a subsystem review scoped to manual code inspection.

**Tools run:** `rg` for pattern searches. No build/test commands (narrow scope).

**Cross-audit preflight:** Read `INDEX.md` and index files for:
- `2026-04-11-panic-recovery-sweep` — 14 provider SSE reader goroutines without recover. Cross-referenced, not re-flagged.
- `2026-04-11-concurrency-cancellation-sweep` — provider lifecycle mapped. Built on existing lifecycle map.
- `2026-04-10-chat-engine` — `scope_guard.go` + `event_pipeline.go` dead code. Confirmed still dead.
- `2026-04-10-mcp-client-transport` — unbounded response bodies. Same class found in providers.
- `2026-04-11-whole-repo-tooling-and-tests-sweep` — `parseAiderJSON`, `parseKiroJSON`, `codexTurnCompleted` unused. Confirmed, not re-flagged.

## Findings

### By severity

**Critical (0)**
- _none_

**High (2)**
- [01 — Gemini API key passed in URL query string](01-high-gemini-api-key-in-url.md)
- [02 — Unbounded response body reads across all providers](02-high-unbounded-response-bodies.md)

**Medium (3)**
- [03 — Non-Anthropic providers lack retry, circuit breaker, and rate tracking](03-medium-non-anthropic-no-retry.md)
- [04 — Gemini model parameter interpolated into URL without validation](04-medium-gemini-model-url-injection.md)
- [05 — PTY bridge has latent double-cmd.Wait() risk](05-medium-pty-double-wait-risk.md)
- [06 — SubprocessBridge uses SIGKILL without graceful SIGTERM](06-medium-subprocess-no-graceful-kill.md)

**Low (2)**
- [07 — Anthropic error body forwarded verbatim — potential indirect key leak](07-low-anthropic-error-body-may-leak-key.md)
- [08 — PTY/subprocess parse errors log raw CLI output lines](08-low-pty-parse-error-logs-raw-line.md)

**Info (2)**
- [09 — Dead code confirmation: scope_guard.go and event_pipeline.go](09-info-dead-code-confirmed.md)
- [10 — Positive observations and architectural notes](10-info-observations.md)

### By topic

**Security — API key / secret handling**
- [01 — Gemini API key in URL](01-high-gemini-api-key-in-url.md)
- [04 — Gemini model URL injection](04-medium-gemini-model-url-injection.md)
- [07 — Error body may leak key](07-low-anthropic-error-body-may-leak-key.md)
- [08 — Parse errors log raw lines](08-low-pty-parse-error-logs-raw-line.md)

**Memory & Resources — response body handling**
- [02 — Unbounded response bodies](02-high-unbounded-response-bodies.md)

**Concurrency — PTY/subprocess lifecycle**
- [05 — Double cmd.Wait() risk](05-medium-pty-double-wait-risk.md)
- [06 — Subprocess no graceful kill](06-medium-subprocess-no-graceful-kill.md)

**Error handling — resilience**
- [03 — Non-Anthropic no retry](03-medium-non-anthropic-no-retry.md)

**Dead code**
- [09 — Dead code confirmed](09-info-dead-code-confirmed.md)

**Architecture praise**
- [10 — Positive observations](10-info-observations.md)

## Recommended next steps

1. **Fix finding 01 (Gemini key in URL) immediately.** One-line change per call site — move key to `x-goog-api-key` header. Highest priority because it's the only finding that currently exposes a secret.
2. **Add `io.LimitReader` wrappers (finding 02).** A shared `limitedReadAll` helper applied across all providers. Can be done in a single sweep.
3. **Extract retry/circuit/rate infrastructure (finding 03).** The code exists in `circuit.go`, `ratelimit.go`, `retry.go`. Wire it into the other 7 HTTP providers. This is a larger refactor but high-value for user-facing reliability.
4. **Add model name validation (finding 04).** Quick regex check at the provider boundary.
5. **Consolidate `killProcess` (findings 05, 06).** Extract to a shared function, use `sync.Once` for `cmd.Wait()`, consider `cmd.Cancel` (Go 1.20+).
6. **Follow-up pass: provider test coverage with `-race`.** Deferred to the tooling sweep. The provider tests use `httptest.NewServer` which is clean, but the PTY/subprocess tests should be verified under `-race`.

## Known issues skipped

- **14 provider SSE reader goroutines without recover.** Cross-ref `2026-04-11-panic-recovery-sweep`. Not re-flagged.
- **PTY reader cancellation is indirect (via process death, not ctx.Done select).** Cross-ref `2026-04-11-concurrency-cancellation-sweep` item 12. Not re-flagged.
- **`parseAiderJSON`, `parseKiroJSON`, `codexTurnCompleted` unused.** Cross-ref `2026-04-11-whole-repo-tooling-and-tests-sweep`. Confirmed, not re-flagged.
- **Chat generation goroutines orphaned from shutdown.** Cross-ref `2026-04-11-concurrency-cancellation-sweep` item 1. Upstream of providers, not provider-layer issue.

## Noticed but out of scope

- **Ollama `OLLAMA_HOST` from env var is not validated.** `ollama.go:L27` reads `os.Getenv("OLLAMA_HOST")` and uses it directly as a URL base. A malicious or misconfigured env var could point to an attacker-controlled server, leaking prompts and system prompts. Low risk because env vars are admin-controlled, but worth noting for a `sandbox-env-vars` follow-up scope.
- **Azure OpenAI `AZURE_OPENAI_ENDPOINT` from env var has the same pattern.** `azure_openai.go:L40` — unsanitized env var used as URL base.
- **No HTTP client timeouts set on any provider.** All providers create `&http.Client{}` with zero-value timeouts. A hung API server will block the goroutine indefinitely. The chat engine's 5-minute `WithTimeout` provides an outer bound, but the HTTP client itself has no per-request or connection timeout. Suggested follow-up scope: `provider-http-client-hardening`.
- **`CLIConfig` struct in `cli_adapter.go:L23-28` is defined but never used.** Appears to be a placeholder for future user-configurable adapters. Minor dead code.
- **Embedding API calls (`Embed`, `EmbedBatch`) in OpenAI, Gemini, Ollama, Mistral, Azure have no retry logic.** Same class as finding 03 but for embedding rather than chat. The embedding path is used by `internal/memory/` for memory extraction — failures there silently disable memory.
- **`cost_monitor.go` and `progress_tracker.go` are dead code dependencies of `event_pipeline.go`.** Covered by finding 09 but not called out individually. ~200 lines each.
