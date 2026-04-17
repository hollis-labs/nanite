# [High] Managed section parser missing marker injection tests

**Scope:** internal/agent/
**Topic:** Test Quality — security-critical parser gap
**Date:** 2026-04-11

## Problem

The managed section parser (`internal/agent/managed_section.go`) is tested by `managed_section_test.go` (287 lines, 10 test functions). The tests cover normal CRUD operations (new file, preserve user content, replace existing section, idempotent writes, read, remove). They do not test adversarial inputs: nested markers, malformed HTML comments, or marker injection.

## Evidence

`internal/agent/managed_section_test.go` test functions:
- `TestWriteManagedSection_NewFile` — creates a new file with markers
- `TestWriteManagedSection_PreservesExistingUserContent` — user content before managed section
- `TestWriteManagedSection_ReplacesExistingManagedSection` — replaces old managed content
- `TestWriteManagedSection_Idempotent` — same content produces same file
- `TestReadManagedSection_ReadsContent` — reads between markers
- `TestReadManagedSection_NoMarkers` — returns empty for no markers
- `TestRemoveManagedSection_FileMissing` — handles missing file
- `TestRemoveManagedSection_NoMarkers` — no-op for files without markers
- `TestRemoveManagedSection_PreservesOutsideContent` — keeps user content
- `TestRemoveManagedSection_EmptyAfterRemoval` — reports empty file

Missing test cases:
1. **Nested markers:** A user edits the file to include `<!-- nanite:start -->` inside the managed section or in user content. What happens on the next `WriteManagedSection`?
2. **Multiple managed sections:** Two `<!-- nanite:start -->` / `<!-- nanite:end -->` pairs in the same file.
3. **Orphan end marker:** `<!-- nanite:end -->` without a preceding start marker.
4. **Orphan start marker:** `<!-- nanite:start -->` without a following end marker.
5. **Marker inside code block:** User content has the marker string inside a markdown code fence.
6. **Marker with extra whitespace or variation:** `<!--  nanite:start  -->` or `<!-- NANITE:START -->`.

The reviewer-backend context explicitly flags: "Managed section protocol must be robust against user edits that insert nested markers or malformed HTML comments."

## Impact

Adapter plugins (adapter-claude, adapter-codex, adapter-gemini, adapter-opencode, adapter-nanite-native) all use `WriteManagedSection` to write CLI-specific configuration files. If a user edits a file to include nested markers, the parser could corrupt the file by misidentifying section boundaries, potentially overwriting user content.

## Recommendation

Add adversarial test cases:
1. `TestWriteManagedSection_NestedStartMarker` — file contains `<!-- nanite:start -->` inside user content above the real managed section
2. `TestWriteManagedSection_DuplicateSections` — two complete managed sections
3. `TestWriteManagedSection_OrphanEndMarker` — end marker without start
4. `TestWriteManagedSection_OrphanStartMarker` — start marker without end
5. `TestWriteManagedSection_MarkerInCodeFence` — marker text inside triple backtick block

## References

- Reviewer-backend context: "Managed section protocol must be robust against user edits that insert nested markers or malformed HTML comments."
- `2026-04-11-managed-section-parser` audit — robustness findings
- `internal/agent/managed_section_test.go` — current test suite
