# [High] Managed-section parser test suite does not exercise any of the hard cases its callers depend on

**Scope:** managed-section parser / writer / remover
**Topic:** Test quality — coverage of error paths and ambiguous inputs
**Date:** 2026-04-11

## Problem

`internal/agent/managed_section_test.go` covers seven happy-path cases and one "no markers in user file" case for `ReadManagedSection`. It does not exercise any of the ambiguous, malformed, adversarial, or caller-relevant inputs that `WriteManagedSection` / `ReadManagedSection` / `RemoveManagedSection` are required to handle in production. Each missing case is a silent failure mode: the parser compiles, the happy-path tests pass, but production inputs produce corrupted files or wrong return values.

This finding enumerates the gaps. Several map to already-filed audit findings (this audit's `01-high-*` and installer `03-high-*`); the rest are undocumented test debt. The severity is High because these are the exact cases a reviewer or engineer would write to validate a parser of this shape, and their absence means any future refactor or bug fix in this file has no safety net.

## Evidence

Existing tests (`managed_section_test.go:L10-287`):

| Test | What it covers |
|---|---|
| `TestWriteManagedSection_NewFile` | Fresh file, no pre-existing content |
| `TestWriteManagedSection_PreservesExistingUserContent` | File with user content, no markers, append |
| `TestWriteManagedSection_ReplacesExistingManagedSection` | Happy-path replace — single clean marker pair |
| `TestWriteManagedSection_Idempotent` | Double-write returns identical bytes |
| `TestReadManagedSection_ReadsContent` | Happy-path read, single clean marker pair |
| `TestReadManagedSection_NoMarkers` | File with no markers returns `("", nil)` |
| `TestRemoveManagedSection_FileMissing` | Missing file returns `(false, false, nil)` |
| `TestRemoveManagedSection_NoMarkers` | File without markers is untouched |
| `TestRemoveManagedSection_PreservesOutsideContent` | Happy-path remove, content before and after preserved |
| `TestRemoveManagedSection_EmptyAfterRemoval` | Managed-only file collapses to empty |

### Gaps, grouped by failure mode

**Gap group A — caller-supplied content contains markers.** See finding `01-high-content-payload-can-inject-marker-strings.md` for the production impact. Not covered:

- `WriteManagedSection(path, "contains <!-- nanite:end --> here")` — should either reject or escape; currently silently writes a file that corrupts on next write.
- `WriteManagedSection(path, "contains <!-- nanite:start --> here")` — same.
- `WriteManagedSection(path, managedNotice + " literal")` — the notice string inside content confuses `ReadManagedSection` which strips all occurrences via `strings.ReplaceAll`. See finding `04-medium-read-managed-section-silent-notice-stripping.md`.

**Gap group B — pre-existing file-content markers.** See installer audit finding `03-high-managed-section-end-marker-confusion.md`. Cross-referenced, not re-filed:

- File contains two pairs of markers (installer 03 §Scenario 3).
- File contains `managedEnd` before `managedStart` (installer 03 §Scenario 2).
- File contains marker literals inside a fenced code block (installer 03 §Scenario 1).
- File contains marker literals inside YAML frontmatter (not in installer 03; same mechanism).
- File contains the start marker but no end marker (handled by the `endIdx == -1` branch; no test asserts the branch behavior is correct).
- File contains the end marker with no start marker (falls into the "no markers" branch because `strings.Index(managedStart)` returns -1 first; no test asserts the orphan end marker is left alone or surfaced).
- File contains only one of the two marker strings inside a comment that is not a real marker pair (e.g., `<!-- nanite:start --- -->` — truncated-lookalike; the exact-string `strings.Index` does not match, but a future change to regex would; no regression test exists).

**Gap group C — whitespace / newline variants on the marker itself.** None covered:

- `managedStart` followed by CRLF (`\r\n`) instead of LF. Go `strings.Index` matches bytes exactly, so `<!-- nanite:start -->\r\n...` matches the marker, but no test validates the subsequent line-boundary handling.
- `managedStart` surrounded by whitespace (`  <!-- nanite:start -->  `). Again, `strings.Index` matches substring, so the indentation is silently preserved in `before` / swallowed in `after`. No test covers the layout.
- File with `\r\n` line endings throughout. No test writes `\r\n` and verifies round-trip.

**Gap group D — large / pathological file sizes.** None covered:

- Large (>1MB) managed-section file. `strings.Index` is O(n); session-prep runs this on every start. No benchmark, no allocation test.
- File with only a single byte that is not a marker. Not tested; `strings.Index` returns -1, goes to "append" branch, but no test asserts the result is well-formed.
- Zero-byte file (exists but empty). Not tested; `os.ReadFile` succeeds, `len(existing) == 0`, the `HasSuffix("\n")` check is false, `sep = ""`, and the append path produces `"" + "" + block` = bare block. No test asserts this.

**Gap group E — `RemoveManagedSection` edge cases.** Not covered:

- `RemoveManagedSection` on a file where `managedStart` appears but `managedEnd` does not. Current behavior (`managed_section.go:L144-149`): return `(false, false, nil)` and leave the file untouched. This is the documented behavior in the comment, but no test asserts it. A future refactor could change it to strip-through-EOF without any test failing.
- `RemoveManagedSection` on a file where `managedEnd` appears but `managedStart` does not. Same shape — `strings.Index(existing, managedStart)` returns -1, early return. No test.
- `RemoveManagedSection` on a file with two pairs of markers. The first pair is stripped, the second is not. No test asserts either the current behavior or the desired behavior.
- `RemoveManagedSection` called twice on the same file. First call strips, second call should be a no-op. No test.

**Gap group F — `ReadManagedSection` caller assumptions.** Not covered:

- `ReadManagedSection` returns `("", nil)` for both "no markers" and "start without end". Caller cannot distinguish. No caller currently depends on the distinction, but if one did, it would be silently wrong. No test exercises the ambiguity.
- `ReadManagedSection` strips ALL occurrences of `managedNotice` from inner content via `strings.ReplaceAll`. If the managed content legitimately contains the notice text (e.g., a code example), the notice is silently removed from the returned string. See finding `04-medium-read-managed-section-silent-notice-stripping.md`. No test.

**Gap group G — concurrent / interrupted writes.** Not covered:

- Two goroutines calling `WriteManagedSection` on the same path concurrently. `os.WriteFile` is not atomic at the filesystem level (it truncates then writes); interleaved calls can produce torn files. No test, no file-locking protection.
- Write interrupted mid-`os.WriteFile` (simulate with chmod or a short-write fake). No test. The function uses `os.WriteFile` with no temp-file-then-rename pattern, so a crash during write leaves the file in an indeterminate state.

**Gap group H — round-trip / fuzz.** None covered:

- No fuzz test (`go test -fuzz`) despite the function accepting untrusted byte-in. A `FuzzWriteReadRoundTrip` that writes a random content string and then reads it back would quickly surface the marker-injection bug in finding 01.
- No property test asserting `ReadManagedSection(WriteManagedSection(content)) == content` for arbitrary `content`. Would fail immediately on any content containing marker substrings.

## Impact

The parser is on the hot path of every `nanite install`, every `SyncProjectRoot` call from every adapter plugin (five plugins today, more in the pipeline), and every adapter cleanup. Failure modes in the parser manifest as user data loss, which is severe, and as "my CLAUDE.md mysteriously has orphaned markers", which is opaque and hard to diagnose in production. The test suite offers no defense against regressions in any of the above gap groups.

Concretely:

- A bug fix for installer-03 that changes the `strings.Index` logic to a regex or state machine has no test that would catch a regression in the existing happy path under the new implementation's edge cases.
- A refactor of `buildManagedBlock` to add a fingerprint (recommended by installer-03) has no round-trip test to verify `ReadManagedSection` still returns the caller's original content unchanged.
- The four adapter plugins that feed user-controlled data into the writer have no integration test asserting that profile fields containing marker substrings are handled safely.

## Recommendation

Add table-driven test cases for every gap above. Group them in `managed_section_test.go` by function (`TestWriteManagedSection_AmbiguousInputs`, `TestReadManagedSection_MalformedFiles`, `TestRemoveManagedSection_EdgeCases`). Make the "reject or corrupt" behavior explicit so future refactors can't silently regress it.

Three structural suggestions to pair with the table tests:

1. **Add a `FuzzWriteReadRoundTrip` fuzz test.** Go's native fuzzer is the cheapest possible coverage for a byte-in byte-out parser. A 30-second fuzz run would likely find the marker-injection bug in finding 01 within seconds.
2. **Add a property test: `ReadManagedSection(WriteManagedSection(content)) == content`** for arbitrary `content`. The current function cannot satisfy this because of the `strings.TrimSpace` + notice stripping on read, but the *spec* should be made explicit in a test that documents the round-trip loss.
3. **Add at least one concurrent-write test.** Even if the fix is "document that this is not safe", the assertion should be present. A failing test today is better than a silent race in production.

Do not write these tests as part of this audit. The task was enumeration. This finding is the backlog.

## References

- `internal/agent/managed_section_test.go:L10-287` — the existing coverage.
- `internal/agent/managed_section.go:L28-184` — the functions under test.
- Finding `01-high-content-payload-can-inject-marker-strings.md` — one specific gap with a proof of data loss.
- Finding `04-medium-read-managed-section-silent-notice-stripping.md` — another specific gap.
- `docs/audits/2026-04-10-installer/03-high-managed-section-end-marker-confusion.md` — cross-referenced file-content gaps (gap group B).
- Go fuzz test docs: https://go.dev/security/fuzz/
