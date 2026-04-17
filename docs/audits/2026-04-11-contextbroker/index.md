# Deep Review: contextbroker — 2026-04-11

## Scope

**Scope string:** `contextbroker` (INDEX.md item 26)

**Interpretation:** Deep review of `internal/contextbroker/` as a subsystem — broker core, all 5 source adapters, the gate mechanism, intent classification, and budget allocation. Extended to `internal/memory/` (service and extraction) for the memory source's backing implementation, and to `internal/chat/context_client.go` + `internal/service/container.go` for the consumer/wiring side. Explicitly excluded: token counting, compaction, context-window management (separate scope, item 27).

**Files read in full:**
- `internal/contextbroker/broker.go` (299 lines)
- `internal/contextbroker/broker_test.go` (224 lines)
- `internal/contextbroker/intent.go` (79 lines)
- `internal/contextbroker/source_conduit.go` (183 lines)
- `internal/contextbroker/source_pcc.go` (181 lines)
- `internal/contextbroker/source_pcc_test.go` (102 lines)
- `internal/contextbroker/source_engine.go` (221 lines)
- `internal/contextbroker/source_session.go` (86 lines)
- `internal/contextbroker/source_memory.go` (137 lines)
- `internal/contextbroker/gate_hadron_blueprints.go` (300 lines)
- `internal/contextbroker/gate_hadron_blueprints_test.go` (330 lines)
- `internal/memory/service.go` (286 lines)
- `internal/memory/extraction.go` (334 lines)
- `internal/chat/context_client.go` (lines 290-388, broker enrichment)
- `internal/service/container.go` (lines 300-360, broker wiring)

**Dependency read (cross-package):**
- `vanta-conduit/internal/memory/recall.go` — similarity ranking validation logic

**Prior audits cross-referenced:**
- `2026-04-10-chat-engine/06-high-context-broker-unsanitized-injection.md` — prompt injection via FormatPacket (not re-flagged)
- `2026-04-11-concurrency-cancellation-sweep/index.md` — contextbroker has no goroutines, caller ctx is orphaned (not re-flagged)
- `2026-04-11-whole-repo-tooling-and-tests-sweep/05-golangci-lint-findings.md` — QF1012 in source_memory.go (not re-flagged)
- `2026-04-11-panic-recovery-sweep/goroutine-map.md` — contextbroker not in goroutine inventory

## Methodology

**Categories applied:**
- Security — path traversal, PII persistence, trust boundaries
- Error handling — error propagation, silent failures, graceful degradation
- Correctness — budget allocation, ranking, ordering, truncation
- Concurrency — parallel fetch opportunity, fire-and-forget goroutines (in memory/extraction)
- Antipatterns — dead code, stale naming

**Categories deferred:**
- Standards and tooling — `go vet`, `golangci-lint`, `go test -race` deferred to whole-repo-tooling-and-tests-sweep (already complete for this campaign)
- Test quality — tests exist and cover core paths; not a deep test-quality pass

**Tools NOT run (narrow scope deferral):**
- `go vet ./internal/contextbroker/...` — deferred, covered by whole-repo sweep
- `go test -race ./internal/contextbroker/...` — deferred, no goroutines in this package
- `govulncheck` — deferred to dependency-audit scope

## Findings

### By severity

**Critical (0)**
_none_

**High (2)**
- [01 — MemorySource similarity ranking always fails](01-high-similarity-ranking-always-fails.md)
- [02 — Conduit raw truncation splits multi-byte UTF-8](02-high-conduit-raw-truncation-splits-multibyte.md)

**Medium (5)**
- [03 — BudgetForIntent excludes memory source](03-medium-budget-for-intent-excludes-memory.md)
- [04 — PCC source path traversal via scope parameter](04-medium-pcc-source-path-traversal.md)
- [05 — Memory extraction fire-and-forget goroutines / PII persistence](05-medium-extraction-fire-and-forget-goroutines.md)
- [06 — SessionSource reverse chronological order](06-medium-session-source-reverse-order.md)
- [07 — SessionSource message truncation splits multi-byte UTF-8](07-medium-session-source-truncation-splits-multibyte.md)

**Low (2)**
- [08 — HadronBlueprintGate defined but not wired](08-low-hadron-gate-not-wired.md)
- [09 — Broker sequential fetches](09-low-broker-sequential-fetches.md)

**Info (1)**
- [10 — Observations](10-info-observations.md)

### By topic

**Correctness / Budget allocation**
- [01 — MemorySource similarity ranking always fails](01-high-similarity-ranking-always-fails.md)
- [03 — BudgetForIntent excludes memory source](03-medium-budget-for-intent-excludes-memory.md)
- [06 — SessionSource reverse chronological order](06-medium-session-source-reverse-order.md)

**Correctness / Truncation**
- [02 — Conduit raw truncation splits multi-byte UTF-8](02-high-conduit-raw-truncation-splits-multibyte.md)
- [07 — SessionSource message truncation splits multi-byte UTF-8](07-medium-session-source-truncation-splits-multibyte.md)

**Security**
- [04 — PCC source path traversal](04-medium-pcc-source-path-traversal.md)
- [05 — Memory extraction PII persistence](05-medium-extraction-fire-and-forget-goroutines.md)

**Concurrency**
- [05 — Memory extraction fire-and-forget goroutines](05-medium-extraction-fire-and-forget-goroutines.md)
- [09 — Broker sequential fetches](09-low-broker-sequential-fetches.md)

**Antipatterns / Dead code**
- [08 — HadronBlueprintGate not wired](08-low-hadron-gate-not-wired.md)

**Observations**
- [10 — Observations](10-info-observations.md)

## Recommended next steps

1. **Fix finding 01 (High).** Add `Query` field to `memory.RecallOpts`, wire it through, and have `source_memory.go` pass intent keywords as the query. Fall back to activation ranking when no keywords are available. This restores the memory source to functionality.

2. **Fix findings 02 and 07 (High/Medium).** Replace byte-offset truncation with rune-aware truncation in `source_conduit.go` and `source_session.go`. One-line fixes.

3. **Fix finding 04 (Medium).** Add path-confinement check to `PCCSource.resolveProjectDir`. Small change with security benefit.

4. **Address finding 05 PII aspect (Medium).** Add PII scrubbing to the memory extraction prompt or run the user-message filter chain before extraction. This closes the PII persistence loop called out in the reviewer-backend context.

5. **Fix finding 06 (Medium).** Reverse the session context output order. Small fix.

6. **Decide on finding 08 (Low).** Either wire the HadronBlueprintGate or remove it.

7. **Finding 09 (Low) and 03 (Medium) can be deferred** — they're latent bugs or optimizations, not actively broken paths.

## Known issues skipped

- **FormatPacket unsanitized injection** — `2026-04-10-chat-engine/06-high-context-broker-unsanitized-injection.md`. The prompt injection surface in `FormatPacket` and `enrichWithContextBroker` is already tracked. Not re-flagged.
- **generateResponse orphaned context** — `2026-04-11-concurrency-cancellation-sweep`. The broker's caller (`generateResponse`) runs on a `context.WithoutCancel` context. The broker itself is sound; the lifecycle weakness is in the caller. Not re-flagged.
- **golangci-lint QF1012** — `WriteString(Sprintf)` style in `source_memory.go`. Tracked in `2026-04-11-whole-repo-tooling-and-tests-sweep/05-golangci-lint-findings.md`. Not re-flagged.
- **Memory extraction goroutine lifecycle** — The concurrency aspect of fire-and-forget goroutines in `extraction.go` is catalogued in the concurrency-cancellation-sweep's goroutine map. This audit's finding 05 covers the PII and partial-write consequences specific to the contextbroker scope, not the general lifecycle concern.

## Noticed but out of scope

- **`internal/chat/context_client.go:L351-L376` intent classification is keyword-only.** The `classifyContextIntent` function uses simple `strings.Contains` matching on the user message. A message like "debug the build system" matches "debug" first and returns `IntentDebugIssue`, even though "build" would suggest `IntentWriteCode`. No weighting, no multi-match. Suggested follow-up scope: `context-intent-classification`.

- **`internal/service/container.go:L327` uses a relative PCC path.** `NewPCCSource(".nanite/pcc/global")` is relative to the working directory. If the process is started from a different directory, PCC resolution fails silently. Suggested follow-up: make the path absolute at wiring time using the resolved project root.

- **Conduit and Engine MCP source `ServerName` defaults are undocumented.** `ConduitSource.ServerName` defaults to `"conduit"` and `EngineSource.ServerName` defaults to `"engine"`, but these are the only values that work with the MCP tool name format `mcp__{server}__tool_name`. If the MCP server is registered under a different name, the source silently fails. Suggested follow-up: validate server name at construction time or document the convention.

- **`vanta-conduit/internal/memory/recall.go` similarity ranking filters out unembedded revisions post-sort.** Revisions without embeddings get score 0 from `similarityScore` but are included in the sorted results, then filtered at `recall.go:L169-L174`. This is correct but wastes sort work. Not a contextbroker concern; noted for a future Conduit audit.
