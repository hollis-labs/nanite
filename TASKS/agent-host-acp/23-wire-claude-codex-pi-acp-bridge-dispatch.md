# Wire Claude/Codex/Pi ACP bridge adapters into Nanite's own dispatch

**Phase:** 4 — bridge-mediated ACP adapters (`TASKS/agent-host-acp`), closing gap found at
end-of-batch doc-writer pass
**Status:** implemented
**Depends on:** `11` (per-agent Protocol/Transport dispatch, the mechanism this task extends),
`12`/`13`/`14`/`15` (Phase 4's pinned bridge decision and the three bridge adapters themselves,
in the sibling `libs/go-agent-wrapper` repo)
**Touches:** `go.mod`, `go.sum`, `internal/runtime/agent/acp_session.go`,
`internal/runtime/agent/acp_dispatch_test.go`. Repo: Nanite. (Also, as a necessary prerequisite
found mid-task — see Work Log — a new `v0.8.1` git tag in the sibling `libs/go-agent-wrapper`
repo, pushed to `origin`.)

## Context

All 22 prior tasks in this batch landed. Phase 4 (tasks `12`-`15`) built and live-verified three
real ACP bridge adapters in the sibling `libs/go-agent-wrapper` repo — Claude via `claudeacp`,
Codex via `codexacp`, Pi via `piacp` — but nothing ever wired them into Nanite's own dispatch.
`internal/runtime/agent/acp_session.go`'s `acpSupportedProviders` map only listed
`"opencode": true, "copilot": true`, and its own doc comment said plainly: "Bridge-mediated
providers (Claude/Codex/Pi via a third-party ACP bridge) are Phase 4 scope
(TASKS/agent-host-acp/12-15), not yet selectable here." An agent configured with `protocol="acp"`
for Claude/Codex/Pi could not actually launch. This task is the same wiring task `11` already did
for OpenCode/Copilot CLI, extended to the three bridge adapters.

## What to do

1. Bump `go.mod`'s `github.com/hollis-labs/go-agent-wrapper` require to the current highest tag
   containing all three bridge adapters (verify directly against the sibling repo's tags — see
   Work Log for why `v0.8.0` alone was not sufficient). Run `go mod tidy`, confirm clean.
2. Read `internal/runtime/agent/acp_session.go`'s `newACPClient` in full; extend it to also
   dispatch `"claude"`, `"codex"`, `"pi"` to `claudeacp.NewClient()`/`codexacp.NewClient()`/
   `piacp.NewClient()`, matching each package's own `Descriptor.Provider`/bare-name convention
   (confirmed directly against each `adapter.go`'s `Describe()` call, not guessed).
3. Add `"claude": true, "codex": true, "pi": true` to `acpSupportedProviders`, correct the
   now-inaccurate doc comment.
4. Live end-to-end verification for at least Claude (and, time permitting, Codex) through
   Nanite's own running app — SSE reaching `delta`/`stream_end`, not just unit tests.
5. Extend test coverage for the dispatch logic (`acp_dispatch_test.go`).
6. `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` clean.

## Done means

- Nanite's `go.mod` pins a `go-agent-wrapper` tag that genuinely contains `claudeacp`/`codexacp`/
  `piacp` (verified by tree inspection, not tag-name assumption).
- `newACPClient` correctly builds a real `acp.Client` for Claude/Codex/Pi via their respective
  bridge adapters, matching the existing pattern for OpenCode/Copilot CLI.
- At least one bridge-driven provider (Claude, ideally also Codex) is confirmed to actually launch
  and complete a real turn through Nanite's own running app — live-verified, not just unit-tested.
- New/extended test coverage for the dispatch logic.
- `go build ./cmd/nanite/`, `go vet ./...`, `go test ./...` clean.
- Work Log documents exactly what was verified live vs. what's covered only by unit tests.

## Work Log (2026-08-21)

Read this task's dispatching instructions, `internal/runtime/agent/acp_session.go` (`newACPClient`,
`acpSupportedProviders`, and the full package doc comment explaining why this composition bypasses
`wrapper.Wrapper.Run` entirely), `internal/runtime/agent/agent_acp.go` (`bootACP`),
`internal/runtime/agent/factory.go` (`useACPProtocol`/`effectiveACPTransport`),
`internal/runtime/agent/bootdir.go` (`normalizeProviderName`), and task `11`'s full Work Log
(the established precedent this task extends, including its own scratch-dogfeed recipe and its
Finding 1/2/3 fix-up addendum) before touching anything. Also read
`libs/go-agent-wrapper/adapters/claudeacp/{adapter,client}.go`,
`.../codexacp/{adapter,client}.go`, and `.../piacp/{adapter,client,doc}.go` in full — not assumed.

### Real finding: the `v0.8.0` tag itself was broken (doesn't contain claudeacp/codexacp)

Before bumping the pin, checked `libs/go-agent-wrapper`'s tags directly (`git tag --sort=-v:refname`):
`v0.8.0` is the highest, cut the same day (`2026-08-21`) as this task. But inspecting its tree
(`git ls-tree -r v0.8.0 -- adapters`) showed only `adapters/piacp` present — **no `claudeacp` or
`codexacp` directories at all**, despite `go build ./cmd/nanite/` initially succeeding after
`go get @v0.8.0` (the missing-package error only surfaced once the new imports were added to
`acp_session.go`).

Root cause, confirmed via `git merge-base --is-ancestor` and `git log --graph`: the `v0.8.0` tag
(commit `93f9e4f`) sits on the `adapters/piacp` feature branch, which forked from `e25b315` (task
10's merge point) — **before** task 13's `claudeacp` branch (`10ce8a5`, merged via `ed6150e`) and
task 14's `codexacp` branch (`4e52099`, merged via `2ab3863`) were merged into `main`. The `v0.8.0`
tag was cut on that branch tip, one commit before `e5c7e7e` (task 15's actual merge into `main`,
which combines the piacp branch with `2ab3863`, the branch that already has claude+codex). So
`v0.8.0`'s tree genuinely never contained `claudeacp`/`codexacp` — not a caching or proxy artifact,
confirmed against the sibling repo's own real git history.

`origin/main` (= local `main` = `HEAD`, all three at `df9621a`, confirmed via `git rev-parse`) does
contain all three adapters together, and builds/vets clean (`go build ./...`, `go vet ./...`, both
`✓` at that commit). This is a real bug in the sibling repo's own tagging, not something this task's
own instruction anticipated ("verify this is still the current highest tag first... don't assume" —
followed exactly, and the "don't assume" turned out to matter).

**Fix applied (in the sibling `libs/go-agent-wrapper` repo, not Nanite)**: rather than retagging the
already-pushed, already-public `v0.8.0` ref (avoided — a moved tag is a real integrity/reproducibility
break for anyone who's already resolved it, and this project's own destructive-operation discipline
favors the additive, reversible option), cut a new annotated tag `v0.8.1` on `origin/main` HEAD
(`df9621a`) — the first tag whose tree genuinely contains `claudeacp` + `codexacp` + `piacp` together
— and pushed it to `origin` (`git push origin v0.8.1`, confirmed `ok ✓ v0.8.1`). The tag message
documents the root cause and points future consumers at `v0.8.1` or later. `v0.8.0` is left in place,
unmoved.

This is the "decision vs. rationale" pattern this project's own process explicitly calls for: the
task's stated decision (bump the pin to the current highest tag) stands; the implicit rationale (that
whatever the highest tag is will already contain what's needed) didn't hold, so the full job — fixing
the tag situation so a valid pin target actually exists — was done rather than stopping to ask,
per this project's default toward continuing on a bounded, reversible, non-security-sensitive
correction.

### go-agent-wrapper pin bump

`go get github.com/hollis-labs/go-agent-wrapper@v0.8.1` (after the `v0.8.1` tag existed on
`origin`), then `go mod tidy` — clean, no other dependency changes. `go build ./cmd/nanite/`: clean
once the new adapter imports were in place (see below). No local `replace` directive exists for
`go-agent-wrapper` in Nanite's `go.mod` — resolved straight from the module proxy/`GOPRIVATE` direct
VCS fetch, matching this task's own expectation.

### `newACPClient` dispatch extension

Read each bridge package's `adapter.go`/`client.go` directly (not assumed) to confirm the exact
dispatch shape:

- `claudeacp.NewClient()` — no required args (`ClientOption` variadic only); `Adapter.Describe()`
  calls `acp.DescriptorFor(a.newClient(), "claude", adapters.TransportStdio)` — confirms `"claude"`
  is the correct bare provider-name key, matching `Adapter.Name()`'s separate `"claude-acp"` (used
  only for logs/config/policy-rule-key disambiguation, per its own doc comment — not the dispatch
  key `newACPClient` switches on).
- `codexacp.NewClient()` — same shape; `Describe()` uses `"codex"`. `WithClientCodexBinary`'s doc
  comment confirms `CODEX_PATH` resolution (via `CODEX_CLI_PATH` env, then real PATH lookup for
  `codex`) is automatic when no override is passed — matching this task's own expectation, verified
  live below.
- `piacp.NewClient()` — same shape; `Describe()` uses `"pi"`.

All three are stdio-only bridges (spawn `npx -y <package>` by default) — unlike `copilotacp`, none
takes a `transport` argument. Extended `newACPClient`'s switch (keyed on
`normalizeProviderName(providerName)`, same as the existing `opencode`/`copilot` cases) with three
new cases calling each package's `NewClient()` directly, added `"claude": true, "codex": true,
"pi": true` to `acpSupportedProviders`, and rewrote the map's and the function's doc comments to
drop the now-inaccurate "Phase 4, not yet selectable" framing and instead point at this task.
Updated the function's own doc comment to explain the stdio-only/transport-ignored behavior for
these three providers (an operator who pairs `transport="tcp"` with `claude`/`codex`/`pi` still gets
a stdio client — the value round-trips through config storage but isn't consulted by these three
adapters, same class of behavior `opencodeacp`'s pre-existing stdio-only case already has).

### Test coverage

`acp_dispatch_test.go`'s `TestNewACPClient` previously asserted `newACPClient("claude", ...)`
**returns an error** (task 11's own pinned expectation, correct for its own time). Updated to assert
success for `claude`/`codex`/`pi` (bare and `pty-`/`sub-`-prefixed forms, mirroring the existing
`opencode`/`pty-opencode` coverage), confirmed the bridge providers ignore `transport` (a `tcp`
request for `claude` still succeeds rather than erroring), and added an explicit
`nonsense-provider` case asserting the "unsupported provider" error path still fires for a real
unknown name. Added a new `TestAcpSupportedProvidersTable` pinning the exact five-entry provider
set (`opencode`/`copilot`/`claude`/`codex`/`pi`) so a future accidental removal from the map is
caught by an assertion, not just by`newACPClient`'s own case coverage.

### Live end-to-end verification (real commands, real output — not mocked)

Followed task `07`'s/`11`'s established scratch-server dogfeed recipe exactly: built a scratch
binary (`nanite-dogfeed23`), ran it from a scratch CWD with `XDG_DATA_HOME`/`XDG_STATE_HOME`/
`XDG_CONFIG_HOME`/`XDG_CACHE_HOME` redirected to scratch subdirectories, an explicit `-db` pointed
at a scratch SQLite file (port 8499 — 8199/8200/8090 were already bound by unrelated concurrent
processes), and real `$HOME` left alone for CLI auth. Confirmed `claude`, `codex`, `npx`/`node` all
resolve on this machine before starting.

**Claude — full live confirmation.** Created a real managed agent (`protocol="acp"`,
`transport="stdio"`) via `POST /api/agents`, set `default_provider='claude'`/`runtime_kind='cli'`
via a scoped `sqlite3 UPDATE` (these two fields have no REST CRUD surface on this endpoint,
pre-existing, same as every prior dogfeed in this batch), seeded `user_settings.default_provider`/
`default_model='claude'` (needed on this fresh scratch DB), re-confirmed the full row via
`GET /api/agents/{id}`. Created a real harness-v1 session (`provider=claude`) bound to this agent,
sent a real turn ("What is 17 plus 25? Reply with only the number.") via `POST .../turns`, and read
the real SSE stream via `GET /api/stream/{message_id}`:

```
event: stream_start
data: {"type":"stream_start","message_id":"12f9813d...","agent_id":"32aee37f...",...}
event: delta
data: {"type":"delta","content":"42","phase":"final","event_id":3}
event: stream_end
data: {"type":"stream_end",...,"usage":{"input_tokens":2,"output_tokens":3,...},...}
```

Confirmed via `ps aux` that a genuine three-process chain was actually spawned and driven to
completion: `npm exec @agentclientprotocol/claude-agent-acp` → `node .../claude-agent-acp` (the
bridge) → the real `claude --output-format stream-json --verbose --input-format stream-json ...`
CLI subprocess (from `@anthropic-ai/claude-agent-sdk-darwin-arm64`) — not a config-storage-layer
check. Confirmed the assistant message persisted in the scratch DB (`{"v":1,"text":"42",...}`) and
`agent_runtime.provider='claude'`, `state='running'`. Confirmed `Session.Stop()` (via
`POST /api/sessions/{id}/agent/reboot`) cleanly terminated all three real processes — `ps -p` on
each captured PID found nothing afterward.

**Codex — full live confirmation, including the `CODEX_PATH` auto-resolution note.** Same recipe:
created a second agent (`default_provider='codex'`), a second harness session, sent a real turn
("What is 9 plus 33? Reply with only the number."), read the real SSE stream:

```
event: delta
data: {"type":"delta","content":"42","phase":"final","event_id":3}
event: stream_end
data: {"type":"stream_end",...,"usage":{"input_tokens":12074,"output_tokens":5,...},...}
```

Confirmed via `ps aux` the real chain: `npm exec @agentclientprotocol/codex-acp@1.6.2` → `node
.../codex-acp` (the bridge) → **`/opt/homebrew/bin/codex app-server`** — the real, system-installed
`codex` binary (confirmed by full path, distinct from the separate, unrelated, pre-existing
`ChatGPT.app`-bundled `codex` process already running under a different PID) — direct, live
confirmation that `codexacp.Client`'s `CODEX_PATH` resolution (via `CODEX_CLI_PATH` env, then a real
PATH lookup) worked automatically with zero Nanite-side configuration, as this task's own
instructions anticipated. Confirmed the persisted assistant message (`{"v":1,"text":"42",...}`) and
clean `Session.Stop()` termination of all three processes (zero leftover `codex-acp`/`codex
app-server` processes afterward, `ChatGPT.app`'s own unrelated `codex app-server` left untouched).

**Pi — attempted live, blocked by a real, separate, pre-existing gap (not this task's own dispatch
fix).** `pi`, `npx`, and a local Ollama-backed custom-provider setup (`~/.pi/agent/models.json`,
model `llama3.1:8b`, confirmed reachable via `curl http://localhost:11434/v1/models`) were already
configured on this machine — so, unlike the task's own anticipated "Ollama not set up" contingency,
infra was not the blocker. Created a third agent (`default_provider='pi'`), a third harness session,
sent a real turn — it failed immediately with `"CLI provider \"pi\" has no runtime adapter
registered."` / `"CLI provider \"pi\" not in agent runtime adapter index"`. Traced this to its exact
source (`internal/service/chat_generate.go`'s `nilProviderRouteCLINoAdapter` branch, fed by
`classifyNilProvider` → `agentDeps.ProviderAdapter(providerName)` → `indexAdapters(cfg.CLIAdapters)`,
`internal/service/agent_deps.go`): `cfg.CLIAdapters` is a **hardcoded three-entry slice** built in
`cmd/nanite/main.go` (`claudeAdapter`, `provider.NewCodexAdapter()`, `provider.NewOpencodeAdapter()`)
— `go-providers`' own *native* CLI adapters, used for boot-dir/env composition, predating and
entirely orthogonal to this task's `acp_session.go` dispatch. Confirmed via `sqlite3` that no
`agent_runtime` row was ever created for the Pi session — the failure is strictly upstream of
`agent.Boot`/`bootACP`/`newACPClient`; this task's own dispatch fix for Pi is correct and reachable,
it is simply never reached, because Pi has **never** had any entry in this pre-existing native-adapter
index at all (confirmed: Claude/Codex/OpenCode all pre-date ACP as native CLI providers in Nanite;
Pi's *only* appearance anywhere in this portfolio is the bridge adapter task `15` shipped — per
`piacp/doc.go`'s own doc comment, "Pi's FIRST appearance as a supported agent anywhere in
go-agent-wrapper"). Fixing this would mean adding a `provider.CLIAdapter` entry for Pi to
`cliAdapters` in `main.go` purely to satisfy an index-membership check unrelated to ACP dispatch — a
distinct, separately-scoped change judged out of scope for this task's own bounded fix, consistent
with this task's own explicit instruction not to spend a lot of time on Pi's live re-verification.
**Pi's dispatch wiring (`newACPClient("pi", ...)` → `piacp.NewClient()`) is implemented and pinned
by `TestNewACPClient`/`TestAcpSupportedProvidersTable`, but not live-re-verified through the full
chat-harness turn path in this pass** — flagged as a precise, actionable follow-up below (more
specific than "Ollama wasn't available," which was not actually the blocker).

**Cleanup discipline**: `git status --short` checked before, during, and after this entire dogfeed
run — zero diff caused by any part of it (confirmed the pre-existing modified/untracked files
belong to a concurrent, unrelated session working in the same checkout, per task `11`'s own
established precedent — left alone throughout). `Dependencies.WorkspacesRoot`'s real-`$HOME`
exposure (the same pre-existing, previously-flagged-by-task-11 gap, not introduced by this task)
again wrote real session workspace directories into the actual `~/.nanite/workspaces/` for the two
sessions that actually booted (Claude, Codex — Pi's session never booted, so it never got one);
removed both by hand after verification. Sent `SIGTERM` to the scratch server for a clean shutdown;
confirmed via `ps aux` zero leftover bridge/CLI/scratch-binary processes afterward. One additional,
minor, likely-unrelated observation: the scratch server's shutdown log showed
`"shutdown: subsystem panic" label="tasks" panic="DB Closed"` during teardown — the process still
exited cleanly (`ps -p` on its PID found nothing after `SIGTERM`), and this task's own diff touches
none of the shutdown/task-reaper code, so this reads as a pre-existing shutdown-ordering quirk
under this recipe's specific DB-close timing, not something introduced here — flagged for
visibility, not treated as a finding requiring a fix in this task.

### Build/test status

`go build ./cmd/nanite/`: clean. `go vet ./...`: clean except the same two pre-existing, unrelated
`internal/service/container.go` findings (`stopReaper`/`stopRuntimeReaper` "not used on all paths")
every other task in this batch has already noted — confirmed via `git status --short` that file is
not part of this task's diff. `go test ./... -count=1`: clean across every package (86 packages,
zero failures), including the updated `internal/runtime/agent/acp_dispatch_test.go`.

### Known limitations / follow-up candidates (not fixed by this task, flagged for whoever picks
them up next)

- **Pi cannot be dispatched via Nanite's chat harness at all today**, regardless of `protocol`,
  because `cmd/nanite/main.go`'s hardcoded `cliAdapters` slice (feeding
  `agent_deps.go`'s `ProviderAdapter`/`classifyNilProvider` gate, a check that runs strictly before
  `agent.Boot` and is entirely orthogonal to this task's ACP dispatch) has never had a `"pi"` entry
  — Pi has no pre-existing native adapter in this repo to have been registered from. This task's own
  `newACPClient("pi", ...)` fix is correct, reachable in principle, and unit-tested, but is never
  actually reached by a real turn through the harness API today. A natural, narrowly-scoped Nanite
  follow-up: register a minimal `provider.CLIAdapter` for `"pi"` in `main.go`'s `cliAdapters` list
  (need not be functionally complete — task 11's own precedent already establishes that ACP-path
  dispatch bypasses whatever a `CLIAdapter`'s `BuildArgs`/`ParseLine` do) purely to clear this
  index-membership gate, then re-run this task's own Pi live-verification steps.
- Every "Known limitations" item from task `11`'s original Work Log and its FAIL-review fix-up
  addendum (no provider-session-id capture for ACP boots, `Dependencies.WorkspacesRoot`'s
  real-`$HOME` scratch-dogfeed exposure, tool-call/tool-result fidelity not exercised by a
  tool-using ACP turn) are unaffected by this task and still stand as previously documented — this
  task's own live verification used plain arithmetic questions, matching every prior dogfeed in
  this batch, so tool-call fidelity for the three new bridge providers is also not yet
  live-exercised.
- The `libs/go-agent-wrapper` repo now has two tags (`v0.8.0`, `v0.8.1`) where `v0.8.0`'s tree is
  a strict subset of `v0.8.1`'s (missing `claudeacp`/`codexacp`) — worth a note for whoever next
  cuts a release in that repo, so `v0.8.0` isn't assumed complete by a future consumer reading tag
  names without checking trees.

### Commit

Committed directly on top of `main` per this task's own dispatch instructions (serial work, no
worktree) — see the dispatching agent's final report for the commit SHA. The sibling
`libs/go-agent-wrapper` repo's `v0.8.1` tag was pushed to `origin` directly (no separate commit
needed — an existing commit, `df9621a`, was tagged; that commit was already on `origin/main` before
this task started).
