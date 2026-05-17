# CW-20260516-0073 — Cold-reboot context recovery for chat sessions

**Status:** Design. Design-first task — this doc is the deliverable. No code
landed; the implementation plan in §5 is scoped for a follow-up task.

**Designed:** 2026-05-17, against `main` @ `f0cd92b`.

---

## 1. Problem (verified against current code)

After a full restart — `nanite-api-service` restarted, or the agent
processes killed — a chat session's next message cold-boots a **fresh,
amnesiac** agent. Verified:

- `driveBootSession` (`internal/service/chat_boot_drive.go:84`) — when
  `activeSessions` has no entry for the session, it boots with
  `Mode: runtimeagent.ModeLongLived` and **no** `ResumeFromCheckpoint`
  (`chat_boot_drive.go:91-97`).
- `ModeLongLived` does not thread a provider session-id; only `ModeResume`
  loads a checkpoint and `--resume`s (`internal/runtime/agent/agent.go:404`,
  `agent.go:36-39`).
- `composeUserPayload` (`chat_boot_drive.go:175`) sends only the
  `SlotUserContext` slot content + the new user message. There is **no
  transcript replay** — the agent never sees the prior conversation.

So the new agent receives CLAUDE.md / boot prompt / slots but not the
conversation. The user still sees the full DB transcript (`messages` table,
`Store.ListMessages` — `internal/store/sessions.go:404`), but the agent is
effectively amnesiac. Crash recovery via the recovery broker uses
`ModeResume` and preserves continuity; a **full restart does not**. This is
a real reliability gap.

## 2. Detection — "history exists, no live session"

The detection point is `driveBootSession`'s cold-boot branch
(`chat_boot_drive.go:84`, `if sess == nil`). That branch already fires for
exactly two cases:

1. A genuinely new session's first turn.
2. A post-restart cold boot of a session that has prior history.

The discriminator is the persisted transcript. At the cold-boot branch the
current user message has already been persisted, so:

```
priorMessages := Store.ListMessages(sessionID, limit)
coldRebootWithHistory := count(priorMessages excluding the just-created user msg) > 0
```

A simpler, robust signal: **any assistant message exists** for the session.
A brand-new session has none; a session that has had ≥1 completed turn does.
Recommended condition:

> At the `sess == nil` branch, the session needs a recovery scan iff the
> `messages` table holds at least one `role = "assistant"` row for the
> session id.

This is a single indexed query and has no false positives on a new session.
A small `Store.HasPriorAssistantTurn(sessionID) (bool, error)` helper (or
reusing `ListMessages` with a tiny limit) is all that is needed.

## 3. Proposal A — recovery-scan directive (PRIMARY)

### Principle

Do **not** dump the transcript into the prompt. When the cold-reboot
condition is detected, inject a **recovery-scan directive**: a bounded
instruction block telling the agent *how to reconstruct context itself*. The
agent scans on demand. This mirrors the compaction-handoff pattern, which is
the proven continuity primitive in this codebase:

- `SlotHandoff` (`internal/context/slot.go:90`) — "handoff survives
  compaction; it IS the continuity primitive" (`slot.go:150`).
- `HandoffPayload` (`internal/context/handoff.go:22`) — a small,
  cap-enforced structured payload (≤ ~1500 tokens): session intent, recent
  decisions, active pointers, next-step anchor. Self-authored pre-compaction,
  auto-injected post-compaction.

The recovery-scan directive is the same shape of idea — bounded, injected at
a lifecycle boundary — but instead of *carrying* recovered state it carries
*instructions to recover state*. "Good enough" recovery by design: ground
the agent and point it at the right tools; it can always query for more.

### Directive content

The injected block instructs the agent to reconstruct context from sources
Nanite already persists:

| Source | How the agent reaches it | Backing store |
|---|---|---|
| Last N messages of the transcript | message-history read | `Store.ListMessages` (`sessions.go:404`) |
| Durable memory / decisions | Vanta / Tesseract `memory_recall`, `conduit_lookup` | external |
| Stashed handoff / scratch content | handoff-stash + scratchpad tools | `handoff_stashes`, `session_handoffs` (`internal/store/handoff_stashes.go`) |
| Prior compaction summary | session row | `sessions.compaction_summary` (`sessions.go:636`) |
| Compaction history | compaction-events read | `Store.ListCompactionEventsBySession` (`compaction_events.go:76`) |
| Recovery breadcrumbs | breadcrumb read | `Store.ListRecoveryBreadcrumbsForSession` (`recovery.go:86`) |
| Reasoning / chain-of-thought | thinking-block captures | persisted thinking blocks |
| Working state | `git status` / `git log` via shell | filesystem |

The directive is a short, fixed template (think ~30-40 lines, well under the
`SlotHandoff` ~1500-token cap), not a data dump. It names the tools and tells
the agent to scan the most relevant ones first (last messages + memory
recall), then proceed with the user's actual new message.

### Where to inject — two options

**Option A1 — one-shot turn payload (recommended for MVP).** Detection in
`driveBootSession` sets a per-session "needs recovery scan" flag (a
`sync.Map`, same shape as the CW-20260516-0057 `rebootingSessions` set).
`composeUserPayload` (`chat_boot_drive.go:151`/`:175`) consumes the flag on
the first post-reboot turn and prepends the directive block ahead of the
`SlotUserContext` content. Cleared after one turn.

- *Pros:* small, localized; no boot-dir plumbing; the directive is naturally
  one-shot — it should not persist in CLAUDE.md for the session's whole life.
- *Cons:* rides the first turn's payload rather than the boot content.

**Option A2 — boot content (`agent-context.md`).** Detection happens before
`runtimeagent.Boot`; a flag in `bootOpts` makes `bootdir_plant`
(`internal/runtime/agent/bootdir_plant.go:167`, `BuildAgentContext`) append
the directive to `.sandbox/agent-context.md` so the agent reads it at
startup.

- *Pros:* the directive is part of boot content, read before turn 1.
- *Cons:* more plumbing (a boot option threaded into `bootdir_plant`); the
  directive would linger in agent-context.md unless explicitly regenerated
  on the next slot change.

**Recommendation:** ship **A1** first. The recovery scan is inherently a
one-shot event ("you just cold-booted — go reconstruct"), which maps cleanly
onto a single turn payload and needs no boot-dir changes. Treat A2 as a
follow-up only if turn-payload injection proves insufficient (e.g. the agent
needs the directive *before* it sees any user content).

## 4. Proposal B — context cache (SECONDARY, feasibility only)

**Idea:** periodically flush a snapshot of agent context to disk so a
recovery boot reloads it directly (RAM-drive-flushed-to-disk analogy).

**Feasibility verdict for the CLI-agent model: low value, not recommended.**

- The CLI agent (`claude`) holds its working context *inside its own
  process*. Nanite cannot snapshot that process's internal context — it can
  only snapshot what it already persists or can derive.
- Everything a context cache would hold, Nanite **already persists**: the
  full transcript (`messages`), `compaction_summary`, `compaction_events`,
  `handoff_stashes` / `session_handoffs`, `nanite_recovery_breadcrumbs`,
  thinking-block captures. A separate cache would largely duplicate these.
- A genuine "self-authored snapshot" already exists in the compaction
  handoff (`HandoffPayload`, self-authored pre-compaction). Extending that to
  fire on a periodic timer (not just pre-compaction) is the *only* part of
  Proposal B with marginal value — and even then it competes with simply
  reading the existing breadcrumbs/summary.
- The real gap is not *capture* (Nanite captures plenty) — it is **replay /
  reconstruction on cold boot**. That is exactly what Proposal A addresses.

So Proposal B is a non-goal: invest in the recovery-scan directive (A); do
not build a parallel cache.

## 5. Recommended implementation plan (follow-up task)

Scoped, in dependency order:

1. **Detection helper.** `Store.HasPriorAssistantTurn(sessionID) (bool,
   error)` — one indexed `SELECT EXISTS` against `messages`. Unit-tested.
2. **Recovery-scan flag.** A `recoveryScanPending sync.Map` on
   `chatServiceImpl`, set in `driveBootSession`'s `sess == nil` branch when
   the helper returns true.
3. **Directive template.** A fixed template constant (the §3 table as
   prose), kept well under the `SlotHandoff` token cap.
4. **Injection.** `composeUserPayload` consumes-and-clears the flag and
   prepends the directive on the first post-reboot turn.
5. **Tests.** Detection true/false (new session vs session with history);
   directive injected exactly once then cleared; brand-new session never
   gets the directive.

Estimated surface: one small store method + ~3 touch points in
`chat_boot_drive.go` + a template constant + tests. No schema changes, no
boot-dir changes, no new slot.

## 6. Why this was not implemented in this task

This is a design-first task. The detection condition (§2) is clear, but the
**injection mechanism is a real A1-vs-A2 decision** with a token-budget and
boot-content tradeoff, and the directive template wording is load-bearing
(it is the entire UX of the feature). Both deserve review before code lands.
Per the task's scope guidance, the design doc is the deliverable; §5 hands a
fully-scoped, low-risk implementation plan to the follow-up task.
