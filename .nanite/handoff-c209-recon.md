# Handoff — c209 smoke recon + CLI-launch investigation

> Written 2026-05-16 pre-compaction. Reload this first after compaction, then
> re-read `git status` / `git log -1` to confirm the working tree still matches
> "In-flight state" below.

## Where we are

- Repo: `/Users/chrispian/dev/hollis-labs/apps/nanite`, branch `main` @ `abfb182`
  (Merge PR #186). All of CW-20260514-0051 → CW-20260516-0007 are merged.
- Backend deployed: `cerberus_resource_deploy nanite-api-service` + `reload`
  (pid was 15554 at deploy; check `cerberus_resource_status` for current).
- The whole arc: the Claude CLI / bootprofile launch path was broken across
  c195–c208; the fix stack (CLI routing → provider threading → workdir create
  → StreamingStdio adapter → Caps.StreamingStdio → NDJSON framing) landed and
  **c209 is the first fully working CLI session.**

## ⚠️ Uncommitted work — do NOT lose this

`ui/src/hooks/useKeyboardShortcuts.ts` is **modified in the working tree, not
committed** (~+68/-9). It is the FE double-shift-search misfire fix:
- Root cause: double-shift detector counted any two Shift `keydown`s in 400ms,
  so typing two capitals (Shift+letter ×2) misfired the search overlay.
- Fix: track Shift on `keyup`; only count a "clean" tap (Shift pressed+released
  with no other key between); any non-Shift key resets the sequence; window
  tightened 400→300ms. `npm run build` passed. No pre-existing task existed.
- **Pending decision:** the user was asked whether to commit + PR this. If
  after compaction the file is still dirty, that decision is still open —
  ask, or commit it as its own small PR.
- (`internal/api/data/` untracked is unrelated runtime detritus — ignore.)

## c209 review — the milestone

Session `6ffcbfb8-16ab-4100-a6fd-2cd039a0997e`, provider `bootprofile:claude-smoke`,
opus 4.7 via streaming-stdio, agent_runtime state=running pid=15922. 2 full
turns + a pending 3rd. Context healthy: 52K peak, **no compaction**, 233K
cache-read on the big turn, ~$0.19 total. `execution_metrics.tool_calls=0`
for CLI rows is EXPECTED (claude runs tools in-process; nanite's loop exits at
iter 0 for CLI sessions). The `adapter "pty"` label in metrics is cosmetic
stale naming.

## Recon findings (the 5 items the user wants to act on)

**1. Todo/plan "todo service not available" — ROOT CAUSE, not a regression.**
`internal/mcpserver/server.go:40` builds `condmcp.NewSelfToolsTransport(s)` but
never sets `.TodoStore`. The API server does (`cmd/nanite/main.go:363` —
`selfTools.TodoStore = s`). CLI launches reach MCP tools via the `nanite mcp`
stdio subprocess → `mcpserver` → `TodoStore` nil → every `todo_*`/`plan_*`
errors. "Worked before" = the GUI/API path (in-process, wired). **Fix: one
line in `mcpserver.New` — `srv.self.TodoStore = s`** (`*store.Store` already
satisfies `condmcp.TodoStoreInterface`). Also audit whether other self-tool
deps are similarly unwired in the stdio path.

**2. `--resume` — recovery-only, NOT per-turn.** Streaming-stdio claude spawns
once and stays long-lived; turns flow as NDJSON `SendInput` frames. `--resume
<id>` is added to BuildArgs only when `cliSessionID != ""`, which happens on a
crash-recovery re-spawn. Per-turn continuity = the live process.

**3. turn-1 tool calls invisible / `G-TYPED-EVENTS-ADAPTER-PATH` — agent's
diagnosis is STALE.** That ID is a gap-doc entry (`docs/architecture/chat-system/
gaps.md`), NOT a Torque task. The doc describes go-agent-sessions **v0.5.0**;
we're on **v0.9.4**, whose `streaming_stdio_session.go:367-379` fires BOTH
`EventFanout` and `TypedEventCallback`. Proof the doc is stale: the user SAW
turn-2's tool calls — impossible if typed events never fired. So typed events
DO flow on streaming-stdio. The turn-1-only invisibility is a narrower, real
issue — likely a first-turn race between claude's first emissions and the
GUI's SSE subscription / per-turn router bind (`driveBootSession` does
`SendInput` then binds the per-turn router). `gaps.md` should be refreshed.

**4. MCP access — the CLI launch is isolated.** The boot dir's `.mcp.json`
wires ONLY the `nanite` self-server (`nanite mcp` stdio subprocess). mux /
Tesseract / Torque are NOT registered for the CLI agent. The user's
expectation ("torque through mux mcp") holds for the GUI (main process has
those MCP servers) but not the CLI launch — each launch gets a fresh isolated
boot dir. The CLI agent reaches Torque/Tesseract only via whatever nanite's
own self-tools re-export (`clockwork_*` / `context_*`). OPEN DESIGN QUESTION
the user raised: should boot dirs get mux/Tesseract/Torque injected, or is
nanite-only-proxy the intended isolation model?

**5. `panel_open` FE/BE disconnect.** Backend acked `{"opened":true}` but the
GUI didn't render. Same SSE-seam class as #3 (CLI-agent action → SSE event →
GUI render). The user is remote-piloting the CLI agent from the GUI; that
cross-harness path is the "not 100% yet" gap. Needs an FE look at the
`panel_signal` handler.

## Alignment note

The c209 agent's audit was strong and well-caveated, but it presented a stale
doc (`gaps.md`, v0.5.0 era) as current state without checking the installed
dependency version. Its #1 recommendation ("bump go-agent-sessions") was
already shipped. Watch for stale-doc-as-current-state.

## Suggested next steps (user has not yet picked)

1. Wire `TodoStore` in `mcpserver.New` — one-line, unblocks `todo_*`/`plan_*`
   for every CLI launch. Quick win.
2. Turn-1 SSE race — why turn-1 tool calls / `panel_open` don't reach the GUI.
3. Refresh `docs/architecture/chat-system/gaps.md` — `G-TYPED-EVENTS-ADAPTER-PATH`
   is stale post-v0.9.4.
4. Decide the CLI-launch MCP injection model (finding #4).
5. Commit/PR the FE double-shift fix (uncommitted — see warning above).

## Other open threads

- CW-20260516-0036 (Torque/Nanite project): backlog item to rebrand
  vanta-conduit → tesseract across ~15 embedded `internal/assets/framework/`
  files. Has 3 unresolved questions (MCP server naming, stale `~/Projects-apps`
  paths, `conduit`/`vanta-conduit` allowlist dup). Not started.
- 4 stale `tether/nanite/general/*` git worktrees under `~/tether/workspaces/`
  — flagged for the user, not yet pruned (their call).
- CLAUDE.md `blg` skill doc names `mcp__clockwork__*`; live backend is Torque
  (`torque_*`) — minor skill-doc drift.

## In-flight state (verify after compaction)

- Branch: `main` @ `abfb182`. No feature branch active.
- Working tree: `ui/src/hooks/useKeyboardShortcuts.ts` modified (the FE fix),
  `internal/api/data/` untracked (ignore).
- No PR open. PRs #185 and #186 both merged.
