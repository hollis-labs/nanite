# Nanite Engineering Docs

This is the canonical, current-state reference for how Nanite's agent system actually works, and how it's meant to work going forward. It supersedes older architecture docs elsewhere in `docs/` — see the archival note below.

**This is developer/engineering documentation, not user-facing documentation.** User-facing docs are a separate, not-yet-started effort.

**This folder is written to be agent-friendly as a first-class goal, not an afterthought.** A large fraction of the problems this documentation set exists to prevent — naming collisions, duplicated mechanisms, stale conventions baked into agent boot prompts — came from many separate, context-isolated agent sessions each making locally-reasonable decisions without a shared, current source of truth to check against. If you're an agent working on this codebase: read `GLOSSARY.md` before introducing new vocabulary, read the relevant `architecture/*.md` file before proposing a design that touches that subsystem, and update these docs as part of the work, not as an afterthought — that's the actual point of this folder existing.

## Layout

- **`GLOSSARY.md`** — canonical term definitions. Start here if a word feels ambiguous.
- **`architecture/`** — one file per subsystem, current target-state design (not a decision log — for the reasoning/history behind these designs, see `docs/architecture-decision-log-2026-08-17.md`).
- **`TASKS.md`** — the concrete, sequenced engineering work implementing the architecture docs.
- **`decisions/`** — Architecture Decision Records (ADRs). One numbering sequence, going forward, from here.
- **`standards/`** — coding standards, testing philosophy, patterns, code quality. Stubs as of this writing — real content gets added as it's actually established, not invented wholesale up front.
- **`deployment.md`** — how Nanite actually gets built and deployed.
- **`faq.md`** — short-answer reference for recurring questions.

## Status and provenance

This folder was created 2026-08-18 as the output of a two-day architecture alignment review covering agent construction, launching, steering, the harness, storage/migrations, session lifecycle & recovery, inter-agent messaging, the Cards (UI-card) system, and the plugin system. The frontend didn't get its own dedicated pass yet — deliberately deferred until more of this backend work lands, since a meaningful amount of frontend work will be informed or resolved by it.

**Keeping this current is meant to be part of normal engineering work from here on**, not a separate documentation project. When a decision here changes, update the relevant file in the same pass as the code change, the same way tests get updated alongside the code they cover.

## Older docs — superseded, not yet archived

`docs/architecture/`, `docs/decisions/`, and most top-level `docs/*.md` files predate this folder and are now superseded where they overlap with it — including two independently-numbered `ADR-001` files in two different directories, and a general `docs/architecture/ARCHITECTURE.md` that this folder now replaces. A proper archival pass (move to a clearly-marked location, or delete where there's no remaining historical value) is a real, scoped follow-up task, not yet done — see `TASKS.md`. Until that lands, treat anything under `docs/` outside this folder and `docs/system-audit/2026-08-17/` as **possibly stale** — this folder wins on conflict.

`docs/system-audit/2026-08-17/` is preserved as-is, permanently, as historical evidence — it is not part of this documentation set and is not meant to be kept current.
