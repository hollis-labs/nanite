# 08 — Remaining security hardening

This folder collects everything security-flavored from the audit that wasn't
already claimed by an earlier wave's grouping. Waves 1-2 (folders `01`-`07`)
cover the release-blocking trust-boundary failures (plugin-install
convergence, Linux sandbox fail-open, agent-slug traversal) and the
correctness/lifecycle/concurrency defects. What's left — an A2A webhook SSRF
gap, an MCP dev-tool symlink TOCTOU, the still-untriaged remainder of a
repo-wide gosec G304 cluster, an ambiguous permission-mode gap, a stale
secret-name heuristic, a one-line Slowloris fix, the server's auth/bind/TLS
default posture, two routine dependency/toolchain CVE bumps, and a handful of
`internal/api` path-confinement and correctness gaps — is genuinely
heterogeneous. These 10 tasks are grouped here because they're all
security-relevant and didn't fit a tighter root-cause bucket elsewhere, not
because they share one underlying cause. This maps to the remediation guide's
**Wave 3 — Remaining security hardening**.

**The remediation guide's trust-classification instruction applies across
this entire folder and is restated here because it's load-bearing for every
task below:** for filesystem/network findings, classify source trust as one
of **external unauthenticated / external authenticated / agent-controlled /
plugin-catalog-controlled / operator CLI-controlled / OS-derived-internal**.
Severity and remediation should follow the *actual* trust boundary a finding
sits behind, not raw analyzer severity — a gosec "medium" reachable only by
an operator who already has a shell on the machine is a fundamentally
different risk than a gosec "medium" reachable by any authenticated API
caller, even though the tool doesn't distinguish them. Every task file below
applies this classification explicitly where it's relevant, and several
(01, 02, 03, 05, 09) name the specific classification and explain how it
shapes the proposed fix's urgency and shape.

Per this batch's own authoring rules: every task cites real `file:line`
pointers pulled from `docs/audits/2026-08-21-go-quality/REPORT.md` and
`findings.json`, not invented ones; every task's `Status` is `not-started`;
and several tasks flag `requires_architect_decision` differently from
`findings.json`'s own recommendation field where the task author judged the
underlying decision to already be concrete enough for an implementer — each
such divergence is called out explicitly in that task's own file, with a
path to escalate back to architect review if a reviewer disagrees.

## Task list

| # | File | Finding(s) | Summary |
|---|---|---|---|
| 01 | `01-a2a-webhook-url-validation.md` | GO-SVCCORE-004 | A2A push-notification webhook URL has no scheme/private-IP validation before an outbound request — classic webhook-callback SSRF; requires tracing the A2A submit endpoint's real auth boundary as a blocking pre-step. |
| 02 | `02-mcp-dev-grep-symlink-toctou.md` | GO-MCPTOOL-008 | `callGrep`'s directory-walk callback re-opens files without re-validating each entry, so a symlink planted inside an already-granted directory can leak content outside the grant — a real, local-filesystem TOCTOU. |
| 03 | `03-triage-remaining-gosec-g304-sites.md` | GO-SEC-003 | 68 production gosec G304 path-traversal hits sit outside the tracked `pathsafe.ResolveUnder` adoption scope; this task produces the still-unenumerated filtered list (minus the already-triaged `cmd/nanite` and `internal/agent` subsets) for an architect to walk. |
| 04 | `04-permission-default-mode-write-gap.md` | GO-SEC4-003 | `permission.Engine`'s `ModeDefault` may silently allow non-destructive writes its own doc comment says it should prompt for — a genuinely ambiguous finding (possible stale comment vs. live gap) that needs an explicit architect call, not a unilateral fix. |
| 05 | `05-secret-key-heuristic-hardening.md` | GO-SEC4-004 | The sandbox's secret-key-name substring heuristic still misses `DATABASE_URL`, `REDIS_URL`, `SLACK_WEBHOOK_URL`, `GH_PAT`, and similar names — a still-open re-confirmation of a prior audit finding, highest-risk on `AgentExec`. |
| 06 | `06-sandbox-proxy-header-timeout.md` | GO-SEC4-008 | Sandbox proxy's `http.Server` has no `ReadHeaderTimeout` (Slowloris) — narrow exposure (loopback-only), zero-risk one-line fix, the smallest task in this folder. |
| 07 | `07-server-auth-bind-tls-posture.md` | GO-RUNTIME-002 | Auth is opt-in, the listener binds all interfaces by default, there's no TLS, and no startup warning signals the gap — a documented single-user-local-app tradeoff with no enforcement toward it; one of the guide's named required architect decisions. |
| 08 | `08-dependency-toolchain-vuln-bumps.md` | GO-SEC-001, GO-SEC-002 | Routine version bumps: the OTLP HTTP exporter (reachable memory-exhaustion CVE) and the Go toolchain itself (13 reachable stdlib CVEs) are both behind released patches. |
| 09 | `09-autocomplete-and-artifact-path-hardening.md` | GO-API-001, GO-API-002, GO-API-003, GO-API-008 | Four low-severity `internal/api` path/network gaps: unconstrained autocomplete `repo_path` walk, an artifact write path missing the confinement its sibling already has, an unbounded catalog-install fetch, and a plugin-UI static route that doesn't resolve symlinks. |
| 10 | `10-api-validation-duplication-and-pagination-bug.md` | GO-API-004, GO-API-005 | Two unrelated `internal/api` findings bundled for convenience: self-documented transport-layer validation duplication across 3 producers (schedules/settings), and a real pagination bug where `handleListMemories` filters after capping the fetch window, silently under-returning results. |
