# [Medium] `dev_grep` / `dev_glob` walks are unbounded in file count and depth

**Scope:** `internal/mcp/dev_tools.go` — `callGrep`, `callGlob`
**Topic:** Memory & resources, availability
**Date:** 2026-04-10

## Problem

`callGrep` and `callGlob` both use `filepath.Walk` / `filepath.WalkDir` with exactly one safety net: `matchCount >= maxMatches` (for grep) or `len(matches)` used after the walk completes (for glob). Neither imposes:

- A maximum file-count visited.
- A maximum directory depth.
- A cancellation check on the caller's context (see also finding 08).
- A time budget.

The existing skip list (`.git`, `node_modules`, `vendor`, `dist`) helps on typical Go/Node projects but is insufficient for:

- Python `.venv/`, `__pycache__/`, `site-packages/` (millions of files in a mature project).
- Rust `target/` (tens of thousands of files).
- Java/Kotlin `.gradle/`, `build/`, Maven `~/.m2/repository/` if reached via a path.
- Monorepos with many nested worktrees.
- Generated docs/output dirs (`_site/`, `public/`, `out/`).

`callGlob` is further exposed because it has no `matchCount` short-circuit — it collects *all* matches into a slice and only truncates at `maxResults` on output. The walk continues through the entire tree regardless. Glob against `**/*` in a 200k-file monorepo reads every directory entry into RAM.

This is not remotely exploitable — walking files is relatively cheap — but a single tool call against the wrong directory can lock the handler for minutes and grow memory proportional to the tree size. It compounds with finding 06 (`callGrep` also reads every file's content).

## Evidence

```go
// internal/mcp/dev_tools.go:230-296 (grep walk)
err = filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
    ...
    if info.IsDir() {
        base := filepath.Base(path)
        if base == ".git" || base == "node_modules" || base == "vendor" || base == "dist" {
            return filepath.SkipDir
        }
        return nil
    }
    if globFilter != "" {
        matched, _ := filepath.Match(globFilter, filepath.Base(path))
        if !matched {
            return nil
        }
    }
    if matchCount >= maxMatches {
        return filepath.SkipAll
    }
    ...
```

```go
// internal/mcp/dev_tools.go:409-434 (glob walk)
err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
    if walkErr != nil {
        return nil
    }
    if entry.IsDir() {
        base := filepath.Base(path)
        if base == ".git" || base == "node_modules" || base == "vendor" || base == "dist" {
            return filepath.SkipDir
        }
        return nil
    }

    relPath, err := filepath.Rel(dir, path)
    if err != nil {
        return nil
    }

    if globMatch(pattern, relPath) {
        info, err := entry.Info()
        if err != nil {
            return nil
        }
        matches = append(matches, fileEntry{path: relPath, modTime: info.ModTime()})
    }
    return nil
})
```

`matches` is unbounded — the slice grows linearly in "files matching the pattern" which, for `**/*` patterns, is the whole tree.

## Impact

- **Latency:** a `dev_glob(pattern="**/*", directory="~/Projects-apps")` against a dev workstation's projects directory can take tens of seconds to minutes. The MCP dispatch layer is synchronous, so the whole chat turn stalls.
- **Memory:** `callGlob` holds one `fileEntry` per matched file. ~72 bytes per entry on 64-bit (string header + time.Time). A 500k-file match uses ~36MB + slice growth overhead. Not fatal, but disproportionate for a "tool call."
- **Cancellation:** without context propagation (finding 08), a user-aborted turn can't stop the walk.

## Recommendation

Add three bounds, shared between both handlers:

```go
const (
    maxWalkFiles = 20_000
    maxWalkDepth = 20
)

// Inside the walker callbacks:
visited++
if visited > maxWalkFiles {
    return fmt.Errorf("walk exceeded %d files; narrow the search", maxWalkFiles)
}

// Compute depth from relative path:
rel, _ := filepath.Rel(dir, path)
if strings.Count(rel, string(os.PathSeparator)) > maxWalkDepth {
    if entry.IsDir() {
        return filepath.SkipDir
    }
    return nil
}

// Context check (see finding 08):
if err := ctx.Err(); err != nil {
    return err
}
```

For `callGlob`, also:

- Short-circuit once `len(matches) >= maxResults * 2` (collect more than needed for a clean "sorted top-N" but stop well before the full tree).
- Move sorting to a heap of size `maxResults` so memory doesn't scale with the number of matches.

For `callGrep`, add a per-file size guard (covered in finding 06) so the file-count bound does not accidentally allow a small number of enormous files to consume memory instead.

**Extend the directory denylist.** Move the hardcoded skip list to a package-level `var` and add at minimum:

```go
var skipDirs = map[string]bool{
    ".git": true, "node_modules": true, "vendor": true, "dist": true,
    "target": true, ".venv": true, "venv": true, "__pycache__": true,
    ".gradle": true, "build": true, "_site": true, "public": true,
    "out": true, ".next": true, ".nuxt": true, ".cache": true,
}
```

Recommended severity: Medium. Availability impact is real, user-triggerable, but not security-relevant. Fix is mechanical.

## References

- `internal/mcp/dev_tools.go:L208-L306` — `callGrep`
- `internal/mcp/dev_tools.go:L388-L462` — `callGlob`
- Go stdlib: `filepath.Walk`, `filepath.WalkDir`, `filepath.SkipDir`, `filepath.SkipAll`
- Related: `06-high-dev-grep-loads-full-files-into-memory.md`, `08-high-tool-context-not-propagated.md`
