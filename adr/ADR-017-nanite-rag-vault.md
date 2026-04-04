# ADR-017: Nanite RAG Vault — Multi-Vault Knowledge Retrieval with System Agents

**Status:** Accepted
**Date:** 2026-03-13
**Deciders:** chrispian, Mentat
**Relates to:** ADR-016 (Carrier Absorption), ADR-013 (Nanite Separation)

## Context

Nanite is a keyboard-driven personal task/note manager (Wails v2 desktop app) with:
- Multi-vault SQLite databases (independent per vault)
- FTS5 full-text search
- Rich text with TipTap (@mention cross-references)
- REST API server for external integrations
- Inbox system for fast capture

The vault multi-tenancy system is already 80% of the way to a configurable RAG solution. Each vault is an independent SQLite database that could serve a different knowledge domain. Adding embeddings and a retrieval-focused System Agent would transform Nanite from "note app" into "knowledge vault infrastructure."

## Decision

Evolve Nanite into a configurable RAG vault system while preserving its standalone desktop app identity.

### New Capabilities

#### 1. Embedding Storage
- Add vector column to items (via `sqlite-vec` or similar SQLite extension)
- Embedding generation on item save (configurable model: local or API)
- Hybrid retrieval: FTS5 keyword search + vector similarity search + cross-reference graph

#### 2. Vault Profiles
- Each vault gets a profile YAML describing:
  - Domain (e.g., "gameworld-lore", "programming-notes", "book-manuscript")
  - Retrieval strategy (keyword-heavy, semantic-heavy, hybrid)
  - Chunking configuration (for long documents)
  - Schema expectations (what fields matter for this domain)
  - System Agent configuration (which agent manages this vault)

#### 3. System Agent per Vault
- A lightweight agent responsible for:
  - Chunking and embedding new/updated items
  - Context assembly for retrieval queries
  - Index maintenance (re-embed on model change, prune stale embeddings)
  - Domain-specific retrieval logic (entity extraction for lore vaults, code awareness for dev vaults)
- Runs as a background process within Nanite or as a Hadron-scheduled job

#### 4. Nanite Plugin (Deep Integration)
- Rich conversational access to any vault via Nanite
- Vault selection in chat context (switch between Second Brain, GM vault, dev notes, etc.)
- `/recall` command for explicit retrieval
- Ambient context injection (auto-retrieve relevant items based on conversation)
- Cross-vault search when scope permits

### Example Use Cases

| Vault | Domain | System Agent Behavior | Nanite Command |
|-------|--------|----------------------|-----------------|
| Second Brain | Personal notes | Broad semantic retrieval | `/recall` |
| GM Assistant | Gameworld knowledge | Entity-focused, lore consistency | `/lore` |
| Dev Notes (Mentat) | Code context, ADRs | Architecture-aware retrieval | `/context` |
| Book Collaborator | Manuscript + research | Chapter-scoped, continuity checking | `/draft` |

### Dual Distribution Model
- **Standalone**: Nanite desktop app works as-is — notes, tasks, vaults, keyboard UX
- **Integrated**: Nanite MCP exposes vault operations; Nanite plugin provides conversational access
- The binary is the product, the MCP is the API, the plugin is the integration

## Consequences

### Positive
- Nanite becomes the storage/retrieval layer for domain-specific knowledge across the portfolio
- Hot-swappable vaults let users configure RAG for any domain without code changes
- Standalone value increases (personal RAG is a compelling product)
- Natural integration point for Carrier's ingested artifacts (post-absorption)
- Each vault is independently distributable (share a vault file = share a knowledge base)

### Negative
- SQLite vector extensions add a native dependency (may complicate pure-Go builds)
- Embedding model choice affects quality and portability
- System Agent adds background processing complexity to a currently simple app

### Risks
- Scope creep — Nanite should stay keyboard-first and fast; RAG features must not slow down the core UX
- Vector search quality depends heavily on embedding model choice; need benchmarking
- Multi-vault System Agents could create resource contention on constrained machines

## Implementation Path

1. **Phase 1**: Add `sqlite-vec` integration, embedding column, basic similarity search API
2. **Phase 2**: Vault profiles (YAML config per vault), retrieval strategy configuration
3. **Phase 3**: System Agent skeleton — embed on save, background index maintenance
4. **Phase 4**: Nanite plugin — conversational vault access, ambient context injection
5. **Phase 5**: Cross-vault search, vault sharing/export format
