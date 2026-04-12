# Audit: Observability

**Date:** 2026-04-11
**Reviewer:** nanite-reviewer-backend (deep-review)
**Branch:** audit-campaign-2026-04-11

## Scope

**Scope string:** `observability`

**Interpretation:** `internal/otel` wrapper usage (actual path: `../framework/libs/go-otel/`), structured-logging discipline, PII in log fields, metric coverage, error-reporting surface, span discipline. Subsystem review covering the OTel library, all logging call sites, and all span creation sites across the nanite codebase.

**Scope extension note:** The scope label references `internal/otel` but nanite has no such directory. The OTel wrapper lives in the sibling library `../framework/libs/go-otel/` (module `github.com/hollis-labs/otel`), imported via local `replace` directive. The scope was extended to cover the full library (`feotel.go`, `logging.go`, `tracing.go`, `metrics.go`, `genai/`, `redaction/`, `propagation/`, `internal/resource.go`) plus all consumer call sites in nanite.

**Packages read in full:**
- `../framework/libs/go-otel/` — all source files (feotel.go, logging.go, tracing.go, metrics.go, internal/resource.go, redaction/redaction.go, propagation/propagation.go, genai/genai.go, genai/attributes.go)
- `internal/server/server.go` — middleware chain, recover handler, logging middleware
- `internal/service/chat_generate.go` — primary span creation site (33 log.Printf calls)
- `internal/service/chat_tool_executor.go` — tool call spans
- `internal/service/delegation.go` — delegation span
- `internal/mcp/manager.go` — MCP discovery and tool call spans
- `internal/chat/context_client.go` — context assembly span
- `pkg/provider/anthropic.go` — provider spans and SSE tracking
- `internal/plugin/logger.go` — plugin logging adapter
- `internal/task/service.go` — only file using slog
- `cmd/nanite/main.go` — OTel init, startup logging

**Packages sampled:**
- All 69 files containing `log.Printf` calls (grep-sampled for patterns)
- All provider files in `pkg/provider/` (grep for span/log patterns)

**Packages skipped:**
- Test files (not relevant to production observability)
- Frontend (`ui/src/`) — not relevant to backend observability
- Plugin source in `plugins/` — plugin logging is covered via `internal/plugin/logger.go`

## Methodology

**Categories applied:**
- OTel wrapper correctness — exporter config, sampling, resource attributes, middleware wiring
- Structured logging discipline — slog vs log.Printf, log levels, machine-parseability
- PII in logs — API keys, passwords, tokens, user content in log fields
- Metric coverage — what metrics are emitted, what is missing
- Error reporting — how errors surface to operators
- Span discipline — span creation, closure, error recording, attribute hygiene

**Categories skipped with reason:**
- Security — covered by `security-threat-model`, `sandbox-hardening`, `dev-tools-input-validation` audits
- Concurrency — covered by `concurrency-cancellation-sweep` audit
- Test quality — covered by `whole-repo-tooling-and-tests-sweep` audit

**Tooling deferred:** `go vet`, `golangci-lint`, `go test -race` — narrow subsystem scope. Covered by `whole-repo-tooling-and-tests-sweep`.

**Cross-audit grounding:**
- `2026-04-11-telemetry-privacy-posture` — OTel always exports (finding 01), confirmed no user content in spans. Referenced in findings 01, 05, 08.
- `2026-04-11-panic-recovery-sweep` — single recover() in server.go logs only `%v`. Referenced in finding 06.
- `2026-04-11-provider-abstractions` — error body may leak key in logs (finding 07), PTY parse errors log raw lines (finding 08). Referenced in finding 07.

## Findings

### By severity

**Critical (0)**
- _none_

**High (3)**
- [01 — OTel HTTP propagation middleware defined but not wired](01-high-otel-http-middleware-not-wired.md)
- [02 — Metrics infrastructure defined but zero metrics emitted](02-high-metrics-infrastructure-unused.md)
- [03 — Structured slog handler exists but codebase uses log.Printf exclusively](03-high-slog-handler-unused-log-printf-everywhere.md)

**Medium (3)**
- [04 — GenAI semantic convention spans defined but never used](04-medium-genai-semantic-convention-spans-unused.md)
- [05 — Redaction span processor defined but not wired](05-medium-redaction-processor-not-wired.md)
- [06 — Panic recovery logs only %v with no stack trace or OTel span](06-medium-panic-recovery-no-stack-no-span.md)

**Low (1)**
- [07 — PTY/subprocess parse errors log raw CLI output lines](07-low-pty-subprocess-logs-raw-cli-output.md)

**Info (2)**
- [08 — Span discipline in core paths is sound where used](08-info-span-discipline-assessment.md)
- [09 — No API keys or passwords found logged directly](09-info-no-api-keys-in-log-fields.md)

### By topic

**OTel wrapper correctness**
- [01 — OTel HTTP propagation middleware not wired](01-high-otel-http-middleware-not-wired.md)
- [05 — Redaction span processor not wired](05-medium-redaction-processor-not-wired.md)

**Metric coverage**
- [02 — Metrics infrastructure unused](02-high-metrics-infrastructure-unused.md)

**Structured logging discipline**
- [03 — slog handler unused, log.Printf everywhere](03-high-slog-handler-unused-log-printf-everywhere.md)

**Span discipline**
- [04 — GenAI semantic conventions unused](04-medium-genai-semantic-convention-spans-unused.md)
- [08 — Span discipline assessment (positive)](08-info-span-discipline-assessment.md)

**Error reporting**
- [06 — Panic recovery observability gap](06-medium-panic-recovery-no-stack-no-span.md)

**PII in log fields**
- [07 — Raw CLI output in parse error logs](07-low-pty-subprocess-logs-raw-cli-output.md)
- [09 — No API keys in log fields (positive)](09-info-no-api-keys-in-log-fields.md)

## Recommended next steps

1. **Wire the OTel HTTP middleware** (finding 01). Single-line change in `internal/server/server.go:60` that connects all existing child spans to HTTP request parents. Prerequisite for findings 02 and 06.
2. **Migrate to slog** (finding 03). Largest scope item. Phase the migration: wire the handler at startup, convert hot paths first (`chat_generate.go`, `manager.go`, `server.go`), sweep remaining files. This enables trace-log correlation and structured log aggregation.
3. **Wire metrics** (finding 02). Add meter provider to `feotel.Init`, call `RegisterMetrics`, instrument request paths. This gives operators the basic request/latency/error dashboards.
4. **Adopt GenAI semantic conventions** (finding 04). Replace custom `nanite.*` span attributes with `gen_ai.*` in provider adapters. Extend tracing to all 15 providers (currently only Anthropic).
5. **Wire redaction processor** (finding 05). Latent issue — wire it now before GenAI semantic convention adoption adds prompt content to spans.
6. **Improve panic recovery observability** (finding 06). Add `debug.Stack()`, structured logging, and OTel span event to the recover handler. Depends on findings 01 and 03.

## Known issues skipped

- OTel always initializes with no opt-out — `telemetry-privacy-posture` finding 01. Not re-flagged.
- PTY parse errors log raw lines — `provider-abstractions` finding 08. Cross-referenced in finding 07 but scoped here to the observability dimension (PII risk), not the provider correctness dimension.

## Noticed but out of scope

- **Logging middleware does not record HTTP status code.** `internal/server/server.go:119` logs `%s %s %s` (method, path, duration) but not the response status. The `propagation.HTTPMiddleware` statusWriter captures this, which is another reason to wire finding 01. Suggested scope: `http-middleware-completeness`.
- **Plugin subprocess manager logging.** `internal/plugin/subprocess/manager.go` has 6 `log.Printf` calls for subprocess lifecycle events. These use ad-hoc prefixes like `"subprocess:"`. When slog migration happens, the plugin subprocess package should get a named `slog.Logger` with `"component"="plugin-subprocess"`. Covered by finding 03's scope.
- **Activity emitter observability.** `internal/chat/activity.go` has 5 `log.Printf` calls for activity event emission. These fire on every chat turn. If an activity endpoint is down, the error is logged but not metriced. Suggested scope: `activity-emitter-reliability`.
- **Context broker source errors.** `internal/contextbroker/broker.go:163` logs source errors but does not record them on any span. The `nanite.broker.assembleContext` span exists but source errors don't appear on it. Suggested scope: `contextbroker-error-propagation`.
