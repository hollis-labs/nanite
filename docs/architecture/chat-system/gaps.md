# Chat System Gaps

> Consolidated index of known gaps in the chat system as of 2026-05-08 (post foundation-lib delivery). Each gap has a short-id used inline in the subsystem docs, a severity, a one-line description, and pointers to where it appears.
>
> **Lib version reference (2026-05-08):** `go-sandbox` v0.2.0 (shipped), `go-runner` v0.3.0 (awaiting merge+tag), `go-providers` v0.8.0 (awaiting merge+tag), `go-agent-sessions` **v0.5.0** (awaiting merge+tag — bumped from prior local v0.4.0). PTY supervision follow-up will land as `go-agent-sessions` v0.6.0.

## Severity legend

- **P0** — user-visible bug or correctness gap; planning priority
- **P1** — known limitation with a workaround; structural fix non-trivial
- **P2** — design intent partially landed; activation pending
- **P3** — observability / UX gap

## Index

| ID | Severity | Title | Subsystem(s) |
|---|---|---|---|
| [G-FE-SINGLETON](#g-fe-singleton) | P0 | FE `useChatStore` is a singleton — cross-session bleed | [09](09-session-and-slot-management.md), [13](13-notification-and-card-surface.md), [01](01-chat-stream-messaging.md) |
| [G-PTY-SUPERVISION](#g-pty-supervision) | **P0** | `go-agent-sessions` v0.5.0 ships ptySession without Supervisor/ResourceLimits — blocks production long-lived chat (ghost session accumulation) | [05](05-external-agent-execution.md), [04](04-chat-harness-and-loop-orchestration.md) |
| [G-PTY-NO-TOOL-EVENTS](#g-pty-no-tool-events) | P1 ✓ resolvable | CLI/PTY children don't emit per-tool SSE — closed at lib level by `go-providers` v0.8.0 typed events; pending nanite consumption | [05](05-external-agent-execution.md), [13](13-notification-and-card-surface.md), [12](12-inbox-and-internal-messaging.md) |
| [G-PTY-RESUME-DROP](#g-pty-resume-drop) | P1 ✓ resolvable | `--system-prompt` dropped on `--resume` turns; closed at lib level by `go-agent-sessions` v0.5.0 long-lived ptySession; pending nanite consumption | [05](05-external-agent-execution.md) |
| [G-SSE-UNSUBSCRIBED](#g-sse-unsubscribed) | P2 | Three backend SSE events emit but FE doesn't subscribe | [03](03-sse-envelope-and-interaction-protocol.md), [13](13-notification-and-card-surface.md) |
| [G-HOT-SWAP-DEAD](#g-hot-swap-dead) | P2 | `SlotFlags.LazyLoad` plumbing complete; no production caller activates it | [09](09-session-and-slot-management.md), [10](10-context-window-management.md) |
| [G-CACHE-RACE](#g-cache-race) | P1 | Provider singleton cache hints mutated under concurrency | [10](10-context-window-management.md), [07](07-provider-routing.md), [02](02-tool-invocation-and-authority.md) |
| [G-BG-PRIVILEGED](#g-bg-privileged) | P3 | `internal/background/pty.go` shell-task backend has no sandbox / permissions / file-dir enforcement (NOT a CLI-agent spawn primitive — separate from the agent-spawn lane) | [05](05-external-agent-execution.md), [06](06-permission-and-authority.md) |
| [G-MAC-MEMORY-LIMITS](#g-mac-memory-limits) | P3 | `ResourceLimits.MemoryMax` silently dropped on darwin (no systemd, bash `ulimit -v` doesn't expose RLIMIT_AS) | [05](05-external-agent-execution.md), [06](06-permission-and-authority.md) |
| [G-GO-SIGXCPU](#g-go-sigxcpu) | P3 | Go runtime swallows SIGXCPU; CPU time limits don't terminate Go binaries | [05](05-external-agent-execution.md) |
| [G-TYPED-EVENTS-ADAPTER-PATH](#g-typed-events-adapter-path) | P3 | `go-agent-sessions` v0.5.0 `TypedEventCallback` fires only on PTY runtime, not adapter (subprocess-per-turn) runtime | [05](05-external-agent-execution.md), [03](03-sse-envelope-and-interaction-protocol.md) |
| [G-EVENTRESOURCELIMITHIT-NOT-EMITTED](#g-eventresourcelimithit-not-emitted) | P3 | `go-runner` v0.3.0 declares `EventResourceLimitHit` but does not emit it (heuristic only); consumers correlate `ExitError.Signal` themselves | [04](04-chat-harness-and-loop-orchestration.md) |
| [G-MODE-CONFIRM-UX](#g-mode-confirm-ux) | P3 | Mode-suggestion plumbing wired; confirm-card UX staged but not in production | [08](08-classification-and-intent.md), [13](13-notification-and-card-surface.md) |
| [G-NO-AUTO-RECALL](#g-no-auto-recall) | P3 ✓ Closed (2026-05-08) | Auto-recall has been per-turn since `phase-3 S2b`; per-agent gating + observability landed on `feat/memory-auto-recall` | [11](11-memory-and-knowledge-integration.md) |
| [G-MEMORY-SLOT-EMPTY](#g-memory-slot-empty) | P3 ✓ Closed (2026-05-08) | `MemorySource` is registered with the broker and populates `SlotMemory` per turn; the original gap framing was stale | [11](11-memory-and-knowledge-integration.md), [09](09-session-and-slot-management.md) |
| [G-PROGRESSIVE-ALLOW-LIST](#g-progressive-allow-list) | P3 | `tools_allow_list` × progressive seed builtins interaction not fully traced | [02](02-tool-invocation-and-authority.md) |
| [G-RECOVERY-COVERAGE](#g-recovery-coverage) | P3 | Provider error → recoverable matrix is centralized in one fn; new providers need care | [04](04-chat-harness-and-loop-orchestration.md) |
| [G-HANDOFF-CLASSIFY](#g-handoff-classify) | P2 | Sessions without `intent` set never hit Glass-4 path | [09](09-session-and-slot-management.md) |
| [G-TILDE-NOTE-LOCATION](#g-tilde-note-location) | P3 | `tildeAcceptanceNote()` referenced in spec/decisions, not located as code symbol | [06](06-permission-and-authority.md) |
| [G-INBOX-NO-PUSH](#g-inbox-no-push) | P3 | `message_received` SSE in taxonomy; FE consumption path not verified | [12](12-inbox-and-internal-messaging.md), [03](03-sse-envelope-and-interaction-protocol.md) |
| [G-SUBAGENT-STATUS](#g-subagent-status) | P3 | `subagent_run_status_changed` declared; FE consumption for new spawn flows not verified | [03](03-sse-envelope-and-interaction-protocol.md) |

---

## G-FE-SINGLETON

**Severity:** P0
**Status:** Open

**Symptom:** When two chat sessions are open in the same browser, switching between them leaks streaming state. The typing indicator follows the active session, drawer banners (circuit-open, session-takeover, stream-stalled) appear in the wrong session, errors and warnings cross sessions, the active mode/model/effort dial is not session-bound.

**Root cause:** `ui/src/stores/useChatStore.ts:165-200` is a Zustand singleton. The following keys are global (one value across all sessions):

```
isStreaming, streamingContent, streamingNarration, streamingFinal,
streamingThinking, streamingSessionId, statusMessage, circuitOpen,
sessionTakeover, streamStalled, chatErrors, pendingApprovals,
toolWarnings, pendingModeSuggestion, chatToast,
activeMode, activeModel, activeEffort
```

The following are correctly session-keyed via `Map<sessionID, ...>`:

```
toolCallsBySession, pluginEnvelopesBySession, autoSwitchSessionOverrides
```

**Backend is correct.** Each session has its own `inFlightGen`, `generateResponse` goroutine, SSE stream, path-grants bucket. The bleed is entirely on the FE.

**Fix shape:** Refactor `useChatStore` to keep all per-session state in `Map<sessionID, ChatSessionState>` (or instantiate one store per session). UI components select against the active session's slice. Components that show across sessions (e.g. an inbox-style notification list) opt-in.

**Test:** Open sessions A and B; send message in A; switch to B mid-stream; assert B's transcript shows no streaming indicators / narration. Currently fails.

---

## G-PTY-NO-TOOL-EVENTS

**Severity:** P1
**Status:** ✓ Resolvable at lib level; pending nanite consumption (`go-providers` v0.8.0 typed events surface ships the primitive — `events.ToolUse` / `events.ToolResult` / etc. emit per-line via the adapter's `EventParser` interface; nanite wires `WithEvents(ctx, callback)` on its PTY consumer to surface as SSE `tool_call`/`tool_result` events)

**Symptom:** When a CLI/PTY child agent (Claude Code, Codex, etc.) is running tools, the nanite UI shows a single "running tool" pip in the Tools drawer with no detail. The thinking indicator and tool spinner are on, but there's no visible activity — "hard to tell anything is happening."

**Root cause:** Claude Code (and the other CLI agents) run their own internal tool loop. Per-tool calls happen *inside* the CLI subprocess; the bridge in `~/Projects-apps/go-providers/provider/pty.go:92-243` only sees the final `assistant` / `result` events. Individual `tool_use` blocks emitted by the CLI are not surfaced as nanite `tool_call` SSE events. The "no-silent-drop" guard at `pty.go:151-158` emits a single `EventError` if the CLI produced *only* tool_use blocks with no text delta.

**What's available today:**
- `EventDelta` per text chunk
- `EventToolUse` (parsed from stream-json — has the tool name)
- `EventUsage`
- `pty_turn_start` / `pty_turn_complete` / `pty_turn_failed` rows in `session_events`
- `ActivityCallback` per parsed line
- `ProcessCallback` for start/stop

**Two leverage points for fix:**

1. **Transport-level (reliable)** — emit a `status` SSE per `EventToolUse` with the tool name, displayed as a single overwriting line ("WORKING — calling Read on …"). Doesn't require CLI cooperation. The data is already parsed; only the SSE emission is missing.
2. **Prompt-level (richer)** — instruct the CLI agent (via `CLAUDE.md` / `agent-context.md` in the sandbox dir) to emit `nanite_message_send` `status_update` messages between major steps via its MCP back-channel. Opt-in per agent prompt.

Layer 1 is the quick win.

**Test:** Spawn a Claude Code child with a multi-step task; observe that the user only sees one running pip and no progression. Verify the SSE stream contains the parsed tool-use events on the backend side.

---

## G-PTY-RESUME-DROP

**Severity:** P1
**Status:** ✓ Resolvable at lib level; pending nanite consumption (`go-agent-sessions` v0.5.0 long-lived `ptySession` runtime eliminates the per-turn `--resume` pattern entirely — system prompt is set once at process start and persists for the PTY's lifetime; nanite adopts via `Caps.PTY=true` in its `AdapterRuntimeConfig`)

**Symptom:** When a PTY-based Claude Code chat session is on its second-or-later turn, slot-block changes (mode addendum, agent prompt update, rules) are not propagated.

**Root cause:** `~/Projects-apps/go-providers/provider/pty_claude.go:38-42`. The Claude Code CLI does not accept `--system-prompt` together with `--resume`. The adapter sends:

- **First turn:** `claude -p "<prompt>" --output-format stream-json --verbose --system-prompt "<systemPrompt>"`
- **Subsequent turns:** `claude --resume <cliSessionID> -p "<prompt>" --output-format stream-json --verbose`

`--system-prompt` is dropped on resume. Claude Code maintains its own internal state — the system prompt set on turn 1 is what persists.

**Partial mitigation:** The sandbox dir is repopulated per turn with fresh `CLAUDE.md`, `.sandbox/agent-context.md`, `.sandbox/envelope-schema.md`, `.mcp.json`. Claude reads `CLAUDE.md` as project context, so changes there propagate. Slot block changes that map onto `CLAUDE.md` content (agent profile, mode addendum) can be re-encoded there.

**Fix shape:** No clean fix without CLI cooperation. Mitigations: (a) push more state into `CLAUDE.md` regeneration, (b) end the `--resume` chain and start fresh when slots materially change (loses CLI-side history).

---

## G-SSE-UNSUBSCRIBED

**Severity:** P2
**Status:** Open (UX work pending)

Three backend SSE event kinds emit cleanly, but the FE has no listener:

| Event | Emitter | Intended UX |
|---|---|---|
| `notify_pause` | `chat_notify_pause.go:111` (per `dev_*` exec) | (planned) inline pre-tool banner with cancel |
| `rate_budget_pause` | `chat_rate_budget_pause.go:38, 104` | (planned) toast/banner with auto-retry / user_action |
| `chat-loop-budget-soft-warning` | `chat_loop_budget_soft_warning.go` (devmode-gated SSE) | (planned) inline soft-warning at `max_turns` |

Backend signals are in production; users see them only via logs (or via SSE inspector in devmode). Three card components are pending UX work; the wire is ready.

**Fix shape:** Add listeners + cards. The `notify_pause` is the most user-visible — affects every `dev_*` tool call.

---

## G-HOT-SWAP-DEAD

**Severity:** P2
**Status:** Open (no production caller)

`SlotFlags.LazyLoad` + `LoadHint` shipped in `internal/context/window.go:151` (Glass-5, CW-20260502-0012). When `LazyLoad: true`, the slot ships only the `LoadHint` pointer string instead of full content; cache invalidates on toggle.

**`grep "SetFlags.*LazyLoad" internal/service/`** returns 0 hits. **No production caller activates LazyLoad on a slot.**

`SkillEssentialCap=25` (`internal/chat/context.go:333`) is a catalog partition primitive, not slot LazyLoad.

The `tool-block-hot-swap-design.md` doc describes the design intent — saving tokens on rarely-used tool definitions by replacing them with a hint pointer until the agent asks for them via `request_tools`.

**Fix shape:** Wire callers. Tool catalog is the obvious first target — for tools beyond `SkillEssentialCap`, ship a LoadHint instead of the full description. `request_tools` already exists for the pull-on-demand path ([02](02-tool-invocation-and-authority.md)).

---

## G-CACHE-RACE

**Severity:** P1
**Status:** Documented; structural fix gated on go-providers seam change

**Symptom:** Telemetry `cacheable_prefix_tokens` and rate-budget pre-flight estimates can be wrong under concurrent sessions on the same provider.

**Root cause:** `prov` is a singleton fetched from the registry. State-mutation methods (`SetCacheHints`, `EstimateCacheablePrefix`) mutate the shared instance rather than per-call `ChatRequest`. Two callsites:

- `chat_generate.go:252-254` — `SetCacheHints(provider.DefaultCacheStrategy())`
- `chat_generate.go:836-844` — rate-budget pre-flight

Concurrent session B's `SetCacheHints` lands between session A's call and `StreamChat`, overwriting hints for A's pre-flight. Documented in code at lines 814-835.

**Severity caveat:** affects telemetry + rate-budget gating, **not turn correctness** — the provider gets `SlotBlocks{CacheKey}` per call, so actual cache placement is correct. The race is in nanite's pre-flight estimate.

**Vanta:** `limitations.nanite.cache_hints_shared_singleton_race` (draft).

**Fix shape:** Move cache hints onto `provider.ChatRequest`. Touches the go-providers interface + every adapter + every caller. Out of recent cleanup scope.

---

## G-BG-PRIVILEGED

**Title:** `internal/background/pty.go` shell-task backend has no sandbox / permissions / file-dir enforcement

**Severity:** P3 (downgraded from P1)
**Status:** Open — resolution path TBD

**Audit finding (agent-boot-adoption Phase 4b, implementer report 2026-05-08):** `~/Projects-apps/nanite/internal/background/pty.go` is **NOT** a CLI-agent spawn primitive. It is a detached shell-task backend used for short-term shell tasks (file scrapers, scheduled health probes, fire-and-forget operations) — `/bin/sh -c <task>` with `Setpgid=true`. Despite the package path containing "pty", it does not allocate a real PTY. CLI agents (Claude Code, Codex, etc.) are spawned via the Async PTY/CLI lane in [05](05-external-agent-execution.md), not via this file.

**Concern as a standalone shell-task hardening issue:** the backend provides

- **No sandbox** (no `go-sandbox` profile, no sandbox-exec/Landlock)
- **No permissions** (no path-grant check, no profile permissions)
- **No file-dir enforcement**
- **No SSE / inbox / chat surface** (no observability path back to the user)

Mechanism: `exec.CommandContext` with `Setpgid=true`, captured stdout/stderr to bounded buffer, `CompletionFunc` callback. Output cap via `Budget.MaxOutputBytes`.

**Severity rationale (P3):** the file is not in the chat-system's hot path; its use cases (file scrapers, scheduled probes) don't intersect with chat sessions or subagents. Hardening would still be valuable if the primitive sees scaled use, but the previously-proposed fix (fold under `ModeBackground` with full gate stack) does not apply — `ModeBackground` is part of the agent-spawn consolidation and this file is outside that lane.

**Fix shape (resolution path TBD):** audit current callers; either constrain to a vetted command allowlist (no arbitrary shell), or wrap in `go-sandbox` independently of the `agent.Boot` pattern. Either path is a separate concern from the agent-spawn consolidation work.

---

## G-MODE-CONFIRM-UX

**Severity:** P3
**Status:** Open (B3 not wired)

Mode-suggestion SSE event emits when classifier confidence ≥ 0.7 ([08](08-classification-and-intent.md)). FE stages it in `useChatStore.pendingModeSuggestion`. The `ModeSuggestionCard.tsx` component exists, but the wiring from staged-suggestion to rendered confirm card (B3) is not complete in production paths.

**Fix shape:** Render the staged suggestion as a confirm card; on accept, update `useSession.activeMode`; on dismiss, suppress for the rest of the session.

---

## G-NO-AUTO-RECALL

**Severity:** P3
**Status:** ✓ Closed (2026-05-08, `feat/memory-auto-recall`)

Original framing claimed there was no automatic `memory_recall` at chat-loop start. That framing was stale: `MemorySource` (`internal/contextbroker/source_memory.go`) has been registered with the ContextBroker since `phase-3 S2b` (commit `75871a3`), querying Vanta with `relevance` ranking on every turn using the user's last message as the query. The broker's `Fetch` iterates **all** registered sources, so MemorySource runs whenever it is registered (`internal/service/container.go:491` — guarded by `memorySvc != nil`).

What the 2026-05-08 closure added:
- `AgentProfile.Settings` keys `auto_recall`, `auto_recall_limit`, `auto_recall_min_confidence` — per-profile dial. Defaults preserve prior behavior (enabled, limit 30, confidence ≥ 0.4).
- 2s timeout enforced inside `MemorySource.Fetch` (`context.WithTimeout`); silent on timeout, leaves Memory slot empty for that turn.
- Structured slog output (`contextbroker/memory: auto-recall ok|hit_count=0|timed out|disabled by intent`) so operators can see hit rate, latency, and timeouts per session.
- `contextbroker.Intent` carries `AutoRecall *bool` + the three override fields, populated by `chat.deriveIntent` from the agent profile.

---

## G-MEMORY-SLOT-EMPTY

**Severity:** P3
**Status:** ✓ Closed (2026-05-08, framing was stale)

Same root cause as [G-NO-AUTO-RECALL](#g-no-auto-recall) above: the Memory slot is populated each turn by `MemorySource` when conduit/`memorySvc` is wired, with results filtered by `formatPacketItemsBySource(memoryOnly=true)` (`internal/chat/context_client.go:216`) and assigned via `cw.SetContent(ctxpkg.SlotMemory, sources.Memory)` (`internal/service/context.go:195`). If a deployment runs without conduit, the slot is legitimately empty — that's the expected fallback, not a gap.

---

## G-PROGRESSIVE-ALLOW-LIST

**Severity:** P3
**Status:** Open (verification needed)

The interaction between `tools_allow_list` (profile permissions) and progressive-discovery seed builtins isn't fully traced. Possible mismatch: a tool surfaces in `selection.Catalog` (so the agent can `request_tools` it) but is filtered out at `SelectForAgent`. Needs verification before testing progressive discovery edge cases.

**Fix shape:** Add an audit test that walks every progressive seed and confirms `tools_allow_list` consistency.

---

## G-RECOVERY-COVERAGE

**Severity:** P3
**Status:** Open

Recovery from provider errors flows through `ctxpkg.IsCompactRecoverable`. This is a single function that classifies provider error → recoverable. Adding a new provider with novel error semantics requires updating this function; without it, recovery silently doesn't run.

**Fix shape:** Per-provider recovery mapping or a documented adapter contract for "what's recoverable."

---

## G-HANDOFF-CLASSIFY

**Severity:** P2
**Status:** Open (migration consideration)

`ensureGlass4HandoffPreCompact` runs only when `IsLongRunning(sess)` ([09](09-session-and-slot-management.md)). The `session.intent` column was added by migration 052; sessions created before classification or where classification didn't set intent never hit the Glass-4 path — they fall back to legacy P7 stash.

**Fix shape:** Default-classify older sessions (pick a heuristic — message count, age) at session attach. Verify CW-20260504-0004 (handoff-continuity smoke) covers this case.

---

## G-TILDE-NOTE-LOCATION

**Severity:** P3
**Status:** Open (resolution = code archeology)

`tildeAcceptanceNote()` is referenced in decisions / spec / boot prompts as the convention for "tool descriptions accept `~/` verbatim and let the permission layer expand." The exact code symbol wasn't located in the recent investigation — could be a comment in description templates rather than a function. Worth resolving before tool-description docs are revised.

---

## G-INBOX-NO-PUSH

**Severity:** P3
**Status:** Open (verification needed)

`message_received` is in the SSE event taxonomy ([03](03-sse-envelope-and-interaction-protocol.md)). The FE consumption path for push-style inbox arrivals (toast a notification when an agent posts to your inbox while you're in chat) was not verified during recent investigation. Inbox is correctly polled via `getAgentMessageUnreadCount`; push enrichment would be a UX upgrade.

---

## G-SUBAGENT-STATUS

**Severity:** P3
**Status:** Open (verification needed)

`subagent_run_status_changed` is in the event constant table. FE consumption path for the new external-agent flows ([05](05-external-agent-execution.md)) was not verified. Could be a lever for the CLI/PTY visibility problem ([G-PTY-NO-TOOL-EVENTS](#g-pty-no-tool-events)) — relay subagent status to a status surface in the parent's UI.

---

## G-PTY-SUPERVISION

**Severity:** **P0**
**Status:** Open — boot prompt drafted for `go-agent-sessions` v0.6.0 supervision wiring

**Symptom:** Once nanite adopts `go-agent-sessions` v0.5.0 long-lived `ptySession` runtime for chat sessions, there is no idle-kill, no restart-on-crash, no watchdog, and no `ResourceLimits` enforcement on the PTY-backed CLI child. Concrete consequences:

- Browser tab closes → daemon retains the PTY indefinitely (ghost session accumulation across uptime).
- Claude segfaults mid-session → consumer must detect `cmd.Wait` exit and re-spawn manually; lib doesn't help.
- Runaway CLI agent (infinite loop in tool execution) → no watchdog kill.
- Recovery broker pattern ([D-SUBAGENT-RECOVERY-BROKER](file:///Users/chrispian/Projects-apps/nanite/docs/architecture/chat-system/future-work.md#d-subagent-recovery-broker--internal-recovery-broker-for-clipty-failures)) has no lib hook to integrate with.

**Root cause:** `go-agent-sessions` v0.5.0 ships `ptySession` without `Supervisor` / `ResourceLimits` wiring. Locked decision per the v0.5.0 implementer (matching mux's pattern): `creack/pty.Start` doesn't compose with `go-runner`'s `Supervisor` which wraps `runner.Run`. PTY supervision needs a PTY-native restart loop observing `cmd.Wait` directly inside `ptyRuntime`.

**Resolution path:** focused follow-up session on `go-agent-sessions` to land v0.6.0 with `StartOptions.Supervisor` + `StartOptions.ResourceLimits` natively wired on ptySession. Boot prompt at `agent-workspaces/execution/go-agent-sessions/2026-05-08-supervision/implementer-prompt.md`.

**Until v0.6.0 lands, nanite must:**
- Implement app-level idle-kill via SSE-stream-stalled detection + manual `session.Stop()`, OR
- Defer production long-lived chat until lib supervision lands.

**This is the only lib-level blocker for nanite Boot adoption.**

---

## G-MAC-MEMORY-LIMITS

**Severity:** P3
**Status:** Documented constraint (lib-level)

`go-runner` v0.3.0's `ResourceLimits.MemoryMax` is silently dropped on darwin: macOS bash's `ulimit -v` doesn't expose `RLIMIT_AS`, and there's no systemd to layer cgroup memory limits on top. Native macOS ProcessTask APIs aren't wired (would require a different mechanism than the lib's universal `sh -c "ulimit ...; exec ..."` wrap).

**Implications for nanite:**
- On darwin (the user's primary dev platform), `MemoryMax` set on a Boot's `ResourceLimits` is advisory. Don't rely on it for hard memory enforcement; document this clearly anywhere a config exposes the field.
- On linux production deployments (when those happen), `MemoryMax` works via systemd-run. Cross-platform parity is impossible without VM isolation.

**Workaround:** none ergonomic. VM isolation (Lima / OrbStack / Apple Virtualization) is the documented escape hatch. Out of scope for the chat system.

---

## G-GO-SIGXCPU

**Severity:** P3
**Status:** Documented constraint (lib-level)

The Go runtime swallows `SIGXCPU`. Empirically verified in `go-runner` v0.3.0's testing: `RLIMIT_CPU` enforcement works for native CLIs (`sh`, `claude`, `codex`), but Go binaries (which includes nanite, mux, clockwork themselves) do not terminate at the soft CPU limit.

**Implications for nanite:**
- `ResourceLimits.CPUTime` set on a `Boot()` for a CLI child works as expected (the child is a native binary like `claude`).
- `ResourceLimits.CPUTime` set on a Go-binary child (e.g. a hypothetical `nanite-tool` subprocess) would not enforce.
- Cgroup `CPUQuota` is the answer for portfolio Go binaries on linux. Not yet wired through `ResourceLimits.CPUTime` on the linux+systemd-run path (currently only `MemoryMax` maps to a systemd property). Filed as a sensible go-runner increment.

**Workaround:** for nanite chat, this doesn't bite — every spawned CLI child is a native binary. Document for awareness.

---

## G-TYPED-EVENTS-ADAPTER-PATH

**Severity:** P3
**Status:** Documented; v0.6.0+ lib increment candidate

`go-agent-sessions` v0.5.0 fires `StartOptions.TypedEventCallback` only on the PTY runtime path (`Caps.PTY=true`), not on the adapter runtime path (subprocess-per-turn, `Caps.PTY=false`).

**Reason:** the adapter path drives `runner.Run` which surfaces provider events as `runner.EventProviderEvent` (carrying `provider.StreamEvent` shape). Threading typed events through requires either:
- (a) Extending `go-runner` to carry `events.Event` in its event payload (touches Tier 1 lib).
- (b) In `adapterSession`, when `TypedEventCallback` is set, translate each `runner.EventProviderEvent`'s `StreamEvent` to `events.Event` at emission time (duplicates `go-providers`'s unexported `translateStreamEvents`; cleanest if `go-providers` exports it).

**Implications for nanite:**
- Nanite's primary path is PTY (long-lived chat), where typed events fire correctly. Low priority.
- Subprocess-per-turn fallback (used when `Caps.PTY=false` for an adapter without long-lived support) would not get per-tool surfacing in nanite UI; legacy `EventFanout` is still the only signal on that path.
- For adapters like opencode / codex / etc that nanite uses without PTY today, accept the gap or wait for v0.6.0+ lib increment.

---

## G-EVENTRESOURCELIMITHIT-NOT-EMITTED

**Severity:** P3
**Status:** Documented; best-effort heuristic gap

`go-runner` v0.3.0 declares `EventResourceLimitHit` but does not emit it. Inferring "this exit was caused by RLIMIT_X" is heuristic at best (Cause=oom_kill mapping requires reading `/proc/PID/cgroup` or the systemd journal post-mortem).

**Implications for nanite:**
- When the recovery broker classifies a child crash, it cannot directly attribute "hit memory limit" vs "hit CPU limit" vs "watchdog fired" via a lib event. It must correlate `ExitError.Signal` and `ExitError.Cause` (when set) heuristically.
- For most of nanite's classifier branches (transient / config-permissions / permanent), `Signal` + `Cause` are sufficient. Resource-limit attribution is nice-to-have, not blocking.

**Future fix path** (filed in go-runner): on linux+systemd-run, parse the transient unit's exit metadata via `systemctl --user show <unit>` post-mortem. Heavy but reliable. Or infer from (signal, ResourceLimits-non-zero) heuristics with documented best-effort semantics. Punted in v0.3.0.
