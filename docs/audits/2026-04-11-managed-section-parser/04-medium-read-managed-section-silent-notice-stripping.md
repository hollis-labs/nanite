# [Medium] `ReadManagedSection` silently strips all occurrences of the notice line from the returned content, losing data if the managed content legitimately contains it

**Scope:** managed-section parser / reader
**Topic:** Error handling / idioms — silent data mutation
**Date:** 2026-04-11

## Problem

`ReadManagedSection` uses `strings.ReplaceAll(inner, managedNotice, "")` to remove the notice comment from the returned inner content. This strips *every* occurrence of the notice string, not just the one that was inserted by `buildManagedBlock`. If the managed content written to the file legitimately contains the notice string (as a code example, as documentation of the notice itself, as a reference in an agent description), the returned string is a lossy rendering of the file — content has been silently removed.

In addition, `ReadManagedSection` cannot distinguish four different states of the file:
1. File has no markers at all.
2. File has a start marker but no matching end marker (truncated / corrupted).
3. File has markers but the inner content is empty.
4. File has markers and valid content.

States 1, 2, and 3 all return `("", nil)`. No caller today depends on the distinction, but any future caller that wants to differentiate "this file has never been managed" from "this file has a malformed managed section that needs repair" has no way to do so. This is a latent API gap rather than a present bug.

## Evidence

```go
// internal/agent/managed_section.go:L84-114
func ReadManagedSection(path string) (string, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return "", err
    }

    text := string(data)

    startIdx := strings.Index(text, managedStart)
    if startIdx == -1 {
        return "", nil
    }

    contentStart := startIdx + len(managedStart)
    relEnd := strings.Index(text[contentStart:], managedEnd)
    if relEnd == -1 {
        return "", nil
    }

    inner := text[contentStart : contentStart+relEnd]

    // Strip the notice line if present.
    inner = strings.ReplaceAll(inner, managedNotice, "")

    // Trim surrounding whitespace/newlines.
    inner = strings.TrimSpace(inner)

    return inner, nil
}
```

The comment on line 107 says "Strip the notice line if present" but the implementation uses `ReplaceAll`, which strips *all* occurrences. A single targeted strip of the leading notice line would do less damage:

```go
inner = strings.TrimLeft(inner, "\n")
inner = strings.TrimPrefix(inner, managedNotice)
inner = strings.TrimLeft(inner, "\n")
```

### Reproduction

Write a managed section containing the notice text as a code sample:

```go
content := "Example of the Nanite notice format:\n\n    " + managedNotice + "\n\nEnd of example."
_ = WriteManagedSection("/tmp/CLAUDE.md", content)
got, _ := ReadManagedSection("/tmp/CLAUDE.md")
// got == "Example of the Nanite notice format:\n\n    \n\nEnd of example."
// The indented notice line is silently removed.
```

### State-distinguishing gap

```go
// "No markers" and "start without end" return the same value:
ReadManagedSection(fileWithNoMarkers)      // ("", nil)
ReadManagedSection(fileWithStartNoEnd)      // ("", nil)
ReadManagedSection(fileWithEmptyManagedBlock) // ("", nil)
```

No current caller (`adapter_cleanup.go`, adapter plugins) actually calls `ReadManagedSection` — `grep` confirms its only callers are the tests. Its existence as a public function suggests future callers will, and the return-value ambiguity is a trap waiting to be sprung.

## Impact

- **Data loss on read**: any managed content that contains the literal notice string has the notice silently deleted from the returned value. Scope is narrow today because nothing calls `ReadManagedSection` in production — but it is exported (`Read` is capitalized) and documented as part of the public API of the `agent` package, so external or future callers are plausible.
- **Caller confusion on state**: a future caller that wants to distinguish "managed section exists but is empty" from "file has no markers" has no signal to work with.
- **Contract drift**: the docstring says the function "returns an empty string if no markers are found" but the implementation also returns empty for the start-without-end case, which is a separate error condition. The docstring is silent on that case.

Severity is Medium, not High, because:
- `ReadManagedSection` has zero production callers today (verified by grep).
- The data loss is bounded — only managed content containing the notice substring is affected.
- The state-distinction gap is latent, not present.

If a production caller is added before this is fixed, the severity should be re-evaluated.

## Recommendation

Three concrete changes:

1. **Strip only the leading notice line, not all occurrences.** Replace `strings.ReplaceAll(inner, managedNotice, "")` with a targeted strip of the leading notice. The notice is always the first non-whitespace line of the inner content, as written by `buildManagedBlock`. Strip that specific line only:

   ```go
   inner = strings.TrimLeft(inner, "\n")
   inner = strings.TrimPrefix(inner, managedNotice)
   ```

2. **Return a distinct error or state for "start without end".** Options:
   - Return `("", ErrManagedSectionTruncated)` where `ErrManagedSectionTruncated` is a sentinel.
   - Return a typed `ManagedSectionResult` struct with fields `{Content string, State ReadState}` where `ReadState` is an enum of `{NoMarkers, Truncated, Empty, OK}`.
   - Document the current behavior explicitly in the docstring so callers know not to rely on the distinction.

   Option 1 is the cheapest change that preserves the current call signature. Option 2 is the most correct API shape. Option 3 is a compromise that defers the API change but locks in the current semantics.

3. **Document the round-trip loss.** The current `ReadManagedSection(WriteManagedSection(content))` is not the identity — it strips whitespace and the notice. The docstring should call this out, or the function should be reshaped into a lossless pair.

## References

- `internal/agent/managed_section.go:L84-114` — `ReadManagedSection`.
- `internal/agent/managed_section.go:L107` — the `strings.ReplaceAll` call.
- `internal/agent/managed_section_test.go:L137-186` — existing read tests; none exercise the data-loss case or the state-distinguishing gap.
- Finding `02-high-test-coverage-enumeration.md` — gap group F.
- Finding `01-high-content-payload-can-inject-marker-strings.md` — sibling data-loss issue on the write side.
