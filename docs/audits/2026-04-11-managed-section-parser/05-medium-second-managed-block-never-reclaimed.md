# [Medium] `WriteManagedSection` silently leaves a second managed block in place when re-writing; repeated installs accumulate dead content

**Scope:** managed-section parser / writer
**Topic:** Correctness — idempotency under pre-existing anomalies
**Date:** 2026-04-11

## Problem

If a target file already contains *two* complete marker pairs (from a prior install bug, a copy-paste, a merge conflict resolution that left both halves, or a manual repair gone wrong), `WriteManagedSection` replaces only the *first* pair and leaves the second pair in place indefinitely. The parser uses `strings.Index` for both the start and the end, finding the earliest occurrence of each — it has no marker-counting logic, no "replace all" loop, and no warning when a second pair is detected.

The second block is dead content: it is not refreshed on subsequent installs, not removed on adapter cleanup, not scanned by `ReadManagedSection`, and not snapshotted on rollback. It persists in the user's file forever unless manually deleted.

This finding is closely related to installer finding 03 (`docs/audits/2026-04-10-installer/03-high-managed-section-end-marker-confusion.md` §Scenario 3), which describes the same phenomenon. Installer finding 03 is flagged as High because it is part of a broader data-loss pattern; this finding is Medium because on its own the "second-block-never-updated" behavior is a *stale-data* issue rather than a *data-loss* issue — the user's content outside the block is preserved, and the first block is correctly managed. But the dead block will confuse future reviewers, future tools, and future manual inspection. And because `RemoveManagedSection` has the same shape (only removes the first pair), adapter cleanup also does not reach the second block.

## Evidence

```go
// internal/agent/managed_section.go:L41-52
startIdx := strings.Index(existing, managedStart)
if startIdx == -1 {
    // ...
}

// Find managedEnd *after* managedStart to ensure correct pairing.
endIdx := strings.Index(existing[startIdx:], managedEnd)
if endIdx == -1 {
    // ...
}
endIdx += startIdx // convert to absolute index
```

Neither `strings.Count(existing, managedStart)` nor `strings.Count(existing, managedEnd)` is consulted. The parser assumes at most one pair.

```go
// internal/agent/managed_section.go:L139-150
startIdx := strings.Index(existing, managedStart)
if startIdx == -1 {
    return false, false, nil
}

endIdx := strings.Index(existing[startIdx:], managedEnd)
if endIdx == -1 {
    return false, false, nil
}
endIdx += startIdx + len(managedEnd)
```

`RemoveManagedSection` shares the shape: first-pair-only. A cleanup of a file with two blocks leaves the second block in place, and from the adapter's perspective reports the file as "stripped" (partially).

### Reproduction walkthrough

1. Start with a CLAUDE.md containing two clean marker pairs (paste them manually, or arrange via a merge conflict):
   ```markdown
   # My Project
   <!-- nanite:start -->
   <!-- DO NOT EDIT — managed by Nanite. Edit NANITE.md instead. -->
   block A (old)
   <!-- nanite:end -->

   Some user notes.

   <!-- nanite:start -->
   block B (older)
   <!-- nanite:end -->
   ```

2. Run `nanite install` which calls `SyncProjectRoot` → `WriteManagedSection(path, "new content")`.

3. Output:
   ```markdown
   # My Project
   <!-- nanite:start -->
   <!-- DO NOT EDIT — managed by Nanite. Edit NANITE.md instead. -->
   new content
   <!-- nanite:end -->
   Some user notes.

   <!-- nanite:start -->
   block B (older)
   <!-- nanite:end -->
   ```

4. Every subsequent install refreshes block A, leaves block B.

5. Remove the claude adapter from the config and run install again. `RemoveManagedSection` strips block A, reports `becameEmpty=false` (because block B and user notes remain), writes back the file. Block B stays, orphaned, forever.

### The installer-03 mitigation would surface this, not fix it

Installer finding 03 recommends counting markers at the top of `WriteManagedSection` and refusing to edit a file with more than one pair. That fix would force the user to manually resolve the two-block case before the installer would touch the file — which is safer than the current silent partial rewrite, but it is a refuse-to-edit mitigation rather than a correct-by-construction fix. The user is stuck in a "installer refuses to edit until you fix the file" loop. For the two-clean-pairs case specifically, the installer could reasonably treat the file as self-consistent and either (a) replace the union of the two blocks with one new block, or (b) leave block B as user content and only refresh block A. Either is a judgment call that needs product input.

## Impact

- **Stale content**: block B in the example above will contain data from an older Nanite install indefinitely. A user inspecting their CLAUDE.md will see contradictory sets of agents listed, or an outdated "Available Nanite agents:" header that doesn't match their current config.
- **Confused tooling**: any future tool that does `grep -c '<!-- nanite:start -->' CLAUDE.md` to detect whether the file is under Nanite management will see count=2 and have to decide whether to trust that or flag it.
- **Adapter cleanup incomplete**: removing an adapter leaves block B in place. A user who removes the claude adapter to stop Nanite from touching CLAUDE.md will still see block B in their file.

Severity is Medium because:
- The user's content outside the blocks is preserved.
- The dead block is visible to manual inspection (HTML comments, discoverable via grep).
- Installer-03's marker-counting fix, if implemented, surfaces this case by refusing to edit — so the user is warned.

## Recommendation

Once installer-03's marker-counting fix is in place, handle the two-clean-pairs case as an explicit product decision rather than a silent partial rewrite. Three options, in rough order of complexity:

1. **Reject and warn.** If `start_count > 1 || end_count > 1`, return an error listing the byte positions of each marker and asking the user to resolve. This is installer-03's recommendation. Simple, safe, but inconvenient.

2. **Reject, but offer `--force` to collapse all blocks to one.** A `WriteManagedSectionOpts{Force: true}` variant collapses all pairs (and any content between the first start and the last end) into a single managed block, preserving only user content outside the union. Loud, irreversible, but recoverable via snapshot.

3. **Walk all blocks and replace the first, leaving the rest alone, with a logged warning.** Closest to current behavior but at least surfaces the anomaly. Not recommended — the silent continuation is exactly the current bug.

For `RemoveManagedSection`, the same three options apply. Reject-on-ambiguous is the cheapest change and matches the tone of installer-03.

### Pair with count-based detection

The minimal fix is a two-line addition at the top of both functions:

```go
if strings.Count(existing, managedStart) > 1 || strings.Count(existing, managedEnd) > 1 {
    return fmt.Errorf("%s has multiple managed-section marker pairs; resolve manually", path)
}
```

This does not fix the underlying product question but does prevent silent damage.

## References

- `internal/agent/managed_section.go:L41-79` — `WriteManagedSection` first-pair-only logic.
- `internal/agent/managed_section.go:L128-184` — `RemoveManagedSection` first-pair-only logic.
- `docs/audits/2026-04-10-installer/03-high-managed-section-end-marker-confusion.md` §Scenario 3 — same root cause, already flagged. This finding is kept as a Medium to preserve the adapter-cleanup dimension, which installer-03 did not cover.
- Finding `02-high-test-coverage-enumeration.md` gap groups B and E — no existing test exercises the two-pairs case for either function.
