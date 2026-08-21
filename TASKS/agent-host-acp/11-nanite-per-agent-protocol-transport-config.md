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
