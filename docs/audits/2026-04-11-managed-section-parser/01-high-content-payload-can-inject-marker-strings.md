# [High] `WriteManagedSection` does not validate the `content` parameter; caller-supplied content containing `<!-- nanite:end -->` corrupts the file on the next write

**Scope:** managed-section parser / writer
**Topic:** Security / correctness — trust boundary between caller-supplied payload and the marker sentinel format
**Date:** 2026-04-11

## Problem

`WriteManagedSection(path, content)` wraps the caller's `content` in a managed block (`managedStart` ... notice ... `content` ... `managedEnd`) without ever checking whether `content` itself contains the literal marker strings. If `content` contains `<!-- nanite:end -->`, the file emitted by the first write has **two** end markers: one inside the payload, one closing the block. On the next call to `WriteManagedSection` on the same file, `strings.Index(existing[startIdx:], managedEnd)` finds the *inner* end marker first — so the computed `before`/`after` split slices mid-block, leaving an orphaned `managedEnd` in the user-content portion of the output. Subsequent writes compound the damage because the orphan is now treated as the section end.

This is a distinct bug from installer audit finding 03 (`2026-04-10-installer/03-high-managed-section-end-marker-confusion.md`), which concerns marker strings in *pre-existing user file content*. This finding concerns marker strings in *caller-supplied managed-content payloads* — the `content` argument — which is not mentioned in installer-03 and is not blocked by any of the mitigations installer-03 recommends (e.g., start/end count checks would still pass on a freshly-written block containing a smuggled inner marker, because the initial write produces a file with `start=1, end=2` which the count check would *reject*, but only *after* the buggy file has been written).

The `content` parameter flows from caller-controlled data paths that are plausibly user-influenced:

- `adapter-claude/plugin.go:L162-181` builds content from `ap.Name` and `ap.Description` (`store.AgentProfile` fields, which are user-editable via the agent profile CRUD API).
- `adapter-codex/plugin.go` (same pattern, `AGENTS.md`).
- `adapter-gemini/plugin.go` (same pattern, `GEMINI.md`).
- `adapter-opencode/plugin.go` (same pattern, `OPENCODE.md`).
- `internal/service/install/claudemd.go:L99,136` forwards `managedContent` through unchanged.

None of these callers validates that the agent name or description is free of HTML comment markers; the profile is stored free-form in SQLite and exposed through the agent CRUD API. A user who edits their own profile description to include `<!-- nanite:end -->` (accidentally, via paste from documentation, or via any HTML-comment note-taking habit) triggers silent corruption on the next session prep that runs `SyncProjectRoot`.

## Evidence

```go
// internal/agent/managed_section.go:L15-21
func buildManagedBlock(content string) string {
    return managedStart + "\n" +
        managedNotice + "\n\n" +
        content + "\n\n" +
        managedEnd + "\n"
}
```

`content` is concatenated without inspection. Now trace the second write:

```go
// internal/agent/managed_section.go:L41-68
startIdx := strings.Index(existing, managedStart)
// ... startIdx == 0 in our scenario
endIdx := strings.Index(existing[startIdx:], managedEnd)
// ... finds the marker INSIDE the content, not the closing marker
endIdx += startIdx

before := existing[:startIdx]
after := existing[endIdx+len(managedEnd):]
after = strings.TrimPrefix(after, "\n")

var b strings.Builder
b.WriteString(before)
b.WriteString(block)
if after != "" {
    b.WriteString("\n")
    b.WriteString(after)
}
```

### Reproduction walkthrough

Suppose a caller in `adapter-claude` produces agent listing content where one agent's `Description` is `"Handles <!-- nanite:end --> markers in docs"` (free-form text, written by the user via the profile editor). `buildManagedBlock` returns:

```
<!-- nanite:start -->
<!-- DO NOT EDIT — managed by Nanite. Edit NANITE.md instead. -->

Available Nanite agents:

- **docs-helper** — Handles <!-- nanite:end --> markers in docs

<!-- nanite:end -->
```

First write is correct: one logical block, structurally well-formed from the writer's point of view.

Second write (next `SyncProjectRoot`, which runs on every `nanite install` and on every adapter re-sync):

- `strings.Index(existing, managedStart)` → 0.
- `strings.Index(existing[0:], managedEnd)` → points at `Handles <!-- nanite:end -->`, the inner marker inside the description line, not the closing marker.
- `endIdx+len(managedEnd)` lands partway through the description line.
- `after` begins at `" markers in docs\n\n<!-- nanite:end -->\n"` (plus anything after the real block).
- `TrimPrefix(after, "\n")` leaves `" markers in docs\n\n<!-- nanite:end -->\n..."`.
- Output file:

```
<!-- nanite:start -->
<!-- DO NOT EDIT — managed by Nanite. Edit NANITE.md instead. -->

<NEW content>

<!-- nanite:end -->
 markers in docs

<!-- nanite:end -->
<rest of user file>
```

The user's file now has:

1. An orphaned fragment `" markers in docs"` sitting outside any block.
2. An orphaned `<!-- nanite:end -->` sitting outside any block.
3. On the *third* write, `strings.Index(existing, managedStart)` still finds the real start, but `strings.Index(existing[startIdx:], managedEnd)` now finds the closing marker of the real block — so the next rewrite leaves the orphaned `managedEnd` in place, and from that point on the file permanently contains a dead marker pair that breaks installer finding 03's proposed "count markers" mitigation (start=1, end=2 → error/refuse-to-edit).

### Test coverage gap

`internal/agent/managed_section_test.go` does not exercise any case where the `content` argument contains either `managedStart` or `managedEnd`. See finding `02-medium-test-coverage-enumeration.md` for the full enumeration.

## Impact

- **Silent data loss** in every adapter target file where the caller-supplied content contains a marker substring. Scope is not hypothetical: agent descriptions are user-authored free text, and the Nanite project itself documents the marker format in user-facing docs that a user might copy-paste into a profile field.
- **Installer audit finding 03's recommended mitigation is insufficient on its own.** Counting `managedStart`/`managedEnd` occurrences at the top of `WriteManagedSection` (installer-03 recommendation #1) will refuse to edit a file that has already been corrupted by this path, but does not prevent the initial corruption — it only surfaces it on the next run, at which point the file is already wedged and the user has to resolve the conflict manually. The fix must also reject `content` inputs that contain either marker.
- **Rollback does not help.** `snapshotAdapterTargets` (installer audit) snapshots the pre-edit file, but the corruption only surfaces on the *second* write — by which time the snapshot has been overwritten by the first (structurally valid) write.
- **Every adapter plugin session-prep and installer run is exposed.** Any of the five `SyncProjectRoot` implementations (claude, codex, gemini, opencode, and any future adapter) feeds profile data into the block verbatim. `UpdateCLAUDEmd` forwards unchanged. None of them sanitize.

## Recommendation

Reject content containing either marker at the top of `WriteManagedSection`. Two concrete changes, with tradeoffs:

**Option A (recommended): hard-reject.** Refuse to write and return an error if `content` contains `managedStart` or `managedEnd`.

```go
func WriteManagedSection(path string, content string) error {
    if strings.Contains(content, managedStart) || strings.Contains(content, managedEnd) {
        return fmt.Errorf("managed-section content contains reserved marker string; refusing to write %s", path)
    }
    // ... rest of function
}
```

This is loud, it catches the bug at the injection point (not on the next write), and it forces callers to sanitize. Callers that build content from user-controlled data (adapter plugins) should either (a) escape the markers before passing in, or (b) validate the source fields at the profile-write boundary and reject marker strings there.

**Option B: escape the markers in content.** Replace `managedStart`/`managedEnd` in `content` with an escaped form (e.g., `<!-- nanite&#58;start -->`) before embedding. This preserves user intent but introduces a second parsing layer: `ReadManagedSection` would also need to unescape on read, and external tools that parse the file would see the escaped form. Not recommended — the round-trip subtlety makes this fragile.

**Ancillary**: the four adapter plugins that build content from `store.AgentProfile` should *also* validate the profile fields at the read boundary (`SyncProjectRoot`), either by rejecting markers or by HTML-escaping them for display. Defense in depth — a single missed sanitization in the writer should not be the only control.

### Pair this with installer-03's fix

Installer finding 03 recommends counting markers and rejecting files with unexpected counts. That fix catches *pre-existing* file corruption. This finding's fix catches *content-originated* corruption. Both are needed — neither subsumes the other.

## References

- `internal/agent/managed_section.go:L15-21` — `buildManagedBlock`, which performs the unsafe concatenation.
- `internal/agent/managed_section.go:L28-79` — `WriteManagedSection`, which re-parses the file without detecting the injected marker.
- `internal/plugin/builtin/adapter-claude/plugin.go:L162-181` — first caller, builds content from `store.AgentProfile` fields.
- `internal/plugin/builtin/adapter-codex/plugin.go:L140-148` — same pattern.
- `internal/plugin/builtin/adapter-gemini/plugin.go:L140-148` — same pattern.
- `internal/plugin/builtin/adapter-opencode/plugin.go:L143-151` — same pattern.
- `internal/service/install/claudemd.go:L99,L136` — installer forwarder.
- Related: `docs/audits/2026-04-10-installer/03-high-managed-section-end-marker-confusion.md` — cross-referenced; sibling bug on the file-content trust boundary.
- Related: `04-medium-read-managed-section-silent-notice-stripping.md` — `ReadManagedSection` has a symmetric sanitization gap on the read side.
