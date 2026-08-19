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

**Ollama is removed, not built.** Reversed 2026-08-18 (`TASKS/phase-0/05-remove-ollama-routing.md`, `TASKS/ESCALATIONS.md`): the "currently-run local provider" framing above didn't hold up against the code — no `internal/llm/ollama` (or equivalent) exists anywhere in this repo or the sibling monorepo, and `internal/store/seed.go`/`pkg/models/registry.go` both document a prior, deliberate removal of Ollama from the provider catalog (Step 6.5, SP-20260508-0001). `chat.InferProvider`'s dangling `"ollama"`-returning branch — which routed to a provider name never registered in `initProviders` — has been deleted; matching model names now fall through to the function's default routing floor. See `docs/engineering/TASKS.md` item 5.

**Provider reliability parity is accepted as best-effort.** Rate-limiting/circuit-breaking/prompt-caching are Anthropic-specific because that's what its SDK exposes. Nanite supports what each provider actually offers, on every provider it uses.

**`resolveProvider`'s bespoke fallback chain collapses into the construction-model cascade** (see [Agent Construction](01-agent-construction.md)). Once an `agents` row has a real, cascade-resolved `model_id`, provider/model resolution is "read the already-resolved value," not a second independent walk.

## CLI-vs-API routing is an explicit typed field

`agents.runtime_kind` (`cli` | `api`, nullable) replaces a fragile four-site string-prefix convention (`chat.IsCLIProvider`/`NormalizeCLIProvider`, `shouldUsePTY`, `agent_deps.go`'s `stripRegistryPrefix`, plus the boot-profile catalog's own layered `pty-` prefix convention) that has caused real historical misrouting bugs. One typed field, checked once, not a string pattern matched in four places expected to stay in sync by convention.

Part of this work: fully scrub the remaining "PTY" naming — see `GLOSSARY.md`.

**Both CLI and API substrates are being kept, not decided between.** A dedicated 2026-08-19 review found the two paths much closer in behavior than originally assumed. The remaining work is a three-tier default cascade: an app-level default (**CLI**), a system-wide override (`UserSettings.DefaultRuntimeKind`), and the existing per-agent override (`agent_profiles.runtime_kind`, nullable — NULL means "inherit"). Per-agent beats system-wide beats app default. See `TASKS/phase-8/01-set-default-runtime-kind.md`.
