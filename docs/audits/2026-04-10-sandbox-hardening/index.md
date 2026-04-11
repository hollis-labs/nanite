# Deep Review — Sandbox Hardening

**Date:** 2026-04-10
**Reviewer:** `nanite-reviewer-backend` (code-review + go roles, deep-review skill)
**Scope slug:** `sandbox-hardening`
**Release context:** first beta for developer friends. Blockers must be fixed; polish can wait.

## Scope

The user asked for a sandbox-hardening deep review following up on the "Sandbox Hardening 2026-04-07" milestone and the reviewer-context file's explicit flag that this subsystem needs extra attention.

**Interpreted as:** review the `internal/sandbox/` package and every call site that crosses the sandbox trust boundary, with attention to:

- sandbox escape vectors (path escapes, seatbelt/bwrap arg injection, env leakage, fd inheritance, ptrace, IPC, devices)
- network allowlist correctness (domain vs IP, DNS, port, IPv6, proxy bypasses)
- process isolation strength (PID/IPC/user ns, signal handling, resource exhaustion)
- subprocess lifecycle under sandbox (zombies, cancellation, timeout cleanup, shutdown order)
- concurrency/races in sandbox setup and teardown
- secret/credential exposure to sandboxed processes
- observability blind spots
- platform coverage gaps (darwin vs linux vs "other")
- regression risk in recent sandbox-hardening work
- secure defaults

### Files read in full

- `internal/sandbox/exec.go`
- `internal/sandbox/exec_test.go`
- `internal/sandbox/sandbox.go`
- `internal/sandbox/sandbox_test.go`
- `internal/sandbox/denylist.go`
- `internal/sandbox/proxy.go`
- `internal/sandbox/proxy_test.go`
- `internal/sandbox/os_darwin.go`
- `internal/sandbox/os_linux.go`
- `internal/sandbox/os_other.go`
- `internal/mcp/code_exec_tools.go` — callers of `sandbox.Dir` / `AgentExec`
- `internal/api/shell.go` — caller of `sandbox.UserExec` (UserExec YOLO path)
- `internal/workflow/handlers.go` (partial) — `ShellStep` caller
- `internal/service/chat_generate.go:880-928` — `setupCLIContext` caller
- `cmd/nanite/main.go` (grep-level) — `NewCodeExecTransport` registration

### Files sampled

- `internal/mcp/code_exec_tools_test.go` — grepped for session_id test coverage
- `.nanite/agents/reviewer-backend.md` — full (context file)
- `.nanite/agents/backend.md` — skimmed as companion
- `docs/hardening-phase-plan.md` — grepped for design intent

### Files NOT read (explicit blind spots)

- `internal/plugin/*` — plugin system is out of scope unless it intersects sandbox
- `internal/plugin/builtin/adapter-*` — managed-section protocol is known issue, not re-inspected
- `internal/provider/*`, `internal/chat/*` — except the one sandbox call site
- `internal/mcp/dev_tools.go`, `general_tools.go` — tool surface outside code_exec
- `internal/store/*`, migrations — trust boundary elsewhere
- Any code that would require running tests or bwrap to verify behavior claims

## Methodology

Followed the six-step methodology in `~/.agentrc/skills/deep-review.md`:

1. Scope parsing: sandbox-hardening → package + direct callers
2. Category enumeration: Security (focus), Concurrency correctness (focus), Memory and resource leaks (focus), Error handling (secondary), Standards/tooling (skipped for this scope — dedicated in a later pass)
3. Evidence gathering: read actual code, cited `file:line`, quoted snippets in findings
4. Severity classification via the skill rubric; erred one step lower where the reviewer was between two levels, except for clear sandbox-escape primitives
5. One file per topic cluster; grouped minor items in `11-low-minor-observations.md`
6. Index assembled last; summary tables by severity and topic

Tooling **not** run in this pass (deliberate, to keep to a read-only audit): `go vet`, `go test -race`, `golangci-lint`, `staticcheck`, `errcheck`, `govulncheck`. These should be the next pass. The reviewer-context lists them as required for the tooling category; that category is explicitly skipped here so the security findings land first.

Invariants honored:

- All findings cite `file:line`
- No code changes
- Pre-existing known issues (beta-known-issues list, plugin-dev limitations, etc.) are not re-flagged
- No findings on plugin system, frontend, provider/LLM code, or other out-of-scope areas

## Summary by severity

| #  | Severity | Topic                      | Title                                                                                              | File                                                                                                                 |
|----|----------|----------------------------|----------------------------------------------------------------------------------------------------|----------------------------------------------------------------------------------------------------------------------|
| 01 | Critical | Security / input validation| Unvalidated MCP `session_id` → path traversal + seatbelt profile injection (macOS sandbox escape)  | [01-critical-session-id-path-traversal-and-seatbelt-injection.md](01-critical-session-id-path-traversal-and-seatbelt-injection.md) |
| 02 | Critical | Security / SSRF            | Network proxy allowlist bypassable via DNS→RFC1918, CONNECT port-any, IPv6 literals                | [02-critical-proxy-ssrf-rfc1918-and-port.md](02-critical-proxy-ssrf-rfc1918-and-port.md)                              |
| 03 | Critical | Security / silent failure  | Linux without `bwrap` silently falls back to unsandboxed AgentExec; no fail-closed                 | [03-critical-linux-silent-sandbox-fallback.md](03-critical-linux-silent-sandbox-fallback.md)                          |
| 04 | High     | Concurrency / lifecycle    | Proxy CONNECT goroutine leak + Stop() blocks indefinitely on hijacked conns                        | [04-high-proxy-connect-goroutine-leak-and-host-header.md](04-high-proxy-connect-goroutine-leak-and-host-header.md)    |
| 06 | High     | Security / platform        | Linux bwrap weaker than macOS — `--ro-bind /`, no PID/IPC/user ns, proxy mode skips `--unshare-net`| [06-high-linux-bwrap-configuration-gaps.md](06-high-linux-bwrap-configuration-gaps.md)                                |
| 07 | High     | Concurrency / lifecycle    | Sandbox subprocess lacks process group; macOS timeouts leave orphan children                      | [07-high-subprocess-lifecycle-no-process-group-orphan-children.md](07-high-subprocess-lifecycle-no-process-group-orphan-children.md) |
| 05 | Medium   | Security / control framing | Denylist substring match: false positives + false negatives; mis-framed as security control       | [05-medium-denylist-framing-and-bypasses.md](05-medium-denylist-framing-and-bypasses.md)                              |
| 08 | Medium   | Security / env handling    | `isSecretKey` heuristic over-blocks safe names and under-blocks real secrets (`DATABASE_URL`, etc.)| [08-medium-secret-key-substring-heuristic.md](08-medium-secret-key-substring-heuristic.md)                            |
| 09 | Medium   | Security / platform        | macOS seatbelt profile uses `(allow default)`, leaving reads and IPC open                          | [09-medium-seatbelt-allow-default-sandbox-escape-surface.md](09-medium-seatbelt-allow-default-sandbox-escape-surface.md) |
| 10 | Medium   | Observability              | Sandbox failures under-observable; denies, skips, timeouts not surfaced to tracing/audit          | [10-medium-observability-and-audit-gaps.md](10-medium-observability-and-audit-gaps.md)                                |
| 11 | Low      | Polish (grouped)           | 10 grouped minor observations: limitedBuffer truncation flag, Apple Silicon bin paths, etc.        | [11-low-minor-observations.md](11-low-minor-observations.md)                                                          |
| 12 | Info     | Design / praise            | Package design observations, praise, blind-spot declarations, questions for the maintainer        | [12-info-sandbox-package-notes-and-praise.md](12-info-sandbox-package-notes-and-praise.md)                            |

**Counts:** Critical 3, High 3, Medium 4, Low 1 (grouped), Info 1 (grouped)

The numbering has a small non-contiguity (03 Critical precedes 04 High, but 05 Medium interleaves before 06 High) — this happened during reviewer revision when finding 03's severity was upgraded after finding 05 was drafted. Rather than renumber files and break links in the detail files, the index table is sorted by severity for reading order; on-disk filenames preserve the write order. Future passes should renumber on write if severity reordering happens.

## Summary by topic

### Security — input validation and trust boundaries
- [01 Critical — session_id path traversal + seatbelt injection](01-critical-session-id-path-traversal-and-seatbelt-injection.md)
- [05 Medium — denylist framing and bypasses](05-medium-denylist-framing-and-bypasses.md)
- [08 Medium — isSecretKey heuristic](08-medium-secret-key-substring-heuristic.md)

### Security — network isolation
- [02 Critical — proxy SSRF via DNS / port / IPv6](02-critical-proxy-ssrf-rfc1918-and-port.md)
- [06 High — Linux bwrap gaps including proxy-mode bypass](06-high-linux-bwrap-configuration-gaps.md)

### Security — platform sandbox strength
- [03 Critical — Linux silent unsandboxed fallback](03-critical-linux-silent-sandbox-fallback.md)
- [06 High — Linux bwrap weaker than macOS](06-high-linux-bwrap-configuration-gaps.md)
- [09 Medium — macOS seatbelt allow-default baseline](09-medium-seatbelt-allow-default-sandbox-escape-surface.md)

### Concurrency correctness and resource management
- [04 High — proxy CONNECT goroutine leak + Stop hang](04-high-proxy-connect-goroutine-leak-and-host-header.md)
- [07 High — subprocess lifecycle / orphan children](07-high-subprocess-lifecycle-no-process-group-orphan-children.md)

### Error handling and observability
- [10 Medium — sandbox observability gaps](10-medium-observability-and-audit-gaps.md)
- [11 Low — grouped minor error-handling observations](11-low-minor-observations.md)

### Test quality
- Implicit across findings 01, 02, 07 — missing negative tests for traversal, SSRF, process-tree cleanup
- Explicit observation in [12 Info](12-info-sandbox-package-notes-and-praise.md)#I5

### Standards and tooling
- **Skipped this pass.** Run as the next audit scope. The reviewer-context lists `go vet`, `go test -race`, `golangci-lint`, `staticcheck`, `errcheck`, `govulncheck` as required for the tooling category; this audit prioritized security findings. Recommend scoping the next pass as `2026-04-1N-sandbox-tooling-and-tests` with `-race` and `govulncheck` as the focus.

### Antipatterns
- **Skipped this pass.** None observed at a High/Medium level; the low-severity package hygiene items are captured in finding 11.

## Recommended next steps

Priority order for the maintainer:

1. **Fix the three Criticals before beta ships.**
   - Finding 01: validate `session_id` at the MCP tool boundary AND inside `sandbox.Dir`; escape the seatbelt profile path literal. Do ALL THREE mitigations in the recommendation — regex + base-dir containment check + seatbelt escape.
   - Finding 02: add IP classification and DNS-pinning in the proxy dial paths; restrict CONNECT ports to 443 (configurable). Add regression tests for `127.0.0.1`-resolving hostnames and port-other-than-443.
   - Finding 03: change `os_linux.go` to fail closed when bwrap is missing; add the opt-out env var for CI; add startup probe in `cmd/nanite/main.go`.

2. **Fix the three Highs before beta ships (or document why not).**
   - Finding 04: unblock-by-close idiom on proxy CONNECT; per-CONNECT deadline; in-flight conn registry for Stop. Regression test: Stop-with-stalled-CONNECT completes in bounded time.
   - Finding 06: tighten the bwrap invocation. At minimum: add `--unshare-pid --unshare-ipc --unshare-user --unshare-uts --unshare-cgroup`, swap `--bind /tmp` for `--tmpfs /tmp`, and either fix the proxy-mode network gap or drop proxy mode on Linux for beta.
   - Finding 07: add `Setpgid` + `Cancel` + `WaitDelay` to the exec setup. Regression test: spawn-child-then-timeout-then-verify-no-orphan.

3. **Medium findings can land in a beta-1 hotfix window.**
   - Finding 05: reframe denylist as an advisory hint, parse with `mvdan/sh` for tokenization.
   - Finding 08: switch `isSecretKey` to an exact-match deny list combined with the substring check; add `DATABASE_URL` etc. explicitly.
   - Finding 09: document the allow-default stance; move `/tmp` writes inside the sandbox dir; add secret-dir read denies (`.aws`, `.ssh`, `.gnupg`, `.anthropic`).
   - Finding 10: add structured events via OTel; log bwrap-missing on every call; add `sandbox_status` endpoint and startup banner for degraded mode.

4. **Follow-up review passes:**
   - **Sandbox tooling and tests (`2026-04-1N-sandbox-tooling-and-tests`)** — run `go vet`, `go test -race`, `golangci-lint`, `staticcheck`, `errcheck`, `govulncheck` and file findings. Write the negative tests from findings 01/02/07.
   - **Plugin trust boundary (`2026-04-1N-plugin-trust-boundary`)** — the reviewer-context explicitly frames plugins as in-process with full host access. A focused pass on what a malicious plugin can do with Nanite's sandbox machinery (can it call `sandbox.Dir` with a hostile session_id? Can it construct its own AgentExec?) would complement this audit.
   - **MCP tool inventory review (`2026-04-1N-mcp-tool-inventory`)** — every built-in MCP tool is a trust boundary. This audit only looked at `nanite_code_execute`. `dev_tools.go` (grep/read/write/edit/glob) and `general_tools.go` (web_fetch/web_search) are likely hiding similar input-validation gaps.
   - **Proxy hardening v2** — if the proxy stays, it needs a deeper review on its own, including multipart/chunked body handling, header injection, and WebSocket upgrade paths (does the current proxy handle `Upgrade: websocket`?).

## Known issues skipped

Pre-existing items tracked elsewhere, NOT re-flagged per the reviewer-context guardrails:

- **Beta known issues** (`docs/beta-known-issues.md`) — all P0 items are closed as of 2026-04-10; P1 empty.
- **Plugin framework limitations** (`.nanite/agents/plugin-dev.md` §Known Limitations, items 1–26): scaffold template imports, `adapter-opencode` format, `oembed` dead path, fragments-engine 503s, `Host.Shutdown` mutex-across-Unload, `Host.RegisterEnvelopeType` missing on SDK, plugin isolation gap, HTTP route leak.
- **Engine backlog post-beta items:** BLG-20260410-001..004.
- **Deliberate architecture:** http.ServeMux no route removal; migrations DDL-only vs seed data; two binaries only one live; `.agentrc/` vs `.nanite/` path drift; broken `.claude/commands` symlinks; `shadcn-ui` typo in config.

One item worth cross-referencing: the reviewer-context says "Envelope emission is broken system-wide" and `internal/plugin/event_stream.go` is a "dead endpoint." Neither intersects the sandbox findings here. Skipped, as instructed.

## Notes for the skill shakedown

This was the first real invocation of the `deep-review` skill. Observations for the skill maintainer, to be applied as skill refinements:

1. **Numbering vs severity re-ordering.** The skill says filenames are `NN-<severity>-<topic>.md` where `NN` is severity order. In practice, during revision a finding was upgraded from High→Critical after later-numbered findings were drafted, leaving 03 (Critical) next to 05 (Medium). The skill should either (a) instruct the reviewer to renumber on write when severity changes, accepting the link-rewrite cost, or (b) accept non-contiguous numbering and clarify that the index table is the sort of truth. This audit chose (b); the skill should say so.

2. **Tooling category skip.** The skill's methodology step 2 says "Mark skipped categories in the index methodology section with a reason." Did that here (tooling+antipatterns). But the skill's invariant list and the reviewer-context both list tooling commands as "always" for the security category. Clarify: is tooling gating, or can it legitimately be a follow-up pass? Recommend explicit: "if you skip tooling in a security-scoped pass, you MUST file a follow-up audit scope for it."

3. **Index table width.** The summary-table markdown renders wide. On a narrower terminal or in GitHub's rendered view, the last columns wrap. Consider a two-column layout or a linked short-form (number + title + severity only, links to the finding files).

4. **"Per-finding file structure" section** specifies `Problem / Evidence / Impact / Recommendation / References`. In practice, complex findings (like 01, 02, 06) benefit from sub-headings or explicit lists inside each section. The skill should say the required sections are minima, and sub-structure is allowed when it helps readability.

5. **`file:line` vs `file:line-range`.** The skill says "cite file:line." Some findings needed a range (e.g. a 10-line function). The audit used `file:line-line` which is unambiguous. Make this explicit in the skill.

6. **"Hard rule: anything longer than 1–2 sentences goes in its own file."** Good rule. Finding 11 grouped 10 low-severity items into one file; that's consistent with the "short findings can be grouped" note. But: finding 11 is almost 200 lines. Consider adding a cap: grouped files shouldn't exceed ~300 lines; beyond that, split.

7. **Renaming on severity change.** When finding 03 was upgraded Medium→Critical, the filename `05-medium-denylist...md` was renamed. The skill should include a concrete step: "if you rename a finding file, update all cross-references in the index and other finding files." Doing this by hand is error-prone; a grep for the old filename is advisable. (The reviewer did this manually; recommend skill adds a ritual.)

8. **Release-context amplification.** The skill rubric says to factor in release context. The reviewer-context file says "first beta for developer friends." This audit interpreted that as "prioritize exploitable paths, de-prioritize polish" and it worked — but the skill could be more explicit about HOW release context maps to severity shifts. Suggest a short subsection in the skill explaining: alpha → flex one level lower; beta → rubric as-written; RC → flex one level higher.

9. **Scope drift guard.** The skill should add a paragraph telling the reviewer to explicitly note when they looked at a neighbor file and found something out of scope. Helps the caller decide whether to open follow-up scopes. This audit did this informally in finding 12, section I7.

10. **Output format banner.** The skill ends with a confirmation line template. The reviewer should return this verbatim as the final chat reply. In this invocation, the parent task asks for a longer handoff report; the one-line confirmation is folded into that. Skill should clarify: if the caller asks for more, the reviewer reports more; the one-liner is the default.
