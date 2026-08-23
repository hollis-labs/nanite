# A2A push-notification webhook URL has no SSRF validation

**Phase:** Wave 3 — Remaining security hardening (guide §4; this task-creation batch is sequenced 2026-08-21 — see the sequencing block below)
**Status:** reviewed
**Depends on:** none
**Touches:** `internal/service/a2a_push_notifier.go`, `internal/service/a2a_push_notifier_test.go`; task/finding tracking metadata
**Requires architect decision:** false — the operator approved the traced external-unauthenticated disposition and narrow delivery-seam remediation; A2A authentication redesign remains out of scope

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 3 — remaining security hardening · **Dispatch unit:** `W3`
> - **Depends on:** Wave 2 complete
> - **Blocks:** `11/07` — that task deduplicates the SSRF CIDR logic this one may introduce or reuse
> - **Parallel-safe with:** `08/02`, `08/03`, `08/04`, `08/06`, `08/10`
> - **Gated on:** none
> - **requires_security_review:** true · **requires_regression_test:** true

## Findings addressed

- **GO-SVCCORE-004** (**high severity, high confidence**, security) — report §8.3. Reclassified after the mandatory auth/caller trace confirmed an external-unauthenticated submission path when optional Basic Auth is unset.

> **Resolved divergence:** the mandatory trace found the weaker boundary: the shared Basic Auth middleware is a no-op when its environment variables are unset, and the A2A submit route has no additional authentication gate. The operator approved proceeding with the same narrow SSRF remediation, recording the boundary as external unauthenticated, and correcting severity. `findings.json` now matches that resolution; no A2A authentication redesign is included here.

## Context

**Root cause:** the A2A push-notification webhook URL (`TaskSubmitRequest.PushNotificationConfig.URL`, caller-supplied at task-submit time) flows directly into an outbound `http.NewRequestWithContext` + `client.Do` call at `a2a_push_notifier.go:152-166` with **no validation** — no scheme allowlist, no private-IP/localhost block. This is the classic webhook-callback SSRF pattern the remediation guide names explicitly. Gosec's G107 rule doesn't catch it because G107 only targets the `http.Get`/`http.Post` call shape directly, not the `NewRequestWithContext`+`Do` idiom used here — so this is real uncovered surface, not pre-existing lint noise that was suppressed.

**Trust classification (guide's Wave 3 instruction, applied explicitly):** **external unauthenticated**. The webhook URL is supplied by `SendMessage` callers on `POST /api/a2a/jsonrpc`. That route uses the common middleware chain, but Basic Auth is optional and becomes a no-op when its environment variables are unset; no route-local A2A peer authentication exists. The reviewed 08/07 server change makes loopback the default bind, reducing default exposure, but an explicitly non-loopback deployment with auth unset still presents this unauthenticated SSRF surface. Because such a caller could direct authenticated POSTs toward private services, loopback, link-local/IMDS, CGNAT, ULA, or unspecified destinations, the corrected severity is **high**. The completed trace removes the audit's uncertainty, so confidence is **high**.

**Desired invariant:** an A2A push-notification webhook URL must never resolve to a loopback, link-local, or private-range (RFC1918/CGNAT) host unless that host has been explicitly allowlisted by the operator; scheme must be restricted to `https` (or `http` only under an explicit, documented opt-in).

**Mandatory pre-step (completed):** the production auth boundary and complete caller chain are recorded in the Work Log below.

## What to do

- Confirm the real current entry point for `TaskSubmitRequest` (report only cites the vulnerable request-construction site, `a2a_push_notifier.go:152-166`, not the HTTP/MCP entry point that populates it — find it fresh, don't assume).
- Add validation either at submission time (`a2a_task_manager.go`) or delivery time (`a2a_push_notifier.go`) — either is acceptable per the guide:
  - scheme allowlist (`https` by default)
  - reject resolution to loopback/link-local/private ranges using a **DNS-rebind-safe** pattern (resolve once, validate the resolved IP(s), dial the pinned literal) — mirror the pattern report §8.12 confirms is already correct in `internal/sandbox/proxy.go`, rather than checking the hostname string alone (bypassable, per that same package's own SSRF-fix history).
  - **Check for reuse first:** `internal/sandbox/proxy.go` and `internal/mcp/general_tools.go` already each maintain an SSRF CIDR denylist (loopback/RFC1918/CGNAT/link-local-IMDS/IPv6 ULA) — the audit separately flags these two as duplicated with no parity enforcement (GO-SEC4-007, tasked in folder 11, not here). Reusing one of them here rather than writing a **third** independent copy avoids compounding that exact duplication risk. If reuse isn't practical (import-cycle risk, wrong package layer), a new copy is acceptable but must be justified in the Work Log and cross-referenced against GO-SEC4-007's cleanup task.
- **All production callers:** enumerate every code path constructing an outbound request from `PushNotificationConfig.URL`. The audit names exactly one (`a2a_push_notifier.go:152-166`) — confirm that's still the only one before treating this as a single-site fix.

## Non-goals

Do not build a general-purpose outbound-URL policy engine. Do not touch unrelated A2A task-routing logic — report §8.3 finds `a2a_task_manager.go`'s "derive state from real execution" design and `a2a_push_notifier.go`'s retry/backoff sound aside from this one gap.

## Tests required

- Reject a loopback/private-range URL at validation time.
- Reject a non-allowlisted scheme.
- Regression test reproducing the original gap: a webhook URL pointing at `127.0.0.1` or a private IP is now rejected, not silently accepted.
- If shared CIDR-denylist logic is reused, a test asserting behavior stays consistent with the sandbox proxy's own equivalent cases.

## Prevention

Nothing currently enforces this pattern in the A2A subsystem; this validation *is* the fix. If a third independent CIDR-denylist copy is added instead of reusing an existing one, that itself becomes new duplication the guide's "fix every sibling path" principle would flag on a future pass — note this explicitly if that path is taken.

## Verification

`go build ./...`; `go test ./internal/service/...` (new/updated A2A tests); manual/documented demonstration that a private-IP webhook URL is now rejected at submission or delivery.

## Risk / rollback

Low risk — validation is additive and only rejects previously-unchecked values. Rollback is reverting the validation commit. Watch for any legitimate internal use case (e.g., a documented local-testing pattern posting to `localhost`) that this would newly break — if one exists, it needs an explicit allowlist entry, not a blanket revert.

## Done means

- [x] Auth-boundary pre-step traced and documented in Work Log
- [x] Validation added at delivery time (HTTPS allowlist + private/loopback range rejection, DNS-rebind-safe)
- [x] Tests above pass, including mixed-answer, pinned-dial, redirect-rebinding, policy-parity, and retry/backoff regressions
- [x] `gosec ./internal/service/...` shows no new findings on the touched files; focused G107/G704 scan is clean
- [x] Shared `internal/ssrf` policy from resolved GO-SEC4-007 / task 08/09 reused as the fourth consumer

## Work log

### 2026-08-23 — operator-approved remediation implemented

- **Auth/caller trace:** `API.RegisterRoutes` registers
  `POST /api/a2a/jsonrpc`; `handleA2AJSONRPC` dispatches the public A2A
  `SendMessage` method to `handleTaskSubmit`; that handler copies the wire
  `PushNotificationConfig` into `service.TaskSubmitRequest`; and
  `TaskManager.SubmitTask` persists it. The route is inside the production
  `recover → logging → CORS → basicAuth → callerIdentity → bodyLimit → mux`
  chain, but `basicAuthMiddleware` returns the next handler unchanged when
  `NANITE_AUTH_USER` and `NANITE_AUTH_PASSWORD` are both unset. There is no
  route-local A2A peer check. The boundary is therefore formally classified
  **external unauthenticated**, as approved by the operator. Authentication
  redesign is deliberately out of scope.
- **Severity correction:** GO-SVCCORE-004 is now **high severity / high
  confidence**. The unauthenticated network boundary can supply both the
  callback URL and its Authorization value, giving it a server-side POST
  primitive toward internal services. The loopback-default bind from reviewed
  task 08/07 lowers exposure in the default configuration, but does not remove
  the issue for explicitly exposed, auth-unset deployments. The completed
  trace also removes the audit's only stated source of confidence uncertainty.
- **Sole outbound consumer:** repository-wide searches for
  `PushNotificationConfig.URL` / `config.URL` and outbound delivery construction
  find exactly one production request: `A2APushNotifier.processDelivery`.
  `TaskManager` only persists the config and enqueues state-transition
  deliveries; `cmd/nanite/main.go`'s background ticker only calls
  `ProcessPendingDeliveries`. No sibling outbound request path exists.
- **Implementation:** delivery now uses the shared `internal/ssrf` package as
  its fourth consumer (after sandbox proxy, MCP web fetch, and plugin catalog
  download). HTTPS is the production default. Every DNS answer is validated;
  any denied answer rejects the whole resolution; the dialer receives the
  validated IP literal; keep-alive reuse is disabled; and every redirect is
  scheme-checked and forced through a fresh resolve/validate/pin cycle. This
  cross-references the GO-SEC4-007 consolidation resolved and implemented by
  task 08/09; no CIDR copy or policy fork was introduced.
- **Behavior preservation:** notification payload/auth headers, the 10-second
  overall client timeout, response handling, maximum three attempts, quadratic
  one/four-minute retry schedule before the terminal attempt, and deletion on
  success/max-attempts remain unchanged. SSRF and redirect rejections enter the
  same existing retry/backoff branch as other delivery errors.
- **Regression evidence:** focused tests cover HTTPS-default rejection;
  RFC1918, IPv4/IPv6 loopback, link-local/IMDS, CGNAT, IPv6 ULA, and IPv4/IPv6
  unspecified parity; rejection of mixed public/private DNS answers before
  dial; pinned-literal dialing; redirect-time DNS rebinding; and persistence of
  the existing one-minute first retry after an SSRF rejection. Existing local
  HTTP integration tests now opt into both exceptions explicitly rather than
  weakening production defaults.
- **Validation:** `go test ./internal/service -run 'TestA2APushNotifier'
  -count=10`, `go test -race ./internal/service -run
  'TestA2APushNotifier' -count=1`, `go test ./internal/ssrf
  ./internal/service -count=1`, `go vet ./...`, `go build ./cmd/nanite/`, and
  the final `go test ./...` baseline pass. `gosec -quiet
  -include=G107,G704 ./internal/service/...` is clean. An unrestricted service
  scan still reports 29 pre-existing findings in unrelated files and none in
  `a2a_push_notifier.go`. One earlier full-suite run observed the unrelated
  `TestDurableAgentStopRuntimeErrorMarksFailed` event-ordering flake; its
  expected event was present at index 1 rather than index 0, the test passed
  10/10 in isolation, and the final full baseline passed.

## Review notes

- 2026-08-23: Fresh security review at `60137399ac5c1d6dbae17f9bec238cb1da0afd44` passed with no findings. The reviewer independently confirmed the external-unauthenticated/high-severity correction, sole outbound URL consumer, HTTPS-only all-answer validation, pinned-literal dialing, fresh redirect resolution, and absence of production bypasses for the unexported test seams. Host/SNI/auth handling and existing timeout/retry/backoff behavior remain intact. Focused adversarial tests, race tests, service/SSRF packages, API/server boundary checks, G107/G704 gosec, build, vet, and diff checks passed.
