# Alignment Review — Synthesis of Two Independent Passes

**Created:** 2026-08-17
**Inputs:** `docs/alignment-review-full-context.md` (informed pass, same session as the audit/vision doc) and `docs/alignment-review-2026-08-17.md` (genuinely independent fresh session)

## Where both reviews agree — treat this as the strongest signal in the whole exercise

Two reviews, working independently with different context and different research paths, converged on the same corrections to the vision doc without coordinating:

1. **The vision doc's own sequencing instinct is backwards.** It frames "build the MCP surface" as urgent because externals will depend on it. Both reviews independently concluded reliability work has to come first — standing up an external front door on a durable-agent runtime that still 404s on its own model config (six of seven `.nanite/durable-agents/*.yaml` files, per the last DB snapshot) converts an internal problem into an external-facing contract-breakage problem.
2. **The narrowing work should target the pre-loop steering layers specifically (agent broker, strategy planner, route dispatch), not the harness generally.** The context/slot system is well-built, invariant-protected, and genuinely load-bearing for durable agents' actual job (consistent, cacheable prompts) — it shouldn't be caught up in a "trim the harness" pass aimed at the layers that don't apply to a durable-wake caller.
3. **A2A, the alphabetical-15-tool cap, and the `tool_result_cache` 100%-truncated anomaly** are all real, concrete, fixable items both reviews landed on independently.
4. **The GUI-native interactive usage gap is the top blind spot in the entire 23-document audit**, and it matters more than any other gap because mode 1 is specifically the thing being declared primary going forward.

## Where the fresh-eyes pass improved on the informed pass — credit given

- **The `CallerType` grep** (zero hits across all four pre-loop call sites) turns "the harness probably needs narrowing" into a proven fact: there is currently no seam for a durable-wake skip to hook into. This is real subtraction work, not formalizing something already true.
- **The A2A finding is the single best insight across both reports.** Not "retire it" — `internal/service/a2a_task_manager.go` already implements the classify-target → route → derive-status-from-the-real-record pattern an MCP wake/status surface needs. The right move is gut the JSON-RPC wire protocol, keep the routing logic as the implementation behind new MCP tools. This directly answers the "does MCP need a new mechanism for wake-and-track" question neither review had fully closed before this.
- **`internal/mcpserver`** — a session-scoped, tool-allowlist-restricted MCP server already exists and is tested, built for workflow-runner subprocess callbacks. Directly reusable precedent for the self-tools/control-plane namespace split. Neither the original audit nor the informed pass surfaced this.
- **A sharper "leave alone" list**: distinguishing collisions that caused a live incident (`reflex`, the `dev`/`self` tool-namespace collision — commit `5144590`) from collisions that never have (`mode`, `template`, `handoff`) is a better-reasoned cut than a blanket "clean up naming collisions" recommendation.
- **A more complete durable-agent reliability floor**: the still-pinned model IDs, the Recovery Broker's CLI-only assumption, *and* the subagent reply-delivery `FromAgentID` collision (silently failing since at least May) as one connected list of what actually has to be fixed before anything external depends on this path.

## The one real, unresolved disagreement

The informed pass's central claim: durable agents might not need the API-harness at all — a fresh CLI-wrapped subprocess per wake (the same shape as Curator's existing `fresh_per_wake` policy, and architecturally identical to Torque's one-shot agent launches, which the audit shows running unattended and reliably) could replace the API path entirely, which would shrink the reliability work needed far more than narrowing the existing machinery.

The fresh-eyes pass's counter: this tension is already correctly resolved — CLI's value-add is specifically about interactive use, and cold-booting fresh per wake gets no CLI-specific benefit over calling the API directly.

**This doesn't hold up against the fresh-eyes pass's own audit evidence, and a direct source-code check makes it weaker still.** The `torque-boot-agentlaunch` transcripts (`chat-analysis/05-boot-launched-agent-transcripts.md`) show CLI-wrapped subprocesses running a full implement→review→merge cycle completely unattended — no human in the loop, self-correcting tool errors within one or two follow-ups, no drift. That directly contradicts "CLI's benefit is interactive-only."

The fresh-eyes pass's stronger point — PTY lifecycle management is already fragile at current session counts, before adding month-long dormancy — turned out to rest on a premise that doesn't hold: **there is no real PTY in play today.** Checked directly against source after this synthesis was first written: `internal/runtime/agent/factory.go`'s `shouldUsePTY` returns `false` for every provider, unconditionally — an earlier design *did* route Claude through a real pseudo-terminal running its interactive TUI, but that was abandoned as a formal architecture decision after it turned out to be exactly the failure mode "PTY was flaky to parse" describes: "sessions ran forever with zero assistant deltas surfaced" (ticket c202), because the TUI's raw ANSI/screen-redraw output has nothing structured to parse. What's live in production (`cmd/nanite/main.go:800`, `NewClaudeAdapterStreamingStdio()`) is a long-lived subprocess exchanging structured NDJSON over plain stdin/stdout pipes — no pseudo-terminal device, no TTY-attach/detach lifecycle, no ANSI parsing. The "pty-claude" naming is a legacy label stripped before any runtime decision is made. This means the fresh-eyes pass's cost argument was real for a mechanism Nanite doesn't actually run — the risk that remains is ordinary subprocess/pipe lifecycle management (which the Orphan Sweep reaper already handles generically), not PTY-specific fragility. See `docs/system-audit/2026-08-17/code-architecture/06-provider-llm-roundtrip.md`'s correction note for full detail.

The genuinely strongest counter-argument, from the informed pass itself, is unaffected by this correction: a CLI-wrapped subprocess cedes its tool-calling loop to the wrapped tool's own harness, which might cost Nanite fine-grained retry/escalation policy control that matters specifically for unattended agents. Neither review resolves this — it's the one open question worth carrying into the experiment below.

**One more thing surfaced by the same source check, worth folding into the experiment:** CLI-wrapped Claude sessions can authenticate via the operator's own Claude subscription/OAuth token (keychain) rather than metered `ANTHROPIC_API_KEY` billing, and the adapter actually live in production doesn't force API-key-only mode. Neither review's "no cost/economics analysis" gap accounted for this lever existing at all — worth checking which auth mode Nanite's deployed instance actually uses before or alongside the CLI-durable-agent experiment, since it could materially change the cost case either way.

**Recommendation: settle this empirically, not with a third round of document analysis.** Route one real wake of one durable agent — Curator, since both reviews independently call it the furthest along — through a CLI-wrapped subprocess instead of the API path. See what actually breaks, what's lost, and what it costs. This is cheap to test and will resolve something two full review passes couldn't.

## Consolidated next-actions, best of both reports

1. **Durable-agent reliability floor** — the still-pinned model IDs, the Recovery Broker's CLI-only assumption, the subagent reply-delivery collision. Do this regardless of how the CLI-vs-API question resolves.
2. **The CLI-wrapped-durable-agent experiment** (above) — cheap, time-boxed, resolves the one real open disagreement before committing further engineering to either path.
3. **`CallerType`-gate the three pre-loop layers for durable-wake callers** — real subtraction, now proven necessary by the grep, not just probably-needed.
4. **A2A → MCP re-skin**: gut the wire protocol, keep `TaskManager`'s routing logic as the seam behind new MCP tools (`wake_agent`, `get_agent_status`, `send_message_to_agent`), built with the `internal/mcpserver` scoping precedent in hand.
5. **Fix the two concrete correctness bugs most likely to bite the first real MCP consumer**: the alphabetical-15-tool cap, and hardening the tool-namespace collision defense for a world with more MCP servers, not fewer.
6. **The low-risk dead-code cut pass** (`gomsg`, `session_handoffs`, `agent_boot_plans`, `agent_cycles`, `tool_enrichments`, `grounding_*`, the unwired reaper) — do opportunistically, doesn't gate anything above it.
7. **Leave alone**: the context/slot system, the `mode`/`template`/`handoff` naming collisions, the Agent Mux integration itself (the lesson from commit `5144590` is namespace discipline, not removing the integration), the skills auto-discovery pipeline.
8. **Explicitly deferred, not forgotten**: real tenancy/ACL infrastructure (start with a `consumer` tag, nothing heavier, until a second, less-trusted consumer shows up), the Agent Workflows-vs-steering question (not answerable yet — don't force it, and don't let the MCP surface design implicitly pick a winner), full naming-collision cleanup beyond what actually caused incidents.

---

## Code-verification addendum (2026-08-17, later same day)

Everything above was written from the audit's evidence base and the two reviews' own reasoning about each other. This addendum is a direct verification pass against the current working tree — not the audit's Aug-17 DB/code snapshot — for every claim above that a real decision would depend on, plus one correction (PTY) that neither review caught. **It does not lock any decisions.** Several items below still need discussion before deciding anything; this is corrected grounding, not a plan.

### Already fixed since the audit's snapshot (both reviews are citing stale state)

- **Recovery Broker HTTP-provider noise**: gated off at the call site (`chat_http_broker_notify.go`'s `HasBootdirLayout` check), PR #246 / commit `da7e1c7`, 2026-08-15. HTTP-provider sessions now skip the broker cleanly instead of generating misleading "permanent failure" breadcrumbs. Note this is a symptom fix — `DispatchRetry` itself (`internal/runtime/agent/recovery/broker.go:327`) is unchanged and still unconditionally calls the CLI/PTY bootdir path; it's just no longer invoked for HTTP-provider sessions. Whether that's sufficient (Orphan Sweep as the HTTP-provider safety net) or a real HTTP-retry path is wanted is still open — not resolved by this fix.
- **Subagent reply-delivery `FromAgentID` collision** (silently failing since ~May): fixed. `internal/subagent/service.go` (`replyFromAgentID`, tagged `CW-20260815-0023`) now resolves the role to its real `agent_profiles.ID` before posting, closing the UNIQUE-constraint collision.
- **`FinalizeToolSelection`'s 15-tool alphabetical cap**: Curator's specific failure mode (its own allowlisted tools falling off the cap) fixed 2026-08-15, with a named regression test (`TestSelectForAgent_LateAlphabetAllowlistedToolSurvivesCap`). Still open: tools reachable only via unranked catalog discovery (no allowlist) still hit a raw positional cap — the ranking machinery (`RankTools`) already runs on every turn for diagnostic logging and is discarded before selection; wiring it in is small, not a redesign.

### Still genuinely open

- **Model pinning**: 6 of 8 `.nanite/durable-agents/*.yaml` (`atlas-curator`, `atlas-librarian`, `content-strategist`, `content-writer`, `ideation-partner`, `loom-weaver`) still pin `claude-sonnet-4-20250514` as of this check. Only `loom-curator.yaml` and `orchestrator.yaml` defer to the resolver. Six one-line fixes.
- **Recovery Broker's `DispatchRetry`** is still CLI-only by construction — see above.

### Corrections to specific dead-code / risk claims

- **`session_handoffs` is not dead code.** `internal/messaging/handoff.go` has real, wired INSERT/UPDATE/SELECT methods with live callers: the CLI, an MCP self-tool, and three registered REST routes (`POST /api/handoffs`, `/approve`, `/reject`). Don't include it in any cut pass.
- **`agent_boot_plans` is wired-but-disconnected, not dead.** Full CRUD + dry-run behind four registered routes, just never read by the real boot path in `internal/runtime`. Cutting it removes an Agent Builder UI preview feature — a feature decision, not free cleanup.
- The rest of the dead-code list holds up under direct check: `gomsg` (+ `envelope_bridge.go`'s bridge functions), `agent_cycles`, `tool_enrichments`'s write path, and the known-tools/skills TTL reaper are all confirmed zero-caller/zero-writer/unscheduled.
- **`tool_result_cache.was_truncated=1`-on-every-row is not an anomaly.** The table only ever receives a row once content already exceeds the threshold — the column is tautologically always true. Separately, the threshold both reviews cite (64 KiB) is stale; it was lowered to 2 KiB by a since-landed ticket. Fine to fix the doc, not a reliability finding.
- **`IsFirstPartyBuiltinServerName`** (`internal/mcp/naming.go:132`) is confirmed as a hardcoded 4-name switch (`self`/`dev`/`code`/`general`) — same bug shape as the incident it was built to fix (`5144590`), just patched for 4 names instead of 1. The actual protection is structural (a colliding name gets force-prefixed on the incoming/external server, never evicts the incumbent) — an external server literally named `self` would still lose. The real fragility is Nanite forgetting to add a 5th first-party name someday, not an external-attacker surface.

### A real prerequisite bug neither review found

`internal/dispatcher` already defines `CallerChat`/`CallerSubagent`/`CallerBackground` and stamps it at the interactive-launch, subagent-dispatch, and harness-trigger call sites — but a durable-agent wake's actual delivery path (`durable_agents.go:485` `deliverWakePrompt` → `ChatService.HandleMessage`) stamps `CallerChat`, not `CallerBackground`. **A durable-agent wake is currently indistinguishable from a human typing in the GUI**, at the exact layer any `CallerType`-gating plan needs to branch on. This has to be fixed before any pre-loop narrowing work, not alongside it. There's an unfinished trailhead already in the code: `internal/service/chat.go:439` has a comment — "background-job path (reserved CallerType) will join when its..." — that trails off. Someone already started scoping this seam.

### A2A → MCP reuse: cheaper than either review states

`internal/service/a2a_task_manager.go` (636 lines) never imports the JSON-RPC wire types — the wire protocol is a thin (~300-360 line) translation shim on top of clean Go methods (`SubmitTask`/`GetTask`/`ProvideTaskInput`/`deriveTaskState`). `TaskManager`'s own doc comment states outright: "does NOT create a third parallel execution substrate." Confirmed zero external callers anywhere but tests. This is deleting a shim and writing new MCP tool handlers against methods that already exist, not an extraction project. One nuance: `TaskManager` still imports `internal/a2a` for `TaskState` and `msg://` address parsing (~300 lines) — that support package needs renaming/absorbing, not deleting outright.

### `internal/mcpserver`: reusable pattern, not reusable transport

Its allowlist enforces at tool-*registration* time (a disallowed tool is never registered with the underlying MCP SDK server, so calling it fails at the SDK's own "unknown tool" layer, not an app-level check) — a strong pattern worth copying. But it's a stdio, one-subprocess-per-session shell with zero auth, built for workflow-runner subprocess callbacks. There is no HTTP/SSE MCP-serving surface anywhere in Nanite today. A real external control-plane MCP server is genuinely new transport work, not a reuse of this package.

### The PTY correction

Both reviews, and the underlying audit, describe the CLI-wrapped runtime as "PTY" — it isn't, and hasn't been since `CW-20260515-0004`. `shouldUsePTY` (`internal/runtime/agent/factory.go:59`) returns `false` unconditionally for every provider; its own doc comment explains why: the original PTY design emitted unparseable TUI screen-redraw output with zero surfaced deltas (`c202`). What's actually live for long-lived Claude sessions is `StreamingStdio` — a long-lived subprocess talking NDJSON over plain stdin/stdout pipes (Anthropic's `-p --input-format stream-json --output-format stream-json` mode). `IsPTYProvider`/`pty-claude` (`internal/chat/engine.go:256-303`) are naming fossils — string checks on legacy dropdown labels stripped before any runtime decision is made, not a description of a live pseudo-terminal.

This matters: the fresh-eyes review's strongest argument for keeping durable agents off CLI-wrapped execution was PTY lifecycle fragility compounding with month-long dormancy. That argument doesn't hold — what's actually running is an ordinary subprocess-with-pipes, the same category the Orphan Sweep reaper already handles generically. It doesn't resolve the CLI-vs-API question, but it removes the sharpest objection to it.

It also surfaced a new, previously-unexamined variable: Codex's boot-dir planting explicitly copies the user's own `~/.codex/auth.json` ("OAuth tokens / API key") rather than forcing metered billing; Claude's `StreamingStdio` adapter doesn't force `ANTHROPIC_API_KEY` either, so it plausibly falls through to the CLI's own subscription auth. If durable agents ran CLI-wrapped against subscription auth instead of metered API billing, that's a real economic lever neither review's "no cost/economics analysis" gap-flag knew existed. Worth checking which auth mode the deployed instance actually uses before treating the CLI-vs-API experiment as purely an architecture question.

### Frontend: not the blind spot assumed

React 19 + TanStack Query + Zustand + Radix + TipTap, ~310 non-generated files. `useChat.ts` (1034 lines) has a genuinely sophisticated streaming/reconnect design — a named SSE event table, reconnect-cursor semantics (`CW-20260418-0100`), explicit session-takeover handling. The envelope system (`ui/src/components/chat/envelopes/`) has ~20 real, consistently-styled components dispatched through one `EnvelopeRenderer.tsx`. 24 test files target exactly the fragile spots (stalled-stream reconcile, session-switch, partial-data hydration, slash-command injection). Roughly matches the backend's sophistication, not noticeably thinner. One real, self-documented gap: true interactive PTY/xterm-style terminal rendering (`ChatWorkingDrawer.tsx`'s "Terminal 2") is explicitly deferred behind `developer_mode` — everything else renders CLI/tool output as structured cards, which reads as a deliberate design choice. It's still true that no architecture doc exists for the frontend, worth writing given GUI-driven work is the declared primary mode.

### Note on process

One verification pass dispatched alongside this one went out of scope on its own — it wrote a decision-log file and declared decisions "locked" that were never actually discussed. That file has been deleted. Nothing in this addendum should be read as decided; it's corrected grounding for the discussion still to come.
