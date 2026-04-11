# [High] `dev_grep` loads every scanned file into memory before matching

**Scope:** `internal/mcp/dev_tools.go` — `callGrep`
**Topic:** Memory & resources, DoS via untrusted pattern/directory pair
**Date:** 2026-04-10

## Problem

`callGrep` walks the target directory, opens each file, and collects its entire contents into `[]string` before running the regex — so the memory cost of grepping a directory is "sum of all file sizes, in bytes, plus slice overhead." There is no per-file size limit, no total-memory bound, and no short-circuit when a file is obviously too big. The only cap is `maxMatches = 100` on *matches*, and the walk continues to read every file fully until the hundredth match is found — which for typical "find all TODOs in a small codebase" calls may never happen, meaning every file in the tree gets read into memory unconditionally.

Combined with `filepath.Walk` having no depth or file-count bound, a single `dev_grep(pattern="x", directory="/Users/user/Projects-apps/large-repo")` against a repo containing a 4GB `.sqlite` file, a 2GB `node_modules/.cache` blob, or a 10GB log file can balloon the nanite process to many GB of RSS and then OOM-kill it.

## Evidence

```go
// internal/mcp/dev_tools.go:251-296
f, err := os.Open(path)
if err != nil {
    return nil
}
defer f.Close()

scanner := bufio.NewScanner(f)
scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
var lines []string
for scanner.Scan() {
    lines = append(lines, scanner.Text())
}

relPath, _ := filepath.Rel(dir, path)
if relPath == "" {
    relPath = path
}

for i, line := range lines {
    if !re.MatchString(line) {
        continue
    }
    ...
```

Note the loop on line 260-262: the entire file is appended to `lines` *before* any pattern match is attempted. The match loop on line 269 then iterates the in-memory slice. For a 1GB text file, this is 1GB in `[]string` plus per-string header overhead — on a 64-bit system, ~24 bytes per line — so a 10M-line file is ~240MB of header overhead on top of the content.

Ignored file classes that block would help but are absent. The directory skip list is only:

```go
// internal/mcp/dev_tools.go:234-239
if info.IsDir() {
    base := filepath.Base(path)
    if base == ".git" || base == "node_modules" || base == "vendor" || base == "dist" {
        return filepath.SkipDir
    }
    ...
}
```

— four hardcoded directory names, no file-extension filter, no size check. Binary files (`.sqlite`, `.db`, `.pdf`, `.zip`, `.tar`, `.mp4`) are read in their entirety.

The scanner line-length cap (`1024*1024` = 1MB) provides partial protection: a single-line 10GB binary file returns `bufio.ErrTooLong` on the first `Scan()` and exits the file loop early. That's accidental protection, not design, and any file with newlines in the first N KB defeats it.

## Impact

- **OOM:** one `dev_grep` call against an `~/Projects-apps` subdir containing a single large text-shaped file is enough to kill the nanite process. No elevated permissions, no complex payload.
- **Latency:** even without OOM, reading every file in a large repo serializes against disk I/O and holds the handler for minutes. The whole MCP transport is single-threaded on that call.
- **Who:** any LLM tool caller — standard prompt-injection surface.
- **Reproducibility:** deterministic per directory tree.

## Recommendation

Swap the "slurp into `[]string`, then match" approach for a streaming match:

```go
scanner := bufio.NewScanner(f)
scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

// Ring buffer for trailing context lines.
const ringSize = 8 // must be >= ctxLines
ring := make([]string, 0, ringSize)
ringIdx := 0

var (
    lineNo      int
    pendingTrail int // lines of "after" context still to print
)
for scanner.Scan() {
    lineNo++
    line := scanner.Text()
    if re.MatchString(line) {
        // Print trailing context from ring, the match, and start post-context.
        ...
        matchCount++
        if matchCount >= maxMatches {
            break
        }
    } else if pendingTrail > 0 {
        // Print post-match context line.
        pendingTrail--
    }
    // Update ring.
    if len(ring) < ringSize {
        ring = append(ring, line)
    } else {
        ring[ringIdx] = line
        ringIdx = (ringIdx + 1) % ringSize
    }
}
```

Memory becomes O(context-window) per file, independent of file size.

**Additional bounds to apply while in the function:**

1. **Skip files by size.** Before `os.Open`, check `info.Size()` from the `Walk` callback; skip files over (say) 10MB with a note in the output.
2. **Skip non-text files.** Check file extension against a small allowlist (`.go`, `.md`, `.txt`, `.json`, `.yaml`, `.ts`, `.tsx`, `.js`, `.py`, …) or do a binary-sniff on the first 512 bytes (look for NUL bytes). The rest of nanite has a similar pattern in `internal/truncate/` — reuse if compatible.
3. **File-count bound on the walk.** After (say) 10,000 files inspected, stop and return what has been found with a note. Large monorepos can have > 200k files.
4. **Honor caller context.** As with `dev_bash`, the context argument is discarded. The `filepath.Walk` callback should check `ctx.Err()` and return an error to short-circuit when the caller aborts.

Recommended severity: High. Easy to trigger; kills the process; no security boundary broken but the availability impact is real and the fix is small.

## References

- `internal/mcp/dev_tools.go:L208-L306` — `callGrep`
- `internal/mcp/dev_tools.go:L251-L296` — the load-into-`[]string` pattern
- Go stdlib: `bufio.Scanner`, `filepath.WalkDir` (preferred over `filepath.Walk` for perf, but not the primary issue here)
- CWE-400 — Uncontrolled Resource Consumption
- Related: `04-high-dev-bash-output-unbounded-and-context-ignored.md`, `08-high-tool-context-not-propagated.md`
