# A2A push-notification webhook URL has no SSRF validation

**Phase:** Wave 3 — Remaining security hardening (guide §4; this task-creation batch is unsequenced — see folder README)
**Status:** not-started
**Depends on:** none
**Touches:** `internal/service/a2a_push_notifier.go`, `internal/service/a2a_task_manager.go` (`TaskSubmitRequest`/`PushNotificationConfig` types); possibly a shared SSRF-CIDR-check helper if one is reused instead of written fresh (see Non-goals)
**Requires architect decision:** false — but see the divergence note below and the mandatory pre-step; do not treat "false" as "skip verification"

## Findings addressed

- **GO-SVCCORE-004** (medium severity, medium confidence, security) — report §8.3.

> **Divergence from `findings.json`:** the machine-readable catalog flags this finding's own `requires_architect_decision` as `true`. This task file sets it to `false` per this batch's authoring instruction, on the judgment that the *fix direction itself* (validate scheme + block private/loopback ranges) is unambiguous and doesn't need architect input. What genuinely is unresolved — the A2A endpoint's auth boundary — is called out below as a **mandatory pre-step**, not waved away. If whoever picks this up finds the auth boundary is weaker than assumed, escalate back to architect review before implementing, since that would change the real-world severity above what this task assumes.

## Context

**Root cause:** the A2A push-notification webhook URL (`TaskSubmitRequest.PushNotificationConfig.URL`, caller-supplied at task-submit time) flows directly into an outbound `http.NewRequestWithContext` + `client.Do` call at `a2a_push_notifier.go:152-166` with **no validation** — no scheme allowlist, no private-IP/localhost block. This is the classic webhook-callback SSRF pattern the remediation guide names explicitly. Gosec's G107 rule doesn't catch it because G107 only targets the `http.Get`/`http.Post` call shape directly, not the `NewRequestWithContext`+`Do` idiom used here — so this is real uncovered surface, not pre-existing lint noise that was suppressed.

**Trust classification (guide's Wave 3 instruction, applied explicitly):** the webhook URL is supplied by whoever calls the A2A task-submit endpoint. The audit explicitly states it did **not** independently verify whether that endpoint's auth boundary restricts submission to trusted peer agents only — this is the single open variable governing real severity. Until confirmed, classify conservatively as **external authenticated** (any caller who can reach and authenticate to the A2A submit endpoint) rather than **agent-controlled** (which would imply only Nanite's own already-trusted agents can set this value — not yet shown). If the pre-step below finds the endpoint is unauthenticated or weakly gated, this becomes **external unauthenticated** and should be escalated back for architect/severity re-review before implementation proceeds.

**Desired invariant:** an A2A push-notification webhook URL must never resolve to a loopback, link-local, or private-range (RFC1918/CGNAT) host unless that host has been explicitly allowlisted by the operator; scheme must be restricted to `https` (or `http` only under an explicit, documented opt-in).

**Mandatory pre-step (blocking — do this before writing any fix):** trace the actual production auth boundary on the A2A task-submit HTTP entry point end to end — which handler registers it, what middleware wraps it, whether Basic Auth or any other check gates it. Record the answer in the Work Log. This determines whether the trust classification above holds or needs to escalate.

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

- [ ] Auth-boundary pre-step traced and documented in Work Log
- [ ] Validation added at submission or delivery time (scheme allowlist + private/loopback range rejection, DNS-rebind-safe)
- [ ] Tests above pass
- [ ] `gosec ./internal/service/...` shows no new findings on the touched files
- [ ] Cross-reference note added if the sandbox/MCP CIDR list is reused, or a justification recorded if a new one was written instead

## Work log

<!-- Worker fills this in. -->

## Review notes

<!-- Reviewer fills this in. -->
