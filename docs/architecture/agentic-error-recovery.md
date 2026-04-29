# Agentic Error Recovery — A Portable Architectural Lens

> Default disposition: **recover, don't fail**. Hard errors are the only real show-stoppers; everything else is a recoverable signal the right layer can repair, route, or learn from. This is a lens, not a procedure.

**Vanta key:** `decisions.nanite.architecture.self_healing_tool_surface_lens` (originating capture, 2026-04-28/29 architecture brainstorm). Reference adoptions by this key, not this file.

## The lens

Agentic systems fail in shapes that look catastrophic but are usually local: an envelope schema mismatch, a wrongly-guessed card type, a tool argument that needed coercion. The c107 incident — an entire chat broken by one envelope failing validation — is the canonical shape. The lens reframes that class: each layer has an owner, and each owner gets one chance to recover before escalating. The system *self-heals at the lowest layer that has enough information*. Hard errors (auth, permission, fabrication risk) still fail loudly — that's the contract.

## The four layers

**Discovery.** Tools describe themselves machine-readably so the agent picks the right one without guessing, grounded in a registry rather than the model's prior. Implementation: CW-20260429-0005 (A1 — `nanite_tool_describe`).

**Validation.** Every tool call and result is validated at the boundary before it reaches the LLM or the renderer. Validation is deterministic, cheap, and produces a *typed* error pointing at the exact field. Implementation: CW-20260429-0006 (B1 — `nanite_validate`).

**Repair.** Validation errors are classified by the taxonomy below; recoverable classes get one bounded repair attempt — deterministic first (coerce, coalesce, remap), then a small LLM repair pass when deterministic logic can't reach the fix. Repair always informs the caller and never silently mutates user-supplied values. Implementations: CW-20260429-0007 (C1 — taxonomy), CW-20260429-0008 (C2 — LLM repair).

**Learning.** When repair succeeds, the system writes a hint to durable memory keyed by tool + error class so future calls benefit. The hint must be paired with a recall path — a store with no readers is dead weight. Implementation: CW-20260429-0009 (D1 — `nanite_remember` + Vanta).

## Variations: "ask a peer"

When the local repair LLM can't fix the error inside its budget, dispatch to a peer — a richer model, a specialist subagent, or a human reviewer — with the validator output and the failed payload. The shape stays the same (deterministic-first, LLM-augmented when necessary, learning hint on success); only the budget and authority change. This is how the lens scales from "one cheap repair pass" to real escalation without reshaping the protocol.

## Recoverable error taxonomy

Per C1: `schema_validation`, `type_coercion`, `wrong_card_type`, `missing_optional_field`, `format_mismatch`. Each class names a deterministic repair strategy first; LLM repair is the fallback within a class, never the entry point. Errors outside the taxonomy fail hard.

## When NOT to apply

- **Security boundaries (auth, permission, capability checks).** Never repair — recovery would be privilege escalation. *Mitigation:* taxonomy excludes auth-class errors; validator emits `hard_error`.
- **Value fabrication.** Don't invent missing user-supplied content (names, IDs, message bodies) — that's hallucination dressed as resilience. *Mitigation:* repair may coerce types and remap field names, never synthesise opaque values; missing-required fields fail hard.
- **Infinite-loop risk.** Repair must be bounded and observable. *Mitigation:* per-call repair budget at the harness; learning store prevents re-hitting the same shape.

## Anti-patterns

- **Silent value mutation.** Repairing a payload without telling the caller what changed.
- **Unbounded repair loops.** "Try again" with no budget — one bad tool can spend a session.
- **Repair without informing the caller.** Even successful repairs must surface a summary; the agent's next decision depends on knowing what got coerced.
- **Learning hints with no recall path.** Writing to memory no one reads. If `nanite_remember` has no consumer, it's not learning, it's logging.

## Cross-portfolio applicability

The lens is not Nanite-specific. Mux, Clockwork, Vanta, and plugin authors can each adopt it independently — the four layers map onto any tool-calling surface. Reference the lens by Vanta key so each adoption stays linked to the same conceptual root, even when implementations diverge.

<!-- Reserved for A1: Golden examples convention.
     The A1 ticket (CW-20260429-0005) may add a "Golden examples convention"
     section here describing how `nanite_tool_describe` exposes example payloads
     to drive discovery + repair. Leave this anchor in place. -->

## Cross-references

- **Vanta:** `decisions.nanite.architecture.self_healing_tool_surface_lens`
- **Sprint:** SP-20260428-0002
- **Tickets:** A1 (CW-20260429-0005), B1 (CW-20260429-0006), C1 (CW-20260429-0007), C2 (CW-20260429-0008), D1 (CW-20260429-0009), E1 (sprint retrospective), E2 (this doc — CW-20260429-0011)
