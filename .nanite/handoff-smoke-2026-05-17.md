# Nanite smoke-test session handoff — 2026-05-17

> Fallback file — Tesseract was unreachable (`transport closed`) at handoff time.
> Post to Tesseract once it's back; see "Tesseract" section at the bottom.

## Next session focus
Continue real-world smoke-testing the Nanite chat harness from the GUI; debug issues as they surface. Exercises CLI-launch chat agents (the `claude-smoke` boot profile) and subagent dispatch.

## Deploy state — IMPORTANT
- DEPLOYED & live: PRs #187-#194.
- MERGED to main, NOT deployed: PRs #195-#198 (harness sprint).
- BLOCKED: `git pull`/deploy is blocked — the envelope hardening sprint's work is UNCOMMITTED in the main checkout (`internal/service/envelope_lane.go`, `ui/src/components/chat/envelopes/ChatLoopBudgetSoftWarningCard.tsx`, ChatTranscript/useChat/useChatStore changes, ~22 modified files). Do NOT pull/stash/deploy until that sprint commits — it would clobber in-progress work. Once it commits/PRs: pull main, deploy everything together.
- Deploy recipe: `cd ui && npm run build` -> `rm -rf internal/server/ui_dist && /bin/cp -R ui/dist internal/server/ui_dist` (the Makefile gap CW-20260516-0046 — without this the SPA ships stale) -> `cerberus_resource_deploy nanite-api-service` -> `cerberus_resource_reload nanite-api-service` -> verify status + stderr logs. If Cerberus MCP calls time out: `cerberus daemon restart`.

## How to debug a chat session (start here)
- DB: `/Users/chrispian/dev/hollis-labs/apps/nanite/nanite.db` (SQLite — the running service uses this file).
- Find a session: `sqlite3 nanite.db "SELECT id,short_code,provider,status,created_at FROM sessions WHERE short_code='cNNN';"`
- Tables: `messages` (role/content/envelope/metadata; content is `{"v":1,"text":...}` JSON), `session_events` (pty_turn_start/pty_turn_complete), `execution_metrics` (tokens, finish_reason, error), `subagent_runs` (parent_session_id/child_session_id/role/mode/status/error/timeout_seconds/completed_at), `agent_runtime` (state/pid/workdir/provider_session_id).
- "Turn completed cleanly" = matching pty_turn_start/complete + `finish_reason=end_turn` in execution_metrics.
- Boot dirs: `/var/folders/.../T/nanite-boot-claude-<sessionUUID>-r0-*/` — read `.mcp.json` there to see which MCP servers a CLI agent got; `agent_runtime.workdir` points at the active one.
- Service logs: `cerberus_resource_logs nanite-api-service --stream stderr`. Cerberus resource id is `nanite-api-service` (NOT `nanite-api`).

## Known sharp edges
1. SSE seam (recurring theme): boot-session backend SSE events (stream_end, panel_signal, standalone envelope cards) don't render in the GUI at emit time — they flush on the NEXT turn. Symptoms: transient "generation interrupted" (turn DID complete — refresh clears it; check `end_turn` + `{}` metadata = no real error); panel_open acks but GUI doesn't render; report-card lands detached at transcript bottom. Tasks: CW-20260516-0044/0048/0074; envelope hardening umbrella CW-20260516-0075.
2. `[generation interrupted]` has TWO causes: (a) persisted backend placeholder (`chat_generate.go` persistPartialAssistant) for an empty early-return turn — common in subagent chats, survives refresh; (b) transient FE stuck-streaming from a missed stream_end — clears on refresh. If the DB message has real content + `{}` metadata, it's (b).
3. Cold-reboot amnesia: after a full service restart, a session's next message cold-boots a fresh agent (ModeLongLived, NO `--resume`) — it loses prior conversation context (Nanite does not replay the transcript). Provider/model IS preserved. Crash-recovery (ModeResume) does `--resume` and keeps continuity; a full restart does not. CW-20260516-0073 (design) / CW-20260517-0037 (impl).
4. 300s hang: a silently stalled provider stream consumes the full 300s run timeout (no inactivity timeout in the stream loop / drainCapture). Fix task CW-20260517-0036.
5. codex-via-Torque dispatch broken: codex gets a bare `"Boot @./boot.md"` pointer it does not reliably resolve -> burns the turn orienting -> premature task_complete. CW-20260517-0048.
6. CLI-launch MCP surface: a CLI-launch chat agent's boot `.mcp.json` wires only the `nanite` self-server. Post-#189, self-tool calls proxy to the live harness via `POST /api/tools/call` when `NANITE_API_URL` is planted. clockwork/torque/tesseract are NOT directly on a CLI agent's surface.
7. Subagent fork-bomb is FIXED — recursion cap (CW-20260516-0066) + worker execute-by-default (CW-20260516-0069) deployed. A subagent cannot spawn a subagent.

## Open task clusters
- Envelope/SSE: CW-20260516-0044, 0048, 0074, 0075 (umbrella), CW-20260517-0008.
- Harness/subagent: CW-20260517-0036 (300s fix), 0037 (cold-reboot impl).
- Torque execution environment: CW-20260517-0038 (priority A), CW-20260517-0048 (codex kickoff) — DX umbrella CW-20260517-0011.
- Build/framework: CW-20260516-0045 (role source/install drift), 0046 (ui_dist embed gap), 0036 (vanta-conduit rebrand).
- CW-20260517-0001 (go-providers launch permissions) — being done by a side agent.

## Gotchas
- Verify-before-trusting: CLAUDE.md drifts from code (e.g. it says the envelope manifest is `config/envelopes.yaml`; it is actually the `go-envelopes` lib). Confirm claims against the code.
- The 4 `tether/workspaces/nanite/*` git worktrees are stale — leave them.

## Tesseract (was unreachable at handoff time)
Post this handoff once Tesseract is back. Intended namespace `user/chrispian/memory/smoke/nanite`, key `nanite_smoke_handoff_20260517`. Tesseract was flapping (`mux_call` -> `transport error: transport closed` on every call; restart the tesseract MCP server).
