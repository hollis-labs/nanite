# [Low / Info] Grouped observations — managed-section parser

**Scope:** managed-section parser / writer / remover
**Topic:** Mixed — style, idioms, performance, praise
**Date:** 2026-04-11

This file groups short observations that do not warrant standalone finding files. Each sub-item retains the required minimum sections (Problem / Evidence / Impact / Recommendation) in compressed form.

---

## 06.1 [Low] Partial-line start marker handled implicitly

**Problem.** `WriteManagedSection` computes `before := existing[:startIdx]` without checking whether `startIdx` is at the beginning of a line. If a user (or a previous install bug) has placed the start marker mid-line, `before` ends with the partial line content, and the `block` is concatenated directly after without a newline. The result is a file where the marker is no longer at column 0 but still parses correctly on the next read because `strings.Index` is position-agnostic.

**Evidence.** `internal/agent/managed_section.go:L64`: `before := existing[:startIdx]`. No newline check.

**Impact.** Low. Produces ugly but parseable output. Round-trips remain idempotent because the second write finds the same mid-line marker and preserves the same `before`. No data loss, no corruption — just visual oddity that would confuse a human reader of the markdown source.

**Recommendation.** Strip trailing non-newline content from `before` and trailing whitespace, matching the pattern already used in `RemoveManagedSection` (`strings.TrimRight(before, "\n")`). Alternatively, refuse to edit if the start marker is not at column 0, aligning with the "strict sentinels" recommendation from installer finding 03.

---

## 06.2 [Low] `before` trailing whitespace not trimmed in `WriteManagedSection`

**Problem.** `RemoveManagedSection` trims trailing newlines from `before` (`internal/agent/managed_section.go:L163`) but `WriteManagedSection` does not (line 64). On a replace where the user has `# Header\n\n\n\n<!-- nanite:start -->...`, the three blank lines between header and block are preserved. Re-writing the same content produces the same output, so idempotency holds, but the excess blank lines accumulate if the file is edited by hand and re-written.

**Evidence.** Compare `internal/agent/managed_section.go:L64` (no trim) with `L163` (trim).

**Impact.** Low. Layout drift only. No correctness impact.

**Recommendation.** Normalize `before` with `strings.TrimRight(before, "\n") + "\n\n"` when prepending a block, matching the `RemoveManagedSection` style. Or document that the writer preserves user layout verbatim and the cleanup is the reader's responsibility.

---

## 06.3 [Low] `strings.Index` on large files: linear-scan cost is bounded but unmeasured

**Problem.** Every `SyncProjectRoot` call runs `strings.Index` up to four times over the full file content. For a normal CLAUDE.md (a few KB) this is free. For a pathological 10MB markdown file, the cost is O(4 * n) ≈ 40MB of byte comparisons per install — still fast in Go but not free, and there is no benchmark or bound check.

**Evidence.** `internal/agent/managed_section.go:L41, L52, L92, L99, L139, L144` — six `strings.Index` calls across the three functions. No benchmark in the test file.

**Impact.** Low. Realistic CLAUDE.md files are small. Session-prep runs on every adapter session start, so the cost accumulates, but in absolute terms it's sub-millisecond for any plausible file size. No allocation blow-up observed (the function builds one `strings.Builder` of roughly the file size).

**Recommendation.** Add a benchmark (`BenchmarkWriteManagedSection_Large`) with a 1MB synthetic input to establish a baseline, so any future refactor that accidentally introduces quadratic behavior is caught. Not a production fix, just a regression tripwire.

---

## 06.4 [Low] No file-mode preservation on write

**Problem.** `os.WriteFile(path, data, 0o644)` overwrites the target file's mode with `0o644` every time. If a user has set a custom mode (e.g., `0o600` for a project where CLAUDE.md contains credentials-adjacent notes), the installer silently resets it.

**Evidence.** `internal/agent/managed_section.go:L33, L48, L59, L78, L174, L180` — every write uses `0o644` without reading the existing stat.

**Impact.** Low. Mode reset is a silent behavior change. Unlikely to matter for CLAUDE.md-family files but would be surprising if it did.

**Recommendation.** Stat the existing file (if present) and preserve its mode. Fall back to `0o644` only when creating a new file.

---

## 06.5 [Info] No symlink handling — inherited from installer audit 02

**Problem.** `os.ReadFile` and `os.WriteFile` both follow symlinks. A malicious or accidental symlink at the target path redirects reads and writes to an arbitrary file the process can access.

**Evidence.** `internal/agent/managed_section.go:L31, L33, L48, L59, L78, L129, L174, L180` — all I/O uses the standard `os.*` functions with no `Lstat` / `O_NOFOLLOW`.

**Impact.** Cross-references `docs/audits/2026-04-10-installer/02-high-symlink-following-on-adapter-target-files.md`. That finding is the authoritative location; this is a pointer so the managed-section audit does not leave the reader wondering whether the parser file was inspected for the same issue. It was. Same bug, same fix.

**Recommendation.** See installer 02. The proposed `internal/pathsafe.ResolveUnder` primitive (referenced in `docs/audits/INDEX.md` Cross-cutting themes) would close this at the same time as the sandbox / dev-tools / installer symlink classes.

---

## 06.6 [Info] Praise — the five public functions are well-documented

**Problem.** None. This is praise.

**Evidence.** `internal/agent/managed_section.go:L23-27, L81-83, L116-127` — every exported function has a docstring that explains behavior on every branch (file missing, no markers, malformed, normal case) and, for `RemoveManagedSection`, explicitly documents the three-tuple return shape. The docstrings are the rare kind that include the error-path semantics, not just the happy path. The `managed_section_test.go` file covers every documented behavior — just not the ambiguous cases enumerated in finding 02.

**Impact.** Positive. Future refactoring against the documented behavior is safer than average for this codebase.

**Recommendation.** Replicate the pattern in other packages that own write-side file manipulation. The docstring shape here is a good reference.

---

## 06.7 [Info] Package-level constants are well-chosen sentinel strings

**Problem.** None. This is a design observation.

**Evidence.** `internal/agent/managed_section.go:L9-13` — the three constants (`managedStart`, `managedNotice`, `managedEnd`) are clearly named, unlikely to collide with typical markdown prose (HTML comments are invisible when rendered), and easy to grep for across the codebase.

**Impact.** Positive. The choice makes the parser debuggable by hand. A future change to a fingerprinted marker (e.g., `<!-- nanite:start:v1 -->` as installer-03 recommends) can be localized to these three constants plus a migration step.

**Recommendation.** When implementing the marker fingerprint, keep the constants as the single source of truth. Add a migration function `migrateUnversionedMarkers(path string) error` that upgrades files in-place. Do not scatter marker literals across the codebase.

---

## 06.8 [Info] `RemoveManagedSection`'s three-tuple return is the right shape

**Problem.** None. Design praise.

**Evidence.** `internal/agent/managed_section.go:L128` — `RemoveManagedSection` returns `(removedAny bool, becameEmpty bool, err error)`. The caller in `internal/service/install/adapter_cleanup.go:L42-55` uses both booleans: `stripped` gates the `CleanupReport`, `becameEmpty` decides whether to `os.Remove` the file. A single-bool or error-only signature would have forced the caller to re-read the file or guess.

**Impact.** Positive. The shape is an example of "accept the minimum information, return the maximum information the caller needs." Replicate in similar remove-or-strip APIs.

**Recommendation.** Keep. If `ReadManagedSection` is extended to distinguish its four states (see finding 04), use this function's return shape as the template.
