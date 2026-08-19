# Make the project's post-compaction CLAUDE.md/AGENTS.md re-read mandatory-by-default, code-driven

**Phase:** 2
**Status:** not-started
**Depends on:** none
**Touches:** wherever the current per-agent opt-in flag for this behavior lives (not yet located — confirm during implementation, see Context), `internal/runtime/agent/bootdir.go`/`kickoff.go` (the boot-dir planting infrastructure this task does NOT change, only confirms/doesn't break)

## Context

**Read this task's distinction carefully before starting — a planning landmine flags this precisely**: *"the boot-profile catalog retirement is subtle. The mandatory post-compaction re-read exists because of a specific Claude Code CLI fact — only the boot-dir's own `CLAUDE.md` survives compaction — not because of a general 'agents should re-read project files' preference. Get this distinction right before scoping what to port forward vs. retire."*

There are genuinely **two separate things** here, confirmed against the code, and this task is only the second one:

1. **Nanite's own boot-dir `CLAUDE.md`/`AGENTS.md` planting and its survival through Claude Code's compaction** — this is **already real, shared, working infrastructure**, confirmed via `internal/runtime/agent/prompt.go:19-22`'s doc comment: *"For ModeLongLived sessions the prompt is set ONCE at PTY start... slot changes thereafter regenerate `<bootDir>/CLAUDE.md` and rely on claude's context-recovery re-read."* This is what makes Nanite's own system/workflow instructions durable across compaction — it already works, it's not catalog-specific, and **this task does not touch it**. Decision log §7 is explicit: *"already safe today, nothing to port, just something to not accidentally break when launching gets redesigned."*
2. **Whether an agent is additionally instructed to re-read the *project's own real* `CLAUDE.md`/`AGENTS.md`** (a different file — the project's, not Nanite's boot-dir's) **after compaction** — decision log §7: *"a deliberate, optional design choice layered on top (a deliberate choice, not a technical necessity)... can be made mandatory/code-driven now if wanted, low stakes either way"* and separately, architecture doc `02-agent-launching.md`: *"Post-compaction re-read of the project's real `CLAUDE.md`/`AGENTS.md` is mandatory-by-default, code-driven — not a per-agent opt-in flag."* **This is what this task actually changes.**

### The specific per-agent opt-in flag — not yet located, confirm during implementation

Research for this planning pass did not conclusively locate a specific, currently-existing per-agent flag gating instruction (2) above — grepping `internal/runtime/agent/`/`internal/agent/` for project-CLAUDE.md re-read instructions found only the boot-dir's own regeneration-and-implicit-re-read mechanism (item 1, untouched by this task). **Before implementing, a worker must first locate the actual current mechanism** (likely a boot-profile-catalog-driven prompt addendum, given the boot-profile catalog's `LaunchSpec.BootPrompt` override path documented in `prompt.go:37-44` — `resolveBootPrompt`'s `Options.BootPromptOverride`; if the current "re-read the project CLAUDE.md" instruction is only ever injected via a catalog-authored `BootPrompt`, then it may not exist as a standalone flag at all today outside the (retiring) boot-profile catalog, in which case this task's real job is *introducing* the mandatory code-driven instruction for the first time, not converting an existing opt-in flag to mandatory). Document what's actually found in this file's Work Log — this Context section states the landmine and the search starting points, not a confirmed pre-existing mechanism.

## What to do

1. Locate whatever currently controls whether an agent's boot content instructs it to re-read the project's real `CLAUDE.md`/`AGENTS.md` post-compaction. Document what's found (a real per-agent flag, or confirmation this only ever came from catalog-authored `BootPrompt` content and has no other current source).
2. Make this instruction code-driven and unconditional for every CLI-based agent's planted boot content — not a per-agent opt-in, not YAML-catalog-gated. Land this in the same shared boot-content-composition path `composeSystemPrompt`/`resolveBootPrompt` (`internal/runtime/agent/prompt.go`) already uses, so it's structurally impossible for a new agent to be missing the instruction by omission.
3. Explicitly confirm this task does **not** touch Nanite's own boot-dir `CLAUDE.md` planting/regeneration mechanism (item 1 above) — that's already correct and working; a worker should verify by reading `prompt.go`/`bootdir.go` in full before making any change, to avoid conflating the two.
4. Verify in a real session: trigger compaction on a real CLI-based agent session, confirm the agent's next turn's boot content includes the project-re-read instruction unconditionally (not dependent on any per-agent config).

## Done means

- Every CLI-based agent's boot content includes the project-CLAUDE.md/AGENTS.md-re-read instruction post-compaction, unconditionally, code-driven — no opt-in flag exists or is needed.
- Nanite's own boot-dir `CLAUDE.md` planting/regeneration mechanism is confirmed unchanged (regression check).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` pass.
- Verified end to end in a real session with a real compaction event.

## Work log
<Worker fills this in as it goes: what was actually done, any deviation from plan and why, anything escalated.>

## Review notes
<Reviewer fills this in: pass/fail, what was checked, anything fixed and how.>
