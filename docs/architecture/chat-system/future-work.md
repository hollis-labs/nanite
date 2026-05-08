# Future Work

> Forward-looking, directional decisions and design notes for the chat system. Distinct from [gaps.md](gaps.md) (which catalogs current limitations). This doc captures: locked decisions, design intents, and explicit shape of upcoming work — not implementation tickets.
>
> When a directional decision here graduates to per-ticket implementation, the relevant ticket links into here from its body, not the other way around.

## Locked decisions

### D-AGENT-BOOT — Adopt unified `agent.Boot()` pattern from clockwork cross-app design

**Captured:** 2026-05-07 (Vanta `decisions.nanite.architecture.adopt_agent_boot_pattern` rev `01KR2XZV53H70EKTVHBTDRXN3K`; portfolio convention at `decisions.portfolio.architecture.agent_boot_pattern` rev `01KR2Y2PYBR7FM8VXJBB831WG3`)

**Rule:** Nanite adopts the unified `agent.Boot()` pattern from clockwork's 2026-05-07 cross-app design (`agent-workspaces/planning/agent-boot-unification/2026-05-07-cross-app-design.md`). Single `internal/runtime/agent/` package with `Boot(ctx, deps, opts) (*Session, error)` becomes the only entry point for agent process spawn in nanite.

**Adopted shape:**
- **5-mode flat enum** (universal across portfolio): `ModeLongLived` (default), `ModeOneShot`, `ModeResume`, `ModeSubagent`, `ModeBackground`.
- **Two-dir model:** ephemeral boot dir at `$TMPDIR/nanite-boot-<provider>-<sessionID>-r<runID>-XXXXXX/` (cwd, cleaned on session done) + persistent workspace at `~/.nanite/workspaces/<sessionID>/{prompts,state,logs}/`.
- **Kickoff convention:** `SendInput("Boot @./boot.md")`. Tiny IPC, content lives in planted file, survives post-compaction reload, raw-content fallback for non-`@` providers.
- **Boot-dir naming:** `nanite-boot-<provider>-<sessionID>-r<runID>-XXXXXX` for cross-portfolio forensics.

**Mode mapping for nanite:**

| Mode | Nanite use case |
|---|---|
| `ModeLongLived` (default) | Chat session — long-lived PTY (consumes [D-CLI-LONG-LIVED](#d-cli-long-lived--ptycli-agents-default-to-long-lived-nanite-managed-sessions)). Primary mode for chat surface. |
| `ModeOneShot` | Legacy subprocess-per-turn — explicit exception for short CLI calls. |
| `ModeSubagent` | Replaces `subagent_runner.go`. Requires `Options.ParentSessionID`; preserves path-grant lineage walks. |
| `ModeBackground` | Supersedes `internal/background/pty.go`. **Defaults to FULL gate stack** (cgroups/rlimit/sandbox + path grants + permission engine). `Options.WideOpen=true` retains the privileged primitive when explicitly requested. |
| `ModeResume` | Daemon-restart recovery for capable adapters. Requires `Caps().ProviderSessionID = true`. |

**ModeBackground full-gate-with-WideOpen-flag** — chosen over preserving the bare privileged primitive. Rationale: full spectrum, no caller silently inherits unsandboxed behavior, the privileged path stays available when truly needed. Closes [G-BG-PRIVILEGED](gaps.md#g-bg-privileged) structurally.

**What stays out of Boot:**
- Inline-parallel tool execution (`chat_tool_executor.go` `executeToolBatch`) — tool-exec batching, not agent spawn. Stays as-is.

**Boundary clarification — Boot vs chat harness:** Boot creates the agent process; the chat harness owns the conversation. ModeLongLived covers the stateful process lifecycle, but the conversation surface (slot management, hot-swap, history, mode-suggestion, slot Window assembly) stays in nanite's chat harness. Boot is invoked by the harness; it does not subsume harness concerns.

**Per-provider boot-dir layouts:** Preserve nanite's richer file vocabulary (`CLAUDE.md` + `.sandbox/agent-context.md` + `.sandbox/envelope-schema.md` + `.mcp.json`) over clockwork's sparser pattern. This is a real differentiator — mux has it only on the externshell path. Files: `bootdir_claude.go`, `bootdir_codex.go`, `bootdir_opencode.go`, `bootdir_gemini.go`, `bootdir_copilot.go`.

**Implementation dependencies (shared-lib work goes first):**

- `go-agent-sessions`: long-lived PTY runtime (path c), `AutoFireFirstTurn` hook, better PID propagation, `WorkspaceDir` option separate from `Workdir`, capability-driven selection.
- `go-providers`: **per-line typed event emission** (this is where [D-CLI-PER-TOOL-SSE](#d-cli-per-tool-sse--surface-clipty-in-loop-tool-calls-as-sse-events) lands), `BootDirSpec()` per adapter, AGENTS.md planting helper.
- `go-runner`: structured `ExitCode` return, supervision hooks (idle-kill, restart-on-crash, watchdog), resource limits (cgroups v2 on Linux via systemd-run shell-out, rlimit baseline on macOS).
- `go-sandbox`: `AllowLoopback` knob (already filed).

Shared-lib bumps land first; nanite + clockwork execute against stable lib versions; mux follows when convenient.

**What this supersedes:**
- Multiple in-process spawn paths in nanite (`chatServiceImpl` direct, `subagent_runner` direct, `internal/background/pty.go` direct) → collapse into `agent.Boot()`.
- Per-call boot-dir population in `internal/plugin/builtin/adapter-claude/plugin.go` → moves to `internal/runtime/agent/bootdir_<provider>.go`.

**References:**
- Cross-app design: `agent-workspaces/planning/agent-boot-unification/2026-05-07-cross-app-design.md`
- Clockwork implementer prompt: `agent-workspaces/execution/clockwork-manifold/agentic-execution-flow/2026-05-07/implementer-prompt-agent-boot.md` (CW-20260508-0001)
- Nanite Vanta entry: `decisions.nanite.architecture.adopt_agent_boot_pattern`
- Portfolio Vanta entry: `decisions.portfolio.architecture.agent_boot_pattern`

---

### D-CLI-LONG-LIVED — PTY/CLI agents default to long-lived nanite-managed sessions

**Captured:** 2026-05-07 (Vanta `decisions.nanite.architecture.cli_pty_long_lived_default` rev `01KR2Y16TZJC8X88E6P497JBH3` — supersedes prior revs with go-agent-sessions implementation strategy locked in)

**Rule:** PTY/CLI provider agents (Claude Code, Codex, Gemini, Aider, Junie, Copilot, OpenCode) default to long-lived nanite-managed PTY sessions. The current per-turn `claude -p ... --resume <cliSessionID>` subprocess pattern (`~/Projects-apps/go-providers/provider/pty_claude.go:29-44`) is replaced as default. One-shot `-p` invocations remain as an explicit exception for short calls that don't benefit from persistence.

**Why:** Three load-bearing limitations of the current per-turn `--resume` model:

1. Cold-start lag on every turn (process spawn + CLI init).
2. `--system-prompt` is dropped on `--resume` — slot/mode/agent-prompt changes after T1 never reach the CLI ([G-PTY-RESUME-DROP](gaps.md#g-pty-resume-drop)).
3. No bidirectional channel during a turn — nanite can't inject mid-turn signals; CLI can't stream interim status ([G-PTY-NO-TOOL-EVENTS](gaps.md#g-pty-no-tool-events)).

**Implementation strategy (locked 2026-05-07):** **Path c from clockwork cross-app design §9 — extend `go-agent-sessions` to natively support long-lived PTY runtimes.** Mux's `agent-mux/internal/provider/cli/claudecode/runtime.go:22-89` is the donate-and-clean seed. `Caps().PTY` flag already exists in `go-agent-sessions`, unused for cli adapters today — designed for this.

**Capability-driven selection** (consumed by [D-AGENT-BOOT](#d-agent-boot--adopt-unified-agentboot-pattern-from-clockwork-cross-app-design)):
- `Mode=ModeLongLived` + `Caps().PTY=true` → long-lived PTY runtime
- `Mode=ModeLongLived` + `Caps().PTY=false` → subprocess-per-turn with `--resume` (legacy fallback)
- `Mode=ModeOneShot` → subprocess (single turn, auto-stop)

**Per-adapter long-lived support is incremental:**
- claude-code first (mux donates `claudecode/runtime.go` to `go-agent-sessions`; clean up; nanite adopts).
- opencode, codex, gemini, aider, junie, copilot follow per priority. Each adapter's interactive protocol differs.
- Adapters without stable long-lived stdin protocol may temporarily retain `-p --resume` until upstream support lands.

**Out of scope:** path-grant lineage walk, sandbox dir population (`CLAUDE.md` + `.sandbox/` + `.mcp.json`), MCP back-channel are unchanged. See [05](05-external-agent-execution.md).

**Background spawn (`internal/background/pty.go`)** explicitly remains the privileged exception. [G-BG-PRIVILEGED](gaps.md#g-bg-privileged) is unchanged by this decision.

**Subagent spawn** (`internal/service/subagent_runner.go:228-249`) inherits the long-lived default when the child's provider is PTY/CLI.

---

### D-CLI-PER-TOOL-SSE — Surface CLI/PTY in-loop tool calls; lives in go-providers

**Captured:** 2026-05-07 (this doc; lib-home locked 2026-05-07)

**Rule:** Per-line typed event emission for CLI/PTY-spawned agents lands in **`go-providers`** as a portfolio-shared primitive. Per-adapter `ParseLine` already produces typed events (`EventToolUse`, `EventToolResult`, `EventThinking`); the gap is normalizing the per-line event taxonomy and exposing it through a stable surface that any consumer (nanite SSE, mux MCP middleware, clockwork dispatcher) can subscribe to.

**Why go-providers, not app-internal:**
- Mux and clockwork have the same gap (mux's PTY runtime emits no per-line typed events; clockwork uses subprocess-per-turn with `claudestream`'s ParseLine but doesn't surface tool-step events to its UI either).
- Per-line parsing is a per-adapter concern (each CLI's stream-json shape differs); centralizing keeps the per-adapter stream-json knowledge in one place.
- Consumers downstream (nanite chat UI, mux MCP audit, clockwork dispatch logs) get a uniform event surface.

**Implementation shape (lib-side):**
- Per-adapter `ParseLine` already produces typed events; expose a stable `Events()` channel or callback hook on the adapter.
- Event taxonomy: `EventToolUse` (name, args), `EventToolResult` (success/error + truncated preview), `EventThinking`, `EventDelta`, `EventUsage`, `EventDone`, `EventError`. Consider: `EventSubagentSpawn` (Claude's Task tool), `EventSubprocessStderr` (currently unobserved), synthesized `EventHeartbeat`.
- Privacy: by default, full args. Optional `ToolArgFingerprint` mode (mux ADR 0021 pattern — SHA-256 of arg keys) for shared-deployment scenarios.

**Implementation shape (app-side, after lib lands):**
- Nanite: SSE `tool_call` / `tool_result` events emitted per `Events()` callback; FE Tools drawer pip updates per-step. UX layer: overwriting single-line "WORKING — calling Read on …" with pulsing-orb.
- Mux: subscribe `Events()` for symmetry with `LoggingMiddleware`'s MCP-side audit (different transport, same event shape).
- Clockwork: log per-step events to its dispatcher trail.

**Dependencies:** Independent of D-CLI-LONG-LIVED but composes with it (long-lived process gives a stable correlation between events and the originating turn). Lib bump precedes app-side adoption.

---

### D-SUBAGENT-RECOVERY-BROKER — Internal recovery broker for CLI/PTY failures

**Captured:** 2026-05-07 (this doc; directional, pre-implementation)

**Rule:** Chat-harness-detected CLI/PTY failures are routed to an in-process **subagent recovery broker** that classifies the failure, attempts automated remediation, dispatches a replacement session preserving lineage, and emits user-facing status via the chat envelope. The chat agent itself stays focused on the conversation; recovery is a separate concern handled by a specialized internal agent.

**Why:** Today, a CLI/PTY child crashing mid-turn surfaces as a generic harness `error` SSE with no automated remediation, no retry classification, and no breadcrumb trail for postmortem. Most CLI failures are recoverable (sandbox repopulation, MCP transport restart, transient signals); the harness shouldn't escalate these to the user as raw errors when a small classifier can fix-and-retry.

**Classification (proposed):**

| Class | Examples | Action | User-facing message |
|---|---|---|---|
| Transient | Network blip, rate-limit, SIGINT during tool exec, memory pressure | Retry with backoff | "Agent ran into a temp error, retrying" (info-card) |
| Config/permissions | Sandbox dir write fail, MCP server unreachable, missing env, stale CLAUDE.md, expired credentials | Automated fix + retry | "Fixed a configuration issue and retrying" (info-card with fix detail) |
| Permanent | Version mismatch, fatal signal, unknown error class | Log + telemetry + escalate | error-report or chat-loop-terminated envelope |

**Implementation shape:**

- In-process orchestrator with a classifier (rule-based first, can grow to LLM-assisted) sitting between the chat harness and the spawn primitive (`internal/service/subagent_runner.go`).
- Hooks into the spawn lifecycle: process exit, transport error, mid-stream failure, watchdog timeout (presumes [GAP-PTY-SUPERVISION](#research-gaps-surfaced-by-the-comparison) is also resolved — supervision is a prerequisite).
- Failure event payload: error type, exit code, stderr capture (last N KB), last stream-json position, session/agent context, sandbox dir state at time of failure.
- Lineage preservation: new session inherits parent's path-grant lineage, agent profile, slot Window, and conversation up to the failure point.
- Telemetry breadcrumbs always written, even for transient successes — postmortem-ready.

**Open implementation questions:**

- Should the broker run as a true subagent (its own session, addressable via inbox) or as an in-process module? Subagent gives audit trail; in-process gives speed. Probably hybrid: in-process classifier with telemetry that resembles a subagent's audit.
- How does the broker interact with the existing `runawayFailCap=10` / recoverable-error path in [04 Harness](04-chat-harness-and-loop-orchestration.md)? Recovery broker likely sits *outside* the per-turn loop, handling whole-session failures; the runaway cap stays for in-loop tool denial cascades.
- What's the user-cancel UX when a recovery is in-flight? "Cancel retry" button on the info-card.

**Dependencies:**

- [D-CLI-LONG-LIVED](#d-cli-long-lived--ptycli-agents-default-to-long-lived-nanite-managed-sessions) makes failure modes more interesting (long-lived processes can crash mid-conversation, not just mid-turn).
- [GAP-PTY-SUPERVISION](#research-gaps-surfaced-by-the-comparison) — supervision is a prerequisite for crash detection.
- Live attach broker (mux-pattern) composes well: recovery broker is one of N attach subscribers.

---

### D-IN-PROCESS-TOOLBROKER — Tool broker stays in-process

**Captured:** 2026-05-07 (this doc; informal)

**Rule:** The tool-broker (`github.com/hollis-labs/go-toolbroker` imported at `internal/toolclient/broker.go`) stays an in-process Go package. Sidecar extraction is not pursued.

**Why:** The workload is description composition + per-turn hint enrichment, latency-sensitive and low-CPU. In-process gives function-call latency, shared registry/cache, single-binary deploy. Sidecar trade-offs (process isolation, polyglot, privilege boundary, independent versioning) don't apply: there's no scale/lifecycle independence requirement, and crashes in description composition are co-failure with nanite anyway.

**When to revisit:** If a privilege boundary becomes load-bearing (e.g. broker runs untrusted hint-enricher plugins), or if hint enrichment becomes CPU-heavy, the `~/Projects-apps/tool-broker` repo being a separate module gives the option to extract.

---

## Shared-lib delivery status (2026-05-08)

All four foundation libs shipped or already shipped. Three await user-gated merge + tag.

| Lib | Version | Status | Delivered capabilities |
|---|---|---|---|
| `go-sandbox` | **v0.2.0** | Already shipped 2026-05-01 | `Profile.AllowLoopback`, `Profile.LoopbackForwardPorts` (linux per-port bridge) |
| `go-runner` | v0.3.0 | Shipped, awaiting merge+tag | `ExitError` via `errors.As`, `SupervisorOptions` (idle-kill / restart / watchdog), `ResourceLimits` (sh-c-ulimit + linux systemd-run for memory) |
| `go-providers` | v0.8.0 | Shipped, awaiting merge+tag | Typed events in `provider/events/` sub-package, `Events()` callback surface, `EventParser` optional interface, `BootDirSpec()` per adapter, AGENTS.md helper, `WithToolArgFingerprint` opt-in |
| `go-agent-sessions` | **v0.5.0** | Shipped, awaiting merge+tag (bumped from prior local v0.4.0) | `ptySession` runtime (path c), `Caps.PTY` capability-driven selection, `AutoFireFirstTurn` + `FirstTurnPayload`, `WorkspaceDir` option, `TypedEventCallback`, `LivePID()`/`LastPID()`, attach broker API frozen + tested |

Authoritative implementer reports:
- `agent-workspaces/execution/go-sandbox/2026-05-08/audit-and-closure.md`
- `agent-workspaces/execution/go-runner/2026-05-08/implementer-report.md`
- `agent-workspaces/execution/go-providers/2026-05-08/implementer-report.md`
- `agent-workspaces/execution/go-agent-sessions/2026-05-08/implementer-report.md`

Cross-app summary (Clockwork-facing): `agent-workspaces/planning/agent-boot-unification/2026-05-08-lib-tier-status.md`.

### Blocking gap: PTY supervision in `go-agent-sessions`

**[G-PTY-SUPERVISION](gaps.md#g-pty-supervision) is the only lib-level blocker for nanite Boot adoption.** v0.5.0 ships `ptySession` without `Supervisor` / `ResourceLimits` wiring (locked decision matching mux's pattern — `creack/pty.Start` doesn't compose with `go-runner`'s `Supervisor`). Effect: no idle-kill on ghost chat sessions, no restart-on-crash, no watchdog, no recovery-broker hook.

**Resolution:** focused follow-up `go-agent-sessions` session targeting v0.6.0 with PTY-native supervision wiring. Boot prompt drafted at `agent-workspaces/execution/go-agent-sessions/2026-05-08-supervision/implementer-prompt.md`.

**Until v0.6.0 lands:** nanite implements app-level idle-kill via SSE-stream-stalled detection + manual `session.Stop()`, OR defers production long-lived chat.

### Non-blocking constraints to know (lib limitations)

- **macOS memory limits silently drop** ([G-MAC-MEMORY-LIMITS](gaps.md#g-mac-memory-limits)). `ResourceLimits.MemoryMax` on darwin is unenforced; document anywhere user config exposes the field.
- **Go runtime swallows SIGXCPU** ([G-GO-SIGXCPU](gaps.md#g-go-sigxcpu)). Doesn't bite for nanite chat (CLI children are native binaries); awareness only.
- **`TypedEventCallback` fires only on PTY runtime** ([G-TYPED-EVENTS-ADAPTER-PATH](gaps.md#g-typed-events-adapter-path)). Subprocess-per-turn fallback continues with legacy `EventFanout`. Low priority since long-lived PTY is nanite's primary path.
- **`events.SubprocessStderr` not under PTY**. Kernel-level merge into tty stream; use SubprocessBridge if per-line stderr separation needed.
- **`EventResourceLimitHit` declared but not emitted** ([G-EVENTRESOURCELIMITHIT-NOT-EMITTED](gaps.md#g-eventresourcelimithit-not-emitted)). Recovery broker correlates `ExitError.Signal` + `Cause` heuristically.
- **Six BootDirSpec stubs** in `go-providers` v0.8.0 (gemini, copilot, aider, junie, kiro, qwen). Apps using these adapters today plant bespoke files; iteration enabled per-adapter as `Notes` field clears.

### What this unlocks for nanite

Once v0.6.0 supervision lands, nanite Boot adoption can proceed cleanly:

- **[G-PTY-NO-TOOL-EVENTS](gaps.md#g-pty-no-tool-events)** resolvable via `go-providers` v0.8.0 typed events + `WithEvents(ctx, callback)` consumption.
- **[G-PTY-RESUME-DROP](gaps.md#g-pty-resume-drop)** resolvable via `go-agent-sessions` v0.5.0 long-lived `ptySession` (eliminates per-turn `--resume`).
- **[G-BG-PRIVILEGED](gaps.md#g-bg-privileged)** resolvable via `go-sandbox` v0.2.0 + `ModeBackground` full-gate-with-WideOpen-flag default.

These three were previously P1 chat-system gaps. After lib consumption + nanite Boot adoption, they close.

---

## Activity-awareness events catalog (CLI/PTY)

Companion to D-CLI-PER-TOOL-SSE. Inventory of signals available from the parsed CLI stream-json plus signals we could synthesize. Useful for UX scoping.

| Event | Source | Surfacing today | Surfacing intent |
|---|---|---|---|
| `system/init` (CLI session start) | parsed | persisted to `session.metadata.cli_session_id` | optional inline status "session attached" |
| `assistant` text deltas | parsed | wired (live narration) | unchanged |
| `assistant` `tool_use` block | parsed | NOT surfaced (G-PTY-NO-TOOL-EVENTS) | per-step `tool_call` SSE |
| `assistant` `tool_result` block | parsed | NOT surfaced | per-step `tool_result` SSE |
| `assistant` `thinking` block | parsed (when present) | not used | optional reasoning preview |
| `result` (final) | parsed | wired (turn-complete) | unchanged |
| Subagent / `Task`-tool spawn | parsed (special-case `tool_use`) | not distinguished | depth/fan-out viz in Tools drawer |
| Process stderr | currently unobserved | none | breadcrumb "warnings" lane |
| Resource heartbeat | synthesizable | none | "still alive" pulse when stream-json silent for >Ns |
| Token-rate sample | from `EventUsage` | per-turn aggregate only | mid-turn cost meter |

Once D-CLI-LONG-LIVED lands, two more signals become possible:

- **Inbound nudges** — push a message into the CLI's input stream mid-turn (only if the CLI supports interactive stdin while a tool loop is running; per-adapter survey needed).
- **Mid-turn cancellation** — signal the long-lived process without killing it (per-adapter, depends on signal handling).

---

## Agent Mux comparison

> Researched 2026-05-07 against `~/Projects-apps/agent-mux` + co-located libs (`go-agent-sessions`, `go-providers`, `go-sandbox`, `go-runner`). Two recon rounds.
>
> **Key insight:** mux has **two parallel boot doctrines** in current code — a sparse "managed-session" path and a rich "externshell" (external-terminal) path. The previously-conflicting interpretations are both correct, talking about different code paths. Detail below.

### Two-doctrine bifurcation in mux

Mux today has **two distinct ways to launch a CLI agent**, with different config-injection vocabularies:

| Path | Entry point | Workspace files written | Long-lived? |
|---|---|---|---|
| **Managed-session** (in-mux daemon) | `LaunchSession` → `agentsessions.Manager.Start` → adapter runtime | `prompts/boot.md`, `state/plan.json`, `logs/session.log` (sparse) | claude-code only; everything else subprocess-per-turn |
| **Externshell** (external terminal F2 boot) | `tui.commands.BootWith` → `externshell.BootWith` (`internal/tui/externshell/externshell.go`) | claude: `CLAUDE.md` + `.claude/settings.json` + `boot.sh`. opencode: `agents/<a>.md` + `agents.json` + `opencode.json` + `boot.sh` + `OPENCODE_CONFIG_DIR`. codex: `AGENTS.md` + `boot.sh`. | n/a — runs in user's terminal |

**The user's intuition is right** — mux *does* write rich vocabulary including opencode-specific config. **The prior recon was also right** — the managed-session workspace path is sparse. They're different code paths invoked by different UIs. Whether this bifurcation is intentional or migration debt is a mux-side question, not nanite's.

### Side-by-side (managed-session path only — that's the apples-to-apples comparison with nanite)

| Axis | Agent Mux (managed-session) | Nanite |
|---|---|---|
| Process lifetime | `claude-code` = **long-lived PTY** (`internal/provider/cli/claudecode/runtime.go:22-25` — explicit "claudecode is the only mux adapter that takes the PTY path"); `claudestream`, `opencode`, codex/aider/gemini/junie/copilot/kiro/qwen all subprocess-per-turn via `claudestream.NewWithAdapter` → `agentsessions.NewFromAdapter` | **All providers** subprocess-per-turn |
| PTY allocator | `creack/pty` via `session.Start` (claude-code only) | `creack/pty` via `pty.Start` (every turn) |
| Stdin multiplex | RWMutex on `Handle.PTY` (PTY); `turnInFlight` CAS + `turnMu` (adapter) | None — fresh process per turn |
| Resume continuity | claude-code: NONE (`Caps().ProviderSessionID = false`, `runtime.go:42-47`) — PTY dies with daemon. claude-stream + opencode: daemon-side `--resume` via `Store.SetClaudeSessionID` → `logical_agents.claude_session_id` (single shared column reused for opencode IDs too) | `--resume <id>` rebuilt per turn from caller-managed `cliSessionID` |
| Supervision | **None at runtime layer** — confirmed by recon. No idle-kill, no restart-on-crash, no CPU/mem limits. Daemon-restart sweep flips orphaned `launching/running` → `failed` at boot (`internal/store/sqlite.go:128-133`); that's the entire mechanism. | None evident |
| Resource enforcement | `go-sandbox` (sandbox-exec / Landlock) wraps argv at spawn — **filesystem/network policy only, NOT CPU/mem limits** | Config-dir injection only — file-based, no kernel enforcement |
| Workspace dir (managed) | `prompts/boot.md` + `state/plan.json` + `logs/session.log` | `CLAUDE.md` + `.sandbox/{agent-context,envelope-schema}.md` + `.mcp.json` |
| Workspace dir (externshell) | `CLAUDE.md` / `agents/*.md` + per-CLI config + `boot.sh` | n/a |
| PTY framing | Raw bytes — no envelope. `session.Start` does `io.Copy(sink, ptmx)` to log+fanout (`internal/session/runtime.go:78-81`) — **no parsing in the PTY path** | Raw bytes — `bufio.Scanner` line-decode + adapter `ParseLine` (stream-json, in subprocess-per-turn path only) |
| Live attach to running session | **Yes** — ring-buffered fanout broker + HTTP `GET /sessions/{id}/attach` with `since_seq` resume | Not surfaced — single in-process channel |
| Tool-call surfacing for PTY | **None.** The claude-code PTY runtime emits NO per-line typed events; only `session.state_changed`. PTY output goes to log + fanout raw. | **None today** (G-PTY-NO-TOOL-EVENTS); `EventToolUse` is parsed by go-providers in subprocess-per-turn path but not surfaced as SSE |
| MCP-routed tool surfacing | `LoggingMiddleware` (`internal/mcpadapter/middleware_logging.go`) intercepts MCP `tools/call`, emits `tool_call_start` / `tool_call_end` to events Bus → `proxy_events` SQL store → `/proxy/events` query API. **MCP-proxy concern, not a PTY-adapter concern** (your separation-of-concerns reading is correct) | No equivalent for nanite as MCP proxy (different role) |
| Tool-args privacy | SHA-256 of arg keys (ADR 0021) — only relevant for the MCP-proxy logging path | Full args in stream-json |
| Storage | `StateSink` / `AttachmentSink` / `EventSink` decomposition; mux wires SQLite at composition | Tightly coupled to nanite's store |

### Side-by-side

| Axis | Agent Mux | Nanite |
|---|---|---|
| Process lifetime | `claude-code` = **long-lived PTY**; everything else = subprocess-per-turn | **All providers** subprocess-per-turn |
| PTY allocator | `creack/pty` (claude-code only) | `creack/pty` via `pty.Start` (every turn) |
| Stdin multiplex | RWMutex on `Handle.PTY` (PTY); `turnInFlight` CAS + `turnMu` (adapter) | None — fresh process per turn |
| Resume continuity | Long-lived needs none; adapter captures `EventSessionID` and persists daemon-side, presets `--resume` next turn | `--resume <id>` rebuilt per turn from caller-managed `cliSessionID` |
| Supervision | None at session layer; daemon-restart sweep flips orphans → failed | None evident in `pty.go` |
| Resource enforcement | **`go-sandbox` (sandbox-exec / Landlock) wraps argv** — kernel-level | Config-dir injection only — file-based, no kernel enforcement |
| Workspace dir | `prompts/boot.md` + `state/plan.json` (sparse) | `CLAUDE.md` + `.sandbox/{agent-context,envelope-schema}.md` + `.mcp.json` (richer) |
| Boot prompt delivery | Written to PTY via `io.WriteString(ptmx, bootPrompt)` when `bootMode=="stdin"` | Implicit — first user message; subsequent turns via `--resume` |
| PTY framing | Raw bytes — no envelope | Raw bytes — `bufio.Scanner` line-decode + adapter `ParseLine` (stream-json) |
| Live attach to running session | **Yes** — ring-buffered fanout broker + HTTP `GET /sessions/{id}/attach` with `since_seq` resume | Not surfaced — single in-process channel |
| Tool-call surfacing | **Out-of-band via MCP proxy `LoggingMiddleware`** → `tool_call_start`/`tool_call_end` events Bus → `proxy_events` SQL store → `/proxy/events` query API | In-band — `EventToolUse` parsed from claude stream-json (currently NOT surfaced as SSE — G-PTY-NO-TOOL-EVENTS) |
| Tool-args privacy | **Schema fingerprint only (SHA-256 of arg keys)** per ADR 0021; values never logged | Adapter forwards `tool_use` blocks; full args in stream-json today |
| Storage | `StateSink` / `AttachmentSink` / `EventSink` decomposition; mux wires SQLite at composition | Tightly coupled to nanite's store |

### Lessons for nanite (sorted by leverage, post-recon)

These are candidates, not locked decisions. Each maps to a directional choice the user will make per the [open follow-ups](#open-follow-ups-for-planning) below.

1. **Long-lived PTY for claude-code is proven.** Pattern reference: `agent-mux/internal/provider/cli/claudecode/runtime.go:22-25` — `creack/pty` + RWMutex stdin + `wait` goroutine + Handle.PTY nil-clear on exit. Nanite's [D-CLI-LONG-LIVED](#d-cli-long-lived--ptycli-agents-default-to-long-lived-nanite-managed-sessions) implementation can pattern-match against this directly. **Caveat:** mux's claude-code opts out of `ProviderSessionID` resume — the PTY *is* the session and dies with the daemon. Nanite needs to decide whether long-lived means "no resume needed" (mux pattern) or "resume preserved across daemon restarts" (richer state recovery).
2. **MCP-proxy `LoggingMiddleware` is for proxy-routed tool calls, NOT PTY tool surfacing.** Recon confirmed: `LoggingMiddleware` intercepts MCP `tools/call`, has zero PTY hookup. It addresses a different concern (proxy auditability) than the gap nanite is trying to close ([G-PTY-NO-TOOL-EVENTS](gaps.md#g-pty-no-tool-events) — surfacing CLI-internal tool steps to the chat UI). **The two are complementary, not alternatives:**
   - In-band parse of stream-json `EventToolUse` → SSE → chat UI (closes G-PTY-NO-TOOL-EVENTS) ← **D-CLI-PER-TOOL-SSE stays as previously framed.**
   - MCP middleware → events Bus → SQL audit log (proxy concern; useful if/when nanite acts as an MCP proxy or wants per-tool audit independent of provider).
3. **Kernel-level sandbox enforcement via `go-sandbox`.** Mux uses sandbox-exec on darwin / Landlock on linux for filesystem/network policy. **NOT CPU/mem limits** (recon clarification). Layered on top of config-injection. Nanite should adopt for filesystem/network containment of `dev_*` and CLI children; resolves [G-BG-PRIVILEGED](gaps.md#g-bg-privileged) structurally and hardens every other spawn type at the same time.
4. **Live attach broker.** Net-new capability. `go-agent-sessions/agentsessions/attach.go:35-89` + `agent-mux/internal/api/attach.go:23-52`. Ring-buffered fanout with `since_seq` resume. Composes with [D-SUBAGENT-RECOVERY-BROKER](#d-subagent-recovery-broker--internal-recovery-broker-for-clipty-failures) (recovery broker is one of N attach subscribers).
5. **Daemon-managed CLI session ID.** Mux persists via `Store.SetClaudeSessionID` (`agent-mux/internal/app/service.go:340-344`) into `logical_agents.claude_session_id` (single shared column reused across providers — cosmetic detail). Gated by `providerHasSessionIDContinuity` allow-list (`{"claude-stream", "opencode"}`). Nanite's `cliSessionID` flow is caller-managed through context — works, but less robust across daemon restarts.
6. **Daemon-restart reconciliation sweep.** `internal/store/sqlite.go:128-133` `SweepStaleSessions` + `SweepStaleAttachments` on boot. Nanite-side equivalent not traced; [GAP-CLI-SESSION-PERSIST](#research-gaps-surfaced-by-the-comparison) and the broader supervision story are open questions.
7. **Tool-args fingerprinting (ADR 0021).** Mux logs SHA-256 of arg keys, never values. Privacy-relevant for shared-deployment scenarios. Nanite is single-user today; relevant if/when multi-user. **Note:** this only applies to the MCP-proxy path, not stream-json tool_use (which contains full args).
8. **Workspace separate from repo_root.** Already aligned via nanite's two-root contract. ✅

### What nanite does that mux doesn't (preserve in any rework)

- **Single-doctrine rich vocabulary on the managed-session path.** Mux's rich vocabulary (`CLAUDE.md` + per-CLI config + `boot.sh`) only exists on the **externshell** path (`internal/tui/externshell/externshell.go`); the **managed-session** path writes only `boot.md` + `plan.json` + `session.log`. Nanite's managed sandbox dir always gets the rich set (`CLAUDE.md` + `.sandbox/agent-context.md` + `.sandbox/envelope-schema.md` + `.mcp.json`). **This is a real differentiator** — nanite has one doctrine, mux has two and may be mid-migration. Preserve in any long-lived rework: when nanite goes long-lived, the rich vocabulary continues to populate the sandbox dir (regenerate on session attach + on agent-profile/mode change).
- **In-stream silent-tool-use guard** (`go-providers/provider/pty.go:151-158`) — terminal `EventError "CLI bridge cannot forward tool calls"` when claude returns only `tool_use` blocks with no text. Mux's PTY runtime has no such guard (raw `io.Copy` with no parsing). Keep.
- **`MuxTransportAdapter` devmode-only** — nanite is a *consumer* of mux's MCP surface with a trust-tuple gate `(workspace_id, agent_profile_id)`. Composition direction is nanite → mux.

### What nanite and mux both still need (shared gaps)

Surfaced by recon — **not nanite-behind-mux problems, just shared portfolio gaps:**

- **No runtime supervision in either app.** Zero idle-kill, zero restart-on-crash, zero CPU/mem limits. Both rely on daemon-restart sweep as the only cleanup.
- **No PTY-side tool-call surfacing in either app.** Mux's claude-code PTY runtime emits no per-line typed events; nanite parses but doesn't emit. Both have the same shape of gap; D-CLI-PER-TOOL-SSE addresses nanite's, mux would benefit from a parallel implementation.
- **Filesystem/network sandbox is partial.** Mux uses go-sandbox; nanite uses config-injection only. Even mux's sandbox doesn't do CPU/mem limits.

### Research gaps surfaced by the comparison

To resolve before the long-lived rework lands. These are nanite-side investigations, not portfolio gaps:

- **GAP-CLI-SESSION-PERSIST** — Nanite's persistence of `cliSessionID` across daemon restarts isn't traced. User direction: should be daemon-side (matching mux pattern). Resolution: investigate if a store column exists, add if missing.
- **GAP-PTY-SUPERVISION** — Nanite has none today (matches mux). User direction: **adopt supervision** as part of D-CLI-LONG-LIVED. Implies portfolio-level work — could land in a shared `go-runner`-style lib.
- **GAP-PTY-CRASH-HANDLING** — Resolved by [D-SUBAGENT-RECOVERY-BROKER](#d-subagent-recovery-broker--internal-recovery-broker-for-clipty-failures). Implementation depends on GAP-PTY-SUPERVISION landing first.

---

## Open follow-ups for planning

These aren't decisions yet — they're shapes worth walking through before the next session.

- **Memory-recall-on-session-start** — `G-NO-AUTO-RECALL`: should the harness auto-prime the Memory slot on first turn? Trade-off is latency + tokens for sessions where recall isn't useful. Decide opt-in (per agent profile?) vs always-on with a fast-path.
- **Hot-swap activation** — `G-HOT-SWAP-DEAD`: the LazyLoad / LoadHint primitive is wired but no caller activates. First obvious target is the tool catalog above `SkillEssentialCap=25`. Needs a measurement of how much it would save in real-world chats before committing.
- **FE singleton refactor** — `G-FE-SINGLETON`: shape is clear (Map<sessionID, ChatSessionState>); cost is the refactor + selector audit. Worth scoping as a single epic given how broadly it touches the UI.
- **Cache-hints race fix** — `G-CACHE-RACE`: structural fix moves cache hints onto `provider.ChatRequest` per-call. Touches go-providers interface; cross-portfolio impact (every adapter, every consumer). Needs portfolio-level coordination.
- **Background spawn hardening** — `G-BG-PRIVILEGED`: either constrain Background to a vetted command list or apply the same gate stack as other spawn types. Audit current callers first.
- **In-band PTY tool surfacing (D-CLI-PER-TOOL-SSE) is the right primitive for closing G-PTY-NO-TOOL-EVENTS.** Recon clarified: mux's `LoggingMiddleware` is for MCP-proxy auditability, not PTY tool surfacing — it doesn't address the same gap. **Decision: ship in-band parse first** (data is already parsed in `go-providers/provider/pty.go`; only the SSE emission is missing). MCP middleware logging is a separate future capability if/when nanite needs proxy-level audit independent of provider.
- **Kernel-level sandbox enforcement** — adopt `go-sandbox` (mux's pattern) to layer sandbox-exec / Landlock on top of the existing config-dir injection. Resolves [G-BG-PRIVILEGED](gaps.md#g-bg-privileged) structurally and hardens every other spawn type at the same time.
- **Live attach broker** — net-new capability. Ring-buffered fanout + HTTP attach endpoint with `since_seq` resume. Lets external tools (TUI, debugger, observer) follow a running session without owning the SSE channel. Pattern reference: `go-agent-sessions/agentsessions/attach.go:35-89`.
- **Daemon-managed CLI session ID** — store `cliSessionID` daemon-side instead of caller-context. Robustness against daemon restarts; pattern reference: `agent-mux/internal/app/service.go:341`.
- **Daemon-restart reconciliation sweep** — flip orphaned `launching/running` session rows → `failed` on boot. Self-healing state DB. Verify nanite-side equivalent exists; add if missing.

---

## Portfolio coordination notes

These are not nanite-only decisions; they shape `go-agent-sessions`, `go-runner`, `go-providers`, and possibly `agentd` (see parking-lot section). Will be aligned with Clockwork's breakdown when it lands.

### `agent_session_id` normalization

Today: nanite stores `cliSessionID` in context; mux stores `claude_session_id` in `logical_agents` (single nullable column reused across providers). Both are point solutions.

Proposed canonical shape (lives in `go-agent-sessions`):

```
agent_session_id            -- nullable; CLI/provider-side ID
agent_session_type          -- pty | cli | api | null
agent_session_provider      -- claude-code | opencode | codex | anthropic-api | ...
agent_session_capability_flags  -- bitfield: supports_resume, supports_long_lived,
                                  supports_mid_turn_cancel, supports_provider_session_id
```

`(type, provider)` partition makes future per-provider behavior clean (e.g. resume preset format, mid-turn cancel signal) without per-provider columns.

### Resource limits — pragmatic plan

| Platform | Approach | Mechanism |
|---|---|---|
| Linux | cgroups v2 + rlimit baseline | `systemd-run --user --scope --property=MemoryMax=… --property=CPUQuota=…` first iteration; direct cgroups via `containerd/cgroups` lib later if we outgrow shell-out |
| macOS | rlimit only | `setrlimit(RLIMIT_CPU)` (real, sends SIGXCPU), `RLIMIT_AS/RLIMIT_DATA` (advisory, may not enforce strictly), `RLIMIT_NOFILE`, `RLIMIT_NPROC` |
| macOS — real isolation | parking-lot | Lima / OrbStack / Apple Virtualization — overkill until an OOM event proves it's needed |

cgroup membership composes with supervision: idle-kill terminates the cgroup, observability via cgroup stats, restart re-creates.

### Resume support — opt-in per adapter capability

- Adapter declares `Caps().ProviderSessionID = true|false` (matches mux pattern).
- Capable adapters: daemon persists `agent_session_id`; on restart/reattach, presets the per-adapter resume token on first command.
- Non-capable adapters (e.g. claude-code-PTY): daemon restart = fresh CLI session; conversation rebuilt from nanite's own message store via slot assembly (the chat harness already does this — long-lived means the *process* is fresh after a daemon restart, not the *conversation*).
- Mux symmetry: the `providerHasSessionIDContinuity` allow-list (`agent-mux/internal/app/service.go:572-578`) currently has only `{"claude-stream", "opencode"}`. Whatever shape we land here applies to mux too.

---

## Parking lot — `agentd` (agent launcher daemon)

> **Status:** exploratory; not committed. To revisit after Clockwork breakdown lands and stage 0 (in-process supervision) stabilizes.

### Concept

Extract supervision + spawn + sandbox + resource limits into a separate executable that nanite, mux, clockwork (and any future portfolio app) talk to via gRPC or a Unix socket. Each app becomes a thin client; agentd owns the agent runtime.

### Benefits — real

- **Crash isolation.** Agent process failure (CLI segfault, OOM, runaway tool loop) doesn't propagate into the main app. Apps see a structured exit event from agentd; main app keeps running.
- **Single supervision implementation.** Cgroups, rlimit, sandbox-exec, idle-kill, restart-on-crash live in one place. Three apps share the implementation by sharing the daemon.
- **Cross-app session pool.** Optional: if three apps run on one machine, they could share session capacity, observability, and the attach broker.
- **Privilege boundary.** Daemon runs as a different uid with a tighter sandbox profile than the main apps. Agent files live in agentd's home, not in nanite/mux/clockwork's data dir.
- **Wire-protocol stability.** Apps pin to a min agentd version; agentd evolves underneath.

### Costs — also real

- **IPC overhead.** Spawn rate is low (function-of-user), but PTY output streaming adds bytes-shuffling cost. Manageable, real.
- **Lifecycle.** launchd/systemd unit needs to install alongside. Socket-activation is an option for on-demand spawn.
- **Failure mode.** Apps need a "daemon unreachable" code path. Not hard but new branching.
- **State migration.** Today nanite owns `cliSessionID`. Under agentd, daemon owns; nanite keeps its own row keyed to agentd's session.
- **Maintenance.** Third codebase.

### Common Go-community patterns for this shape

- `containerd` / `runc` — runtime daemon + thin client lib. Closest analog.
- `dockerd` — daemon + REST API; clients via SDK.
- `tmux` server — terminal session daemon; clients via Unix socket.
- `coreos/go-systemd` — systemd integration on Linux.
- `kardianos/service` — cross-platform service install/start/stop helper.

**There is no widely-adopted "agent launcher daemon" in Go OSS today.** Novel for AI agent runtime.

### Cerberus integration as the install story

Make agentd a Cerberus-managed resource:

- Install: user installs Cerberus once (likely already needed for nanite/mux/clockwork), then `cerberus install agentd`. Apps declare `agentd >= X.Y` and call cerberus' health-check API at startup. Absent or stale: friendly error with one-command install hint.
- Lifecycle: Cerberus already owns launchd plist + restart policy + log routing.
- Health checks: existing.
- Min-version: existing artifact pinning + version checks.
- Per-machine singleton: enforced.

This collapses the "more binaries to install" friction — users install Cerberus (already required); agentd rides along.

### Recommendation: staged

- **Stage 0 (now):** Implement supervision + cgroups/rlimit + resume + agent_session_id normalization in `go-runner` / `go-agent-sessions`. In-process for nanite/mux/clockwork. Ship gaps closed.
- **Stage 1 (later, after stage 0 stabilizes):** Extract supervision+spawn into `agentd`. Apps swap their in-process integration for an agentd client. Cerberus packages the install.

**Why staged:** stage 0 closes user-observable gaps fastest. Stage 1 adds isolation without new user-facing capability. Doing stage 0 first surfaces the right wire-protocol shape (because we're using it from inside the apps) before crystallizing it as an external API.

### Naming candidates

- `agentd` — short, daemon-conventional. Recommended.
- `agent-launchd` — descriptive, slightly long.
- avoid: `agentctl` (that'd be the client), `agent-broker` (overlaps with [D-SUBAGENT-RECOVERY-BROKER](#d-subagent-recovery-broker--internal-recovery-broker-for-clipty-failures)).
