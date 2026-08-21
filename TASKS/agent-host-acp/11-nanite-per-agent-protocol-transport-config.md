# Per-agent Protocol/Transport selection (DB-configurable surface)

**Phase:** 3 — ACP client abstraction & native adapters (`TASKS/agent-host-acp`)
**Status:** implemented
**Depends on:** `09`, `10` (at least the native ACP adapters must exist to be selectable);
`07` (host migration validated — this wires real agents into the migrated path)
**Touches:** possibly a new migration (`134` onward — check current highest at dispatch
time), `internal/runtime/agent/context_resolver.go` or a sibling file, `internal/service/
chat.go` (`classifyNilProvider`), `internal/plugin/agent_profiles.go`. Repo: Nanite.

## Context

17-acp.md is explicit: "No new top-level `agents.runtime_kind` value... ACP-driven agents
still route through the existing `cli` value... `Protocol`/`Transport` are internal to the
host descriptor, not a database concern. ... Per-agent protocol/transport selection belongs
in the same DB-configurable surface as the existing `agent_context_resolvers` pattern, not a
new schema axis."

**The precedent this task follows, verified directly**: `internal/runtime/agent/
context_resolver.go`'s `agent_context_resolvers` table (`internal/store/
agent_context_resolvers.go`, migration `118_agent_context_resolvers.sql`) has a `Kind string`
field (`:44`, values `"cmd"`/`"http"`, validated at `:97`) that drives a switch —
`contextResolverToSlotSpec` (`context_resolver.go:113`) builds a different typed spec per
kind, CRUD already exists per-agent, and resolution happens once at launch inside the `agent`
package (`ResolveContextBlocks`, `:62`). This is the exact shape 17-acp.md points at — this
task should follow it closely, not invent a new mechanism shape.

**Where this plugs into real routing, verified directly**: `runtime_kind` is a column on
`agent_profiles` (migration `080_agent_runtime_kind.sql:10`, `'cli'`/`'api'`, validated
`internal/plugin/agent_profiles.go:209`). The actual CLI-vs-API routing decision is
`chatServiceImpl.classifyNilProvider(runtimeKind, providerName string)` at
`internal/service/chat.go:1327` — the authoritative decision point per
`internal/chat/engine.go:260-264`'s own doc comment (`chat.IsCLIProvider` is a demoted
fallback, consulted only when `runtime_kind` isn't populated). A per-agent Protocol/Transport
choice sits *alongside* `runtime_kind='cli'`, not as a replacement for it — an ACP-driven
agent is still `runtime_kind='cli'`, just resolved to a different concrete adapter once
`internal/runtime/agent/factory.go`'s provider-dispatch logic runs (`factory.go:48` already
consults `runtime_kind`; `bootdir.go:238-240` too).

## What to do

1. Add a per-agent Protocol/Transport selection field — following the `agent_context_
   resolvers` `Kind`-driven precedent exactly: a typed column (or small table, if a per-agent
   1:many shape is genuinely needed — check whether one agent might plausibly want more than
   one Protocol/Transport configured, e.g. a fallback; if not, a single column on
   `agent_profiles` is simpler and matches `runtime_kind`'s own precedent more closely than
   a new table would). Values: the native protocols (`claude-stream-json`/`codex-app-server`/
   `opencode-native`) plus `acp`, with `Transport` following whichever the chosen protocol
   allows (most are fixed to `stdio`; ACP can be `stdio` or `tcp` per task `10`'s Copilot-CLI
   case).
2. Wire this into `internal/runtime/agent/factory.go`'s provider-dispatch logic (the same
   place `runtime_kind` is already consulted) so an agent configured for ACP actually resolves
   to one of tasks `09`/`10`'s adapters (or, once Phase 4 lands, a bridge adapter) instead of
   its native one.
3. Default: unset/empty means "use the existing native protocol for this provider," per
   17-acp.md's explicit "additive, not a cutover" framing — no agent should silently switch to
   ACP without an explicit per-agent configuration choice.
4. Add CRUD for this field following whatever the closest existing precedent is (likely
   `agent_context_resolvers`' own REST surface, or a plain field on the existing
   `agent_profiles` update endpoint if it's a single column) — check `docs/engineering/
   architecture/02-agent-launching.md`'s stated rejection of "a proliferating string-prefix
   convention in favor of one typed field" before choosing a shape.
5. If this needs a new migration, check the current highest number on disk immediately before
   writing it (provisionally `134`, per this batch's README — but other tasks/batches may have
   landed migrations first; re-check).

## Done means

- A per-agent Protocol/Transport selection exists, DB-configurable, following the
  `agent_context_resolvers` `Kind`-driven precedent's shape (typed field/small table, CRUD,
  resolved once at launch) rather than inventing a new mechanism.
- No new `agents.runtime_kind` value — confirmed by test, an ACP-configured agent still shows
  `runtime_kind='cli'`.
- An agent explicitly configured for `acp`+OpenCode (or Copilot CLI) actually launches through
  task `09`'s (or `10`'s) adapter — verified end-to-end, not just at the config-storage layer.
- Every existing agent's behavior is unchanged (empty/unset selection = native protocol,
  verified against real existing `agent_profiles` rows, not just a fresh-row test).
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` clean.

## Work Log (2026-08-21)

Read this file, `docs/engineering/architecture/17-acp.md`, `02-agent-launching.md`, this
batch's `README.md`, `docs/engineering/GLOSSARY.md`, and tasks `08`/`09`/`10`'s landed code in
`libs/go-agent-wrapper` (`acp/`, `adapters/opencodeacp/`, `adapters/copilotacp/`) in full before
touching anything.

### go-agent-wrapper pin bump

`go get github.com/hollis-labs/go-agent-wrapper@v0.7.0` — confirmed `v0.7.0` is the current
highest tag (`git tag` in the sibling `libs/go-agent-wrapper` checkout: `v0.1.0`...`v0.7.0`, no
local `replace` directive to worry about, unlike some other sibling-repo dependencies in this
repo's `go.mod`). Pure version bump — `go build ./...` was clean immediately, no code changes
needed for the bump itself (`go.mod`/`go.sum` diff only).

### Correction against the task file's own stated rationale (per this project's "decision vs.
rationale" discipline — noted, not blocking)

The task file's Context claims `factory.go:48 already consults runtime_kind; bootdir.go:238-240
too`. Verified directly: neither literally does. `factory.go`'s `shouldUsePTY` doc comment
*references* `runtime_kind` (explaining that the CLI-vs-API decision already happened upstream
before this package runs), and `bootdir.go:242` does the same in a comment — but no `if
profile.RuntimeKind == ...` branch exists in either file. The actual claim that matters
(runtime_kind's routing decision has already been made by the time this package's dispatch
logic runs) is correct and is exactly the precedent `effectiveProvider`'s own doc comment
already documents for a narrower in-package dispatch question — `useACPProtocol` follows that
same shape. Proceeded with the task as specified; this is a rationale-wording correction, not a
scope change.

### Design decisions

**DB shape: two nullable columns on `agent_profiles` (`protocol`, `transport`), not a new
table.** Followed the task's own explicit preference-check: is there a genuine per-agent 1:many
need (a fallback protocol list)? No — this is a single scalar "how does this agent's CLI process
launch" choice per agent, the same shape `runtime_kind` itself already is (migration 117's own
precedent), not the `agent_context_resolvers` shape (which genuinely needs 1:many — one agent
plausibly wants several named context slots). Migration `134_agent_profiles_protocol_transport.sql`
(confirmed `133` was the highest on disk immediately before writing it — re-checked, still
correct) adds `protocol TEXT CHECK (protocol IS NULL OR protocol IN ('claude-stream-json',
'codex-app-server', 'opencode-native', 'acp'))` and `transport TEXT CHECK (transport IS NULL OR
transport IN ('stdio', 'tcp'))`, mirroring migration 117's own `runtime_kind` CHECK-plus-Go-
validation belt-and-suspenders shape (`validateAgentMultiAgentFields` in
`internal/store/agents.go` enforces the same enum, plus the pairing rule: a non-empty
`transport` is rejected unless `protocol='acp'`). Empty/NULL (every pre-existing row) means
"native" — 17-acp.md's "additive, not a cutover" framing.

**CRUD shape: a direct-DB write method (`store.UpdateAgentACPConfig`), mirroring
`UpdateAgentComposition`'s (RoleID/ConsumerID/ModelID) precedent exactly — not a plain field
flowing through the managed-file pipeline.** Verified directly (not assumed) that this is the
*correct* precedent, not just the closest one: `AgentConfigService.Update`'s managed-file write
path (`writeManaged` → `agent.WriteManagedAgentProfile` → `agent.ParseMDFile` reparse →
`IngestAgentDefinition` → `upsertAgentDef`) always reconstructs the DB write from a *freshly
reparsed* `agent.Definition`, which has zero frontmatter representation for `Protocol`/
`Transport` (same as `RoleID`/`ConsumerID`/`ModelID`) — routing a value through that pipeline
would silently wipe it back to empty on the very next unrelated agent edit. Confirmed this
failure mode with a real failing test first (`TestHandleUpdateAgent_SetsAndClearsProtocolTransport`'s
"unrelated field edit" case failed before the fix), then found and applied the actual fix:
`upsertAgentDef` (`internal/service/ingest.go`) already has a "reingest-preservation" block for
`RoleID`/`ConsumerID`/`ModelID` immediately before its own `UpdateAgent` call — added
`profile.Protocol = existing.Protocol` / `profile.Transport = existing.Transport` right next to
it. This was NOT called out in the task's own "Touches" list, but is a necessary, narrowly-
scoped fix without which the whole DB-configurable surface would be non-durable across any
managed-agent save unrelated to protocol/transport — squarely required by this task's own "Done
means: DB-configurable... resolved once at launch" bar (a value that silently reverts on the
next unrelated PUT is not actually DB-configurable in practice). `internal/api/agents.go`'s
`handleCreateAgent`/`handleUpdateAgent` call `store.UpdateAgentACPConfig` after
`AgentConfigService.Create/Update` succeeds, exactly where the existing RoleID/ConsumerID/ModelID
block already does the same thing — new `protocol`/`transport` fields added to
`CreateAgentRequest`/`UpdateAgentRequest` (`internal/api/types.go`) with the same plain-string
(create) / pointer-partial-update (update) semantics as every other field on those structs.
Also added `protocol`/`transport` optional YAML fields to `internal/plugin/agent_profiles.go`'s
`PluginAgentProfileAgent` (mirroring its existing `runtime_kind` field) for symmetry — plugin-
declared agents go through a direct `UpsertAgentBySlug` write with no reparse round-trip, so no
preservation fix was needed there.

**factory.go dispatch: two small predicates (`useACPProtocol`, `effectiveACPTransport`), consulted
from `agent.Boot` at the same conceptual dispatch point `runtimeConfigForAdapter` already occupies
for the native-runtime-shape decision.** `useACPProtocol(profile *store.AgentProfile) bool`
returns `profile != nil && profile.Protocol == "acp"` — deliberately never reads or writes
`RuntimeKind` (pinned by `TestUseACPProtocolDoesNotTouchRuntimeKind`). `effectiveACPTransport`
defaults to `adapters.TransportStdio` unless `profile.Transport == "tcp"`.

### The real architectural finding this task surfaced (not anticipated by the task file, found by
reading tasks 09/10's own code, not assumed)

Task 09's `adapters/opencodeacp` and task 10's `adapters/copilotacp` both ship an
`adapters.RuntimeAdapter.CLIAdapter()` glue so their `Adapter` is *structurally* dispatchable
through `wrapper.Wrapper.Run`'s existing `ProtocolACP`+`TransportStdio` `runtime_dispatch.go`
entry (task 08) — but **both packages' own doc comments candidly document that composition as
"real but capture-only"**: `provider.CLIAdapter.BuildArgs` runs before the child process exists
and `ParseLine` is read-only, so neither method has a writer available to drive the real ACP
`initialize`/`session/new`/`session/prompt` handshake. Wiring `nativeAdapter`-style through
`wrapper.New` would spawn the real `opencode acp`/`copilot --acp` subprocess but never actually
perform that handshake — a session that looks launched but can never complete a real turn. Both
packages' own real end-to-end tests instead drive `acp.Client` (`Launch`/`Prompt`/`Cancel`/
`Events`) directly, bypassing `wrapper.Wrapper.Run` entirely.

Given this task's own Done-means explicitly requires "verified end-to-end, not just at the
config-storage layer," building the `wrapper.Config.Adapter`-based composition (what the task
file's own step 2 reads as the obvious approach) would have failed that bar outright. Instead,
`bootACP` (`internal/runtime/agent/agent_acp.go`) drives `acp.Client` directly — a second,
parallel `Session` backend alongside the native `wrapper.Wrapper`-based one:

- `Session.acp *acpSession` (new field, `agent.go`) is mutually exclusive with the existing
  `Session.wr *wrapper.Wrapper` — set by `bootACP` instead of the native path's `newNativeAdapter`
  + `wrapper.New` + `wr.Run` sequence.
- `internal/runtime/agent/acp_session.go`'s `acpSession` translates `acp.Client.Events()` onto
  Nanite's existing `EventFanout`/`TypedEventCallback` surfaces (the same seams
  `runtimeEventSink` drives for the native path) — written as a *new*, ACP-specific translator
  rather than reusing `runtimeEventSink.Write` verbatim, because two real, confirmed payload-
  shape facts made reuse actively wrong: (1) opencodeacp's own `KindAgentToolUse`/
  `KindAgentToolResult` payloads are flat (`tool_call_id`/`title`/`kind`/`status`), while
  copilotacp's are nested (`{"tool_use":{"id","name",...}}` matching `wrapper/
  event_translator.go`'s own convention) — a genuine, independently-arrived-at divergence
  between the two sibling packages, confirmed by reading both `translate.go` files directly, not
  assumed; (2) opencodeacp's `KindTurnCompleted` payload always includes a `"usage"` key when the
  turn had one, which would make `runtimeEventSink.handleTurnCompleted`'s existing branch send
  `EventUsage` instead of the terminal `EventDone` a chat consumer needs to know the turn ended
  — confirmed this exact failure mode live (see below).
- `Session.SendInput`/`Session.Stop` (`manager.go`) branch on `s.acp != nil` before falling
  through to the native `s.wr` path. `acpSession.Stop` calls `Cancel` (ACP's `session/cancel` —
  turn-scoped despite its name, per `docs/engineering/GLOSSARY.md`'s "Turn.Cancel vs.
  Session.Stop" entry) then `Close` (the real whole-session-end analog).
- `bootACP` (`agent_acp.go`) skips Nanite's provider-specific boot-dir planting entirely
  (`composeBootdirParams`/`layout.Setup`) — neither native ACP adapter has been shown to consume
  a planted `CLAUDE.md`/`config.toml`, and `bootdirLayoutFor` has no case for a
  Copilot-CLI-only provider name like `"copilot"` (would hard-fail with "bootdir for provider ...
  is not yet implemented" if reached). `providerName := effectiveProvider(opts, profile)` was
  moved earlier in `agent.go`'s `Boot` (ahead of the bootdir-setup block) specifically so the ACP
  branch can dispatch before ever reaching that code — confirmed via `composeBootdirParams`
  independently re-deriving the same value internally, so removing the now-duplicate later
  declaration is a no-op for the native path.
- First-turn kickoff for `AutoFireFirstTurn` modes uses `composeKickoffRaw` (embeds boot content
  directly), not `composeKickoff`'s `"Boot @./boot.md"` file-reference convention — there is no
  planted `boot.md` for an ACP session to resolve that reference against.
- Confirmed, documented (not silently swallowed) real gap: `acp.Client`'s interface (v0.7.0) has
  no `OnSessionID`-equivalent callback and no `SessionID()` getter, so Nanite cannot capture an
  ACP session's provider-side session id for a later resume today — `bootACP`'s doc comment flags
  this as a follow-up for whichever task next touches `libs/go-agent-wrapper`'s `acp` package.

### Live end-to-end verification (real commands, real output — not mocked)

Followed task `07`'s established scratch-server dogfeed recipe exactly: built a scratch binary
(`go build -o .../scratchpad/nanite-dogfeed11 ./cmd/nanite/`), ran it from a scratch CWD with
`XDG_DATA_HOME`/`XDG_STATE_HOME`/`XDG_CONFIG_HOME`/`XDG_CACHE_HOME` redirected to scratch
subdirectories and an explicit `-db` pointed at a scratch SQLite file (port 8299 — 8199/8200 were
already bound by an unrelated `nanite-verify` process), real `$HOME` left alone for CLI auth.
Confirmed both `opencode 1.15.6` and `copilot 1.0.12` resolve on this machine.

Created a real managed agent via `POST /api/agents` with `protocol="acp"`, `transport="stdio"` in
the request body — confirmed both fields round-tripped through the real HTTP response and a
follow-up `GET /api/agents/{id}`. Set `default_provider='opencode'`/`runtime_kind='cli'` via a
scoped `sqlite3 UPDATE` (matching task 07's own precedent — `default_provider`/`runtime_kind`
have no REST CRUD surface on this endpoint either, pre-existing, not something this task changed)
and re-confirmed via `GET /api/agents/{id}` that the full row (`opencode`/`cli`/`acp`/`stdio`)
round-trips through the real running app.

Created a real harness-v1 session (`provider=opencode`, bound to this agent) and sent a real turn
("What is 17+25? Reply with only the number.") via `POST .../turns`, then read the real SSE
stream via `GET /api/stream/{message_id}`:

```
event: stream_start
data: {"type":"stream_start","message_id":"d0dda4b2...","agent_id":"1b790251...",...}
event: delta
data: {"type":"delta","content":"42","phase":"final",...}
event: stream_end
data: {"type":"stream_end","message_id":"d0dda4b2...","agent_id":"1b790251...",...}
```

A real, correct answer ("42") streamed all the way to the chat SSE surface. Confirmed via `ps
aux` that a genuine `opencode acp` subprocess was running (`chrispian ... opencode acp`) — not a
config-storage-layer check, an actually-spawned, actually-driven-to-completion real subprocess.
Confirmed the assistant message persisted in the scratch DB
(`{"v":1,"text":"42",...}`) and that `agent_runtime.provider='opencode'`,
`agent_runtime.state='running'` for the session. Confirmed
`agent_profiles.runtime_kind='cli'` for this ACP-configured row throughout — the "no new
`agents.runtime_kind` value" Done-means bullet, checked against a real live row, not a unit test
fixture.

Confirmed `Session.Stop()` (via `POST /api/sessions/{id}/agent/reboot` → `RebootSessionAgent` →
`Session.Stop`) cleanly terminates the real ACP-driven subprocess: captured the live PID via `ps`
before the call, confirmed via `ps -p <pid>` and a fresh `ps aux | grep "opencode acp"` that it no
longer existed afterward — the real `acpSession.Stop` (Cancel-then-Close) path working end to
end, not just compiling.

**Regression check against an existing (non-ACP) agent, live, not just via the pre-existing unit
test suite**: created a second agent with `protocol`/`transport` left empty and
`default_provider='pty'`/`runtime_kind='cli'` (the existing native-Claude shape), sent a real turn
through the same running scratch server, and got a real, correct answer ("19") from a genuine
`claude -p --input-format stream-json --output-format stream-json --verbose` subprocess with real
token usage in the `stream_end` payload — confirming the `providerName` reordering ahead of the
bootdir-setup block (needed for the ACP branch) did not disturb the native path. `Session.Stop()`
via the same reboot endpoint cleanly terminated this session's `claude -p` process too.

**Cleanup discipline**: `git status --short` checked before, during, and after this entire dogfeed
run — zero diff caused by any part of it (the four pre-existing modified doc files and several
untracked files — `docs/engineering/architecture/19-api-cli-runtime-parity.md`/`20-skills.md`,
`docs/engineering/orchestrator-kickoffs/phase-6.md`, `docs/launch-site/` — were already present
before this task started and were never touched; per task `07`'s own established precedent, this
is a concurrent, unrelated session working in the same checkout, left alone throughout). One
real, worth-flagging finding *outside* the tracked-file boundary: `Dependencies.WorkspacesRoot`
defaults to `os.UserHomeDir()+"/.nanite/workspaces"` (`internal/service/agent_deps.go`) — unlike
`ManagedConfigRoot` (CWD-relative, confirmed scratch-isolated by task `07`), this is **not**
covered by the XDG-var redirection this recipe uses, so a scratch-server dogfeed with a real
`$HOME` (necessary for CLI auth, per task `07`'s own established rationale) writes real per-
session workspace directories into the actual production `~/.nanite/workspaces/` — confirmed
this is a real, long-lived, populated directory (110+ pre-existing session dirs going back to
May), not a fresh path. This is a pre-existing property of the whole batch's established dogfeed
recipe (not introduced by this task) that no prior task's Work Log flagged. Removed the one
workspace directory this run created (`~/.nanite/workspaces/aa4b4a01-.../`) by hand after
verification; flagging here rather than silently leaving it for whichever future task next runs
this recipe. Killed the scratch server via `SIGTERM` and confirmed a clean shutdown log sequence
plus zero leftover `opencode acp`/`claude -p`/`nanite-dogfeed11` processes via `ps`.

### Build/test status

`go build ./cmd/nanite/`: clean. `go vet ./...`: clean except the same two pre-existing,
unrelated `internal/service/container.go` findings every other task in this batch has already
noted (confirmed via `git status` that file is not part of this task's diff). `go test ./...
-count=1`: clean across every package, no regressions — including new test files
`internal/store/agent_protocol_transport_test.go`, `internal/runtime/agent/acp_dispatch_test.go`,
and `internal/api/agents_protocol_transport_test.go` (the latter's "unrelated field edit" case is
what caught the `upsertAgentDef` reingest-preservation gap described above, before it ever reached
live verification).

### Known limitations / follow-up candidates (not fixed by this task, flagged for whoever picks
them up next)

- `acp.Client`'s interface has no provider-session-id capture hook — ModeResume for an
  ACP-driven session cannot resume the *provider's* session today (Nanite's own session/turn
  history still persists and resumes normally; only the underlying `opencode`/`copilot` session
  continuity is affected). A natural `libs/go-agent-wrapper` follow-up.
- Claude/Codex/Pi are not selectable via `protocol="acp"` yet — `newACPClient` returns a clear
  error for any provider outside `{opencode, copilot}`, matching this batch's own Phase 4 gating
  (tasks 12-15, escalation-gated, not yet dispatched).
- `Dependencies.WorkspacesRoot`'s real-`$HOME` exposure in the established scratch-dogfeed recipe
  (noted above) — worth a small, separate fix (either make it XDG-aware like `ManagedConfigRoot`,
  or have the dogfeed recipe pass an explicit `WorkspacesRoot` override) so a future dogfeed in
  this batch doesn't need to remember to clean up by hand.
- Tool-call/tool-result fidelity for the ACP path is real but not exhaustively verified against
  a tool-using turn (this task's live verification used a plain arithmetic question, matching
  every other dogfeed in this batch) — `emitToolUse`/`emitToolResult` in `acp_session.go` handle
  both sibling packages' documented payload shapes, but a live tool-call round-trip through an
  ACP-driven session is untested end-to-end. Flagged for task `16`'s fs/terminal-proxying audit
  or a future side-by-side comparison (task `17`).

### Commit

Committed directly on top of `main` per this task's own dispatch instructions (serial work, no
worktree). See the dispatching agent's final report for the commit SHA.

## Work Log addendum (2026-08-21) — fix-up for the FAIL review

Context: a fresh reviewer's pass on the original implementation (commit `567bd76c`) found two real,
unflagged functional bugs plus one smaller accompanying gap in `acp_session.go`'s event
translation — logged in `TASKS/ESCALATIONS.md`'s 2026-08-21 "Task 11 review: FAIL — two real,
unflagged event-translation bugs in the new parallel ACP session backend" entry. This addendum is
the targeted fix, confined to `internal/runtime/agent/acp_session.go` and `agent_acp.go` per the
reviewer's own explicit recommendation — no architecture change. Read
`internal/runtime/agent/wrapper_sink.go` (the native-path comparison baseline) and
`internal/recovery/broker/classifier.go` in full before touching anything, per the reviewer's own
"verified directly against the real classifier code, don't guess" instruction.

### Finding 1 fix — reasoning/thinking content silently persisting into the visible answer

`handleEvent`'s `KindAgentDelta` case and its `extractContent` helper only read the `"content"`
key and always emitted a plain `llmtypes.EventDelta`, regardless of either sibling package's own
thinking-tag convention (confirmed directly against both `translate.go` files):
`opencodeacp` tags a reasoning chunk `{"content":...,"phase":"thought"}` (as distinct from
`"phase":"message"` on ordinary content); `copilotacp` tags the same distinction
`{"content":...,"thinking":true}`. Replaced `extractContent` with `extractDeltaEvent` (new
`acpDeltaPayload` type reading `content`/`phase`/`thinking`), which emits `llmtypes.EventThinking`
(with a populated `ThinkingBlock`) when either tag is present, mirroring `wrapper_sink.go`'s
`handleDelta` branch on `p.Thinking != nil`. Also corrected the file's own incorrect doc comment
that had claimed this flattening "matches the native runtimeEventSink's own flattening of the same
distinction" — factually wrong; the native path does not flatten this distinction, and reading only
one package's tag convention would have silently kept leaking the OTHER package's reasoning content
into the answer even after a partial fix.

### Finding 2 fix — a crashed ACP subprocess silently treated as a clean exit

`acpSession.handleEvent`'s switch had no case for `runtimeevents.KindProcessExited`, and
`bootACP`'s draining goroutine (`agent_acp.go`) never set `sess.runErr` at all — so
`Session.Wait()` for every ACP-driven session always returned `nil` regardless of how the process
died, meaning `internal/recovery/broker`'s crash-detection/auto-restart pipeline never engaged for
ACP-driven agents.

Verified directly against the real classifier before implementing (per the task's own explicit
instruction not to guess): `internal/service/chat_boot_drive.go`'s `observeSessionForRecovery` does
`errors.As(err, &xe)` against `*agentsessions.ExitError` specifically — a plain `error` would
satisfy `Session.Wait()`'s "non-nil" contract but never reach the broker at all, reproducing this
same bug one layer deeper. `agentsessions.ExitError`'s fields (`Code`, `Signal`, `Killed`,
`ProcessState`, `Cause`) are all exported (only `waitErr` is not), so a real, minimally-populated
value can be constructed directly from this package without reaching into unexported machinery.

Added `acpSession.handleProcessExited` (new `processExitedPayload` type reading opencodeacp's
`waitProcess`-populated `"error"` key) and a `processExitErr error` field on `acpSession`, set
exactly once (single-writer, on `drain`'s own goroutine, strictly before the `for`-range loop
returns). `bootACP`'s draining goroutine now assigns `sess.runErr = sink.processExitErr` strictly
after `sink.drain(...)` returns and strictly before closing `runDone` — the same
write-before-close ordering the native path's `sess.runErr = wr.Run(runCtx)` already uses, so
`Session.Wait`'s happens-before argument holds identically for both backends.

Traced the exact mechanics of *when* `KindProcessExited` can reach `handleEvent` at all, directly
against `opencodeacp/client.go`: `Client.Close` (called by `acpSession.Stop`'s Cancel-then-Close)
closes the Events channel **synchronously**, via `closeEvents()`, well before it ever waits on the
subprocess — so for every intentional Stop, `drain`'s `for`-range loop exits via the channel close
itself, and `waitProcess`'s own later `KindProcessExited` emit (guarded by `eventsClosed`) becomes
a silent no-op. This means `KindProcessExited` only ever reaches `handleEvent` for a genuine,
unprompted process death — exactly the case the broker exists to catch — and confirms no extra
"was this intentional?" bookkeeping is needed at this layer (Nanite's existing
`rebootingSessions`/`displacedSessions` flags in `chat_boot_drive.go` already handle the
intentional-stop-vs-crash disambiguation one layer up, identically for both backends).

Since ACP's wire protocol carries no structured exit-code/signal info (only the wrapped process
error's string), the constructed `*agentsessions.ExitError` is minimally populated:
`&agentsessions.ExitError{Code: -1}` (matching `ExitError.Code`'s own documented convention for
"no real exit code available"), wrapped via `fmt.Errorf("acpSession: process exited abnormally
(%s): %w", p.Error, xe)` so `errors.As` finds it and the original process-error text is preserved
for logs. `Code: -1` (non-zero) is what makes `Classify`'s final default branch
("unclassified non-zero exit... first occurrence transient, retry escalates to permanent")
actually engage instead of falling through to the `Code == 0`/"not a recovery candidate" bottom
branch reserved for a genuinely clean exit — verified directly against `classifier.go`, not
assumed, and pinned by a new `internal/recovery/broker` test (below).

`copilotacp.Client` does not emit `KindProcessExited` at all today (confirmed directly — no
occurrence in its `client.go`) — a real, pre-existing `go-agent-wrapper` gap one level up, out of
scope for this Nanite-side fix per the task's own instruction. Handled gracefully, not
crash-prone: `bootACP`'s draining goroutine only reads `sink.processExitErr` **after**
`sink.drain(...)` returns (i.e. after the Events channel closes for whatever reason, with or
without a `KindProcessExited` event ever having arrived), so a Copilot-CLI-driven session simply
always resolves `Wait()` to `nil` — the same behavior as before this fix for that provider, not a
hang and not a false crash. Pinned by
`TestACPSession_MissingProcessExitedEventDoesNotHang`.

### Finding 3 fix — token/cost usage never populated for ACP turns

`handleEvent`'s `KindTurnCompleted` case unconditionally sent only `EventDone`, never `EventUsage`
— so `chat_generate.go`'s `finalUsage` (feeding the persisted cost ledger and `stream_end` SSE
payload) stayed `nil` for every ACP-driven turn. Added `acpSession.handleTurnCompleted` (new
`acpTurnCompletedPayload` type reading the `"usage"` key both `opencodeacp`'s `finishTurn` and
`copilotacp`'s `awaitPromptResult` payloads may carry): when `Usage != nil`, sends `EventUsage`
first, then the terminal `EventDone` — both from the single combined payload, since (unlike
`wrapper_sink.go`'s native-path `handleTurnCompleted`, which receives usage and done as two
*separate* `Write` calls and must pick one or the other per call) ACP's own `KindTurnCompleted`
event fires exactly once per turn.

Verified directly against both adapters' real code (not assumed) that `copilotacp` never populates
a `"usage"` key at all today (`awaitPromptResult` only ever marshals `{"stop_reason":...}`) — so
this fix is a genuine no-op improvement for Copilot-CLI-driven turns (still correctly emits just
`EventDone`, unchanged from before), and only actually adds signal for `opencode`-driven turns.

One correction against this fix's own initial caution, found only through the live re-verification
below (documented here because it revises what the code's doc comment originally speculated): before
live-testing, `opencodeacp`'s own package doc was read as never confirming the `session/prompt`
response's `"usage"` field was observed in real traffic (only the unrelated `usage_update`
session/update *notification* variant was, and that one is deliberately skipped) — combined with
`llmtypes.Usage` having no JSON struct tags (so a raw `json.Unmarshal` only matches exact Go field
names, e.g. `InputTokens`, not a JS/TS-style `inputTokens`/`totalTokens`), this looked like a real
risk that `EventUsage` would carry a non-nil but silently zero-valued `Usage` for real opencode
traffic. **Live-verified this concern to be unfounded**: real opencode 1.15.6 traffic's
`session/prompt` response usage object round-trips correctly through the tagless
`json.Unmarshal` — `stream_end`'s persisted usage showed real, non-zero `input_tokens`/
`output_tokens` (see the live dogfeed below). Updated the code's own doc comment on
`acpTurnCompletedPayload` to record this live-confirmed finding instead of the pre-verification
caution.

### New regression test coverage

`internal/runtime/agent/acp_session_test.go` (new file):

- `TestExtractDeltaEvent` / `TestHandleEvent_ThinkingDeltaRoutesToFanoutAsEventThinking` — pin
  Finding 1: both tagging conventions (`phase:"thought"`, `thinking:true`) produce
  `llmtypes.EventThinking` with the reasoning text isolated in `ThinkingBlock`, never in
  `Content`; a `phase:"message"` (or untagged) delta still produces a plain `EventDelta`. The
  second test drives the real `handleEvent` dispatch (not just the pure extractor), confirming the
  fanout channel itself receives the two distinct event types for a thought-then-message pair.
- `TestHandleTurnCompleted` — pins Finding 3: a `"usage"`-bearing payload sends `EventUsage` (with
  the numeric fields correctly populated) immediately followed by the terminal `EventDone`; a
  payload with no `"usage"` key (copilotacp's real shape) sends only `EventDone`, unchanged from
  before; an empty/nil payload doesn't panic and still terminates the turn.
- `TestACPSession_AbnormalProcessExitReachesSessionWait` — pins Finding 2's core claim: a fake
  `acp.Client` (new `fakeACPClient` test double, implementing the real 6-method `acp.Client`
  interface with a controllable `Events()` channel — no real subprocess spawned) emits
  `KindProcessExited` with a non-empty `"error"`; `runACPDrainAndWait` replicates `bootACP`'s exact
  production draining-goroutine logic (drain, assign `sess.runErr`, close `runDone`) against a real
  `*Session`; the test asserts `sess.Wait(ctx)` returns non-nil AND that `errors.As` against
  `*agentsessions.ExitError` succeeds with `Code != 0` — the exact two properties
  `internal/recovery/broker`'s classifier requires to engage at all.
- `TestACPSession_CleanExitLeavesSessionWaitNil` — a `KindProcessExited` with no `"error"` key
  (clean exit) leaves `Wait()` nil.
- `TestACPSession_IntentionalStopClosesEventsWithoutProcessExited` — the Events channel closing
  directly (mirroring `Client.Close`'s real synchronous `closeEvents()` call, no
  `KindProcessExited` ever observed) leaves `Wait()` nil — the common "stopped on purpose" path.
- `TestACPSession_MissingProcessExitedEventDoesNotHang` — a Client that never emits
  `KindProcessExited` at all (copilotacp's real shape today) still resolves `Wait()` to nil, not a
  hang.
- `TestHandleProcessExited_DirectUnit` — isolated unit coverage of `handleProcessExited` itself:
  no-op on an empty/absent `"error"`; a populated `"error"` produces a wrapped
  `*agentsessions.ExitError{Code: -1}`, recoverable via `errors.As`.

`internal/recovery/broker/acp_exit_classification_test.go` (new file, in the broker package so it
can use the package's own existing `fakeAgentBoot`/`fakeStore`/`newFakeEnvelope` test fixtures —
`internal/runtime/agent` cannot import `internal/recovery/broker`, which imports it, so this
couldn't live in the `agent` package):

- `TestClassify_ACPUnclassifiedCrashShape` — pins that the *exact* `&agentsessions.ExitError{Code:
  -1}` shape `acp_session.go`'s `handleProcessExited` constructs classifies as `ClassTransient` on
  the first attempt and `ClassPermanent` on a repeat — i.e. it genuinely engages `Classify`'s
  crash-handling branch, not the `Code == 0` bottom branch.
- `TestOnSessionExit_ACPCrashShapeEngagesBroker` — drives that same exact shape through the real
  `Broker.OnSessionExit` pipeline end to end (not just `Classify` in isolation): confirms a
  replacement session is actually dispatched (`AgentBoot.Boot` called with the same `SessionID`)
  and a `ClassTransient` breadcrumb is recorded — i.e. an ACP crash is no longer silently
  swallowed at the point the pre-fix bug swallowed it (`Wait()` never even returning non-nil, so
  `OnSessionExit` was never called at all).

### Live dogfeed re-verification (real commands, real output — not mocked; this is the strongest
confirmation available, per the task's own preference)

Followed the same established scratch-server recipe prior tasks in this batch used: built a scratch
binary (`nanite-dogfeed11fix`), ran it from a scratch CWD with `XDG_DATA_HOME`/`XDG_STATE_HOME`/
`XDG_CONFIG_HOME`/`XDG_CACHE_HOME` redirected to scratch subdirectories, an explicit `-db` pointed
at a scratch SQLite file, and real `$HOME` left alone for CLI auth (port 8398 — 8199/8200/8299
already bound by other concurrent sessions). Confirmed `opencode 1.15.6` (matching this batch's
prior live verification) resolves on this machine.

Created a real managed agent (`protocol="acp"`, `transport="stdio"`) via `POST /api/agents`,
set `default_provider='opencode'`/`runtime_kind='cli'` via a scoped `sqlite3 UPDATE` (same
established precedent as the original task 11 dogfeed — these two fields have no REST CRUD surface
on this endpoint, pre-existing, not something this fix-up touched), and re-confirmed the full row
via `GET /api/agents/{id}`. Also seeded `user_settings.default_model`/`default_provider='opencode'`
(needed for `ResolveProviderAndModel` on this fresh scratch DB — not required in the original task
11 dogfeed, whose scratch DB apparently already carried a usable default from an earlier step; not
a code change, purely scratch-environment setup).

**Turn 1 — Findings 1 and 3, live, in one shot.** Sent a real turn ("What is 9 plus 33? Reply with
only the number.") via `POST .../turns` and read the real SSE stream via `GET /api/stream/{id}`:

```
event: delta
data: {"type":"delta","phase":"thinking","event_id":2}
... (8 more phase:"thinking" deltas) ...
event: delta
data: {"type":"delta","content":"42","phase":"final","event_id":10}
event: stream_end
data: {"type":"stream_end", ...,"usage":{"input_tokens":22821,"output_tokens":19,"cache_creation_tokens":0,"cache_read_tokens":0,"stop_reason":""}, ...}
```

Eight distinct `phase:"thinking"` SSE deltas streamed separately from the `phase:"final"` answer
delta — direct, live confirmation of Finding 1's fix (a genuine `opencode acp` process really does
emit `agent_thought_chunk` updates for this kind of prompt, and they now route to a distinct SSE
phase instead of blending into the answer). Confirmed the persisted assistant row in the scratch DB
is exactly `{"v":1,"text":"42",...}` — no reasoning text leaked into the persisted answer either.
`stream_end` carries real, non-zero `usage` — direct, live confirmation of Finding 3's fix (and the
"field names round-trip cleanly" correction documented above).

**Turn 2 — Finding 2, live, via a genuine mid-turn subprocess kill.** Sent a second turn designed
to run for a few seconds ("Write a detailed 300 word essay about the history of the number
zero..."), located the real `opencode acp` PID via `ps aux`, and `kill -9`'d it ~1.5s into the
turn while streaming was in flight. Observed, in order: the SSE stream immediately received a
`plugin_envelope` (`info-card`, "Reconnecting agent" / "Agent ran into a temporary error. Retrying
now…") followed by `stream_end` — the exact same envelope shape
`TestOnSessionExitTransientRetrySuccess` (`internal/recovery/broker`) pins for a `ClassTransient`
classification. The real server log confirmed the full pipeline end to end:

```
WARN recovery: session exited with error — invoking broker session_id=... cause="" code=-1 signal=0
INFO recovery: terminal exit observed session_id=... attempt=1 cause="" code=-1 signal=0
INFO chat-service: cancelling prior in-flight generation for session session_id=... new_msg_id=...
```

`code=-1 signal=0` is exactly the `&agentsessions.ExitError{Code: -1}` shape `handleProcessExited`
constructs — not a coincidence, a direct confirmation the real code path fired. A **second, genuine,
freshly-spawned `opencode acp` subprocess** (confirmed via `ps aux`, a new PID) was launched by the
broker's dispatched replacement session, and it **completed the retried turn end to end** — the
server log shows a full second `chat-loop-diag: loop start` → `loop exit` cycle (191 streamed
events, ~18.5s) for the replacement, and the scratch DB's `messages` table shows the real,
complete essay (`# The History of Zero...`) persisted as the assistant reply. Before this fix, per
the pre-fix bug, none of this recovery sequence would have fired at all — `Session.Wait()` would
have returned `nil` and `observeSessionForRecovery` would have taken the silent "clean exit,
nothing to recover" branch, exactly as the FAIL review predicted.

**Cleanup discipline**: `git status --short` checked before, during, and after — the entire dogfeed
run produced zero diff against the tracked/untracked file set already present before it started
(the same pre-existing concurrent-session files noted in the original task 11 Work Log — untouched
throughout). `Dependencies.WorkspacesRoot`'s real-`$HOME` exposure (the pre-existing, previously
flagged gap — not something this fix-up touched or introduced) again wrote one real session
workspace directory into the actual `~/.nanite/workspaces/`; removed it by hand after verification.
Sent `SIGTERM` to the scratch server for a clean shutdown and confirmed via `ps aux` zero leftover
`opencode acp`/`nanite-dogfeed11fix` processes afterward.

### Build/test status

`go build ./cmd/nanite/`: clean. `go vet ./...`: clean except the same two pre-existing, unrelated
`internal/service/container.go` findings every other task in this batch has already noted (confirmed
via `git status` that file is untouched by this fix-up's diff). `go test ./... -count=1`: clean
across every package, no regressions — including the two new test files
(`internal/runtime/agent/acp_session_test.go`, `internal/recovery/broker/
acp_exit_classification_test.go`) and the full pre-existing `internal/runtime/agent`/
`internal/recovery/broker` suites (including the original task 11 test files, still passing
unmodified).

### Known limitations (corrected from the original Work Log's version)

- The original `extractContent` doc comment's claim that flattening the thinking/message
  distinction "matches the native runtimeEventSink's own flattening of the same distinction" was
  **factually wrong** — the native path does not flatten this distinction (`wrapper_sink.go`'s
  `handleDelta` explicitly branches on it) — and has been removed/corrected in this fix-up.
- `copilotacp.Client` still does not emit an equivalent `KindProcessExited` signal today — a real,
  pre-existing `go-agent-wrapper` gap one level up, out of scope for this Nanite-side fix (per the
  task's own instruction). Handled gracefully here (confirmed both by unit test and by the code's
  own read-after-drain-returns ordering): a Copilot-CLI-driven session's `Wait()` always resolves to
  `nil` regardless of how the process actually died, same as before this fix, rather than hanging or
  misreporting. A future `go-agent-wrapper` task adding a real exit signal to `copilotacp.Client`
  would need no further Nanite-side change beyond what this fix already wires up — `handleEvent`'s
  `KindProcessExited` case and `handleProcessExited` are already provider-agnostic.
- The three other, pre-existing "Known limitations / follow-up candidates" from the original Work
  Log (no provider-session-id capture for ACP boots, Claude/Codex/Pi not selectable via
  `protocol="acp"` yet, `Dependencies.WorkspacesRoot`'s real-`$HOME` scratch-dogfeed exposure) are
  unaffected by this fix-up and still stand as originally documented.
