# Agent Host + ACP — Handoff to next work

**For:** whoever picks up related follow-on work next — most plausibly a "wire Claude/Codex/Pi
ACP into Nanite's own dispatch" task, an fs/terminal server if one is ever actually needed, or
portfolio-wide `go-agent-wrapper` adoption planning for Tether/Torque. Assume zero shared
context with this batch.

**Batch status:** all 23 tasks (`01`-`22`, including `05a` and the five dogfeed-found bug fixes
`18`-`22`, plus `23`) are `reviewed`/`implemented` per `TASKS/INDEX.md`. Every task where a first
pass found a real issue was fixed and re-verified before being marked done. This doc and
`PHASE-SUMMARY.md` are the closing artifacts task `17`'s own Work Log explicitly deferred to
("Dispatching doc-writer next... per the original kickoff's own closing instruction"). **Task
`23` was added after this doc's first draft**, closing a real gap the doc-writer's own diligence
surfaced (see below) — Claude/Codex/Pi are now genuinely ACP-selectable in Nanite, not just at
the sibling-library level.

---

## What actually shipped

**Phase 1 — host foundation, `libs/go-agent-wrapper` (sibling repo, not Nanite).** Small,
mechanical, foundational, exactly as planned. `go-agent-wrapper`'s `agentkit` pin bumped
`v0.1.0`→`v0.3.0` (zero source changes — `agentsessions`, the only agentkit subpackage the
wrapper imports, was byte-identical across those tags). `Descriptor.Runtime` (a bare string)
split into typed `Protocol`/`Transport`/`InterruptCapability` fields (`adapters/adapter.go`).
Tagged `v0.2.0`.

**Phase 2 — Nanite host migration (`internal/runtime/agent` onto `go-agent-wrapper`'s seams).**
The real lift, and where this batch diverged most from its original plan:

- Task `04` (bootdir → `plant.Planter`) and task `03` (dependency wiring) landed clean.
- Task `05` (sandbox profile → `sandbox.Applier`) hit a genuine architectural mismatch:
  `Applier.Apply(ctx, pid)` is confirmed a **post-spawn** attach-by-pid mechanism (four
  independent sources: the `Config.Sandbox` doc comment, `Wrapper.Run`'s actual call ordering,
  an integration test's own ordering assertion, and `go-sandbox.Apply`'s signature having no
  PID variant on any platform), not a pre-spawn hook — it does not fit `buildSandboxProfile`'s
  pre-spawn model. Closed with **zero code changes**: `wrapper.Config` already had a second,
  separate field (`SandboxProfile`) that is the actual pre-spawn seam Nanite needed. Folded the
  trivial call-site rewire into task `06`. See `TASKS/ESCALATIONS.md`, 2026-08-21, "Task `05`
  (sandbox.Applier migration)…".
- Task `06`'s **first attempt correctly stopped** rather than forcing a fit: `wrapper.Wrapper.Run`
  was structurally non-functional for all three of go-agent-wrapper's own real shipped adapters
  (Claude/Codex/OpenCode) — empirically reproduced with a throwaway repro test, not just read.
  The operator was presented three paths and chose to land a new prerequisite task, `05a`, in
  the sibling repo to fix the library gap (`WorkspaceDir`/`LogPath`/`SessionIDPreset`/
  `AutoFireFirstTurn` seams added to `wrapper.Config`, tagged `v0.3.0`). Task `06` then
  succeeded on retry (commit `1f947c55`, reviewed PASS) — and surfaced **one more** genuine
  library gap beyond `05a`'s four: no `Env` seam in `wrapper.Config`, meaning Codex/OpenCode
  would silently fall back to the operator's real global `~/.codex`/`~/.config/opencode`
  config (a sandbox-bypass risk) rather than the planted, sandboxed one. Worked around
  entirely in Nanite (no further library change needed) via a per-session `sh` wrapper script
  (`<bootDir>/.wrapper-exec/<provider>.sh`) that `exec env -i KEY=val ... /real/binary "$@"`s —
  see `TASKS/agent-host-acp/06-migrate-session-lifecycle-to-wrapper.md`'s Work Log §2 for the
  full reasoning and rejected alternatives.
- Task `07`'s live dogfeed against real Claude/Codex/OpenCode binaries found **five real bugs**
  (`18`-`22`), the most significant being a direct, confirmed hit on `16-agent-host.md`'s own
  named top risk: `agentkit`'s unsupervised waiter never constructed a real
  `*agentsessions.ExitError` on process kill, so `internal/recovery/broker`'s real-process-exit
  classification was silently dead code for every Nanite CLI session. Fixed in `agentkit`
  itself (`v0.4.0`→`v0.5.0`, task `20`) with explicit operator sign-off given the fix's
  six-consumer portfolio blast radius (Nanite, Tether, Torque, agent-mux, clockwork-manifold,
  Hadron).
- A **critical process finding**, caught before Phase 2 was declared done: Go's `replace`
  directives are not transitive. Every sibling-repo fix (`18`/`19`/`20`/`22`) verified clean in
  its own repo's isolated tests, but Nanite's actual compiled binary was still silently running
  the old buggy code, because the sibling libraries' own local `replace`s only apply when that
  library is the main module being built, and Nanite had no `replace`/version bump of its own
  pointing at the fixed commits. Resolved by pushing/tagging all three sibling libraries for
  real (`agentkit v0.5.0`, `go-providers v0.24.0`, `go-agent-wrapper v0.4.0`) and bumping
  Nanite's `go.mod` to pull all three directly with zero local `replace` directives (commit
  `83100a50`). **If you are ever debugging "a sibling-repo fix isn't taking effect," check this
  first** — it is not a one-off oddity of this batch, it's how Go module resolution actually
  works across this monorepo's `libs/` siblings.
- Phase 2 closed with a full re-run dogfeed (all five fixes confirmed working end-to-end
  against real binaries, including a real `kill -9` reaching the broker and spawning a usable
  replacement session) and a whole-section fresh review, PASS.

**Phase 3 — ACP client abstraction & native adapters.** Built `acp.Client`
(`Launch`/`Prompt`/`Cancel`/`Events`/`InterruptCapability`/`Close`) in `go-agent-wrapper`
(`v0.5.0`), then two real native ACP adapters: OpenCode via `opencode acp` (`v0.6.0`) and
Copilot CLI via `--acp`, including its genuinely real-but-undocumented TCP transport (`v0.7.0`,
found by testing — no `--help` flag documents it). Wired per-agent DB-configurable
Protocol/Transport selection into Nanite (migration `134`, `agent_profiles.protocol`/
`transport`). Task `11`'s first review **FAILed** on two real, unflagged bugs in the new parallel
ACP session backend it had to build (thinking-content leaking into the persisted answer instead
of routing to `EventThinking`; a crashed ACP subprocess never reaching the recovery broker
because `Session.Wait()` always returned `nil`) — fixed and re-reviewed PASS, with the
crash-detection fix live-verified via a real `kill -9`.

**Phase 4 — ACP bridge adapters for Claude/Codex/Pi.** Task `12` was a genuine,
escalation-gated design decision, not a routine task. Real research found the field had moved
since planning (`zed-industries/codex-acp` was archived in favor of
`agentclientprotocol/codex-acp`; a new Pi bridge, `svkozak/pi-acp`, appeared that didn't exist
at planning time). A real operator conversation **reframed the deciding criterion mid-discussion**:
real interrupt capability turned out not to be the blocker ("we run today without that
capability... it's not a blocker") — the actual goal is ACP protocol uniformity for future
config-based provider extensibility with an honest per-adapter capabilities map. The operator
chose three per-provider bridges (`claude-agent-acp`, `codex-acp`, `pi-acp`) over the dormant
multi-provider alternative (`beyond5959/acp-adapter` — 4+ months no commits, source-verified
bare-SIGKILL Claude backend, self-rated "Initial" Pi maturity), explicitly as a **reversible**
choice given the real Node.js/npm runtime dependency it introduces for those three specific
providers. All three bridge adapters were built and live-verified against real binaries/APIs.
Task `15` (Pi) is worth calling out specifically: the `pi` CLI wasn't installed and no cloud
credentials were available on the verification machine, so the worker installed it for real and
wired it to a local Ollama instance to get genuine live verification (real turn, real
mid-generation cancel at 11-15ms) rather than reporting a blocker.

**Phase 5 — verification & hardening.** Task `16` audited all five real ACP adapters for
filesystem/terminal proxying needs — clean negative finding, no in-process fs/terminal server
needed, no new scope. Task `17` ran a real side-by-side comparison (native Claude vs.
ACP-bridged Claude) across activity fidelity, interrupt fidelity, tool reporting, and latency —
found a real, interesting gap (native Claude's `stream-json` mode delivers one whole-message
delta per turn, not token-level streaming, while the ACP bridge streams genuinely
incrementally) without making any default-protocol decision, per its own explicit scope fence.

---

## A real gap discovered while preparing this handoff — found and closed same-day by task `23`

**Claude/Codex/Pi's ACP bridge adapters were built, released, and live-verified at the
`libs/go-agent-wrapper` library level — but Nanite itself could not launch an agent through any
of them.** This was a genuine, checkable fact at the time this doc was first drafted, confirmed
directly against the current source (not inferred from any task file's narrative), and it was
not mentioned in `TASKS/INDEX.md`, `TASKS/ESCALATIONS.md`, or any task file's own "Follow-up
candidates"/"Known limitations" section — every one of those described Phase 4 as "complete" and
"unblocked" without this caveat, which read as stronger than what was actually configurable in
Nanite. Confirmed by:

1. `internal/runtime/agent/acp_session.go`'s `newACPClient` function had a hardcoded
   `acpSupportedProviders` map containing only `"opencode"` and `"copilot"`.
2. Nanite's `go.mod` pinned `github.com/hollis-labs/go-agent-wrapper v0.7.0`, never bumped past
   task `10`'s tag.
3. `git log --oneline -- go.mod` showed the last touch to that file was task `11`'s commit — no
   commit after Phase 4/5 landed ever revisited it.

**Root cause, confirmed**: task `11` (the only task in this batch that wires an ACP adapter into
Nanite's own dispatch table) is Phase 3, implemented/reviewed *before* Phase 4's bridge adapters
(`13`-`15`) existed — it could only wire what existed at the time (`09`/`10`). No task after `11`
was ever dispatched to revisit `acpSupportedProviders` once the bridge adapters landed, and
Phase 5's two tasks (`16`, `17`) verify library-level adapter behavior directly (bypassing
Nanite's HTTP surface, by design), so neither would have surfaced this from its own verification
method. Each task did exactly what its own `Touches:`/`Repo:` line said (Phase 4's tasks are
explicitly scoped `Repo: libs/go-agent-wrapper (sibling, NOT Nanite)`, with no Nanite-wiring
step named in any of them) — this was a genuine seam between batches' scope, not a dropped task.

**Closed same-day by task `23`** (`TASKS/agent-host-acp/23-wire-claude-codex-pi-acp-bridge-dispatch.md`,
commit `9ddda4748f5f401c8b2acbfb61e93928d0077ebf`), which also found and fixed a **second**,
previously-unknown bug while closing this one: the `go-agent-wrapper` `v0.8.0` tag itself was
broken — cut on the `piacp` feature-branch tip, which forked from `main` *before* the
`claudeacp`/`codexacp` merges landed, so its tree never actually contained those two packages
(confirmed via `git ls-tree` diffing `v0.8.0` against `v0.8.1`). Fixed by cutting `v0.8.1` on
`origin/main` HEAD and pushing it, rather than moving the already-public `v0.8.0` ref. Nanite's
`go.mod` now pins `v0.8.1`; `acpSupportedProviders` now includes all five providers; Claude and
Codex are live-verified end-to-end through Nanite's own harness API (real bridge subprocess,
real turn, real `Stop`). Pi's dispatch wiring is implemented and unit-tested but not
live-re-verified — it hits a separate, pre-existing, unrelated gap: `cmd/nanite/main.go`'s
hardcoded `cliAdapters` list has no `"pi"` entry (Pi has no prior native adapter in this repo),
so a Pi agent never reaches `bootACP`/`newACPClient` at all. That remaining gap is a real,
actionable follow-up (see below) — the ACP-dispatch gap this section originally described is
fully closed for Claude and Codex, and correctly wired (pending an unrelated upstream fix) for
Pi.

---

## Concrete, checkable verification steps

Don't just trust `TASKS/INDEX.md`'s status column — these are specific, independently
verifiable facts about what landed.

**Sibling repo state (`libs/go-agent-wrapper`, `libs/agentkit`, `libs/go-providers`):**
- `cd libs/go-agent-wrapper && git tag --sort=-v:refname | head -1` → expect `v0.8.1` (**not**
  `v0.8.0` — that tag is broken, see task `23`'s Work Log: it was cut on the `piacp` branch tip
  before the `claudeacp`/`codexacp` merges landed on `main`, so its tree is missing both).
- `find libs/go-agent-wrapper/adapters -maxdepth 1 -type d` → expect eight adapter packages:
  `claude`, `claudeacp`, `codex`, `codexacp`, `copilotacp`, `opencode`, `opencodeacp`, `piacp`.
- `libs/go-agent-wrapper/acp/client.go` → the `Client` interface
  (`Launch`/`Prompt`/`Cancel`/`Events`/`InterruptCapability`/`Close`).
- `libs/go-agent-wrapper/sidebyside/` → task `17`'s comparison harness
  (`claude_live_test.go`, `interrupt_live_test.go`), real `go test`-with-skip files.
- `cd libs/agentkit && git describe --tags` → expect `v0.5.0` or later (task `20`/`22`'s fixes).
- `cd libs/go-providers && git describe --tags` → expect `v0.24.0` or later (task `19`'s fix).

**Nanite's own state:**
- `grep -n 'hollis-labs/go-agent-wrapper\|hollis-labs/agentkit\|hollis-labs/go-providers' go.mod`
  → expect `go-agent-wrapper v0.8.1` (task `23`'s bump — closes the gap described above),
  `agentkit v0.5.0`, `go-providers v0.24.0`, and **zero local `replace` directives for any of the
  three** (the "replace is not transitive" fix from Phase 2's close-out).
- `find internal/store/migrations -maxdepth 1 -name '134_*'` → expect
  `134_agent_profiles_protocol_transport.sql`, which adds `agent_profiles.protocol` (CHECK
  `claude-stream-json`/`codex-app-server`/`opencode-native`/`acp`) and `agent_profiles.transport`
  (CHECK `stdio`/`tcp`).
- `internal/runtime/agent/wrapper_adapter.go` → `nativeAdapter`, `envWrappedCLIAdapter`,
  `wrapEnvForSpawn` (task `06`'s Nanite-owned `Env`-seam workaround).
- `internal/runtime/agent/agent_acp.go` + `acp_session.go` → the parallel ACP `Session` backend
  task `11` built (`bootACP`, `acpSession`, `newACPClient`'s `acpSupportedProviders` map — check
  this directly to see today's real provider coverage, don't assume from the docs).
- `internal/runtime/agent/factory.go` → `useACPProtocol`/`effectiveACPTransport` predicates.
- `internal/recovery/broker/*` → confirm zero diff against pre-batch `main`
  (`git log --oneline -- internal/recovery/broker/` — the last touching commit should predate
  this batch's Phase 2 work; this was a hard constraint every Phase-2 task preserved and every
  reviewer independently re-checked).
- `go build ./cmd/nanite/ && go vet ./... && go test ./...` — should be clean except two
  pre-existing, unrelated `internal/service/container.go` vet findings
  (`stopReaper`/`stopRuntimeReaper`, confirmed pre-dating this batch by every task that touched
  nearby code).

**Behavioral/live checks**, if you want to re-confirm rather than just read code:
- Boot a Claude, Codex, or OpenCode agent through the normal chat path — should work unchanged
  (native protocol is still the default for every existing agent; migration was additive).
- Configure an agent with `protocol=acp`, `provider=opencode`/`copilot`/`claude`/`codex` via
  `POST /api/agents` — should launch through the matching bridge/native adapter and complete a
  real turn (all four are live-verified through Nanite's own harness API as of task `23`).
  `provider=pi` with `protocol=acp` is wired the same way but not reachable yet — Pi has no entry
  in `cmd/nanite/main.go`'s `cliAdapters` list, so it fails upstream of `newACPClient` with a
  provider-not-recognized error, not the old "protocol=acp not supported" error. That's a
  separate, real follow-up (see below), not a regression.
- `kill -9` a live CLI subprocess mid-session — `internal/recovery/broker` should classify the
  exit and dispatch a replacement session (this is task `20`'s fix; confirmed working in Phase
  2's final re-run and Phase 2's whole-section review).

---

## What the next phase should know that wasn't in the original plan

- **The `go-agent-wrapper`/`acpSupportedProviders` gap described above is now closed (task
  `23`)** for Claude and Codex, live-verified end-to-end. **One real, actionable follow-up
  remains for Pi**: `cmd/nanite/main.go`'s hardcoded `cliAdapters` list has no `"pi"` entry (Pi
  never had a prior native adapter in this repo), so a Pi agent's boot request fails before it
  ever reaches `bootACP`/`newACPClient` — task `23`'s own dispatch-layer fix for Pi is correct
  but currently unreachable. Adding a `"pi"` entry to `cliAdapters` (mirroring how `opencode`/
  `copilot` are already registered there) should be enough to close it; worth a small, low-risk
  follow-up task before anyone tries to configure a Pi agent through Nanite.
- **Go `replace` directives are not transitive across this monorepo's sibling `libs/` repos.**
  A fix landing and testing clean in its own sibling repo does not mean it's reaching a
  dependent app's compiled binary — check the dependent app's own `go.mod` for a stale pin or a
  leftover local `replace` before trusting a cross-repo fix is live. This was a real, costly
  near-miss in this batch (Phase 2 was nearly marked `validated` on a build that was silently
  still running the old buggy code) and is very likely to bite the next cross-repo batch too.
- **`go-providers.CLIAdapter`'s contract has no seam for a bidirectionally-real,
  response-correlated JSON-RPC session once `agentkit` owns the spawned process's stdin.**
  Discovered independently by tasks `09`, `10`, `13`, `14`, and `15` (five separate times, same
  root cause each time) — `BuildArgs` runs before the child exists, `ParseLine` is read-only.
  Every ACP adapter in this batch works around it by owning its subprocess directly rather than
  going through `wrapper.Wrapper.Run()`'s normal composition (confirmed working via `acp.Client`
  driven directly from Nanite's `acp_session.go`, not through `Wrapper.Run`). A `wrapper.Wrapper`
  `JsonRpcCall`-style passthrough (mirroring `agentkit/agentsessions.Manager.JsonRpcCall`) is a
  real, repeatedly-flagged follow-up candidate that would let *any* jsonrpc-stdio-shaped
  adapter — including the pre-existing Codex app-server adapter, which has the identical,
  independent gap — be driven through the normal `Wrapper.Run()` path instead of a bespoke
  parallel backend. Not built in this batch; flagged five separate times in Work Logs
  (`09`, `10`, `13`, `14`, `15`) as out of scope.
- **A minor, optional robustness gap**: `copilotacp`'s `handleLine` answers every
  server-initiated request (including `session/request_permission`) with a generic JSON-RPC
  error, unlike its four sibling adapters, which give `session/request_permission` a graceful
  ACP-native "cancelled" deny. Not live-verified (blocked by a real, still-present Copilot CLI
  account quota exhaustion during task `16`'s audit) — a real, low-risk, optional fix (mirror
  `opencodeacp/translate.go`'s `handleServerRequest` pattern), not gating anything.
- **Whether/how Tether/Torque adopt `go-agent-wrapper`** is explicitly out of this batch's scope
  per its own README — a portfolio-level call for those apps' own planning, not decided or
  even leaned on here.
- **A pre-existing, unrelated flaky test**: `TestRunEndToEndAdapterRuntime` in
  `libs/go-agent-wrapper/wrapper/wrapper_integration_test.go` — intermittent "sequence not
  monotonic" failures, confirmed non-deterministic and unrelated to this batch's work by
  multiple independent workers/reviewers reproducing it on a clean pre-batch base commit. Never
  fixed (out of scope); logged in that repo's own `CHANGELOG.md`, not filed as a Nanite
  escalation.
- **A pre-existing, unrelated `internal/service` combined-package `-race` test timeout** — `go
  test ./internal/runtime/agent/... ./internal/recovery/... ./internal/service/... -race
  -count=1` does not complete within Go's default 10-minute timeout, isolated to
  `internal/service` alone, reproduced identically on the pre-batch base commit. Not a
  regression from this batch; nobody has let it run past the default timeout to determine
  whether it's slow-but-convergent or a real goroutine leak. Worth a small standalone ticket.
- **The `beyond5959/acp-adapter` bridge library was explicitly rejected** but the decision is
  framed by the operator as reversible if it matures — `acp.Client`'s interface already
  isolates every caller from which concrete bridge implementation sits behind it, so revisiting
  this later (or swapping any of the three pinned bridges) is a new implementation, not a
  rearchitecture.
- **Two other uncommitted, unrelated artifacts** were noticed sitting in the shared checkout
  during Phase 2's review (a new `docs/engineering/architecture/19-api-cli-runtime-parity.md`
  and a `docs/launch-site/` directory, from a different concurrent session) — correctly left
  untouched by three separate points in this batch's own work, flagged to the operator directly.
  If they're still there, they're not this batch's and shouldn't be assumed related to it.
