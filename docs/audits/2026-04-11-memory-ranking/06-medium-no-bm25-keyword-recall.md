# [Medium] No BM25 or keyword-based recall -- only vector and activation ranking exist

**Scope:** memory / ranking
**Topic:** Correctness / Design Gap
**Date:** 2026-04-11

## Problem

The memory ranking system supports three modes: `activation`, `chronological`, and `similarity` (vector). There is no BM25, TF-IDF, or keyword-based recall mode. When the user's intent has keywords but no embedding infrastructure is available (or before embeddings are generated), there is no text-matching fallback that can leverage those keywords for retrieval.

## Evidence

Conduit's `RecallInput` defines three ranking modes:

```go
// vanta-conduit/internal/memory/recall.go:L15-L21
const (
    RankingActivation    Ranking = "activation"
    RankingChronological Ranking = "chronological"
    RankingSimilarity    Ranking = "similarity"
)
```

The scoring switch in `Recall`:

```go
// vanta-conduit/internal/memory/recall.go:L147-L155
switch in.Ranking {
case RankingActivation:
    score = activationScore(rev, st, now)
case RankingChronological:
    score = float64(chronologicalKey(rev))
case RankingSimilarity:
    score = similarityScore(rev, queryVec)
}
```

Grep for `BM25`, `bm25`, `keyword.*search`, `fulltext`, `full.text` across the entire Conduit codebase returns zero hits in Go source files.

The `fetchCandidates` SQL query filters by namespace, status, origin, confidence, and time window -- but performs no text search on `payload_summary` or `payload_body`.

## Impact

Without keyword recall, the system has a gap between "return everything ranked by activation" and "return semantically similar results." When a user asks about a specific topic (e.g., "what did I say about SQLite?"), and similarity ranking is unavailable (currently always), the only option is activation ranking, which returns memories ranked by access frequency and recency -- not by content relevance to the query. This produces poor recall quality for targeted questions.

## Recommendation

Add a `keyword` ranking mode using SQLite FTS5:

1. Create an FTS5 virtual table over memory revision payloads:

```sql
CREATE VIRTUAL TABLE IF NOT EXISTS memory_fts USING fts5(
    revision_id UNINDEXED,
    memory_id UNINDEXED,
    summary,
    body,
    content='memory_revisions',
    content_rowid='rowid'
);
```

2. Add a `RankingKeyword` constant and implement keyword scoring via FTS5 `rank`:

```go
case RankingKeyword:
    // Use FTS5 MATCH + bm25() for keyword relevance
```

3. Consider a hybrid mode that combines keyword scores with activation scores using rank fusion.

This is not a release blocker but significantly improves recall quality, especially for the common case where embeddings are unavailable.

## References

- `vanta-conduit/internal/memory/recall.go:L15-L21` -- ranking modes
- `vanta-conduit/internal/memory/recall.go:L147-L155` -- score switch
- `vanta-conduit/internal/memory/recall.go:L192-L271` -- fetchCandidates SQL (no text search)
- SQLite FTS5 documentation: https://www.sqlite.org/fts5.html
