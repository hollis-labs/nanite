# Wave 6 handoff - for the Wave 7 orchestrator

**Audience: a fresh session with zero memory of Wave 6.** Wave 6 is closed.
All 16 tasks are `reviewed`; none is in progress or blocked. Wave 6a
closed the semantic-divergence and migration-drift items under AD-19, AD-20,
and AD-29. Wave 6b then closed the mechanical and boilerplate duplication
items.

The working tree that produced this handoff is not committed in this checkout.
Trust the task files, `TASKS/INDEX.md`, and `findings.json` as the durable
tracking record; do not infer commit boundaries from local history until this
wave is committed.

---

## 1. What shipped

| Task | Final outcome | Production impact |
|---|---|---|
| `11/01` | Migrated `resolveSubagentCompletionPolicy` onto the shared `override.Resolve` cascade. | Shared override semantics now cover both message wake and subagent completion policy while preserving separate defaults. |
| `11/02` | Consolidated Harness v1 durable-agent start/resume/wake handlers onto native handlers. | One implementation now serves both route families; native wake HTTP coverage was added. |
| `11/05` | Extracted the duplicated MCP post-`CallTool` result-processing tail. | `Manager.ExecuteTool` and `ExecuteToolOnServer` now share result assembly, trust-tier size validation, and span attributes. |
| `11/06` | Unified OpenAI and Anthropic malformed streamed tool-call JSON behavior. | Both providers now gracefully preserve malformed JSON as `{"_raw": ...}` and continue to usage/`EventDone`. |
| `11/07` | Confirmed this folder's SSRF CIDR item is cross-reference only. | No code here; real consolidation remains the already-reviewed `08/09` AD-28 work. |
| `11/08` | Applied AD-20's bounded rename inside `internal/config`. | `config.Config` became `config.RuntimeConfig`; `config.AppConfig` became `config.TunablesConfig`; on-disk paths were unchanged. |
| `11/09` | Applied AD-29's delete decision for dead client-side elicitation code. | Removed `ClientElicitMiddleware` references, `routeClientElicitation`, `parseElicitationCreate`, stale tests, and stale comments. |
| `11/10` | Replaced three independent envelope-registry wiring calls with one shared wiring helper. | `cmd/nanite/main.go` now calls `envelopewiring.InstallSharedRegistry`; pointer-identity coverage spans chat, envelope, and plugin host. |
| `11/11` | Kept the two `dispatch_to_agent` evaluators intentionally independent and added a parity test. | No production dispatch behavior changed; shared rule interpretation is now regression-tested across both real paths. |
| `11/03` | Extracted the shared StructuredMessage-shaped JSON unwrap rule. | `chat.replayContent` and `recovery/pack.MessagePlainText` now share `internal/structuredmessage.UnwrapText` without importing `internal/chat` into recovery. |
| `11/04` | Exported the canonical inspector traffic-light rule. | `internal/service/inspector_producers.go` now calls `inspector.TrafficLight`; duplicate `trafficLightFor` was deleted. |
| `11/12` | Removed the duplicate `DevServerName` declaration from `internal/toolclient`. | `toolclient` now references `mcp.DevServerName` directly. |
| `11/13` | Explicitly deferred the optional generic Store scan-loop helper. | No Store code changed; the fresh Store `dupl` count remained 46 and focused Store build/vet/test checks passed. |
| `11/14` | Added plugin helper primitives and migrated the four CLI adapter plugins. | `LoadEmbeddedManifest` and `BasePlugin` now carry shared embedded-manifest and lifecycle boilerplate for Claude, Codex, Gemini, and Opencode adapters. |
| `11/15` | Switched three API decode bypasses to `a.decode`; accepted/deferred CRUD-family extraction. | Provider and plugin-config update handlers now use the shared decode helper while preserving `400 invalid JSON: ...` behavior. |
| `11/16` | Closed workflow/dispatch naming findings as awareness-only. | No code changed; source docs still support the intentionally separate package responsibilities. |

`TASKS/INDEX.md` marks every Wave 6a and Wave 6b row `reviewed`.
`TASKS/audit-remediation/findings.json` has 18 findings mapped to
`11-semantic-duplication-migration-drift`; all 18 now have
`task_status: "reviewed"`: 14 `remediate`, 4 `defer`.

## 2. Operator decisions preserved

### AD-19 - per-instance duplication policy

AD-19 did not say "DRY everything." It resolved each duplicated rule by
classification:

- Migration drift and textual boilerplate generally moved to shared
  implementation (`11/01`, `11/02`, `11/05`, `11/06`, `11/10`).
- Intentionally independent semantics stayed independent, with sync/parity
  coverage where a shared interpretation still matters (`11/11`).
- Cross-reference and optional cleanup items stayed explicitly non-invasive
  (`11/07`, `11/13`, `11/16`).

Use this as the precedent: the classification is the decision surface, not raw
line-count reduction.

### AD-20 - role-clear config names

AD-20 chose a bounded rename within the existing package, not a package split
and not an on-disk configuration rename. The active exported names are now:

- `config.RuntimeConfig` - project-root/runtime `nanite.yaml`.
- `config.TunablesConfig` - checked-in `config/nanite.yaml` tunables.

Historical audit/task prose may still mention `config.Config` or
`config.AppConfig`; active Go and canonical engineering docs should not.

### AD-29 - delete dead client-side elicitation code

AD-29 chose delete, not build. `11/09` removed the dead client-side path and
updated comments around the remaining live Nanite-owned server-side
elicitation path. Do not resurrect `ClientElicitMiddleware` from task prose
without a new explicit operator decision.

## 3. Verification state

The individual reviewed task files record focused verification plus repeated
baseline checks. The common baseline shape that passed by the end of the
reviewed wave was:

```bash
jq empty TASKS/audit-remediation/findings.json
go build ./cmd/nanite/
go vet ./...
go test ./...
git diff --check
go test -race ./... -count=1
```

The aggregate race command exited 0. The longest useful package durations
recorded were approximately: `internal/store` 221.505s, `internal/api`
106.653s, `internal/service` 53.034s, and `internal/selftools` 25.347s.

W6a fresh reviews passed after small fixes. W6b fresh reviews by
Zeno/Dewey/Linnaeus passed.

Representative focused checks that passed include:

- `go test ./internal/service/... -run 'Reactor|WakePolicy|CompletionPolicy' -v`
- `go test ./internal/api/... -run 'Harness|DurableAgent' -count=1 -v`
- `go test ./internal/llm/openai/... ./internal/llm/anthropic/... -run 'Stream' -v`
- `go test ./internal/service/... ./internal/selftools/... -run 'TestDispatchToAgentReflexInterpretationParity' -v`
- `go test ./internal/envelopewiring -run TestInstallSharedRegistryWiresAllConsumersToSameInstance -v`
- `go test ./internal/plugin ./internal/plugin/builtin/...`

Two transient failures were recorded and resolved during the wave:

- `internal/llm/anthropic` usage-count expectations failed transiently during
  full-suite runs and passed in isolation/package reruns and subsequent full
  reruns.
- Early in `11/02`, full-repo vet/test was blocked by concurrent `11/09`
  deletion work (`routeClientElicitation` undefined) plus the pre-existing
  `driveBootSession` send-on-closed-channel class; later reviewed task logs
  record passing full build/vet/test after reconciliation.

This handoff did not rerun the Go suite; it summarizes the reviewed task
verification and final closeout commands already recorded in the task files.

## 4. Process note carried forward

`TASKS/ESCALATIONS.md` now records the 2026-08-24 process finding
**"Wave 6a dispatch assumptions overstated file disjointness and isolation."**
The W6a kickoff overstated file disjointness and the available `multi_agent_v1`
dispatch mechanism edited the shared checkout rather than providing reliable
per-worker isolated worktrees.

Concrete effect in Wave 6: `11/08` and `11/10` both touched
`cmd/nanite/main.go`, so `11/10` had to serialize after `11/08`; central
tracker files required hand reconciliation.

For future waves in this tool environment, do not rely on prompted worktree
isolation for write agents. Either dispatch serially, or keep parallel
subagents read-only/report-only and have the orchestrator apply central
tracking edits.

## 5. Wave 7 dependency state

Wave 6's dependency is satisfied.

- `12/01` still depends on `00/02`; that row is already `reviewed` in
  `TASKS/INDEX.md`.
- `12/01` is gated on AD-21, which is decided, but its task file still says
  the first implementation step is to ask/confirm whether an external CI or
  scheduled lint mechanism already exists and where a new gate should live.
- `12/03` depends on `04/04`, which is already reviewed.

The repository-wide development freeze remains in effect as documented in
`TASKS/INDEX.md` and the batch README. Wave 6 completion unblocks the Wave 7
dependency, but it does not itself authorize the next dispatch unless the
operator says to resume.

## 6. Final Wave 6 state

| Status | Count |
|---|---:|
| Reviewed | 16 |
| In progress | 0 |
| Blocked | 0 |

Wave 6 is closed.
