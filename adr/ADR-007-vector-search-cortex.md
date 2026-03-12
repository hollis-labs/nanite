---
title: "Vector Search Belongs in Cortex"
status: accepted
date: 2026-03-07
decision_makers: [chrispian, mentat]
origin: mentat-cli ADR-005
---

# ADR-007: Vector Search Belongs in Cortex

## Context

Multiple Tiamat apps need semantic search. Cortex already owns context and memory across all projects.

## Decision

**Vector search capabilities belong in Cortex. No other Tiamat service maintains its own embedding store.**

- Cortex adds an `embeddings` table alongside `records`
- New MCP tools: `context_embed` (generate/store embeddings), `context_search` (semantic search with cosine similarity)
- Local-first: Ollama with `nomic-embed-text` (768 dims, runs on CPU)
- Brute-force cosine similarity in Go (sufficient for <50k records)
- Consumers (Mentat, Volon) call Cortex via MCP for semantic search

## Consequences

- Ollama dependency (optional — Cortex works without it, search tools return errors)
- Schema migration for `embeddings` table
- Upgrade path: sqlite-vec if brute-force becomes slow at scale
