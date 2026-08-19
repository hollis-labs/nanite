# Review `adapter-nanite-native`'s use of `.nanite/config.yaml` as a runtime-agent source — likely conflates dev-boot convention with the real agent system

**Phase:** 1
**Status:** deferred — operator needs to deep-dive this personally before any fix is scoped; do not dispatch mechanically

## ⚠️ Not ready for dispatch

The operator has said directly they believe this was already "sorted" at some point and wants to trace where that got lost before deciding what to do — this needs their own review, not a worker guessing at intent. This file exists to capture the concrete, verified findings so nothing gets lost, not to prescribe a fix. **Do not dispatch a worker against this task until the operator has done that review and given explicit direction.**

## What's verified, directly, as of 2026-08-18

**Two unrelated things share one filename.** `docs/engineering/GLOSSARY.md`'s **Agent** entry is explicit: *"the unrelated `.nanite/config.yaml`/`~/.nanite/roles/` developer-persona-boot convention used for working **on** this codebase... neither of those participates in Nanite's own runtime agent system."* This repo's actual `.nanite/config.yaml` confirms the intent — it declares `nanite-backend`, `nanite-frontend`, `nanite-planner`, `nanite-reviewer`, etc., the same personas this project's own `CLAUDE.md` boot instructions reference ("If the user says 'Boot nanite-backend'...").

**But `internal/plugin/builtin/adapter-nanite-native/plugin.go` treats this file as a real runtime-agent definition source anyway.** Verified by reading the code directly:
- `Plugin.Load()` runs at every boot (plugin loading is unconditional at startup), parses `.nanite/config.yaml`'s `agents:` map, composes a system prompt per entry from `~/.nanite/roles/*.md`, and calls `store.UpsertAgentBySlug(ap)` for every declared agent, with `Source: "nanite"`.
- `store.UpsertAgentBySlug` (`internal/store/agents.go:838`) is a **raw, unconditional overwrite** — `GetAgentBySlug` → `UpdateAgent` if found, `CreateAgent` if not. No freeze, no source/bootPass gating, completely independent of `08`'s (Round 1 or Round 2) `upsertAgentDef`/`AutoIngestAgents` fix — a third, entirely separate write path neither round of `08` touches or was scoped to touch.
- Confirmed live, not just by reading code: both of this session's own dogfeed boot logs (Wave 1 and Wave 2 validation checkpoints) show `"adapter-nanite-native: synced agent"` for `nanite-backend`, `nanite-frontend`, `nanite-plugin-dev`, `nanite-planner`, `nanite-reviewer`, `nanite-reviewer-backend`, `nanite-reviewer-frontend` — 7 real `agent_profiles` rows, every single boot, unconditionally overwritten.
- Separately, `Adapter.Discover()` *also* independently re-reads and re-composes the same file into `agent.Definition`s for `internal/agent/discovery.go`'s adapter tier (priority 5+), feeding into the *gated* `AutoIngestAgents`/`upsertAgentDef` pipeline — so there appear to be **two parallel ingestion paths** for the same config file, one gated (via `Discover`), one not (via `Plugin.Load`'s direct `UpsertAgentBySlug` call).
- `Adapter.PopulateSandbox` writes a *minimal* `.nanite/config.yaml` into a CLI-subprocess sandbox directory at launch time — a real DB→file generation direction, but scoped to ephemeral per-launch sandboxing, not persistent agent-definition storage. Noted for completeness in case it's relevant to what "generated as files" meant in an earlier discussion this session — this is the only real DB→file agent-related generation found anywhere in the codebase.

**Why this wasn't caught by Phase 0 `16-cut-external-agent-import.md` or Phase 1 `08`'s two rounds:** `16`'s own text says *"this must keep running, because it's also how the nanite-native adapter's Discover... gets invoked, and that one is not being cut"* — reads as accepting the adapter's existing behavior as settled infrastructure rather than questioning whether *this specific* config→agent-row behavior is architecturally sound. `08`'s Round 2 (in progress as of this writing) was explicitly told not to touch this adapter, per that same Phase 0 precedent — this finding surfaced only when the operator asked to review the adapter directly, independent of either task's own scope.

## Open questions for the operator's review (not answered here)

- Was there ever a real product intent behind "declare a runtime Nanite agent via `.nanite/config.yaml`," distinct from the dev-boot-persona use this file also serves in this repo? Or is this adapter's agent-sync behavior simply a leftover/mistake that should be cut entirely?
- If it should be cut: does `PopulateSandbox`'s sandbox-config-generation behavior (the real DB→file direction) stay, since it's a different, narrower, launch-time mechanism?
- Where specifically did this get "sorted" before (per the operator's own recollection) and not carried through — worth checking `docs/architecture-decision-log-2026-08-17.md` in full again, and possibly the original two-day design-review's fuller history if it exists outside the committed docs, once the operator has time.

## Work log
<Not started — awaiting operator review per the banner above.>

## Review notes
<N/A until a fix is actually scoped and dispatched.>
