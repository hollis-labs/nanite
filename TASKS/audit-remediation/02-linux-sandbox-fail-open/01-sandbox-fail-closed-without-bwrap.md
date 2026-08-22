# Sandbox must not silently degrade to unisolated execution when `bwrap` is absent on Linux

**Phase:** Wave 1 — Release-blocking trust boundaries (per remediation guide §4)
**Status:** not-started
**Depends on:** none within this batch (sequencing note: this task's outcome — whether
`ExecResult`/`AgentExec` grows an observable "sandbox applied" signal — is a natural
prerequisite for anything in `08-remaining-security-hardening/` or future work that wants
to surface sandbox status in the UI, but no task in this batch currently depends on that).
**Touches:** `internal/sandbox/os_linux.go`, `internal/sandbox/exec.go`,
`internal/sandbox/denylist.go`, `internal/sandbox/os_other.go` (same silent-fallback shape,
in scope per "all production callers" below), plus whatever new test files the chosen
direction requires (e.g. `internal/sandbox/os_linux_test.go`).

```yaml
requires_architect_decision: true
requires_security_review: true
requires_regression_test: true
```

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 1 — release-blocking trust boundaries · **Dispatch unit:** `W1`
> - **Depends on:** `00/01`, `00/02`
> - **Blocks:** `08/05` (same file, `internal/sandbox/exec.go`)
> - **Parallel-safe with:** `01/01`, `01/02`, `02/02`, `03/01`, `12/02`
> - **Gated on:** AD-01 (fail-closed vs. visible opt-in) **and** AD-02 (network allowlist level) — decide both together; an inconsistent pair is worse than either coherent answer.
> - **requires_security_review:** true · **requires_regression_test:** true

> ## ✅ AD-01 AND AD-02 DECIDED (2026-08-22) — implement these, do not re-open them
>
> **AD-01 — fail closed, with an explicit config opt-in to degrade.** Absent
> `bwrap`, `AgentExec`/`UserExec` return an error. A new config knob (none
> exists today) lets an operator deliberately accept unisolated execution; when
> set, **every** degraded exec logs at warn.
>
> - **Delete the `sync.Once`** (`os_linux.go:14`, `os_other.go:11`). Today the
>   warning fires once per *process lifetime*, so every unsandboxed exec after
>   the first is silent. That is the actual mechanism of the finding.
> - **Change `applyOSSandbox`'s return type to carry an isolation verdict.**
>   `(cleanup func(), err error)` has no third state, so `exec.go:158` and
>   `exec.go:203` cannot tell "isolated" from "not isolated". This is a
>   prerequisite, not an option.
> - **Scope is `os_linux.go` + `os_other.go`** — same fail-open shape, same
>   `sync.Once`. macOS is unaffected (`sandbox-exec` ships with the OS).
> - **Accepted consequence:** on Linux without bubblewrap this disables
>   `internal/mcp/dev_tools.go`, `internal/mcp/code_exec_tools.go`,
>   `internal/workflow/handlers.go`, and `internal/api/shell.go` until `bwrap`
>   is installed or the knob is set. Intended, not a regression.
> - **Test the Linux path deliberately.** The primary dev platform is darwin,
>   where this code never fires — a default `go test` run proves nothing here.
> - **Cross-reference `GO-SEC4-006`.** The opt-in re-creates the exact scenario
>   in which the bypassable command denylist is the *only* remaining control.
>   Anyone setting the knob is relying on it as their whole security boundary.
>
> **AD-02 — fix the network inversion properly.** Implement this file's own
> `TODO(network-isolation)` (`os_linux.go:141-145`): socket-passing handoff so
> the allowlist proxy runs inside the sandbox netns, making `--unshare-net`
> unconditional.
>
> - Today `os_linux.go:146` applies `--unshare-net` only `if
>   len(networkAllow) == 0`, so **configuring an allowlist makes the sandbox
>   strictly weaker than configuring nothing**, with enforcement reduced to
>   `HTTP(S)_PROXY` convention that any raw socket ignores.
> - `GO-SEC4-002` moved `needs-architect-decision` → `remediate` in
>   `findings.json` as a result.
>
> **Re-scope warning.** AD-02 is real engineering — a proxy/socket-passing
> restructure — not a flag flip, and it is larger than AD-01's change. This
> task file's scope and effort framing predate both decisions; treat the
> sections below as evidence and context, and this banner as the instruction
> where they differ.

## Context

### Findings addressed

- **GO-SEC4-001** (critical, confidence high) — primary. On Linux, if `bwrap`
  (bubblewrap) isn't installed, `applyOSSandbox` silently falls back to no
  OS-level isolation and returns success. `AgentExec` treats this identically
  to a real sandbox being applied; nothing distinguishes the two outcomes at
  any call site.
- **GO-SEC4-002** (high, confidence high) — related, `requires_architect_decision: true`
  in the audit's own finding record. Even when `bwrap` is present, the Linux
  network allowlist is not enforced at the OS/namespace level in proxy mode:
  when `NetworkAllow` is non-empty, the sandboxed process keeps the host's
  network namespace, and confinement to the loopback proxy relies entirely on
  `HTTP_PROXY`/`HTTPS_PROXY` env-var convention.
- **GO-SEC4-006** (low, confidence high) — related. The command denylist is a
  bypassable literal-substring blocklist. Low severity in isolation (the OS
  sandbox is normally the real boundary), but its weakness matters more
  precisely because GO-SEC4-001 means it is sometimes the *only* remaining
  control on a Linux host without `bwrap`.

Source: `docs/audits/2026-08-21-go-quality/REPORT.md` §8.12 and
`docs/audits/2026-08-21-go-quality/findings.json`. All three findings are
explicit **re-confirmations against current code** — the 2026-08-21 audit
independently re-verified the prior `docs/audits/2026-04-10-sandbox-hardening/`
audit's findings rather than trusting that document, and these three are
still open. See this folder's `README.md` for the broader disposition of that
prior audit's other findings (most are already fixed).

### Root cause

`applyOSSandbox` on Linux (`internal/sandbox/os_linux.go`) treats "the
OS-level isolation tool is unavailable" as a warn-and-continue condition
rather than a failure condition, and its function signature — `(cleanup
func(), err error)` — has no way to represent a third outcome ("ran, but
degraded") distinct from "succeeded" and "failed." The caller, `AgentExec`
(`internal/sandbox/exec.go`), only branches on `err != nil`; a nil error is
read as "sandbox applied." This is the same underlying shape as the 2026-04-10
audit's finding 03, which recommended fixing it and was not (fully) acted on
— `bwrap`'s *absence* still returns success-shaped output today.

The network-allowlist gap (GO-SEC4-002) shares the same root cause at one
remove: even when `bwrap` *is* present, the code deliberately chooses to keep
the sandboxed process in the host network namespace rather than a strict
`--unshare-net` when `NetworkAllow` is non-empty, because moving the
allowlist proxy inside the sandbox's own netns is real, undone
infrastructure work (see the `TODO(network-isolation)` comment in current
source, cited below). The enforcement point ends up being an env-var
convention, which is not a boundary — it degrades gracefully for
well-behaved HTTP clients and not at all for anything else.

The denylist gap (GO-SEC4-006) is a pre-existing, independently weak control
that becomes load-bearing specifically because of GO-SEC4-001 — it's included
here because its severity is a direct function of the primary finding, not
because it needs a different root-cause analysis.

### Current behavior

**GO-SEC4-001 — silent fallback on missing `bwrap`:**

```go
// internal/sandbox/os_linux.go:64-71
func applyOSSandbox(cmd *exec.Cmd, sandboxDir string, extraWritePath string, networkAllow []string) (cleanup func(), err error) {
	bwrapPath, lookErr := exec.LookPath("bwrap")
	if lookErr != nil {
		bwrapWarnOnce.Do(func() {
			slog.Warn("sandbox: bwrap not found — install bubblewrap for OS-level isolation (using Tier 1 only)")
		})
		return func() {}, nil
	}
	...
```

`bwrapWarnOnce` (`sync.Once`, declared `internal/sandbox/os_linux.go:14`) means
this warning is logged **at most once per process lifetime**, not once per
call — a long-running `nanite serve` process that logs this warning at first
use will never log it again, even though every subsequent `AgentExec` call is
equally unsandboxed.

The call site never distinguishes the two outcomes:

```go
// internal/sandbox/exec.go:157-162 (AgentExec)
// Apply OS-level sandbox (no-op on unsupported platforms).
cleanup, err := applyOSSandbox(cmd, sandboxDir, opts.WorkingDir, opts.NetworkAllow)
if err != nil {
	return nil, fmt.Errorf("sandbox: os-level setup: %w", err)
}
defer cleanup()
```

`ExecResult` (`internal/sandbox/exec.go:89-94`) — the struct every caller
receives back — has no field at all indicating whether OS-level isolation was
actually applied:

```go
type ExecResult struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	TimedOut bool   `json:"timed_out"`
}
```

The same missing-tool-means-silent-fallback shape exists in
`internal/sandbox/os_other.go` for platforms with no OS sandbox support at
all (Windows/BSD) — that file's comment acknowledges the degradation
explicitly, which is a materially different situation (the project has never
claimed OS-sandbox support there) from Linux's case (bwrap support is claimed
and silently absent). Both are in scope for this task's fix because both feed
the same `AgentExec`/`ExecResult` surface — see "Scope" below for how they
should be treated differently.

**GO-SEC4-002 — network allowlist not namespace-enforced:**

```go
// internal/sandbox/os_linux.go:132-148
// --unshare-net is conditional on the absence of a host-side allowlist.
// Rationale: AgentExec's allowlist proxy runs in the host network
// namespace; once the sandbox gets its own netns, loopback is
// per-namespace and the proxy becomes unreachable from inside. When
// networkAllow is non-empty the caller has opted in to mediated egress,
// so we keep the sandbox in the host netns and rely on the proxy +
// HTTP(S)_PROXY env vars for enforcement. With no allowlist we unshare
// the network namespace for full offline isolation.
//
// TODO(network-isolation): move the allowlist proxy into the sandbox
// netns (e.g. via a helper socket or a proxy pre-bound to a socket
// inherited across unshare) so --unshare-net can be unconditional.
// Revisit once the proxy is restructured to run co-located with the
// sandboxed process or once a socket-passing handoff is in place.
if len(networkAllow) == 0 {
	bwrapArgs = append(bwrapArgs, "--unshare-net")
}
```

This code is honest about the gap in its own comment (unlike GO-SEC4-001,
this isn't a silently-introduced regression — it's a documented, deliberate,
still-open tradeoff from the same `TODO`). macOS is unaffected: seatbelt's
`(deny network-outbound)`/`(deny network-inbound)` pair
(`internal/sandbox/os_darwin.go:120-130`) is kernel-enforced regardless of
proxy mode.

**GO-SEC4-006 — denylist is a bypassable substring blocklist:**

```go
// internal/sandbox/denylist.go:42-50
func CheckDenylist(command string) (blocked bool, reason string) {
	lower := strings.ToLower(strings.TrimSpace(command))
	for _, p := range defaultDenyPatterns {
		if strings.Contains(lower, strings.ToLower(p.pattern)) {
			return true, "blocked by denylist: " + p.reason + " (matches " + repr(p.pattern) + ")"
		}
	}
	return false, ""
}
```

`defaultDenyPatterns` (`internal/sandbox/denylist.go:10-38`) is a fixed list of
literal command-prefix strings (`"rm -rf /"`, `"mkfs"`, `"dd if="`, etc.)
matched via `strings.Contains` on a lowercased, trimmed string. This is
defeated trivially by whitespace variation (`rm  -rf /`), flag reordering,
variable expansion, or wrapping in an interpreter (`sh -c 'rm -rf /'` where
the outer denylist check sees the wrapper, not the interpreted payload —
though note `dev_bash`'s own call already wraps the user command in `sh -c`
before the denylist check runs on the *outer* string, so a payload that
itself contains further shell indirection is unchecked).

### Desired invariant

**Nanite must not report sandboxed execution as successfully isolated when
required OS isolation is absent.** (Verbatim from the remediation guide's
Wave 1 framing.) Concretely:

- A caller of `AgentExec` (or a human debugging a session) must be able to
  determine, after the fact, whether OS-level isolation was actually applied
  to a given execution — "sandbox requested and applied" and "sandbox
  requested but degraded to Tier 1 only" must be observably different
  outcomes, not the same `(result, nil)` shape.
- The choice of what happens *at the moment of degradation* (refuse to run,
  or run with visible degradation) is the architect decision this task
  queues — see "Proposed direction" below — but whichever is chosen, silent,
  unobservable degradation must not remain possible.
- The network-allowlist gap (GO-SEC4-002) and denylist weakness (GO-SEC4-006)
  should each end up either genuinely fixed, or explicitly and visibly
  documented as an accepted reduced-guarantee mode — not left as an
  implicit assumption baked into `(allow default)`-style code paths.

### Scope

- `internal/sandbox/os_linux.go` — `applyOSSandbox`, `bwrapWarnOnce`, the
  `--unshare-net` conditional.
- `internal/sandbox/exec.go` — `AgentExec`, `UserExec` (both call
  `applyOSSandbox`), `ExecResult`, `AgentExecOpts`, `UserExecOpts`.
- `internal/sandbox/denylist.go` — `CheckDenylist`, `defaultDenyPatterns`.
- `internal/sandbox/os_other.go` — same call-signature contract; verify
  whether the chosen fail-closed/opt-in mechanism should also apply here, or
  whether "no OS sandbox exists on this platform at all" legitimately
  warrants different handling than "OS sandbox exists but its dependency is
  missing." Do not conflate the two without a documented reason.
- `internal/sandbox/os_darwin.go` — **not** in scope for the fail-open fix
  itself (macOS has no equivalent "tool missing" fallback — `/usr/bin/sandbox-exec`
  is assumed present; note this is a real, distinct, un-investigated gap the
  2026-04-10 audit's finding 03 also raised — see "Non-goals" below), but any
  new shared "sandbox status" field on `ExecResult` should be populated
  correctly on darwin too (i.e., "sandbox applied: true" there, honestly).

### All production callers

Per the guide's "fix every sibling path" principle — every caller of
`sandbox.AgentExec`/`sandbox.UserExec` needs to be checked against whatever
new degraded-mode contract this task introduces, since all of them currently
receive an `ExecResult` with no way to know if isolation actually applied:

| Caller | File:line | Path | Exec fn |
|---|---|---|---|
| `dev_bash` MCP tool | `internal/mcp/dev_tools.go:1203` (`callBash`) | agent-controlled shell command via chat | `AgentExec` |
| `code_execute` MCP tool | `internal/mcp/code_exec_tools.go:135` (`callCodeExecute`) | agent-controlled code execution | `AgentExec` |
| `ShellStep` workflow step | `internal/workflow/handlers.go:34` (`ShellStep.Execute`) | pipeline/workflow-defined shell step | `AgentExec` |
| Session shell endpoint | `internal/api/shell.go:85` (`handleShellExec`) | GUI/API-reachable, user-typed command, `Sandboxed: mode != shell.ModeYOLO` | `UserExec` |

Three of the four (`dev_bash`, `code_execute`, `ShellStep`) go through
`AgentExec` — the path this finding is centered on, where the caller (an LLM
agent or a workflow definition, not necessarily the human operator) cannot
itself decide to accept degraded isolation, which is precisely why silent
degradation is dangerous here. The fourth (`handleShellExec`) goes through
`UserExec`, gated by `Sandboxed: mode != shell.ModeYOLO` — a user in YOLO
mode has already explicitly opted out of the OS sandbox, so that call site's
"is the sandbox actually on" question is different in kind (already
opted-out vs. silently-degraded) and should be treated separately in the
fix: the degradation-signal work applies to the `Sandboxed: true` case for
this caller.

Whatever mechanism is added (return value change, new `ExecResult` field,
error type, etc.) must be threaded through or explicitly handled at all four
call sites, not just verified compiles.

### Proposed direction

**This is the architect decision the remediation guide explicitly names in
Wave 1 ("Linux sandbox behavior when `bwrap` is absent" — guide §9, item 1;
also §9 item 2 for the network-allowlist enforcement level). Do not pick one
here — implement whichever the architect selects, with the other's rejected
tradeoffs recorded in Work log for future reference.**

**Option A — fail closed without `bwrap`.**

`applyOSSandbox` returns a real, non-nil `error` when `bwrap` cannot be
found, instead of `(func(){}, nil)`. `AgentExec` propagates that error
exactly as it already does for other `applyOSSandbox` failures (the
`if err != nil` branch at `exec.go:159-161` already exists and needs no new
control flow — only the fallback's *classification* changes from
success-shaped to error-shaped).

- Pros: matches the "fail closed" framing the guide's own Silent Security
  Degradation standard implies; simplest to reason about and test (one new
  error path, not a new signal type threaded through every caller); an
  agent-controlled call site (`dev_bash`, `code_execute`, `ShellStep`) simply
  cannot execute unsandboxed on Linux without `bwrap`, which is the strongest
  version of the invariant.
- Cons: breaks `nanite serve`/agent tool execution entirely on any Linux host
  without `bwrap` installed — including CI runners, minimal containers, and
  developer machines that haven't installed it — with no code-level escape
  hatch unless one is explicitly added (an opt-out env var, per the
  2026-04-10 audit's own recommendation: `NANITE_ALLOW_UNSANDBOXED_AGENT_EXEC=1`
  or similar). Needs a decision on whether that opt-out exists, and if so,
  whether it also needs "log every time, not once" behavior (the 2026-04-10
  audit's finding 03 recommendation) so it can't silently persist across a
  long-running process.

**Option B — require an explicit, highly-visible opt-in to degraded/no-OS-isolation execution.**

`applyOSSandbox` still returns `(cleanup, nil)` when `bwrap` is absent, but
only if the caller has explicitly opted in (e.g. via `AgentExecOpts`/config,
not an ambient env var alone) — and the degraded outcome is surfaced back to
the caller through a new observable signal (e.g. a `SandboxApplied bool` /
`SandboxDegraded bool` field on `ExecResult`, or a distinguishable warning
returned alongside the result) so every consumer — MCP tool result, workflow
step output, API response — can see and, if desired, surface it to the
end user or refuse to proceed on that basis.

- Pros: doesn't hard-break execution on hosts without `bwrap`; keeps a path
  for degraded-but-functional operation for CI/dev use cases the 2026-04-10
  audit anticipated.
- Cons: "highly visible" is doing a lot of work — it requires real,
  deliberate propagation through every one of the four call sites above (the
  MCP tool result needs to actually put this in front of the agent/user, not
  just log it server-side) or the opt-in becomes exactly as silent as today's
  behavior, just with an extra flag nobody set knowingly. Larger surface
  area than Option A (new field on a shared struct, new plumbing at every
  caller) for a security property that's harder to verify is actually
  visible end-to-end.

**GO-SEC4-002 (network allowlist) — direction, not an either/or with the above:**

Per the finding's own recommendation, either (a) enforce egress at the
namespace level (co-locate the proxy inside the sandbox netns, or a
veth/socket-passing approach — the `TODO(network-isolation)` in current
source already sketches this), which is real, non-trivial infrastructure
work, or (b) explicitly document/gate proxy-mode network access on Linux as
a reduced-guarantee mode — consistent with whichever framing Option A/B
above establishes for "reduced guarantee" reporting. Do not silently leave
this as today's undocumented-in-user-facing-terms tradeoff; the code comment
is honest but nothing surfaces it to an operator or agent.

**GO-SEC4-006 (denylist) — direction:**

Per the finding's own recommendation: either (a) reframe the denylist in
comments/docs as advisory/defense-in-depth rather than a hard security
boundary (cheap, honest, low-effort), or (b) replace substring matching with
real shell tokenization (e.g. `mvdan.cc/sh` or similar) for meaningfully
better bypass resistance. This sub-decision is lower-stakes than A/B above
and can likely be resolved by whoever implements this task rather than
escalated further, but should be recorded as a deliberate choice in Work
log either way, since GO-SEC4-006's severity is explicitly coupled to
whatever Option A/B is chosen for GO-SEC4-001 (it matters more under Option
B's degraded-mode path than under Option A's hard-fail path).

### Non-goals

- **Not** fixing the macOS `/usr/bin/sandbox-exec`-missing case (a real,
  separate gap the 2026-04-10 audit's finding 03 also flagged: `os_darwin.go`
  assumes the binary exists and will fail with a cryptic `exec` error rather
  than a clean fail-closed message). Worth a follow-up, out of scope here
  because it's a different code path with a different failure mode, not
  because it's unimportant.
- **Not** implementing the full namespace-level network enforcement for
  GO-SEC4-002 unless the architect selects that direction over the
  documentation/gating alternative — this task specifies both options for
  the architect but does not presume which gets built.
- **Not** rewriting the denylist to a full shell-parser/tokenizer unless the
  architect (or implementer, per the lower-stakes note above) selects that
  over the cheaper "reframe as advisory" fix.
- **Not** a general "sandbox observability/telemetry" project — the new
  signal this task adds (however Option A/B resolves) should be scoped to
  "was OS isolation applied," not a broader sandbox-metrics overhaul.
- **Not** touching `internal/permission`, `internal/secrets`, `internal/pathsafe`,
  `internal/fsutil`, or `internal/safego` — those are reviewed and found
  healthy in the same audit section (§8.12) and are out of scope for this task.

### Dependencies

- None within this batch. This task can proceed independently of
  `01-plugin-install-convergence/` and `03-agent-slug-traversal/`, the other
  two Wave 1 groupings.
- **External dependency: an architect decision is required before
  implementation starts** (Option A vs. B for GO-SEC4-001, and the
  enforce-vs-document direction for GO-SEC4-002). This task file specifies
  both options in full; it does not resolve the choice.

### Tests required

- **Linux, `bwrap` present vs. absent** — mock/stub `exec.LookPath` (or
  manipulate `PATH` in the test environment) to simulate both conditions.
  Assert:
  - the two conditions produce **observably different** outcomes from
    `applyOSSandbox`/`AgentExec` (an error in Option A; a distinguishable
    degraded-signal in Option B) — never the same success shape;
  - whichever direction is chosen, a test explicitly asserts the previous
    silent-fallback behavior (`(func(){}, nil)` with no way to detect
    degradation) is **not** reachable anymore for the "expected but missing"
    case.
- **GO-SEC4-002** — a test verifying network-namespace enforcement when
  `NetworkAllow` is set (if the enforce-at-namespace-level direction is
  chosen), **or**, if the document/gate direction is chosen, a test/assertion
  that the reduced-guarantee mode is explicitly surfaced (not silently
  identical to the fully-isolated case) — i.e., document the gap in a way a
  test can verify didn't silently regress further, per the task brief's
  explicit instruction to "document the gap" if not fixing it outright.
- **GO-SEC4-006** — a regression test asserting the denylist's actual
  matching behavior (whatever form the fix takes) against at least the
  bypass classes named above (whitespace variation, flag reordering,
  interpreter wrapping) — either confirming they're now caught (if
  tokenization is chosen) or confirming the comments/docs accurately
  describe them as uncaught (if the advisory-reframe direction is chosen).
- Regenerate/extend `internal/sandbox`'s existing test suite rather than
  creating a parallel one — check for existing `os_linux_test.go` /
  `exec_test.go` coverage first per "What to do" below.
- All four production callers (see table above) should have at least one
  test or documented manual-verification step confirming they handle the new
  degraded/error signal sensibly, not just that the package compiles.

### Prevention

- **Silent Security Degradation** (remediation guide's own named standard,
  §4 Wave 7): "A required security boundary must not silently degrade while
  reporting success." This finding is the direct motivating case for that
  standard — landing this fix and citing the standard in the commit/PR
  description closes the loop between this specific defect and the
  general rule.
- A unit test asserting `applyOSSandbox`'s Linux success/failure/degraded
  paths are distinguishable is itself the regression guard — any future
  change that collapses them back to one signal breaks that test.
- Consider whether `make lint-goroutines` or an equivalent static check
  should flag any future `return func(){}, nil`-shaped early return in a
  function whose doc comment promises isolation — noted as a possible
  follow-up for `12-quality-ratchet-and-standards/`, not required here.

### Verification

```bash
GOOS=linux go build ./internal/sandbox/...
go vet ./internal/sandbox/...
go test ./internal/sandbox/... -run TestApplyOSSandbox -v
go test -race ./internal/sandbox/...
go test ./internal/mcp/... ./internal/workflow/... ./internal/api/... -run 'Exec|Sandbox|Shell'
```

Observable behavior required for PASS:

- On a Linux test environment (or `GOOS=linux` cross-compiled unit test with
  `exec.LookPath` mocked) with `bwrap` unavailable, `AgentExec` no longer
  returns a plain `(*ExecResult, nil)` indistinguishable from the sandboxed
  case — whichever Option A/B was selected is observably in effect.
- All four production callers still build and their existing tests still
  pass.
- `go vet`/`staticcheck` clean for the touched files.

### Risk / rollback

- **Option A (fail closed) regression surface:** any Linux host currently
  relying on the silent Tier-1-only fallback (dev machines, CI, containers
  without `bwrap`) will see `AgentExec`/`dev_bash`/`code_execute` start
  failing outright unless an opt-out is added and set. This is a deliberate,
  intended behavior change per the finding's own recommendation, but it is a
  real, user-visible breaking change that should be called out prominently
  in release notes / the PR description, and ideally gated behind a
  clear error message with remediation instructions (install bwrap, or set
  the opt-out).
- **Option B (opt-in + visible signal) regression surface:** smaller
  behavioral risk (existing degraded-fallback callers keep working), but
  real *plumbing* risk — if the new `ExecResult` field or signal isn't
  correctly wired through all four call sites, the "highly visible" half of
  the requirement silently fails to hold, which is arguably worse than
  today's status quo because it would look fixed without being fixed.
  Mitigate by making the new field/signal a required, non-optional part of
  each call site's test coverage.
- **Rollback:** revert the `internal/sandbox` changes; the fallback shape is
  currently isolated to `os_linux.go`/`exec.go`/`denylist.go`, so a rollback
  should not require touching the four caller files if their handling of the
  new signal is written defensively (e.g., a caller that doesn't understand
  a new `ExecResult` field just doesn't read it — Go zero-value defaults keep
  this safe on revert).

### Done means

- [ ] Architect decision recorded (Option A or B for GO-SEC4-001; enforce or
      document for GO-SEC4-002) before implementation begins.
- [ ] `applyOSSandbox` on Linux no longer returns a success-shaped result
      when `bwrap` is absent without an explicit, observable signal
      distinguishing that outcome from real isolation (per whichever option
      was chosen).
- [ ] GO-SEC4-002's network-allowlist gap is either enforced at the
      namespace level or explicitly, visibly documented/gated as a
      reduced-guarantee mode — not left as an undisclosed assumption.
- [ ] GO-SEC4-006's denylist is either meaningfully hardened or explicitly
      reframed in code comments/docs as advisory/defense-in-depth, not
      described as a real security boundary it isn't.
- [ ] All four production callers (`dev_bash`, `code_execute`, `ShellStep`,
      `handleShellExec`) build, pass existing tests, and correctly handle
      the new signal/error shape.
- [ ] New regression tests exist for: bwrap-present vs. bwrap-absent
      producing observably different outcomes; the network-allowlist
      decision; the denylist decision.
- [ ] `go build ./...`, `go vet ./...`, `go test ./...`, and
      `go test -race ./internal/sandbox/...` all pass.
- [ ] The prior silent-fallback behavior is provably unreachable (a test
      exists that would fail if it regressed).

## Work log

<Worker fills this in.>

## Review notes

<Reviewer fills this in.>
