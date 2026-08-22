# Linux sandbox fail-open

This grouping covers findings in `internal/sandbox` where the sandbox's
observable behavior can silently understate its actual isolation guarantee:
on Linux, missing `bwrap` collapses "no OS-level isolation applied" into the
same success shape as "sandbox applied" (GO-SEC4-001), the network allowlist
in proxy mode relies on an ignorable env-var convention rather than
namespace-level enforcement (GO-SEC4-002), the command denylist is a
bypassable substring blocklist that matters more precisely because it can
become the only remaining control (GO-SEC4-006), and macOS's `(allow
default)` seatbelt profile leaves file reads and process inspection open by
design, a tradeoff whose disclosure this batch found reason to question
(GO-SEC4-005). The shared theme, and the reason all four sit together as
Wave 1 rather than scattered across later waves: **a required security
boundary must not silently degrade while reporting success** — the
remediation guide's own named "Silent Security Degradation" standard, and
the most direct trust-boundary failure mode this audit found.

This is Wave 1 (release-blocking trust boundaries) because the sandbox is
documented in this project's own reviewer context as the **primary security
boundary** for agent-initiated code execution — a silent gap here isn't a
correctness bug with contained blast radius, it's the specific control an
agent-controlled or prompt-injected tool call is supposed to be unable to
escape. It sits alongside `01-plugin-install-convergence/` and
`03-agent-slug-traversal/` as the audit's three release-blocking
trust-boundary groupings.

Worth stating explicitly: GO-SEC4-001/002/005/006 are not fresh discoveries.
The 2026-08-21 audit (`docs/audits/2026-08-21-go-quality/`, §8.12)
independently **re-verified all 12 findings** from an earlier dedicated
`docs/audits/2026-04-10-sandbox-hardening/` audit against current code
rather than trusting that document — and found that **most of that prior
audit's findings have already been fixed**: session-ID path traversal,
seatbelt-profile injection, proxy SSRF via DNS-rebind to RFC1918, a proxy
goroutine leak on `Stop`, the Linux read-boundary widening (narrowed
`--ro-bind` set), PID/IPC/UTS/user-namespace unsharing, and the `/tmp`
tmpfs/cross-session-leakage fix are all confirmed landed in current source.
Only these four remain open. That track record is itself signal: this isn't
a package nobody has looked at — it's had real, iterative, verified
hardening attention across two audits four months apart, and what's left is
specifically the harder architectural tradeoffs (a namespace-enforcement
redesign, a fail-open-vs-fail-closed policy call) rather than straightforward
bugs.

- `01-sandbox-fail-closed-without-bwrap.md` — GO-SEC4-001 (critical),
  GO-SEC4-002 (high), GO-SEC4-006 (low): Linux OS-sandbox fail-open on
  missing `bwrap`, the network-allowlist enforcement gap, and the
  denylist's bypassability.
- `02-macos-seatbelt-read-boundary-disclosure.md` — GO-SEC4-005 (low):
  whether the macOS seatbelt `(allow default)` read/process-inspection
  tradeoff still holds and whether it's actually disclosed — including
  direct evidence found during this task-writing pass that an internal
  planning doc currently describes stronger guarantees than the shipped
  profile provides.
