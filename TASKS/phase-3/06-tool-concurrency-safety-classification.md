# Tool concurrency-safety classification — replace name-heuristic with declared metadata

**Phase:** 3
**Status:** not-started
**Depends on:** none
**Touches:** wherever the current name-heuristic lives (`GetToolMeta` per the architecture doc's own reference — locate and confirm exact file:line during implementation), `known_tools` catalog (Phase 1) if a declared-metadata field belongs there instead of/alongside the current mechanism.

## Context

Architecture doc `03-steering.md`, "Two correctness gaps carried into implementation": *"Tool concurrency-safety classification is currently pure name-heuristic (suffix/substring matching), not derived from declared tool metadata."* Decision log §11 restates this as a real, explicitly-not-forgotten gap (contrasted with things genuinely deferred by design) — a misleadingly-named destructive tool could be misclassified as safe to run concurrently with others, which is a real correctness/safety risk, not a style nit.

This item is listed under Steering in the architecture doc but is not spelled out as its own bullet in `TASKS.md`'s terse Phase 3 summary — it's included here because the architecture doc explicitly assigns it to this subsystem's "carried into implementation" list, and `TASKS.md` is deliberately terse (`docs/engineering/TASKS.md:1`: "Concrete, sequenced work implementing `architecture/*.md`").

## What to do

1. Locate the current heuristic (find `GetToolMeta` or equivalent — grep for the concurrency/parallel-safety classification logic; the architecture doc names it as suffix/substring name matching, e.g. treating tools named like `*_read`/`*_get` as safe and `*_write`/`*_delete` as unsafe, or similar).
2. Audit the heuristic against the full live tool catalog (built-in tools + any registered plugin/MCP tools) for misclassifications — a tool whose name doesn't match the expected pattern but is genuinely destructive (or vice versa: a tool that looks destructive by name but is actually safe).
3. Design a declared-metadata mechanism: a real field (likely on `known_tools`, Phase 1's new catalog table, or wherever tool metadata is registered today if `known_tools` isn't ready yet) that explicitly marks a tool's concurrency safety, set at registration time by whoever defines the tool — not inferred from its name.
4. Migrate the classification logic to read the declared field, falling back to the name-heuristic only for tools that haven't declared it yet (if a staged rollout is needed), or requiring every tool to declare it up front if that's more consistent with the "real relational references, not free-text strings" principle already governing Phase 1.
5. Fix every misclassification found in step 2 by setting the correct declared value.

## Done means

- Every built-in and currently-registered tool has an explicit, correct concurrency-safety classification — not inferred from its name.
- The name-heuristic is either removed entirely or reduced to a documented, narrow fallback (not the primary mechanism).
- The audit from step 2 and its findings are recorded in this file's Work Log, including any misclassifications found and fixed.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
