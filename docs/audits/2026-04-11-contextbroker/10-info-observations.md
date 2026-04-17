# [Info] Observations

**Scope:** contextbroker
**Topic:** Various
**Date:** 2026-04-11

## 10a. Package doc references "Mentat" — stale naming

`broker.go:L1` says "Package contextbroker provides universal context retrieval for Mentat." The project has been rebranded to Nanite. Not a functional issue; cosmetic.

**File:** `internal/contextbroker/broker.go:L1`

---

## 10b. Test helper `contains` reimplements `strings.Contains`

`gate_hadron_blueprints_test.go:L315-L329` defines a custom `contains` function that is a verbose reimplementation of `strings.Contains`. The manual byte-by-byte scan is harder to read and slower than the stdlib.

**File:** `internal/contextbroker/gate_hadron_blueprints_test.go:L315-L329`

---

## 10c. Well-structured source interface and budget allocation

The `ContextSource` interface is clean (3 methods including `Name`), the budget allocation is well-tested, and the `trimToBudget` function correctly uses `continue` (not `break`) to skip oversized items while still considering smaller items later in the list. The test coverage in `broker_test.go` covers no-source, single-source, multi-source, budget enforcement, and source-error cases. Good reference implementation.

**Files:** `internal/contextbroker/broker.go`, `internal/contextbroker/broker_test.go`

---

## 10d. Token estimate heuristic is byte-based

`EstimateTokens` uses `(len(text)+3)/4` which counts bytes, not characters. For ASCII text this is a reasonable 4-bytes-per-token approximation. For CJK or emoji-heavy text, byte count overestimates token count (CJK characters are ~1 token each but 3 bytes in UTF-8, so the heuristic says ~1 token per character which happens to be close). The heuristic is consistent across all sources and is fine for budget allocation purposes.

**File:** `internal/contextbroker/broker.go:L264-L266`

---

## 10e. Embedding provider fallback is outside contextbroker

The reviewer-backend context asks about embedding provider fallback (OpenAI vs Ollama). This lives entirely in `vanta-conduit/internal/embedding/`, not in the contextbroker or `internal/memory/` packages. The contextbroker's memory source simply calls `memory.Service.Recall`, which calls Conduit's `Store.Recall`, which calls the embedder. The fallback chain is a Conduit concern. Within the contextbroker scope, the relevant behavior is: if embedding fails, the Conduit recall returns an error, the memory source wraps it, and the broker logs it and continues without memory (broker.go:L162-L164). This is correct graceful degradation.

**Files:** `internal/contextbroker/source_memory.go:L33`, `internal/contextbroker/broker.go:L162-L164`

---

## 10f. golangci-lint QF1012 style finding

`source_memory.go:L126-L131` uses `sb.WriteString(fmt.Sprintf(...))` which should be `fmt.Fprintf(&sb, ...)`. Already noted in the whole-repo-tooling-and-tests-sweep (`05-golangci-lint-findings.md`). Not re-flagged.

**File:** `internal/contextbroker/source_memory.go:L126-L131`
