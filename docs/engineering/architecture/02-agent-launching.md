# Agent Launching

## CLI-based subprocess launching

Nanite has never run agents through a real pseudo-terminal in the current design — that approach was abandoned for `StreamingStdio` (a long-lived subprocess exchanging NDJSON over plain stdin/stdout pipes, for Claude) or a fresh subprocess per turn (Codex, OpenCode). "PTY" is a naming fossil surviving in a few provider-name strings and function names being actively scrubbed — see `GLOSSARY.md`.

**The boot-directory strategy is correct and stays exactly as-is.** Every CLI launch spawns with `cwd` set to a Nanite-owned boot directory, not the project directory — project access happens via `--add-dir`. This is deliberate: only the `CLAUDE.md`/`AGENTS.md` loaded from the process's own boot-dir `cwd` survives Claude Code's context compaction. Planting into Nanite's own directory is *why* Nanite's own system/workflow instructions reliably survive compaction.

**Post-compaction re-read of the project's real `CLAUDE.md`/`AGENTS.md` is mandatory-by-default**, code-driven — not a per-agent opt-in flag.

**The boot-profile catalog is retired as a standalone system.** It never competed with agent construction — it only ever overrode prompt *content*, with a real `agents` row still resolved underneath. Two pieces carry forward as first-class, DB-configurable mechanisms available to every agent (not gated behind a separate catalog):
- The `cmd`/`http` dynamic-resolver capability (fetch live data at launch time and fold it into assembled context).
- The mandatory compaction re-read, above.

Nothing else carries forward. "Lineage" (built for Tesseract's version-diffing use case) is dropped entirely. Cross-app portability (Tether/Torque interop via a shared boot-profile file format) is deliberately opt-in, not a structural default — cross-app agent access goes through MCP.

## API-based launching

**Provider registration (code) and provider/model metadata (DB) are two distinct concerns.** Registering a new HTTP provider (an SDK wrapper implementing `llmcontracts.Provider`) is inherently a code change. The `providers`/`models` DB tables are catalog/display metadata layered on top of already-registered providers; they can never make a new provider exist.

**Ollama gets fixed for real, not cleaned up as dead code.** It's a real, currently-run local provider — `chat.InferProvider` already special-cases Ollama-shaped model names but routes to a provider that was never (re-)registered. Needs a proper `internal/llm/ollama` adapter and registration.

**Provider reliability parity is accepted as best-effort.** Rate-limiting/circuit-breaking/prompt-caching are Anthropic-specific because that's what its SDK exposes. Nanite supports what each provider actually offers, on every provider it uses.

**`resolveProvider`'s bespoke fallback chain collapses into the construction-model cascade** (see [Agent Construction](01-agent-construction.md)). Once an `agents` row has a real, cascade-resolved `model_id`, provider/model resolution is "read the already-resolved value," not a second independent walk.

## CLI-vs-API routing is an explicit typed field

`agents.runtime_kind` (`cli` | `api`) replaces a fragile four-site string-prefix convention (`chat.IsCLIProvider`/`NormalizeCLIProvider`, `shouldUsePTY`, `agent_deps.go`'s `stripRegistryPrefix`, plus the boot-profile catalog's own layered `pty-` prefix convention) that has caused real historical misrouting bugs. One typed field, checked once, not a string pattern matched in four places expected to stay in sync by convention.

Part of this work: fully scrub the remaining "PTY" naming — see `GLOSSARY.md`.
