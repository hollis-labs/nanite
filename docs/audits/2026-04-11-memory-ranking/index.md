# Deep Review: Memory Ranking

**Date:** 2026-04-11
**Reviewer:** nanite-reviewer-backend
**Branch:** audit-campaign-2026-04-11

## Scope

**Scope string:** `memory-ranking`

**Interpretation:** Activation vs similarity vs hybrid ranking in the memory subsystem. Audit of ranking correctness, embedding pipeline, BM25/keyword recall, PII scrubbing, and cross-verification of the contextbroker audit finding 01 (similarity ranking always fails).

**Packages read in full:**
- `internal/memory/` (Nanite) -- `service.go`, `extraction.go`, `service_test.go`
- `internal/contextbroker/source_memory.go`, `broker.go` (Nanite)
- `internal/memory/` (Conduit) -- `ranking.go`, `recall.go`, `activation.go`, `embed.go`, `write.go`, `read.go`, `decay.go`, `store.go`, `types.go`, `queue.go`, `dedup.go`, `adapter.go`
- `internal/embedding/search.go`, `mock.go`, `vectorindex.go` (Conduit)
- `conduit.go`, `embed_handler.go` (Conduit top-level)
- `internal/service/container.go` (Nanite) -- Conduit initialization

**Tests read in full:**
- `internal/memory/service_test.go` (Nanite)
- `internal/memory/recall_test.go`, `activation_test.go`, `embed_test.go`, `stubs_test.go`, `adapter_test.go`, `decay_test.go` (Conduit)

**Cross-audit references read:**
- `docs/audits/2026-04-11-contextbroker/01-high-similarity-ranking-always-fails.md`

**Packages sampled:** None. Full read of all relevant files.

**Packages skipped:** `internal/memory/ids.go`, `internal/memory/keys.go`, `internal/memory/namespaces.go`, `internal/memory/promote.go` (utility code tangential to ranking).

## Methodology

**Categories applied:**
- Correctness -- ranking formula verification, embedding pipeline, query wiring
- Security -- PII/secret scrubbing in memory extraction
- Error Handling -- fallback behavior when similarity fails
- Concurrency -- extraction goroutine lifecycle
- Architecture -- hybrid ranking design, BM25 gap analysis

**Categories deferred:**
- Standards and Tooling -- `go vet`, `golangci-lint`, `go test -race` deferred to whole-repo tooling sweep (`docs/audits/2026-04-11-whole-repo-tooling-and-tests-sweep/`)
- Test Quality -- unit test completeness for the ranking path was assessed qualitatively but no new tests were run

**Cross-audit grounding:** The contextbroker audit (2026-04-11) finding 01 identified that similarity ranking always fails because `source_memory.go` requests similarity but the Nanite `RecallOpts` has no `Query` field. This audit independently confirmed that finding and discovered the deeper root cause: embeddings are never generated because Nanite doesn't wire a job queue into Conduit.

## Findings

### By severity

**Critical (1)**
- [01 -- Embeddings never generated: NoopQueue discards all embed jobs](01-critical-embeddings-never-generated.md)

**High (2)**
- [02 -- RecallOpts lacks Query field](02-high-recall-opts-missing-query-field.md)
- [03 -- No hybrid ranking or graceful fallback](03-high-no-hybrid-ranking-fallback.md)

**Medium (4)**
- [04 -- Extraction fire-and-forget goroutines use background context](04-medium-extraction-fire-and-forget-no-ctx.md)
- [05 -- No PII or secret scrubbing before memory storage](05-medium-no-pii-scrubbing.md)
- [06 -- No BM25 or keyword-based recall](06-medium-no-bm25-keyword-recall.md)
- [07 -- MemorySource ignores Conduit recall scores](07-medium-relevance-scoring-ignores-conduit-score.md)

**Low (1)**
- [08 -- Activation ranking formula edge-case observations](08-low-activation-ranking-correctness.md)

**Info (1)**
- [09 -- Observations and praise](09-info-observations.md)

### By topic

**Embedding pipeline**
- [01 -- Embeddings never generated: NoopQueue discards all embed jobs](01-critical-embeddings-never-generated.md)

**Similarity ranking**
- [02 -- RecallOpts lacks Query field](02-high-recall-opts-missing-query-field.md)
- [03 -- No hybrid ranking or graceful fallback](03-high-no-hybrid-ranking-fallback.md)

**Activation ranking**
- [08 -- Activation ranking formula edge-case observations](08-low-activation-ranking-correctness.md)

**BM25 / keyword recall**
- [06 -- No BM25 or keyword-based recall](06-medium-no-bm25-keyword-recall.md)

**Broker integration**
- [07 -- MemorySource ignores Conduit recall scores](07-medium-relevance-scoring-ignores-conduit-score.md)

**Security**
- [05 -- No PII or secret scrubbing before memory storage](05-medium-no-pii-scrubbing.md)

**Concurrency**
- [04 -- Extraction fire-and-forget goroutines use background context](04-medium-extraction-fire-and-forget-no-ctx.md)

**Praise**
- [09 -- Observations and praise](09-info-observations.md)

## Recommended next steps

1. **Fix the embedding pipeline (01 + 02).** These two findings are the root cause of memory being completely non-functional for similarity ranking. Wire a `go-queue` instance into Conduit and add the `Query` field to `RecallOpts`. This unblocks the entire similarity path.

2. **Add fallback ranking (03).** Even with the embedding pipeline fixed, new memories won't have embeddings until the queue processes them. A fallback to activation ranking ensures memory context is always populated.

3. **Propagate recall scores (07).** Without this, the broker's cross-source relevance sorting is based on static confidence rather than dynamic ranking scores. Fix alongside or immediately after the embedding pipeline.

4. **Add PII scrubbing (05).** Before any public release, ensure secrets pasted into chat don't persist in memory.

5. **Consider BM25 (06).** SQLite FTS5 is straightforward to add and provides keyword-based recall that works without embeddings.

6. **Fix extraction context propagation (04).** Lower priority but improves graceful shutdown behavior.

## Known issues skipped

- Contextbroker audit finding 01 (`01-high-similarity-ranking-always-fails.md`) -- cross-referenced and confirmed from the memory side. Not re-filed as a separate finding; findings 01 and 02 in this audit are the memory-side root causes.
- Contextbroker audit finding 05 (`05-medium-extraction-fire-and-forget-goroutines.md`) -- same pattern flagged from the broker side. Finding 04 in this audit covers the memory-side view.

## Noticed but out of scope

- **Conduit embed_handler.go** (`vanta-conduit/embed_handler.go`) has no retry logic for transient embedding API failures. If the OpenAI/Ollama call fails, the job fails and (with `MaxTries: 3`) retries up to 3 times, but there is no backoff. Suggested follow-up scope: `conduit-queue-reliability`.
- **`internal/service/container.go:L261-L268`** -- Ollama probe uses a 3-second timeout with a real embedding call (`"test"` as input). If Ollama is installed but a model isn't pulled, this blocks for 3 seconds on every startup. Suggested follow-up scope: `startup-performance`.
- **Memory extraction prompts** (`extraction.go:L159-L172`, `L237-L256`) are in English only. Non-English user messages may not extract well. Suggested follow-up scope: `i18n-memory-extraction`.
- **DecayJob** (`decay.go`) runs in a goroutine started by Conduit (`conduit.go:L116`). Nanite has no visibility into whether decay is running or has errored. Suggested follow-up scope: `conduit-observability`.
