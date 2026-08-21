# Filesystem Snapshots (Shadow Git)

## What this is, and what it isn't

An undo/audit primitive for the filesystem state an agent's granted paths hold — capture a project tree before/after each step, diff two captures, selectively restore specific paths. It is not universal process rollback ("undo everything this process changed anywhere" is a different, much harder system — filesystem virtualization or copy-on-write sandboxing, not this). It is not a backup system. It is not the same thing as restoring conversation/session state, and the two must never be conflated — see below.

## Naming: "snapshot," not "checkpoint"

`checkpoint` already means something specific and different in this codebase: `agentkit/agentruntime/checkpoint` defines provider-session **resume hints** (native resume vs. fresh-boot vs. unsupported), and Tether's "resume a logical agent from its most recent checkpoint" is the same concept. Zero overlap with filesystem state. This feature must not reuse that word — `GLOSSARY.md` needs a new entry for the filesystem sense, explicitly distinguished from `checkpoint` and from Glass-4's incidental use of the plain word "snapshot" in its own definition (`docs/engineering/GLOSSARY.md`: "the agent-self-authored continuity snapshot"). Three different things currently reachable by the word "snapshot" in casual conversation about this codebase; only one of them should own the term going forward.

## Two independent axes, never conflated

Agent/session timeline (turns, Glass-4, compaction, handoffs — [06-session-lifecycle-and-recovery.md](06-session-lifecycle-and-recovery.md)) and filesystem timeline (S0, S1, S2...) are genuinely separate. Confirmed clean today: Nanite's existing session-state machinery is entirely about conversation/context content and has zero filesystem awareness — there's nothing to conflate with yet, which is exactly the point at which to keep it that way rather than reach for convenience later.

The UI can still *correlate* the two axes without the underlying data model coupling them — "restore files touched after this point in the conversation" is a perfectly good feature. This is what OpenCode's own shipped UX does: restoration selects a conversation boundary, then restores only the paths attributed to steps after it. Correlation at the UI layer is fine; coupling at the data layer is not — a session restore must never implicitly mean a filesystem restore, or vice versa.

## Host boundary: mechanism vs. policy

Continuing [16-agent-host.md](16-agent-host.md)'s boundary and its policy/mechanism split (`policy.Engine` vs. `policy.Store`): the host owns capture, diff, preview, restore, target resolution, and shadow-store lifecycle/cleanup. The product/config layer owns *when* to capture and *how long* to retain. A generic interface, in the same driver/adapter shape already established repeatedly this session (`go-providers.CLIAdapter`, the ACP client abstraction, `policy.Engine`):

```go
type FilesystemSnapshotProvider interface {
    Capture(ctx context.Context, targets []Target) (SnapshotSet, error)
    Diff(ctx context.Context, from, to SnapshotSet) (Diff, error)
    Preview(ctx context.Context, set SnapshotSet, paths []string) (Preview, error)
    Restore(ctx context.Context, set SnapshotSet, paths []string) error
}
```

with `ShadowGit` as the first (only) concrete implementation. Naming the interface around intent (`snapshot_capture`/`snapshot_diff`/`snapshot_restore`), not mechanism (`git_shadow_commit`), leaves room for a non-git implementation later (APFS snapshots, overlayfs/btrfs/ZFS, copy-on-write workspaces) if some deployment environment wants a different physical mechanism. No need to build one now — the seam is cheap, and this codebase already treats that as reason enough to leave it (`00-overview.md`'s "abstract the intent, not the capability away").

## Reference implementation: OpenCode's shadow git

Verified directly against OpenCode's own current docs, not taken from secondhand paraphrase:

- A separate internal Git object database lives in OpenCode's own data directory. It never touches the real repo's `.git`/index, never creates real commits or branches there.
- Capture happens **per model-step** — immediately before the model call and after each cleanly-completed step. Finer-grained than "once per turn": a turn with several tool calls gets several capture pairs, which is what makes partial-turn undo possible.
- Exclusions: ignored files, files outside the active/session scope, changes to Git metadata itself, and individual untracked files over 2 MiB.
- Restore is selective and path-scoped, anchored to a conversation boundary — not a whole-tree checkout by default.
- Explicit disclaimers worth carrying into this design verbatim: **capture is best-effort and never blocks the actual work** (a failed capture doesn't stop a model step — consistent with this codebase's existing "hints, not control" principle); it is **not a concurrency lock** (external edits between capture and restore aren't protected against); and snapshots are **not "secret-free"** — they hold complete file contents, so anything sensitive that ever touched a captured file persists in the shadow store until cleanup. That last point is a real security/retention consideration this doc's source material didn't raise on its own.
- Real field bugs worth designing around from day one, found while verifying this section: a whole-tree restore touching every file's mtime, breaking editor reload state and build-tool caches (a concrete argument for selective-only restore, never whole-tree-by-default); a shadow directory that wasn't initialized before first capture, silently producing zero snapshot data (argues for failing loudly on a capture/init problem, never a silent no-op); and a report of the snapshot service corrupting the real repo's git index when invoked from a pre-commit hook (argues for being paranoid about `cwd`/working-directory isolation whenever shadow operations shell out to `git`).

## What's already in Nanite to build on

Not hypothetical — this maps onto live schema and code:

- `projects.repo_path` + `agent_projects` (the agent↔project scope join, [01-agent-construction.md](01-agent-construction.md)) is exactly a `SnapshotSet`-across-configured-roots: one logical checkpoint ("turn 42") is one shadow-git tree hash per project root the agent is scoped to, not one synthetic repo spanning unrelated roots.
- `buildSandboxProfile`'s `FS.Write` allowlist (`internal/runtime/agent/sandbox_profile.go`) is the source snapshot targets should derive from, filtered to exclude ephemeral entries (`bootDir`, `naniteHomeDir()`) that aren't project content.
- `internal/worktree.Manager` already gives worker sessions a real, disposable `git worktree add`-based checkout on its own branch — a second, independent safety mechanism that predates this feature.

## Settled design calls

- **Capture granularity: per model-step**, matching OpenCode's own field-tested choice — before the model call, after each cleanly-completed step. Enables undoing part of a turn, not just the whole thing.
- **Snapshot targets derive from the sandbox `FS.Write` allowlist**, not a separately configured list — avoids two lists (what an agent may write, what gets snapshotted) silently drifting apart over time.
- **Applies uniformly regardless of worktree isolation.** A worker session already inside an isolated worktree/branch still benefits from undoing one bad turn without discarding the whole worktree's other legitimate work — shadow-git snapshotting is not made redundant by worktree isolation; the two are independent safety mechanisms operating at different granularities (whole-branch-discard vs. per-path-per-turn undo).
- **Restore is selective/path-scoped by default, never whole-tree-unconditional** — directly informed by OpenCode's own mtime/editor-reload field bug above. A lightweight conflict check (compare the current file's hash against the snapshot's hash before restoring that path; warn rather than silently overwrite) is cheap given git's content addressing and belongs in v1 — this is a hash comparison, not "sophisticated merge behavior," so it doesn't conflict with deferring real conflict resolution below.
- **Capture is best-effort and must never block a turn/step from proceeding.**
- **Retention/cleanup**: the host exposes a TTL/cleanup mechanism per target or per `SnapshotSet`; the app/config supplies the actual retention duration — the same policy-vs-mechanism split already established for `policy.Store`.
- **Security**: the shadow store holds full file contents indefinitely until cleanup and needs the same access-control treatment as other host-owned artifacts (boot dir, sandbox) — documented plainly here rather than assumed away.

## What's genuinely still open

- Real conflict/concurrency handling beyond the lightweight hash-check above — explicitly deferred, not blocking v1, but named now because it becomes especially important once [Teams](15-teams.md)' multiple slots can touch the same project concurrently. A naive whole-tree restore by one agent could erase another's concurrent work; the lightweight hash-check above catches the single-path case but not a genuine merge scenario. Revisit once Teams sees real multi-agent usage on shared projects.
- The GUI surface for selective restore (a per-changed-file checklist, "changed by this turn: [x] internal/foo.go [x] internal/bar.go [ ] docs/design.md, restore selected") is a real, valuable UX but a frontend concern — deferred per [00-overview.md](00-overview.md)'s existing stance that the frontend doesn't have its own architecture doc yet.
- Whether/how this becomes portfolio-shared (Tether, Torque) vs. Nanite-first. Torque already does per-run worktrees (`internal/worktree/spec_test.go`, `perrun_worktree_test.go`) — real reuse potential — but this doc doesn't sequence that; the planner does, same pattern as [16-agent-host.md](16-agent-host.md).

## What's cut

Universal filesystem rollback — a fundamentally different, much harder system than this. The explicit, documented guarantee: snapshot recovery applies only to targets derived from the sandbox's write allowlist; anything outside those paths, and any non-file side effect (database writes, service calls, shell commands with effects beyond the filesystem), is not recoverable through this mechanism. If stronger guarantees are ever needed, that belongs at the sandbox/filesystem layer, not by stretching shadow-git into something it isn't.
