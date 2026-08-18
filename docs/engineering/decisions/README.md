# Architecture Decision Records

## Numbering — one sequence, starting here

There are currently **two separate, colliding ADR sequences** elsewhere in this repo: `docs/decisions/ADR-001-models-catalog-sync.md` and `docs/architecture/ADR-001-tool-scoping-and-resilience.md` — same number, unrelated topics, two different directories. Both predate this folder and are not being renumbered retroactively (that's real, careful work — reading each, confirming it's still valid, updating any cross-references — tracked as a follow-up, not done automatically).

**Going forward, this directory (`docs/engineering/decisions/`) is the one real ADR sequence.** The next ADR is `ADR-001` here, distinct from either legacy sequence. If you need to reference a pre-existing legacy ADR, name it by its full path, not by number alone, until the archival/renumbering pass resolves the collision.

## When to write one

An ADR captures a real decision between genuine alternatives, with the rationale — not routine implementation work. Most of the decisions from the two-day architecture review that produced this documentation set are captured in `docs/architecture-decision-log-2026-08-17.md` rather than as individual ADRs; that log is the record for this specific review. New architectural decisions from here on should get a real ADR here if they're the kind of call a future session would otherwise have to re-derive from scratch or risk re-litigating.

## How

Use the `adr` skill (`Skill({skill: "adr"})`) — it captures a decision as a formal ADR via a sub-agent. Don't hand-write one from scratch if the skill is available; it exists specifically so this stays consistent.

## Format

Standard ADR shape: title, status (proposed/accepted/superseded), context, decision, consequences. Link to the relevant `../architecture/*.md` file(s) the decision affects, and update that file if the decision changes the target design described there — an ADR records *that* a decision was made and *why*; the architecture doc records the *current state* that decision produced. Keep both in sync.
