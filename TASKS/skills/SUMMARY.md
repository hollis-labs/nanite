# Summary — Skills batch (`TASKS/skills/`)

For the operator. Short version: **the Skills batch is complete — all 12 tasks implemented,
validated, and reviewed.** This is a full, clean-slate rebuild of the skills mechanism per
`docs/engineering/architecture/20-skills.md`, and it is the first time in this project's history
that a skill's actual body content — not just its name in a catalog list — can reach a model
through any live mechanism, in any runtime.

## What can happen now that couldn't before

- **Skills are real authored packages, not ad-hoc DB rows.** A skill is now a real `SKILL.md` +
  `scripts/`/`references/`/`assets/` directory, installed explicitly (never auto-discovered, never
  swept), content-hashed, and vendored into an immutable, addressable store. The old mechanism (8
  embedded builtins, auto-discovery from MCP tool visibility, and two agent-facing self-tools that
  let an agent free-form-author a fake "skill" via flat JSON fields) is deleted outright — it was
  never observed in use and was structurally incompatible with real packages.
- **Composition actually works.** A skill can declare a dependency on another skill and choose
  `inline` (its content gets spliced directly into the parent's materialized output) or `fork`
  (the nested skill is delegated to a real subagent turn and only its result folds back). Installing
  a package that would create a dependency cycle, or nest more than 5 levels deep, is rejected at
  install time.
- **Parameters are real.** A skill can declare parameters bound to either a caller-supplied static
  value or an existing per-agent dynamic context resolver, resolved at the moment the skill is used.
- **Content reaches the model through two real delivery paths.** CLI-hosted agents (Claude Code,
  OpenCode) get a granted skill's actual files planted into their own native skill directory at
  boot and kept in sync every turn, with no restart required — the agent uses its own built-in skill
  mechanism unmodified. API-direct agents get a new `skill_get` tool that runs the full pipeline and
  returns materialized content directly.
- **All of it is gated by a real sandbox and capability check**, not an "it would have refused"
  claim. Every script or inline-command execution a skill triggers routes through one policy/sandbox
  gate that checks a per-agent, per-skill grant, refuses if the skill's content has changed since
  that grant was approved, and runs the actual command inside a `go-sandbox` profile scoped to
  exactly what was granted — nothing more.

## Security findings — both real, both fixed, both independently re-verified

- **Secret leak (task `09`).** The sandbox gate never filtered the inherited process environment —
  a skill with no elevated capabilities at all could run an ordinary `` !`env` `` command and get the
  full host environment (including real secrets) back in model-visible output. Fixed with an
  unconditional environment-filtering floor no capability grant can override. The fix was verified
  by deliberately disabling it and confirming a live secret token actually leaked before restoring
  the fix — not just re-running the test suite.
- **Path traversal (task `10`).** A skill's slug had no format validation anywhere in the codebase.
  A malicious or malformed slug (e.g. `"../.."`) could cancel out the boot-directory destination path
  entirely, silently overwriting an agent's own system-prompt file with a vendored skill file — zero
  error, zero log. Fixed with an explicit check that a skill's planned destination can never escape
  its own subtree. The fix was verified by fuzzing roughly 25 adversarial slug variants against it,
  not just the two cases that were originally found.

Both fixes shipped in the same review round they were found, and both were re-reviewed clean by a
second, independent reviewer before the task was closed.

## Everything the batch found and fixed, in one place

Nine real bugs were found by independent review across the batch's 12 tasks and fixed before
closure — two are the security findings above; the other seven are functional-correctness bugs
(a grant/removal collision between two admin write paths, a permanent-ID bug that made installed
skills un-updatable via the REST API, a zero-test-coverage gap on new security-relevant logic, a
doc/code mismatch in an error type's dedup guarantee, an unreachable-in-production composition mode,
a marker-execution regex gap that let documentation text execute as a real command, and two minor
accuracy findings in the final task). Full detail, including how each was found and independently
re-verified, is in `TASKS/skills/HANDOFF.md` and `TASKS/ESCALATIONS.md`'s 2026-08-21/22 entries.

## Still flagged, needing attention

- **A small number of genuine follow-ups were filed, not fixed, as explicitly out of this batch's
  scope**: a production-inert typed-nil hazard in one delivery path's subagent wiring; the new
  "preview" endpoint can't materialize a `fork`-composed skill (no live session to delegate from —
  a real design question for later, not a bug); and a pre-existing, unrelated flaky race in an
  unrelated file, surfaced incidentally during this batch's testing and filed for whoever next owns
  that file. None of these block using what shipped.
- **The admin Skills Browser frontend page will crash** against the new API response shape — it
  reads four fields (`settings`, `tool_bindings`, `is_builtin`, `prompt`) that no longer exist on a
  skill row. This is a known, deliberately out-of-scope consequence (frontend work was explicitly
  fenced out of this entire batch) — it needs a small frontend fix before that specific admin page
  is used again. The two frontend surfaces this batch actually protects (the Agent Builder Wizard
  and the Agent Capabilities Panel) were verified working throughout.
- **`TASKS/INDEX.md`'s tracked-file status needs a look before its next edit.** The version already
  committed on `main` correctly shows this batch as fully complete. The copy currently sitting
  uncommitted in the working tree has reverted that section back to a stale, pre-completion state
  (showing several tasks as not started). This looks like accidental collateral from unrelated,
  concurrent work touching the same file, not a real project-status question — but it should be
  reconciled before anyone commits further changes to that file, so stale status doesn't get
  re-introduced into tracked history.

## Batch status

All 12 tasks: **implemented, validated, reviewed.** No open, unresolved blockers. Operator sign-off
on the underlying design was already recorded in-chat and captured in
`docs/engineering/architecture/20-skills.md`'s own `## Status` section (2026-08-21).
