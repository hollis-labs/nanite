# Nanite audit index

Tracking ongoing first-pass coverage of the Nanite codebase. This index lists completed audits, their headline findings, and queued scopes drawn from each audit's "Noticed but out of scope" sections plus the reviewer-backend context's priority list.

**Goal:** first-pass audit coverage of the entire Nanite codebase. Each subsystem reviewed at least once. Findings filed in `docs/audits/<date>-<scope>/` per the `deep-review` / `plan-review` skill output contract. Severity rubric: Critical / High / Medium / Low / Info.

**Reviewer agents:** `nanite-reviewer-backend` (Go, deep-review + plan-review), `nanite-reviewer-frontend` (React/TS, deep-review + plan-review).

---

## Completed audits

### 2026-04-10 — `sandbox-hardening` (Go, deep-review)
- **Counts:** 3 Critical, 3 High, 4 Medium, 1 grouped Low (10 items), 1 Info
- **Folder:** `docs/audits/2026-04-10-sandbox-hardening/`
- **Headline:** Path traversal + seatbelt profile injection in `nanite_code_execute`; proxy SSRF via DNS-resolved IPs and missing port restriction; Linux silent sandbox fallback when `bwrap` is missing.
- **Cross-audit update (from installer audit):** Sandbox finding 03 stays Critical — installer does NOT check for `bwrap`. Both should be fixed together.

### 2026-04-10 — `plugin-system-plan-eval` (Go, plan-review)
- **Counts:** 1 Critical, 6 High, 4 Medium, 1 grouped Low (10 items), 1 grouped Info
- **Folder:** `docs/audits/2026-04-10-plugin-system-plan-eval/`
- **Headline:** UnloadPlugin has the same mutex-across-Unload deadlock the plan fixes in Shutdown but only fixes Shutdown; transport serialization will block proliferating RPC calls; transport timeout permanently kills the pipe; Host.EmitEvent has no panic recovery; Unregister gap is systemic across 11 of 14 register categories.
- **Note:** Finding `07-high-beta-scope-...` is **disregarded** per user — misinformed framing (time/release-window judgments are out of scope per the refined skill).

### 2026-04-10 — `installer` (Go, deep-review)
- **Counts:** 0 Critical, 4 High, 5 Medium, 1 grouped Low (13 items), 1 Info
- **Folder:** `docs/audits/2026-04-10-installer/`
- **Headline:** No sandbox prerequisite check on any platform (cross-cuts sandbox finding 03); installer follows symlinks throughout (TOCTOU on managed-section file writes); managed-section parser uses first-match `strings.Index` with no marker-pair counting (data loss possible); scaffold symlinks created without target validation.
- **Praise logged:** Migrate rollback contract is well-designed; `state.go` phase markers are the right shape; tri-state `Adapters *[]string` config; high test coverage in the install package.
- **Strong validation signal for skill refinements:** the new "Out of scope for findings" rule worked — reviewer caught themselves twice trying to write release-stage framing and rewrote on technical grounds.

### 2026-04-10 — `mcp-client-transport` (Go, deep-review)
- **Counts:** 2 Critical, 5 High, 3 Medium, 1 grouped Low (8 items), 1 grouped Info
- **Folder:** `docs/audits/2026-04-10-mcp-client-transport/`
- **Headline:** Every stdio timeout/cancel **leaks a subprocess, a goroutine, and an FD pair** (`t.started = false` without kill/wait/close); neither stdio `bufio.ReadBytes` nor HTTP `json.Decoder` has any **response-body size cap** — one malicious or buggy MCP server OOMs the host.
- **Trust model assessment: NOTHING.** Reviewer ran `rg 'validate|sanitize|Validate|Sanitize' internal/mcp/` — zero matches. No `scope_guard` analog, no validator, no filter. No response size cap, no `tools/list` schema validation, no argument validation, no tool-name allowlist, no result-type filtering, no secret-scoping on env inheritance. Every trust boundary in the reviewer-backend context is unprotected on the MCP client side. **Stronger than "dead code": "no code." The trust model is "trust the MCP server unconditionally."**
- **Cross-audit pattern repetition (significant):**
  - **Plugin finding 02 (mutex serialization) — same pattern, independently implemented.** `StdioTransport.call` holds `t.mu` across the write-then-read cycle with an unused `nextID atomic.Int64` field, mirroring `internal/plugin/subprocess/transport.go` line-for-line in spirit.
  - **Plugin finding 03 (timeout kills pipe) — similar but worse.** Plugin closes the pipe permanently; MCP doesn't close, it just re-starts a new subprocess and orphans the old one. Subprocess/goroutine/FD leaks per timeout, plus a Manager with no crash detection.
  - **Chat engine finding 04 (context-blind send) — same class.** No channel sends here, but the context-drop in in-process transports is the same "cancellation doesn't flow" bug. Found in `SelfToolsTransport`, `DevToolsTransport`, `GeneralToolsTransport`, `CodeExecTransport` — 11 sites in self-tools alone.
  - **Chat engine finding 02 (no panic recovery) — same gap, no fix inherited.** Zero `recover()` calls in all of `internal/mcp/`. Nil-receiver panics on post-construction-injected services crash the host.
- **Praise preserved:** `MCPTransport` interface shape (small, consumer-defined, easy to fake); `memory_tools.go` honors the context contract and is the reference for the others; `NewHTTPTransport` sets a client-level timeout with rationale.
- **Strong validation signal (5th run):** rule held; reviewer reported the new "check dead-code defense" instruction from the task brief was *more valuable than expected* — suggested folding it into the skill methodology as a standard security-step ("grep for `validate|sanitize|filter|guard` to verify any defense layer is wired and not just named"). Also noted a small friction at scope edges (where dev-tools and mcp-transport overlap).

### 2026-04-10 — `dev-tools-input-validation` (Go, deep-review)
- **Counts:** 3 Critical, 5 High, 3 Medium, 1 grouped Low (10 items), 1 grouped Info
- **Folder:** `docs/audits/2026-04-10-dev-tools-input-validation/`
- **Headline:** **`dev_bash` runs `exec.CommandContext(ctx, "sh", "-c", command)` in the nanite host process with LLM-controlled `command` and NO `sandbox.AgentExec` wrapping** — straight RCE-as-user via prompt injection. The workDir allowlist is cosmetic (`sh -c "cat ~/.ssh/id_rsa"` uses absolute paths). Plus: symlink escape in `isAllowed` (`EvalSymlinks` falls back to unresolved abs on error → arbitrary write anywhere via planted symlink); `web_fetch` SSRF with zero URL validation, doesn't use the sandbox proxy at all (proxy is subprocess-only).
- **Cross-audit comparisons:** dev tools are **strictly worse** than `code_exec_tools.go` on one axis — `code_exec_tools.go` at least wraps in `sandbox.AgentExec`; `dev_bash` doesn't even try. `web_fetch` SSRF is worse than the sandbox proxy bypass because it doesn't even attempt protection. The chat-engine envelope injection (finding 05) has its cleanest *remote* delivery vector via `web_fetch`.
- **Refactor flagged:** a shared `internal/pathsafe.ResolveUnder(root, userPath)` helper would close the symlink class across sandbox, installer, and MCP dev tools in one primitive.
- **Strong validation signal (5th run of `deep-review`):** *"The 'Out of scope for findings' rule held firmly. I caught myself once drifting toward 'this is a beta blocker' phrasing in finding 03 and rewrote as 'highest technical severity.' No release-stage framing attempted, no meta-findings. The rule feels internalized."*

### 2026-04-10 — `chat-engine` (Go, deep-review)
- **Counts:** 3 Critical, 4 High, 3 Medium, 1 grouped Low (10 items), 1 grouped Info
- **Folder:** `docs/audits/2026-04-10-chat-engine/`
- **Headline:** **`scope_guard.go` is dead code.** `NewScopeGuard` and `NewEventReactionPipeline` only have test callers; `chat_generate.go:L337-L339` calls `prov.StreamChatWithTools` directly with no wrap. The named line of defense against prompt-injection escapes is not wired into production. Even if it were wired, the implementation is keyword-substring matching with `AllowedScopes: ["*"]` (allows everything) and `ScopeViolationMode: "log"` (never terminates). Two more Criticals: no panic recovery in `generateResponse` (plugin filter panic crashes the server); shared provider callback race in `chat_generate.go:L132-L148` (writes `ap.OnStatus`/`ap.OnCircuitOpen` directly on the registry singleton, two concurrent sessions race on correctness-relevant fields).
- **Praise logged:** `loopState` + `ContinueSite` typed constants; three-phase `preCheckTools` → `executeToolBatch` → `postProcessToolResults` split; `EnforceTokenBudget` cascade with `TokenBreakdown` visibility; real-SQLite test harness via `t.TempDir()`; flat-struct DTO discipline (StreamEvent, PresenceEvent); config-struct constructor pattern in `NewChatService`.
- **Strong validation signal:** 4th real run of refined `deep-review`. Reviewer report: *"The 'release blocker' clarification worked cleanly — I did not once write release-window language. Caught myself once in finding 10 starting to type 'before beta ships' and rewrote it as 'compounds over time'. The rule is doing its job."* And: *"No meta-findings attempted. I did not catch myself wanting to write a meta finding about development organization, backlog size, etc. The boundary feels natural now."*

---

## Queued audit scopes

Drawn from prior audits' `Noticed but out of scope` sections, the reviewer-backend context's priority list, and cross-cutting concerns surfaced during execution. Order is suggestive, not strict.

### High priority (security and correctness)

1. **`managed-section-parser`** — `internal/agent/managed_section.go`. Cross-cuts installer + every adapter plugin's session-prep call. The data-loss risk in installer finding 03 has a second life here that wasn't audited. **The installer handoff agent flagged that `agent.ParseMDFile` (called from `adapter-claude/plugin.go:L114`) has a test-coverage gap that should be addressed in this scope.**
3. ~~**`dev-tools-input-validation`**~~ — **COMPLETED 2026-04-10**, see above. Found 3 Critical including straight RCE via `dev_bash`. Spawned new queue items: `memory_tools.go` audit, `self_tools_transport.go` audit (762 lines), `loadPersistedMCPServers` URL/auth audit.
4. **`api-privilege-boundary`** — `internal/api/plugins.go` and the rest of the HTTP API surface. Auth, CORS, authorization on plugin install/management endpoints, request validation.
5. **`mcp-client-transport`** — `internal/mcp/` client side. Subprocess management, JSON-RPC framing, tool result handling, plugin-hosted MCP servers.
6. **`store-and-migrations`** — `internal/store/`. SQLite raw queries, transaction correctness, migration safety, plugin schema persistence (relevant to plan track B's yaml-authoritative work).
7. **`provider-abstractions`** — `internal/provider/`. PTY bridge goroutine lifecycle, API key handling, Anthropic/OpenAI/Ollama paths, secret exposure in logs.
8. **`worker-lifecycle-regression`** — guard the three race fixes from 2026-04-10 (`fix/worker-field-sync`, `fix/plugin-trigger-dispatch-race`, `fix/worker-shutdown-race`).

### Medium priority (depth and follow-on)

9. ~~**`plugin-tooling-and-tests`**~~ — deferred from the plugin audit. Run `go vet`, `-race`, `staticcheck`, `golangci-lint`, `govulncheck` against the plugin package; fill the test gaps the plan-eval flagged. **~~SUPERSEDED by `whole-repo-tooling-and-tests-sweep` (appended 2026-04-11)~~**
10. ~~**`sandbox-tooling-and-tests`**~~ — same, scoped to `internal/sandbox/`. **~~SUPERSEDED by `whole-repo-tooling-and-tests-sweep` (appended 2026-04-11)~~**
11. ~~**`installer-tooling-and-tests`**~~ — same, scoped to `internal/service/install/` + `cmd/nanite/install_cmd.go`. **~~SUPERSEDED by `whole-repo-tooling-and-tests-sweep` (appended 2026-04-11)~~**
12. **`auto-triggers-disposition`** — `internal/plugin/auto_triggers.go` was never opened during the plugin audit. Disposition unknown; in the plugin package, may be load-bearing for the plan.
13. **`framework-libs-go-plugin-inventory`** — `framework/libs/go-plugin/`. The plan's Track I.1 deletes this without a verified inventory. Audit before any deletion happens.
14. **`assets-framework-content-correctness`** — `internal/assets/framework/`. The embedded roles/skills/commands the installer extracts. Their correctness is its own audit, distinct from the install code that ships them.
15. **`config-loader-merge`** — `internal/config/`. User vs project merge precedence, config validation, hot-reload (if any).

### Frontend (queued, not yet started — needs `nanite-reviewer-frontend`)

16. **`chat-transcript-and-sse-streaming`** — `useChat.ts` race-prone effects, `pendingJump` + `scrollToMessageId`, markdown XSS via `MessageContent`, `ArtifactChip` URL-scheme validation.
17. **`envelope-system-and-plugin-rendering`** — runtime validation, Suspense boundaries, the codegen CI check, dynamic plugin component loading.
18. **`zustand-store-map-immutability`** — guard the existing pattern against regression.
19. **`api-client-type-safety`** — every `any` / `as` / `@ts-ignore`, runtime validation at API boundaries.
20. **`plugin-settings-dynamic-forms`** — `secret` field handling, dev/recover-mode gating.
21. **`observability-panels`** — privileged kill-stale button, large log rendering performance.
22. **`provider-and-settings-ui`** — API key masking, CLI path validation in settings.
23. ~~**`keyboard-shortcuts-and-a11y`**~~ — focus traps, Esc handling, WCAG AA contrast, ARIA on icon buttons. **~~MOVED to frontend workstream follow-up (2026-04-11)~~** — see `.nanite/agents/frontend.md` deferred section.

### Plan / design audits (use `plan-review` skill)

24. **Future plans as they appear** — the `plan-review` skill exists for design-doc evaluation. Run against any new architecture/migration plan before the plan is committed to.

### Subsystems not yet enumerated

A pre-execution inventory pass would identify any package not on this list. Candidates likely missed: `internal/brand/` (probably trivial), the `plugins/` directory (concrete plugin implementations as a class — pattern audit, not per-plugin), `cmd/` entrypoints other than install, integration tests as their own audit target, `scripts/` (build scripts, codegen).

### Appended 2026-04-11 — from release-prep meta-project

**Source:** `~/Projects-apps/agent-workspaces/planning/nanite-release-prep/plans/phase-1-scope-alignment.md`. User-confirmed during the 2026-04-11 audit-orchestrator brainstorm. Order within each group is suggestive, not strict.

#### From TRIAGE technical audit scopes

25. **`toolbroker`** — `internal/toolclient/` (broker, tool_knowledge, permissions). Deep-review.
26. **`contextbroker`** — `internal/contextbroker/`. Deep-review of the broker subsystem as code. **Not** about token counting, compaction, or context-window management — those are separate scopes.
27. **`context-management`** — slot system, hot-swap, auto-compaction, `/compact` command. Mixed deep-review + claimed-vs-actual verification. **Explicit mandate:** prove hot-swap and slots actually work end-to-end. Reviewer should verify behavior, not just code presence. Suspected stubs/partials per user report.
28. **`context-counting-in-widgets`** — frontend-side targeted check. Suspected hardcoded token counting. Small scope. Cross-reference with `tokens-and-model-hardcoding`.
29. **`tokens-and-model-hardcoding`** — every place model names, token limits, context windows, pricing are hardcoded. Deep-review. (The models.dev integration design is deferred to a backend-agent scoping session; see `.nanite/agents/backend.md` deferred section.)
30. **`eval-subprocess-pty-sdk`** — subprocess / PTY / SDK usage patterns for running CLI agents. Best-pattern determination. Deep-review of existing implementations.
31. **`frontend-hygiene`** — component reuse vs. hardcoded, semantic tokens, composition, props-down/messages-up, Tailwind no-hardcoded-styles, modal/alert/drawer reuse. Frontend deep-review.
32. **`entities-tags-relational`** — objects / entities / tags relational correctness. Tags per-entity vs per-object. FK stubs vs real relationships. Deep-review.
33. **`memory-ranking`** — activation vs similarity vs hybrid ranking. Deep-review of the current memory ranking implementation; pair with a hybrid BM25+vector recall audit at dispatch time.
34. **`backpressure-followup`** — confirm backpressure coverage in all sites that need it. Follow-on from the initial PTY build.

#### From tests-and-coverage split

35. **`whole-repo-tooling-and-tests-sweep`** — mechanical sweep. `go vet`, `go test -race`, `staticcheck`, `golangci-lint`, `govulncheck`, `errcheck` across the whole tree. Single pass, single dispatch. Supersedes `plugin-tooling-and-tests`, `sandbox-tooling-and-tests`, `installer-tooling-and-tests`. Deliverable shape: one summary file + one failure-listing per tool.
36. **`tests-coverage-overall`** — test-design gap analysis. What's missing, what's under-tested, what's misaligned with its subject. Judgment-heavy. Deep-review skill with test-design lens.

#### From `*`-proposed security/trust-model items

37. **`security-threat-model`** — fresh deep-review. Trust-boundary audit against the enumeration in `reviewer-backend.md` §"Trust boundaries". Cover every trust boundary not yet audited by a named scope: plugin manifest parser, PTY input/output, env var handling surface, provider API paths, HTTP API input validation (minus the `api-privilege-boundary` scope already queued). Synthesis of completed-audit findings is out of scope for this audit — that happens in Phase 4 meta-synthesis.
38. **`dependency-supply-chain`** — Go `go.mod` + frontend `package.json`: license compliance, CVEs (beyond `govulncheck` alone), vendored-vs-hosted, version lag, known-bad detection. Cross-stack scope; orchestrator decides dispatch strategy at pull time.
39. **`telemetry-privacy-posture`** — what leaves the user's machine: embedding APIs, provider APIs, error reporting, any analytics. Opt-in defaults. Local-first guarantees. Distinct from observability.
40. **`plugin-capability-model`** — map the current plugin capability surface. What plugins can touch via `internal/*` imports. What host state leaks. What's implicitly available. What a malicious plugin could do. **Not** a sandbox design — the design of a sandbox is future work. Deliverable: capability map + gap list.

#### From `*`-proposed operability/quality items

41. **`observability`** — `internal/otel` wrapper usage, structured-logging discipline, PII in log fields, metric coverage, error-reporting surface. Deep-review.
42. **`single-binary-asset-embedding-mechanics`** — `internal/assets/framework/` embed mechanics, `nanite install` extraction, binary size, cold-start perf, extraction atomicity. Deep-review. **Distinct** from the existing `assets-framework-content-correctness` scope (that audits embedded content; this audits mechanics).

#### Mini-sweep scopes (grep-driven, different deliverable shape)

43. **`panic-recovery-sweep`** — mini-sweep. Grep `recover()` across the whole tree. Produce an authoritative map of every goroutine spawn + panic boundary + recover gap. Closes the cross-cutting "No panic recovery" theme. Deliverable shape: single map file + per-package gap listings, not per-finding folders.
44. **`concurrency-cancellation-sweep`** — mini-sweep. Same shape as `panic-recovery-sweep`. Grep goroutine spawn sites, `ctx.Done` handling, `Close`/cancel paths, graceful-shutdown hooks. Produce a concurrency-lifecycle map. Closes the cross-cutting "Concurrency teardown" theme.

---

## Cross-cutting themes

Patterns that appeared in multiple audits — likely targets for refactoring even before any individual finding is fixed:

- **Symlink handling.** Installer follows symlinks (installer 02); scaffold creates symlinks without validation (installer 04); sandbox dir resolution is unsafe (sandbox 01); dev-tools `isAllowed` falls back to unresolved abs path on `EvalSymlinks` error (dev-tools 02). **Concrete refactor proposed by dev-tools auditor: a shared `internal/pathsafe.ResolveUnder(root, userPath)` helper** would close the symlink class across sandbox, installer, and dev tools in one primitive.
- **Marker / parser injection.** Managed section first-match parser (installer 03); seatbelt profile string interpolation (sandbox 01). Both are "untrusted string interpolated into a structured format without escaping" — same root cause, different format.
- **Concurrency teardown.** UnloadPlugin deadlock (plugin 01), Host.EmitEvent panic propagation (plugin 04), worker shutdown races (already fixed but worth regression-guarding).
- **Trust boundaries that aren't validated.** A consistent set of trust boundaries — user messages, tool arguments, MCP tool results, plugin manifests, PTY input, project paths from `--project` — needs a reviewer-context "validate at every entry to" rule and a corresponding code-side validator pattern.
- **Tooling deferred across all five audits.** `go vet`, `-race`, `staticcheck`, `golangci-lint`, `govulncheck` not run anywhere yet. A single `tooling-and-tests-sweep` scope could cover the whole codebase in one pass.
- **Sandbox bypass via "trusted" callers.** `code_exec_tools.go` wraps in `sandbox.AgentExec`; `dev_bash` doesn't. The pattern of "this code path runs LLM-controlled input through the OS" exists in multiple places and consistency of sandbox wrapping is not enforced. Future audits should check whether any newly-introduced tool/handler wraps OS operations in `AgentExec`. A linter or codegen check would help.
- **Dead code claiming to be defense.** `scope_guard.go` + the entire `pkg/provider/event_pipeline.go` wrapper appear to be dead code that the project thought was its prompt-injection containment layer. Future audits should check whether other "defensive" components are actually wired into production paths — name in the source ≠ active in the call graph. Add an explicit check to the standard methodology: for any component flagged as a security control, grep for non-test callers before trusting its existence.

---

## Skill refinement signals

Tracked here so future skill iterations have grounded evidence:

- **Sandbox hardening audit (1st run of `deep-review`)** — surfaced 6 shakedown notes; all 6 applied in refinement pass 1.
- **Plugin system plan eval (2nd run, also 1st run of plan-audit territory)** — surfaced 4 more shakedown notes; all 4 applied in refinement pass 2. Surfaced the need for a separate `plan-review` skill (forked).
- **Installer audit (3rd run of `deep-review`, post-refinement)** — strong validation signal: reviewer reported the new "Out of scope for findings" rule was load-bearing in a good way, caught themselves twice trying to write release-stage framing, rewrote on technical grounds. One ambiguity flagged: "release blocker" phrase in the Critical severity definition could conflict with the new rule. **Resolved 2026-04-10 with one-line clarification.**
- **Chat engine audit (4th run of `deep-review`, post-clarification)** — strong validation: reviewer caught themselves once attempting release-stage framing and rewrote on technical grounds. **Reported the meta-boundary now feels natural — no meta findings attempted.** One small friction: scope label was `internal/chat/` but the real chat engine spans `internal/service/chat*.go` too. Suggested skill addition: *"if the scope label is a package path and the real code spans packages, extend the scope and note the layout discovery in the index."* Worth folding into a future skill refinement pass.
- **Dev tools audit (5th run of `deep-review`, post-clarification)** — *"The 'Out of scope for findings' rule held firmly. I caught myself once drifting toward 'this is a beta blocker' phrasing in finding 03 and rewrote as 'highest technical severity.' No release-stage framing attempted, no meta-findings. **The rule feels internalized.**"* One small friction noted: the per-finding template's required-minimum-5-sections wording fits poorly when grouped findings have small sub-items — the reviewer adapted by writing a brief preamble + mini-sections per sub-item. Suggested skill note: *"for grouped files, the outer Problem/Evidence/etc. can be replaced by a brief preamble + sub-items each with their own mini-sections."* Worth folding into the next skill refinement.

---

## How to add to this index

When a new audit completes, add an entry under `## Completed audits` with: date, scope, severity counts, folder path, headline (1-2 sentences), and any cross-audit updates it forces. Move the scope from `## Queued audit scopes` to completed. If the audit's `## Noticed but out of scope` section adds new candidates, append them to the queue. Update `## Cross-cutting themes` if a new pattern emerges across multiple audits. Update `## Skill refinement signals` if the audit produced shakedown notes.

This index is the working memory for the audit campaign. Keep it current.
