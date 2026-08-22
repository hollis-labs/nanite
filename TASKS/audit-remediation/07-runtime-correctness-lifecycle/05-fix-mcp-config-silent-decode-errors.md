# Fix `loadPersistedMCPServers` silently discarding Args/Env JSON decode errors

**Phase:** Wave 2 — Correctness, lifecycle, concurrency
**Status:** not-started
**Depends on:** none
**Touches:** `cmd/nanite/main.go` (`loadPersistedMCPServers`, lines 1233-1277). No other files need changes.

> **Planner sequencing (added 2026-08-21).** Supersedes the `**Depends on:**`
> line above wherever they differ — that line predates cross-folder analysis.
> Authoritative copy of this table: `TASKS/audit-remediation/README.md`.
>
> - **Wave:** 2 — correctness, lifecycle, concurrency · **Dispatch unit:** `W2b`
> - **Depends on:** `07/02` (same file, `cmd/nanite/main.go`)
> - **Blocks:** `11/10`
> - **Parallel-safe with:** none
> - **Gated on:** none
> - **requires_security_review:** false · **requires_regression_test:** true

## Context

`requires_architect_decision: false` — a same-file, same-pattern, low-risk fix. The correct pattern already exists three lines away in the same function; this task applies it to the two sites that are missing it.

### Findings addressed
- `GO-RUNTIME-007` — severity **medium** (`findings.json`; described as "low-medium" in `REPORT.md` §8.13's prose), confidence **high**. `docs/audits/2026-08-21-go-quality/REPORT.md` §8.13; `docs/audits/2026-08-21-go-quality/findings.json` id `GO-RUNTIME-007`, evidence: `"main.go:1233-1277: Args/Env unmarshal errors discarded, EnvAllowlist's is correctly warn-logged."`

### Root cause

`loadPersistedMCPServers` (`cmd/nanite/main.go:1233-1277`) unmarshals three separate JSON-encoded string columns from each persisted `mcp_servers` row of transport type `"stdio"`: `Args`, `Env`, and `EnvAllowlist`. Only the third's `json.Unmarshal` return value is checked. The exact current code (`main.go:1244-1262`):

```go
switch cfg.TransportType {
case "stdio":
    var args []string
    if cfg.Args != "" && cfg.Args != "[]" {
        json.Unmarshal([]byte(cfg.Args), &args)
    }
    var envVars []string
    if cfg.Env != "" && cfg.Env != "[]" {
        json.Unmarshal([]byte(cfg.Env), &envVars)
    }
    var envAllowlist []string
    if cfg.EnvAllowlist != "" && cfg.EnvAllowlist != "[]" {
        if err := json.Unmarshal([]byte(cfg.EnvAllowlist), &envAllowlist); err != nil {
            slog.Warn("mcp: malformed env_allowlist json — ignoring",
                "name", cfg.Name, "err", err)
            envAllowlist = nil
        }
    }
    if err := m.AddStdioServer(cfg.Name, cfg.Command, args, envVars, envAllowlist, mcp.TrustTier(cfg.TrustTier)); err != nil {
        slog.Warn("mcp: failed to register persisted stdio server", "name", cfg.Name, "err", err)
    }
```

Lines 1248 (`json.Unmarshal([]byte(cfg.Args), &args)`) and 1252 (`json.Unmarshal([]byte(cfg.Env), &envVars)`) call `json.Unmarshal` and discard the returned error entirely — no variable capture, no check. If either field's persisted JSON is malformed, `args`/`envVars` silently stay at their zero value (`nil`) instead of the operator's actual configured values, and `m.AddStdioServer(...)` (line 1262) proceeds anyway with the wrong (empty) args/env. Nothing anywhere logs that the persisted config didn't decode as intended — this is functionally indistinguishable, from the operator's point of view, from "this server genuinely has no args/env configured."

The `EnvAllowlist` branch three lines below (`main.go:1254-1261`) already does this correctly: it captures the error, `slog.Warn`s with the server name and the error, and explicitly resets to `nil` on failure.

### Production reachability

`loadPersistedMCPServers` has exactly one call site, `cmd/nanite/main.go:1034` (`loadPersistedMCPServers(s, mcpManager)`), invoked once during `cmdServe`'s startup sequence to load every user-configured MCP server row persisted in the store. Any operator who has configured a stdio MCP server with args/env — via the GUI/API or a direct DB edit — whose persisted JSON becomes malformed (corruption, a manual edit gone wrong, a future encoding bug elsewhere in the write path) gets that server silently launched with empty args/env instead of a visible failure. For a stdio MCP server, an empty `Args` typically means the command runs with no arguments and empty `Env` means no extra environment variables are passed — either can silently change or break the server's actual behavior in ways the operator has no log line to diagnose.

### Desired invariant

A malformed `Args` or `Env` JSON value on a persisted stdio MCP server row produces the same operator-visible signal `EnvAllowlist` already produces — a `slog.Warn` naming the server and the decode error — while the runtime still proceeds with the safe empty-slice fallback (this task does not change the "proceed anyway with defaults" policy, only makes the failure visible, matching `EnvAllowlist`'s existing behavior exactly).

## What to do

1. Apply the exact same check-and-warn pattern already used for `EnvAllowlist` (`main.go:1254-1261`) to both `Args` (`main.go:1247-1249`) and `Env` (`main.go:1250-1253`): capture each `json.Unmarshal` call's error, and on non-nil error, `slog.Warn` with a message naming the specific field (e.g. `"mcp: malformed args json — ignoring"` / `"mcp: malformed env json — ignoring"`), `cfg.Name`, and the error — then explicitly reset the variable to `nil` (matching `EnvAllowlist`'s explicit `= nil`, for symmetry and to guard against `json.Unmarshal` partially populating the slice before failing partway through decoding).
2. Re-verify `main.go:1233-1277` is still the current line range before editing — cited from direct reading during this task's authoring pass; confirm no drift since.
3. Do not change the `case "sse":` branch (`main.go:1265-1268`) or the `default:` branch (`main.go:1269-1271`) — `GO-RUNTIME-007` and its evidence are specific to the stdio branch's Args/Env/EnvAllowlist trio; the `sse` branch has no equivalent JSON-decode step.
4. Do not change `m.AddStdioServer`'s own error handling (`main.go:1262-1264`) — it is already correctly warn-logged; out of scope.

## Tests required

- A test exercising `loadPersistedMCPServers` (using its existing signature, `loadPersistedMCPServers(s *store.Store, m *mcp.Manager)`) with a persisted server row whose `Args` JSON is malformed, and a separate case for malformed `Env` JSON, asserting: (a) a `slog.Warn` fires naming the server and the specific field; (b) `AddStdioServer` is still called, with an empty (not partially-decoded-garbage) args/env slice — matching the existing safe-fallback behavior `EnvAllowlist` already has.
- This repo has no existing log-capture test helper found in `internal/slogx`, `internal/store`, or `cmd/nanite`'s own test files as of this task's authoring (checked via grep for `slog.SetDefault`/`slogtest`/similar patterns — none found). Use the simplest reasonable approach (e.g. `slog.SetDefault` with a custom `slog.Handler` writing to a buffer for the duration of the test, restored via `t.Cleanup`) rather than inventing a new shared test-logging package for this one fix, unless a genuinely reusable seam is trivial to add.

## Prevention

The new test is the direct regression check. This is a small instance of a "same-file, same-pattern, one-of-three-forgot-the-check" class of bug — worth flagging (not fixing here) to whoever owns this repo's `errcheck` configuration in `12-quality-ratchet-and-standards/`: `errcheck` is already in this repo's post-remediation verification command list (guide §10), and a bare `json.Unmarshal(...)` with a discarded return value is exactly the class of defect `errcheck` exists to catch — if it isn't currently flagging these two sites, that gap is worth investigating separately from this fix.

## Verification

```bash
go build ./cmd/nanite/...
go vet ./cmd/nanite/...
go test ./cmd/nanite/... -run TestLoadPersistedMCPServers -v
errcheck ./cmd/nanite/...
```

(Adjust the `-run` pattern to the new test's actual name once written.) PASS: new test(s) pass, demonstrating both the warn-log and the safe-fallback behavior; `errcheck` no longer flags the two previously-bare `json.Unmarshal` calls at the corrected lines (if it was flagging them at all — confirm current `errcheck` output on this file before and after as part of verification).

## Risk / rollback

Very low risk — purely additive logging plus an explicit (already-implicit) zero-value assignment; no behavioral change to what gets passed to `AddStdioServer` on the happy path or on the failure path (still proceeds with empty args/env either way) — only a new log line on failure. Rollback is a single-commit revert.

## Done means

- [ ] `Args` and `Env` JSON decode errors in `loadPersistedMCPServers`'s stdio branch are captured and `slog.Warn`-logged, naming the server and the field, matching `EnvAllowlist`'s existing pattern exactly.
- [ ] New test(s) cover malformed `Args` and malformed `Env`, asserting both the warn-log and the safe empty-slice fallback.
- [ ] `sse`/`default` branches and `AddStdioServer`'s own error handling are unchanged.
- [ ] `go build`, `go vet`, `go test ./cmd/nanite/...` all pass.
- [ ] `errcheck ./cmd/nanite/...` run against `main.go` confirms the two sites no longer have unchecked errors.

## Work log

<!-- Worker fills in: what was actually done, any deviation from plan and why. -->

## Review notes

<!-- Reviewer fills in: pass/fail, what was independently re-verified. -->
