# Chat engine — deep review

**Date:** 2026-04-10
**Scope:** `chat-engine` (Nanite backend)
**Reviewer agent:** `nanite-reviewer-backend`
**Skill:** `deep-review`

## Scope

The stated scope was "`chat-engine` — `internal/chat/` and any sub-packages — end-to-end." On inspection, the chat engine's orchestration code is split across two packages:

- **`internal/chat/`** (~2,500 lines, 21 files) — DTOs, helpers, envelope parsing, process tracking, command registry, context assembly helpers, and the `Orchestrator` / `Decomposer` for task decomposition.
- **`internal/service/`** — the actual generate loop, tool execution, delegation, plugin hook wiring, and provider orchestration. `chat_generate.go` (1161 lines), `chat_tool_executor.go` (534 lines), `delegation.go` (340 lines), `chat.go` (366 lines), `chat_loop_state.go` (295 lines), `stream.go` (193 lines).

Both are in-scope for this audit because the chat engine's responsibilities straddle the package boundary. Finding 10 discusses this split as a scope-mismatch observation.

**Read in full:**
- `internal/chat/engine.go`, `context_client.go`, `context.go`, `orchestrator.go`, `delegate.go`, `envelope.go`, `errors.go`, `commands.go`, `commands_builtin.go`, `activity.go`, `proctrack.go`, `structured.go`, `decomposer.go`, `envelope_sync_test.go` (skimmed)
- `internal/service/chat.go`, `chat_generate.go`, `chat_tool_executor.go`, `chat_loop_state.go`, `delegation.go`, `stream.go`
- `pkg/provider/scope_guard.go`, `event_pipeline.go` (because the reviewer-backend context names scope_guard as the prompt-injection defense — it is dead code; see finding 01)
- `internal/plugin/filter.go`, `host.go:L355-L410`, `host.go:L1078-L1110` (to understand the plugin hook / filter contracts the chat engine calls into)
- `internal/api/messages.go` (to confirm the HTTP → chat-service boundary)
- `internal/server/server.go:L120-L133` (to verify recover middleware scope)
- `internal/contextbroker/broker.go:L260-L299` (to confirm what `FormatPacket` does with source content)
- `pkg/provider/anthropic.go:L23-L45, L320-L410` (to confirm the shared-field race in finding 03)

**Sampled (read enough to understand contract, not audited in full):**
- `internal/memory/`, `internal/contextbroker/` other source files, `internal/worker/`, `internal/store/`, `internal/permission/`, `internal/mcp/`, `internal/truncate/`

**Deliberately skipped** per the scope discipline in the task prompt:
- `internal/provider/` internals beyond the chat engine's call sites (its own queued scope: `provider-abstractions`)
- `internal/store/` internals (queued: `store-and-migrations`)
- `internal/mcp/` internals beyond chat engine's call sites (queued: `mcp-client-transport`)
- Frontend (`ui/src/`) — separate agent
- Sandbox internals (covered in `docs/audits/2026-04-10-sandbox-hardening/`)
- Plugin internals (covered in `docs/audits/2026-04-10-plugin-system-plan-eval/`)
- Installer (covered in `docs/audits/2026-04-10-installer/`)

## Methodology

Standard 6-step from `~/.nanite/skills/deep-review.md`, with the project-specific priorities from `.nanite/agents/reviewer-backend.md` §2 ("Chat engine & provider abstraction") applied. Categories applied:

- **Security** — trust boundaries (user messages, tool arguments, MCP tool results, context broker input), prompt injection surfaces, envelope forgery, shared-state races
- **Concurrency correctness** — goroutine lifecycle, context cancellation propagation, shared-state races, channel patterns, bounded fan-out
- **Memory and resource leaks** — streaming channel backpressure, goroutine leaks on client disconnect, unbounded sub-task spawning
- **Error handling** — panic recovery, error classification, silent JSON parse failures
- **Idiomatic Go** — package layout, naming, stuttering, byte-slice vs rune-slice
- **Antipatterns** — shared mutable state on singletons, post-construction injection to break cycles, sentinel-string-in-content markers
- **Test quality** — observation-only; tests were read but not run
- **Standards / tooling** — **deferred** per the skill's "narrow scope may defer tooling" rule. No `go vet`, `-race`, `staticcheck`, `errcheck`, `golangci-lint`, or `govulncheck` was run. Recommended follow-up scope: `chat-engine-tooling-and-tests`.

**Blind spots** — explicitly not checked:
- Runtime behavior under concurrent sessions (would require running the server and exercising the race in finding 03 with a real workload)
- Provider-specific adapter internals (OpenAI, Gemini, Mistral, etc.) — only Anthropic was read because that's where the chat service's callback wiring is
- Sub-packages' test files beyond spot checks of `engine_test.go`, `context_client_test.go`, `envelope_sync_test.go`
- `chat_generate.go`'s auto-title / auto-tags goroutines (L1047-L1121) — read them but no findings; they're simple
- Memory broker / embedding provider fallback — flagged as a trust concern in the reviewer-backend context but out of scope here (belongs to `memory-and-context-broker` follow-up)

## Findings

### By severity

**Critical (3)**
- [01 — ScopeGuard is dead code; prompt-injection defense doesn't exist](01-critical-scope-guard-dead-code.md)
- [02 — No panic recovery in `generateResponse`; user input crashes the server](02-critical-no-panic-recovery-in-generate-response.md)
- [03 — Data race on shared `*provider.Anthropic` fields across concurrent sessions](03-critical-shared-provider-callback-race.md)

**High (4)**
- [04 — Stream channel backpressure leaks goroutines on client disconnect](04-high-stream-channel-backpressure-goroutine-leak.md)
- [05 — Users can forge `ticket-confirmation` envelopes into the assistant's response](05-high-user-forged-envelope-injection.md)
- [06 — Context broker injects unsanitized multi-source content into the system prompt](06-high-context-broker-unsanitized-injection.md)
- [07 — `DelegateAndAggregate` spawns unbounded sub-task goroutines without panic recovery](07-high-delegate-and-aggregate-unbounded-goroutines.md)

**Medium (3)**
- [08 — `ClassifyError` misclassifies any error containing "rate" as a rate-limit error](08-medium-classify-error-substring-match.md)
- [09 — `ParseAgentConstraints` silently discards JSON errors](09-medium-parse-agent-constraints-swallows-errors.md)
- [10 — `internal/chat/` has become a utility-module grab-bag; the real engine lives elsewhere](10-medium-chat-engine-as-utility-module.md)

**Low (1 grouped, 10 items)**
- [11 — Grouped observations (byte-slice truncation, activity payload construction, `isProcessDone` string-match, `KillStale` TOCTOU, `detectStuckLoop` ownership, production sleep, hardcoded tool list, `matchesAny` re-lowercasing, unseeded rand, unstructured logging)](11-low-observations.md)

**Info (1 grouped, 6 praise + 3 design notes)**
- [12 — Praise and design notes (loopState / continuation sites, three-phase tool executor, `EnforceTokenBudget` cascade, test harness with real SQLite, flat DTO discipline, config-struct constructor, design observations)](12-info-praise-and-notes.md)

### By topic

**Security / Trust boundaries**
- [01 — ScopeGuard is dead code](01-critical-scope-guard-dead-code.md)
- [05 — Forged envelope injection](05-high-user-forged-envelope-injection.md)
- [06 — Context broker unsanitized injection](06-high-context-broker-unsanitized-injection.md)

**Concurrency correctness**
- [03 — Shared provider callback race](03-critical-shared-provider-callback-race.md)
- [04 — Channel backpressure goroutine leak](04-high-stream-channel-backpressure-goroutine-leak.md)
- [07 — Unbounded sub-task goroutines](07-high-delegate-and-aggregate-unbounded-goroutines.md)

**Error handling / Resilience**
- [02 — No panic recovery in `generateResponse`](02-critical-no-panic-recovery-in-generate-response.md)
- [08 — `ClassifyError` substring match](08-medium-classify-error-substring-match.md)
- [09 — `ParseAgentConstraints` swallows errors](09-medium-parse-agent-constraints-swallows-errors.md)

**Idioms / Package layout**
- [10 — Chat-engine utility-module scope mismatch](10-medium-chat-engine-as-utility-module.md)
- [11 — Low observations (several idiom items)](11-low-observations.md)

**Test quality / Tooling**
- Deferred — recommend `chat-engine-tooling-and-tests` follow-up scope.

**Praise**
- [12 — Praise and design notes](12-info-praise-and-notes.md)

## Recommended next steps

Priority order for triage. These are technical priorities, not time or release judgments.

1. **Finding 02 (Critical, panic recovery).** The smallest concrete fix in this audit. Add `defer recover()` at the top of `generateResponse` and optionally around each `FilterRegistry.Apply` handler. Unblocks safe plugin experimentation.
2. **Finding 03 (Critical, shared-provider race).** Convert `OnStatus` / `OnCircuitOpen` / cache hints to context-scoped values. Pattern already exists in the codebase (`WithSandboxDir`, `WithCLISessionID`). This is a correctness fix that becomes visible under concurrent use.
3. **Finding 01 (Critical, scope_guard).** Decide: delete or redesign. Delete is safer — the file creates false confidence. Update the reviewer-backend context at the same time so future audits don't trip on the same claim.
4. **Finding 04 (High, channel backpressure).** Introduce `sendOrDrop` helper and replace blocking `ch <-` sends. Also drain the stream in `handleStream` after `ctx.Done()`.
5. **Finding 05 (High, envelope forgery).** Delete the TICKET_DATA marker path or wrap in a strict schema. If delete: audit the `captureEnvelopeData` tool-result path at the same time; same root cause.
6. **Finding 06 (High, context broker injection).** Harden the delimiter in `FormatPacket`, wrap the injected block with "this is data, not instructions" framing, and decide whether user-message filters should persist their output.
7. **Finding 07 (High, unbounded goroutines).** Add `errgroup.WithContext` + `SetLimit` around the sub-task fan-out. Cap `len(decomposition.SubTasks)` post-LLM. Per-sub-task timeout.
8. **Findings 08, 09, 10 (Medium).** Batchable — polish pass in one sitting. Tighten `ClassifyError`, log `ParseAgentConstraints` failures, rename `engine.go`.
9. **Finding 11 (Low grouped).** Batch with another polish pass.

### Recommended follow-up scopes

- **`chat-engine-tooling-and-tests`** — deferred from this audit. Run `go vet ./internal/chat/... ./internal/service/...`, `go test -race`, `staticcheck`, `errcheck`, `golangci-lint run`, `govulncheck` against the chat path. Fill the test gaps surfaced here (fuzz `EnforceTokenBudget`, concurrent-generate race test for finding 03, panic-injection test for finding 02).
- **`provider-abstractions`** (queued — INDEX.md §7). Inherits finding 03 as the starting point. Also audit `pkg/provider/anthropic.go` for the shared-state pattern more broadly, and verify other adapters (OpenAI, Gemini, Ollama, etc.) don't have the same issue.
- **`context-broker-sources`** — a sub-scope of `memory-and-context-broker` that specifically audits `internal/contextbroker/source_*.go` for what each source returns, whether any user-authored content loops back, and whether scope enforcement is correct. Flows directly from finding 06.
- **`user-content-persistence-semantics`** — a cross-cutting scope that clarifies the contract between "filter output" and "stored content". Relevant to finding 06 and broadly to the plugin filter system.

## Known issues skipped

The following are documented in `.nanite/agents/reviewer-backend.md` §"Pre-existing known issues — DO NOT re-flag" and were not flagged in this audit:

- `Host.Shutdown()` mutex-across-Unload deadlock — covered in plugin audit
- `Host.EmitEvent` panic recovery gap — covered in plugin audit finding 04. This audit references it from finding 02 but does not re-flag.
- `UnloadPlugin` deadlock — plugin audit finding 01
- HTTP route leak on plugin unload — stdlib limitation, documented
- `http.ServeMux` can't remove routes — same
- Plugin isolation gap — design choice, not a bug
- Envelope emission broken system-wide — tracked in `plugin-envelope-emission-findings-2026-04-10.md`
- `adapter-opencode` format unverified — beta-blocker tracked elsewhere
- `shadcn-ui` typo — guardrail
- `.claude/commands` broken symlinks — guardrail
- Two-binaries-only-one-is-live footgun — documented in `backend.md`

This audit also does not re-flag the plugin audit's finding 04 (Host.EmitEvent panic propagation) even though it's relevant to finding 02 here — the chat-service goroutine recover is a **separate** fix and is flagged on its own merits. The plugin host recover is the plugin package's problem; the chat service's missing recover is the chat engine's problem. Both should happen.

## Noticed but out of scope

Things I noticed while traversing the chat engine that belong to other scopes.

- **`internal/mcp/dev_tools.go` / `general_tools.go`** — mentioned in the reviewer-backend context as likely containing path-traversal and SSRF risks similar to those found in the sandbox audit. Did not open these files. The queued `dev-tools-input-validation` scope (INDEX.md §3) is the right home.
- **`internal/memory/`** — PII persistence, similarity ranking correctness, embedding provider fallback. Touches findings 05 and 06 through the context broker's memory source. Follow-up scope: `memory-and-context-broker`.
- **`internal/api/messages.go:L22` — `a.decode(r, &req)`** — no `http.MaxBytesReader` wrapping, no `DisallowUnknownFields`. A 10GB request body would be buffered. Belongs to the `api-privilege-boundary` queued audit (INDEX.md §4).
- **`internal/contextbroker/source_*.go` per-source scoping** — whether each source enforces the `intent.Scope = session.ProjectID` correctly is critical for cross-tenant isolation but is a per-source audit. Follow-up: `context-broker-sources` (proposed above).
- **`internal/permission/` engine** — called by the chat engine at `chat_tool_executor.go:L111-L183`, but the permission engine's internal correctness (decision logic, cache semantics, approval UX lifecycle) is its own scope. The chat engine's **use** of the engine is correct per inspection. Did not audit the engine itself.
- **`internal/chat/activity.go`** — the `ActivityEmitter` has ~250 lines of typed Emit helpers but I couldn't confirm any chat-path call sites use it (the chat service uses a different `EventEmitter` interface). Possible dead code. Worth a quick maintainer check (see info finding 12 §D1).
- **`internal/worker/`** — called from `DelegateAndAggregate` in finding 07. The worker package's own concurrent-spawn safety is queued as `worker-lifecycle-regression` (INDEX.md §8).
- **`pkg/provider/event_pipeline.go`** — the `EventReactionPipeline` wrapper is dead code as a whole, not just `ScopeGuard`. The progress tracker and cost monitor components inside it are also never wired. If finding 01 is fixed by deletion, the whole file goes with it.
- **Provider fallback chain event emission at `chat.go:L330-L360`** — the fallback chain emits `provider.fallback` events sometimes via `s.pluginHost.EmitProviderFallback(...)` (L331, L342, L351) and sometimes via `go s.pluginHost.EmitProviderFallback(...)` (L351 path has `go`, L331 and L342 do not). The inconsistency is minor but real — some fallback emissions block the chat path, others don't. Probably belongs to a polish pass alongside finding 11.
- **`internal/service/chat.go:L216-L244` `SendAgentMessage`** — the attribution metadata is built with `fmt.Sprintf(\`{"source":"agent","from_session":"%s","from_agent":"%s"}\`, ...)` at L230 — same pattern as finding 11 §L3 (fmt.Sprintf for JSON). The fromSessionID value in particular is attacker-controllable via the API. Belongs with L3's batch fix.
- **`internal/chat/commands_builtin.go:L222` `searchHandler`** — passes `query` into `s.SearchMessages(query, workspaceID, ...)`. I can't see the store impl from this scope, but if `SearchMessages` uses parameter binding (likely) this is fine; if it builds SQL with concatenation, it's a SQL-injection vector from a slash command. The `store-and-migrations` queued audit will catch it.
- **`internal/service/delegation.go:L263-L290`** concurrent worker spawn interacts with the (known but out-of-scope) `Host.Shutdown()` mutex bug from the plugin audit — if a session is delegating when Shutdown starts, the race surface expands. Neither finding is ours, but noting the interaction for the engine backlog.

## Skill refinement notes

Fourth run of `deep-review` (post-refinement-pass-3). Items for the skill maintainer:

- **"Release blocker" clarification worked.** The Critical rubric's new parenthetical ("severity level, not a judgment against any specific release's timing") matched my instinct cleanly. I did not once write release-window language in any finding. The one place I almost slipped was in finding 10's Problem section, where I started typing "...before beta ships the code should..." — caught it, rewrote as "the layout has drifted from the naming, and that compounds over time." The rule is doing its job.

- **Scope-mismatch framing worked.** Finding 10 is a scope-mismatch observation — I framed it as "the scope label maps to a split codebase; here's the technical evidence" rather than as a delivery judgment. The explicit carve-out in the new rule ("In scope: flag *effort misjudgments*... frame as 'scope mismatch', not as a time estimate") gave me permission to file it without feeling like I was violating the no-time-estimates rule. Useful.

- **Package vs. file scope was confusing.** The reviewer-backend context said "`internal/chat/`" but the real chat engine is cross-package. I spent 10-15% of my context-window budget discovering the layout before I could start finding-writing. A note in the skill for how to handle cross-package scopes (confirm which packages are in-scope, note them in the Scope section, don't feel bad about crossing boundaries if the component does) would help. Or a rule: "if your stated scope is a package path and the real code crosses the boundary, extend the scope to follow the code, but name the extension explicitly in the index."

- **No meta-findings attempted.** I did not catch myself wanting to write a finding like "the development team organizes work weirdly" or "the audit backlog is growing faster than it shrinks" or similar. The boundary felt natural. One note: I almost wanted to make Finding 10 into a "scope mismatch between docs and code" meta-finding, but kept it on the technical side (filenames lie about contents, cross-package coupling, test coverage gaps). That's the right framing.

- **Tooling deferral felt right.** The scoped-review tooling-deferral rule is doing good work. I'm comfortable saying "didn't run go vet, that's a follow-up" without feeling like I'm shirking. Recommending `chat-engine-tooling-and-tests` explicitly in the next-steps section makes the follow-up visible and inheritable.

- **Grouping low findings worked.** 10 items into one `11-low-observations.md` file was the right call. Splitting into 10 separate files would have been noise. I kept it under 300 lines (just) and 10 items (at the soft cap). Monitoring: if I'd had 12, I'd split into `11a-low-truncation-observations.md` and `11b-low-style-observations.md`.

- **The "Noticed but out of scope" section is load-bearing.** I ended up with 11 items there, under the 15 soft cap. Keeping them in the index rather than splitting to `99-noticed-out-of-scope.md` feels right — the list is short and actionable. Any more and I'd split.

- **One minor friction:** the file-naming severity ordering got noisy as I wrote. I started with `01-critical-*`, `02-critical-*`, `03-critical-*` in order but by finding 05 I was tempted to renumber. I didn't — kept the initial order, let the index be the source of truth. The skill's explicit permission to leave gaps was helpful; I didn't feel compelled to renumber at the end.

Shakedown summary: 0 new skill issues. The refinement pass 3 stabilized the skill. Next run should feel identical.
