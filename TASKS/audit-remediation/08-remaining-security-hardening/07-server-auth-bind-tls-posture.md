# Default auth/bind/TLS posture — no enforcement or signal toward the documented local-only tradeoff

**Phase:** Wave 3 — Remaining security hardening (guide §4; sequenced 2026-08-21 — see the sequencing block below)
**Status:** not-started
**Depends on:** none
**Touches:** `internal/server/auth.go`, `internal/server/server.go`, `internal/server/caller_identity.go`; likely `internal/config` (any new bind-address/TLS/warning config options); `cmd/nanite/main.go`/`cmdServe` (composition-root wiring, per report §8.13)
**Requires architect decision:** true — this is explicitly named in the guide's §9 architect-decision-queue as **item 3** ("Default auth/bind/TLS/warning posture")

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 3 — remaining security hardening · **Dispatch unit:** `W3`
> - **Depends on:** `07/02` (same file and function, `cmdServe`)
> - **Blocks:** `11/10`
> - **Parallel-safe with:** `08/01`–`08/06`
> - **Gated on:** AD-15 (default auth/bind/TLS/warning posture) — a product decision as much as a security one
> - **requires_security_review:** true · **requires_regression_test:** true

> ## ✅ AD-15 DECIDED (2026-08-22) — loopback default, explicit bind-wide opt-in, always announce auth
>
> Three changes:
> 1. **`server.go:163`** — `fmt.Sprintf(":%d", s.port)` becomes `127.0.0.1:<port>`
>    unless an explicit bind-address/bind-all option is set. **That option does
>    not exist today** — add it in `internal/config` as part of this task.
> 2. **`server.go:164`** — the startup line reports `addr` and `dev` but never
>    auth status. It must always state whether auth is on, and warn when it is
>    not. Same silent-degradation class AD-01 closed.
> 3. **`auth.go:15-22`** — `basicAuthMiddleware`'s no-op-when-unset behaviour is
>    unchanged; (2) is what stops it being silent.
>
> **TLS is explicitly out of scope.** Ceremony on loopback, and a reverse proxy
> is the right answer for the wide case. Do not add it.
>
> ### ⚠ This is a breaking change and this task owns the migration note
>
> Every deployment relying on the bind-all default breaks on upgrade — Docker
> port mapping, LAN access, remote dev, a reverse proxy aimed at a non-loopback
> interface. The failure is silent from the operator's side: the service starts
> normally and simply stops being reachable.
>
> **Done-means must include** the new opt-in option named explicitly, and
> operator-facing release-note text giving the change, the symptom ("starts fine
> but is no longer reachable from other hosts"), and the one-line fix. The
> migration note is part of this task, not a follow-up.

## Findings addressed

- **GO-RUNTIME-002** (**HIGH severity**, high confidence, security — **with a substantial documented-intent caveat**) — report §8.13.

## Context

With default configuration, `nanite serve` runs an unauthenticated HTTP API bound to every network interface with zero operator-visible warning. Specifically: auth is entirely opt-in via env vars (a no-op middleware if unset — "local dev mode"); the listener binds **all** interfaces by default (no loopback-only bind option exists in config at all, per the audit's own check); there is no TLS anywhere; and no startup-time log line announces whether auth is enabled or disabled.

**Present both sides directly — do not resolve this yourself, this is exactly what the architect-decision-queue exists for:**

- **Documented intent:** `internal/server/caller_identity.go` explicitly documents this as an intentional design decision for a single-user local app ("single-user local app... Basic Auth is optional and coarse"). Under that framing, the current defaults are a deliberate, self-aware tradeoff, not an oversight.
- **The actual gap:** the documented intent has **no enforcement or even a default toward it**. There's no loopback-only bind option, and no runtime signal when the "local-only, trust-based" assumption is silently unmet — e.g., the host later lands on a shared network, a firewall rule lapses, or someone runs `nanite serve` on a machine they don't fully control. A design that's fine "as long as the host stays local-only" currently has zero mechanism telling the operator when that assumption breaks.

**Trust classification:** this finding is fundamentally about the *absence* of a trust boundary (no auth by default), not about a specific input crossing an existing one — the guide's filesystem/network trust taxonomy applies more directly to findings 01-06/09 in this folder. Here the relevant question is "what should the default posture assume about its own network trust," which is exactly why the guide names this a required architect decision rather than an implementation call.

## What to do

**Architect decision required first — this task cannot be dispatched to a worker until the architect chooses among (or names a different) option:**

- **(a)** Default the listener to `127.0.0.1` unless an explicit bind-all opt-in is set — closes the exposure by default without removing the capability for someone who genuinely wants LAN/remote access.
- **(b)** Log a clear startup warning whenever auth is unconfigured, regardless of bind address — cheaper, purely observational, doesn't change any default behavior, just makes the current tradeoff visible instead of silent.
- **(c)** Both (a) and (b) together — the audit's own recommendation names both as a joined "and/or," suggesting they're not mutually exclusive and may both be warranted.
- **(d)** Explicitly accept the current tradeoff as-is and formally close this finding as **accepted-risk**, if the architect judges the documented-intent framing sufficient on its own — a legitimate outcome per the guide's disposition table (§7C), not a foregone conclusion that code must change.

**TLS:** the report separately notes no TLS anywhere. Determine whether this should be bundled with the bind/auth decision (TLS on a local-only loopback bind is arguably unnecessary; TLS on a bind-all default would be a much stronger argument) or deferred as a separate, later decision — flag this bundling question to the architect explicitly rather than assuming either way.

Once a direction is chosen, implement the corresponding change(s) in `internal/server/server.go` (listener construction), `internal/server/auth.go` (middleware wiring / warning log), and `internal/server/caller_identity.go` (update the doc comment to describe the *enforced* posture, not just the aspirational one), plus whatever `cmdServe` (report §8.13, the boot composition root) needs for the new default/flag.

**All production callers:** `internal/server`'s listener construction has effectively one production caller (`cmdServe`) per report §8.13's full review of that function — confirm this remains true before changing the default, since a config default change is exactly the kind of thing that needs every real construction site checked, even though this isn't today a multi-call-site finding.

## Non-goals

Do not build a full auth/authorization system (roles, per-endpoint tiers) as part of this task — report §8.5 separately confirms the current blanket-auth-covers-everything design is an explicitly accepted, self-documented tradeoff on its own terms, not part of this finding.

## Tests required

If a startup warning log is added: a test asserting it fires when auth env vars are unset and does not fire when they're set. If a default bind-address change is made: a test (or documented manual verification, since binding is process-level) confirming the new default and that the bind-all opt-in still works when explicitly requested.

## Prevention

Maps directly to the guide's "Silent Security Degradation" standard (§4 Wave 7: "a required security boundary must not silently degrade while reporting success"). Whichever direction is chosen, the fix is itself the prevention mechanism for this specific finding; consider whether a boot-time self-check (log/warn) should become a repeatable pattern for other opt-in security postures in the codebase, not just this one.

## Verification

Manual verification of `nanite serve` with default config showing the new behavior (bound to loopback, and/or logging the warning, per the chosen direction); `go build ./...`; `go test ./internal/server/...`.

## Risk / rollback

A bind-address default change is the highest-risk option — anyone currently relying on the implicit bind-all default for a legitimate remote/LAN deployment would need to pass a new explicit opt-in flag after upgrading, a real breaking-change consideration the architect should weigh explicitly. A warning-log-only change carries near-zero behavioral risk. Rollback for either is a straightforward revert, but rolling back a bind-default change after operators have started relying on the tighter default could itself be a mid-stream policy flip worth avoiding — get this decision right once rather than iterating in production.

## Done means

- [ ] Architect decision recorded (option a/b/c/d above, or a named alternative)
- [ ] Corresponding implementation landed (or, if (d), the finding formally dispositioned as accepted-risk with rationale recorded)
- [ ] `caller_identity.go`'s doc comment updated to match the actual enforced behavior
- [ ] Tests above pass for whichever direction was implemented

## Work log

<!-- Worker fills this in. -->

## Review notes

<!-- Reviewer fills this in. -->
