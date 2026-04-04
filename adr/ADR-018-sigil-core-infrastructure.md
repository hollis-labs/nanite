# ADR-018: Sigil as Core Infrastructure — Shared Tool, Not Product

**Status:** Accepted
**Date:** 2026-03-13
**Deciders:** chrispian, Mentat
**Relates to:** ADR-016 (Carrier Absorption), ADR-017 (Nanite RAG Vault)

## Context

Sigil is a declarative UI code generation system: YAML page configs → validated → framework-specific source code (Go/Templ+HTMX or React/shadcn+Tailwind). It has 49 component types, 2 render targets, a CLI with 14 commands, an MCP server, a live dev server, and 212 passing tests.

During strategic review, we evaluated whether Sigil belongs in the product lineup alongside Hadron and Nanite, or is better classified as shared infrastructure like `tiamat-otel`.

## Decision

Classify Sigil as **core infrastructure / shared tool**, not a product in the portfolio.

### Rationale

1. **No persona or agent identity** — Sigil doesn't "think." It transforms YAML to code. It's a compiler.
2. **No state or database** — Flat YAML files in `.sigil/`, no service dependencies.
3. **No standalone user story** — Nobody opens Sigil to "use Sigil." They use it inside a workflow to generate UI for something else.
4. **Analogous to `tiamat-otel`** — Shared infrastructure that every project benefits from but nobody "runs" as a product.

### Portfolio Taxonomy (Updated)

| Tier | Projects | Identity |
|------|----------|----------|
| **Core Platform** | Nanite, Cortex, Volon | The product — what users interact with |
| **Ecosystem Tools** | Hadron, Nanite | Standalone value + Fragments Engine plugins |
| **Shared Infrastructure** | Sigil, tiamat-otel, tiamat-mcp-helpers, fragments-ingest | Libraries and tools that agents/apps consume |
| **Absorbed** | Carrier | Special Agent + shared ingest lib (ADR-016) |

### What This Means for Sigil

- **Keep separate repo and binary** — it works independently and has a clean CLI
- **Don't position in product marketing** — it's not alongside Hadron/Nanite in the lineup
- **MCP integration stays** — agents use Sigil via MCP for UI generation tasks
- **No Nanite plugin needed** — the `/sigil-ui` skill is sufficient; Sigil doesn't need rich chat integration
- **No epics or sprints tracked as "product work"** — improvements tracked as infrastructure/tooling tasks

## Consequences

### Positive
- Clearer product narrative: Fragments Engine = Nanite + Cortex + Volon + ecosystem tools
- Sigil development stays focused on code generation quality, not product features
- Reduces portfolio cognitive load (fewer "products" to explain)

### Negative
- Sigil gets less visibility in marketing/docs (mitigated: it's still a useful open-source tool)
- May receive less development attention as infrastructure vs product

### Risks
- Infrastructure neglect — ensure Sigil stays maintained as portfolio grows
- If Sigil gains significant external interest, reconsider classification
