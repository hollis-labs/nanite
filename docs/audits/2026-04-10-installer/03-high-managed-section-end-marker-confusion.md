# [High] `WriteManagedSection` finds end marker before start marker, corrupting user content outside the block

**Scope:** installer / managed-section parser (adapter target files)
**Topic:** Security / correctness — trust boundary between user-edited markdown and installer writer
**Date:** 2026-04-10

## Problem

`agent.WriteManagedSection` uses `strings.Index` to locate the end marker, but after finding the start marker it restarts the search on a slice whose relative offset is not applied back to the absolute string in a way that protects against end markers appearing *before* the first start marker or multiple start/end markers in sequence. Worse, `RemoveManagedSection` and `ReadManagedSection` have the same shape, and together they will misbehave on three realistic user file layouts:

1. **Content outside the block that mentions the markers.** A user's own markdown includes the literal text `<!-- nanite:start -->` and `<!-- nanite:end -->` in a code block (e.g., documentation about Nanite, a changelog entry, a copy-paste from another repo's CLAUDE.md). Because the markers are HTML comments, the markdown renders them as invisible, so the user cannot see they exist. The installer's parser then treats the user's documentation prose as its own managed content and rewrites it on every install.
2. **Two managed blocks in the same file.** If a previous version of the installer (or a hand-edit) left two pairs of markers in the file, `WriteManagedSection` only replaces the first pair — the second stays forever, untouched, as dead content. `RemoveManagedSection` also only removes the first pair.
3. **End marker without matching start marker appearing before a later start marker.** Pathological but not impossible: a copy-paste leaves `<!-- nanite:end -->` above `<!-- nanite:start -->`. `strings.Index` on the end marker then returns the earlier index; `WriteManagedSection`'s subsequent `strings.Index(existing[startIdx:], managedEnd)` finds the second end marker, but the new content is then written as `before := existing[:startIdx]` and `after := existing[endIdx+len(managedEnd):]`, leaving an orphaned `<!-- nanite:end -->` above the new block. Re-running the installer will see the orphaned end marker, combined with a fresh start marker from the re-write, and produce a progressively worse file.

The block markers are not treated as single-pair sentinels. There is no enforcement that `managedStart` count equals `managedEnd` count, no escaping of markers inside content, and no parser test against markers-in-user-prose.

## Evidence

```go
// internal/agent/managed_section.go:L28-78
func WriteManagedSection(path string, content string) error {
    block := buildManagedBlock(content)

    data, err := os.ReadFile(path)
    ...
    existing := string(data)

    startIdx := strings.Index(existing, managedStart)
    if startIdx == -1 {
        // No existing markers — append, ensuring a separating newline.
        sep := ""
        if len(existing) > 0 && !strings.HasSuffix(existing, "\n") {
            sep = "\n"
        }
        return os.WriteFile(path, []byte(existing+sep+block), 0o644)
    }

    // Find managedEnd *after* managedStart to ensure correct pairing.
    endIdx := strings.Index(existing[startIdx:], managedEnd)
    if endIdx == -1 {
        // Start marker without end marker — append fresh block.
        ...
    }
    endIdx += startIdx // convert to absolute index

    // Replace everything from managedStart through managedEnd (inclusive).
    before := existing[:startIdx]
    after := existing[endIdx+len(managedEnd):]
    ...
```

Relevant constants:

```go
// internal/agent/managed_section.go:L9-13
const (
    managedStart  = "<!-- nanite:start -->"
    managedNotice = "<!-- DO NOT EDIT — managed by Nanite. Edit NANITE.md instead. -->"
    managedEnd    = "<!-- nanite:end -->"
)
```

### Proof scenario 1: user prose contains the marker strings

A user has this CLAUDE.md *before* install (no Nanite content yet):

```markdown
# My project

## Nanite notes

The Nanite installer writes its content between these markers:

    <!-- nanite:start -->
    ...managed...
    <!-- nanite:end -->

Do not edit the block between the markers.
```

The first install runs `WriteManagedSection`. `strings.Index` finds the user's example marker, treats the prose between it and the next end marker as "the existing managed block", and replaces the user's documentation with installer content. The original prose is gone.

### Proof scenario 2: orphaned markers from a previous install bug

Suppose a previous install was interrupted between the `before := existing[:startIdx]` and `after := ...` assembly and the `os.WriteFile` — unlikely, but also suppose a user manually pasted half a managed block while trying to clean up after a failed install. The file now contains:

```markdown
<!-- nanite:end -->
Some user content.
<!-- nanite:start -->
managed stuff
<!-- nanite:end -->
```

`strings.Index(existing, managedStart)` returns the position of the *real* start marker. `strings.Index(existing[startIdx:], managedEnd)` then returns the position of the second end marker, correctly. The rewrite produces:

```markdown
<!-- nanite:end -->
Some user content.
<!-- nanite:start -->
...new content...
<!-- nanite:end -->
```

The orphaned first end marker stays. Running the parser again on this file triggers the scenario where the installer and the user disagree about what "outside the block" means.

### Proof scenario 3: two managed blocks

```markdown
<!-- nanite:start -->
old block A
<!-- nanite:end -->
user content in between
<!-- nanite:start -->
old block B
<!-- nanite:end -->
```

`strings.Index` finds the first start, then the first end inside `existing[startIdx:]`. The rewrite replaces block A, leaves user content, leaves block B untouched. Future runs keep "correcting" block A while block B accumulates stale content forever.

### Test coverage gap

`internal/agent/managed_section_test.go` covers happy-path replace, fresh-file create, and no-markers append. It does NOT cover:

- Markers inside fenced code blocks or inline code
- Two pairs of markers
- End-before-start
- Start without end
- Markers in user prose with leading/trailing spaces (the `strings.Index` is whitespace-exact)

## Impact

- **Who:** any user whose CLAUDE.md, AGENTS.md, GEMINI.md, or OPENCODE.md contains the literal marker strings outside an installer-written block. That includes Nanite's own documentation files, any repo forked from another team that already had Nanite, and any CI-generated files that happen to mention the markers.
- **What:** silent data loss of user-authored markdown between the confused marker pair. The user has no warning. The pre-edit snapshot in the archive dir catches the first occurrence (good), but subsequent installs overwrite that snapshot on rollback, so rollback after a second install does not recover the originally-lost content.
- **Installer idempotency violated.** The documented contract of `WriteManagedSection` is idempotency (`internal/agent/managed_section.go:L27`). Scenarios 1 and 2 break idempotency: running twice produces different output.

## Recommendation

Treat the marker pair as strict sentinels. Two concrete changes:

1. **Require exactly one pair per file.** Count `strings.Count(existing, managedStart)` and `strings.Count(existing, managedEnd)` at the top of `WriteManagedSection`. If either count is > 1, or they're unequal in a way that isn't the "start without end" case already handled, return an error asking the user to resolve the conflict manually. A "detected N start markers and M end markers in %s — refusing to edit; please resolve" error is loud and safe.

   ```go
   startCount := strings.Count(existing, managedStart)
   endCount := strings.Count(existing, managedEnd)
   if startCount > 1 || endCount > 1 || (startCount == 1 && endCount == 0) && !appendOK {
       return fmt.Errorf("managed-section markers in %s are inconsistent (start=%d, end=%d); resolve manually", path, startCount, endCount)
   }
   ```

2. **Make the markers more specific** so accidental user-prose collisions are rare. Either add a hash fingerprint to the marker (`<!-- nanite:start:v1 -->`) that future versions can migrate forward, or use a marker with a random token written at install time and stored in `.nanite/`. The former is a smaller change and matches the `version` pattern already present in other Nanite config files.

3. **Add parser tests for the three scenarios above.** These are straightforward table-driven tests in `internal/agent/managed_section_test.go`. Mark the "markers in code block" case as an explicit non-goal if the team decides not to support it, but document it.

### Related behavior: `RemoveManagedSection`

The same issue affects `RemoveManagedSection` (`internal/agent/managed_section.go:L128-184`) and `RemoveAgentrcSection` (`internal/service/install/claudemd.go:L16-75`). The agentrc heading stripper is even more fragile: it finds the first `#{1,6} agentrc` heading, then terminates at the next sibling heading. A user's CLAUDE.md that documents agentrc in a subsection under a `## References` heading will have content after that heading silently dropped.

Fixing `WriteManagedSection` to count markers is the cheapest change and resolves the worst scenarios. The agentrc heading stripper is only touched on migrate, which is a one-time operation, so its blast radius is smaller — but it still deserves the same conservative "refuse to edit on ambiguous input" treatment.

## References

- `internal/agent/managed_section.go:L9-184` — the full parser, including `WriteManagedSection`, `ReadManagedSection`, `RemoveManagedSection`.
- `internal/agent/managed_section_test.go` — existing test coverage, does not hit the ambiguous cases.
- `internal/service/install/claudemd.go:L16-75` — `RemoveAgentrcSection`, same family of issues.
- `internal/plugin/builtin/adapter-claude/plugin.go:L179-181`, `adapter-codex/plugin.go:L140-148`, `adapter-gemini/plugin.go:L140-148`, `adapter-opencode/plugin.go:L143-151` — the four adapter callers, all passing their own `projectDir/FILE.md` through this parser.
- Related finding: `02-high-symlink-following-on-adapter-target-files.md` — the same files are also vulnerable to symlink redirection.
