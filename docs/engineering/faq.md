# FAQ

Short answers to things that have genuinely come up and caused real confusion. If you hit something not here that took real digging to resolve, add it — that's the actual point of this file.

**Is the CLI-wrapped path really PTY?**
No. There is no real pseudo-terminal anywhere in the current runtime. Claude runs as a long-lived subprocess speaking NDJSON over plain stdin/stdout (`StreamingStdio`); Codex/OpenCode spawn a fresh subprocess per turn. "PTY" survives only as naming (a few function/provider-alias names) that's being scrubbed. See `GLOSSARY.md`.

**What's the difference between a Card and an Envelope?**
Envelope = the wire-protocol wrapper (transport shape only). Card = the rendered UI system built on top of it. See `architecture/08-cards.md`.

**What's the difference between Reflexes and `promptrouter`?**
There's only one system now. Reflexes (`internal/agent/reflexes`) is the real, DB-backed steering primitive. `promptrouter` was a separate phrase-match router — it used to be literally named `internal/reflex`, which is exactly why this question needed an FAQ entry — but it's retired: `TASKS/phase-4/03-migrate-promptrouter-to-reflexes.md` deleted the package in full and migrated its phrase catalog onto `dispatch_to_agent` reflex rows (`internal/agent/reflexes/seeds.go`). If you see "promptrouter" in an older doc or commit, mentally substitute "the reflex engine's `dispatch_to_agent` action kind."

**Why did `go build ./cmd/nanite/` not change what's running?**
Because it doesn't deploy anything — it's a compile check. The running service is a separate artifact managed by Cerberus. See `deployment.md`.

**Is `docs/architecture/ARCHITECTURE.md` (or anything else under the old `docs/architecture/`/`docs/decisions/` directories) still current?**
Treat it as possibly stale until the archival pass (`TASKS.md`, Phase 6) lands. `docs/engineering/` wins on conflict.

**Where do durable agents actually run — CLI or API?**
API today, for all observed instances. Whether that should change is the one genuinely open architecture question from the whole review — see `architecture/00-overview.md`.

**Is Torque orchestrating Nanite agents?**
No — this was a real, previously-held mental model that turned out to be false on direct inspection. Torque runs its own, entirely separate copy of the agent-boot machinery; it shares underlying libraries with Nanite, not a runtime relationship.

**Why does a table like `session_handoffs` show zero rows if it's real?**
Zero rows means unused in practice, not unimplemented. Several real, fully-wired features in this codebase have never been exercised yet — check for actual callers before assuming a low row count means dead code.
