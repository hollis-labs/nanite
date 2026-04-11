# [Info] Plan notes, praise, and blind-spot declarations

**Scope:** plan evaluation
**Topic:** info / praise
**Date:** 2026-04-10

## I1 — Praise for the drift analysis

The plan's §0 ("Before anything else — read this") and Track A.1 drift-deletion list are the strongest part of the document. Enumerating the exact paths, classifying them as stale / active / aspirational / snapshot, and giving execution agents a clear rule ("grep returns exactly one result per plugin name") is exactly the kind of practical pre-execution discipline that prevents the next reviewer from wasting a day finding the wrong file. This is the pattern future plans should follow.

## I2 — Praise for the sharp-edges consolidated list (§13)

22 sharp edges enumerated in one place. Most plans hide footguns in the body text where execution agents miss them. Collecting them is high-signal for execution sessions. Would like to see this pattern in other architecture plans.

## I3 — Praise for explicit v2 deferrals (§15)

The "Deferred to v2" section is honest about scope boundaries and gives the team a decision log for why things weren't in v1. This prevents scope creep during execution. Recommend every future plan include a similar section.

## I4 — Plan writing style observation

The plan is written in a "talk to the execution agent" voice, including imperatives ("Must complete in full before any other track starts"), sharp-edge callouts, and "if any of that is news, stop and read" gates. This is good for an execution plan. It is NOT good for a design review by a third party, who has to distinguish between "design decisions that have been made" and "discussion of alternatives." For a plan this large, consider a short "Design decisions (immutable for this plan)" section at the top listing the choices that have been made and won't be re-opened, separated from the "execution details" that an agent can interpret. Right now reviewers have to infer which parts of the plan are load-bearing vs illustrative.

## I5 — Plan cites `finding #1` – `finding #6` but no findings file is in the repo

**Evidence:** Plan §0 says "this plan is the output of the 2026-04-10 discovery + design iteration that produced findings #1–#6. Read those findings in the conversation history for the reasoning trail." Then body text makes dozens of references to "finding #1 §3.4", "finding #2 §E", "finding #3 §Archive layout", "finding #4 §H", "finding #5 §Giphy revisited", "finding #6 §Plugin loader flow (detailed)", etc.

The findings are not in the repo. `docs/architecture/plugin-audit-2026-04-10.md` (the superseded document) is not the same as findings #1-#6. `docs/architecture/plugin-envelope-emission-findings-2026-04-10.md` might be one of them but isn't labeled as such.

**Impact:** Any execution agent in a fresh session cannot follow the plan's internal references. The reasoning trail is in "the conversation history" — i.e., the chat log of whoever wrote the plan. This is a documentation hazard: the plan references sources that don't exist from the agent's perspective.

**Recommendation:** Either inline the relevant finding content into the plan, or extract findings #1-#6 as separate docs under `docs/architecture/` and update the citations. Do this before any execution session starts.

## I6 — Things I noticed but did not investigate fully

This audit focused on the plan's load-bearing claims about `internal/plugin/` and its lifecycle. The following are outside that scope but worth logging for a future pass:

- `internal/chat/commands_builtin.go` — the chat engine's built-in command handlers. Plan §B.12 needs to touch this for envelope propagation but the exact surface isn't mapped.
- `ui/src/lib/plugin-loader.ts` — Track D rewrites this but I didn't read the frontend code. Plan accuracy assumed.
- `scripts/generate-plugin-imports.mjs` — Track D.3 changes this. Not read.
- `cmd/nanite/plugin_cmd.go` — Track G.6 extends CLI commands. Not read; cmd surface not verified.
- `internal/api/plugins.go` — install endpoint. Plan mentions install as a privilege boundary but I didn't read the current handler.
- `internal/store/` — the plan doesn't mention schema changes but yaml-authoritative registration may need persistence (e.g., which plugin is disabled). Not verified.
- `framework/libs/go-plugin/` — the SDK being replaced. Not read; consolidation plan (Track C.2) references fields and types I didn't verify.

Future audit scopes suggested:

- `plugin-tooling-and-tests` — run `go vet`, `go test -race`, `golangci-lint`, `staticcheck`, `errcheck`, `govulncheck` on the plugin system as it stands, file any new findings. This audit explicitly deferred tooling per the skill's scoped-review rule.
- `plugin-frontend-loader` — focused review on `ui/src/lib/plugin-loader.ts`, the importmap setup, and the subscription/useSyncExternalStore patterns the plan proposes.
- `plugin-trust-boundary` — a red-team pass specifically on "what can a malicious plugin do." Companion to the sandbox audit.

## I7 — Conduit lineage note

The plan's scaffold template imports `github.com/hollis-labs/conduit/internal/plugin` — a Conduit (v1 predecessor) reference per my memory notes. The plan treats this as a "fix the scaffold imports" chore (A.2), but it's a breadcrumb showing that the plugin system has genetic material from Conduit. If Conduit's plugin system had other ideas worth revisiting (subject-matter from the Seer v1 lineage memory entry), this may be worth a one-hour archaeology pass before execution begins. Not in scope for this audit — flagging as an observation.

## I8 — Plan is probably correct about what needs to ship eventually

I want to separate the what from the when. The plan's target architecture (yaml-authoritative registration, subprocess plugins, an SDK repo, hot install/uninstall, signed catalog, frontend bundle loading) is a reasonable end state for a mature plugin system and the design is mostly sound. My finding 07 is about timing, not correctness. If the user wants this plan executed post-beta as v0.2 work, most of the design holds up — it just needs the corrections from findings 01–06 and 09–11 baked in.

## I9 — Skill shakedown notes

See the index's "Skill shakedown notes" section. Recorded there so the skill maintainer can iterate.
