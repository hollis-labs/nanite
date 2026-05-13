// Package workspace implements the AGENTS.md walk-up that populates
// SlotWorkspace from a session's working_dir (CW-20260512-0116,
// SP-20260512-0009 W6).
//
// The walk-up takes a session working_dir, walks upward to the nearest
// git root (or filesystem root when no git is found), and collects the
// content of any allowlisted instruction files at each directory level.
// The result populates the workspace slot — same agent in different
// working directories receives different local conventions, matching
// the architectural intent the user laid out in the harness-restoration
// design session.
//
// # Walk direction (decision locked)
//
// Walk order is **innermost-first → outermost-last**, matching opencode's
// findUp convention (packages/core/src/filesystem.ts::findUp,
// packages/opencode/src/session/instruction.ts::resolve). The session's
// working_dir is closest to the user's intent; rules at that level take
// precedence in the LLM's attention because they appear first in the
// concatenated payload. See the comparative reference at
// agent-workspaces/exploration/nanite/2026-05-12-opencode-architecture-comparison/
// opencode-architectural-deltas.md.
//
// # Allowlist (decision locked)
//
// At each directory level we look for files in this priority order:
//
//  1. AGENTS.md
//  2. CLAUDE.md
//  3. NANITE.md
//  4. .nanite/rules.md
//
// All matches at a level are included (not first-match-wins-per-level),
// concatenated in the listed order. Different filenames carry different
// editorial intent (AGENTS.md is the generic agent contract, CLAUDE.md
// is Claude-specific, NANITE.md is Nanite-specific, .nanite/rules.md is
// the local override) and shadowing them at the directory level would
// silently drop project-local Nanite tuning.
//
// # Header format (decision locked)
//
// Each file's content is prefixed with a single-line header matching
// opencode's instruction.ts:160 convention:
//
//	Instructions from: /abs/path/to/file.md
//	<content>
//
// Blocks are joined by "\n\n". Trailing whitespace on each file's
// content is trimmed once before joining; the empty-string case
// (no files found) returns "" so the assembly decider treats the slot
// as empty and skips it via the existing skipped_no_content path.
//
// # Security
//
//   - Symlinks are NOT followed (os.Lstat). Symlinked instruction
//     files at any level are skipped silently. This matches the
//     pathsafe.ResolveUnder posture for the dev-tools layer.
//   - File size is capped at MaxFileBytes (1 MiB). Files over the cap
//     are truncated with a trailing comment; we never refuse silently.
//   - Walk is parent-only. Subdirectories are never recursed into.
//   - Walk stops at the nearest .git directory OR the filesystem root,
//     whichever comes first. We do NOT cross volume / mount boundaries
//     beyond what os.Stat returns naturally.
package workspace

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MaxFileBytes caps the size of any single instruction file. Files over
// this are truncated with a comment. 1 MiB is generous — a real
// AGENTS.md hand-written by the user is unlikely to exceed a few KB.
const MaxFileBytes = 1 << 20

// allowlist enumerates the instruction-file names recognized at each
// directory level. Order matters — files are concatenated in this order
// within a single directory level. The path is relative to the
// directory being inspected; .nanite/rules.md is the only nested entry.
var allowlist = []string{
	"AGENTS.md",
	"CLAUDE.md",
	"NANITE.md",
	filepath.Join(".nanite", "rules.md"),
}

// fileEntry tracks one resolved instruction file: where it lives on
// disk, its content (truncated to MaxFileBytes), and its mtime at the
// moment it was read. mtime is the cache invalidation signal — when
// any cached entry's on-disk mtime no longer matches the recorded one,
// the cache entry is stale and the walk-up reruns.
type fileEntry struct {
	Path    string
	Content string
	ModTime time.Time
}

// Result is one walk-up's output. Files is in innermost-first order
// (matching the concatenated payload's reading order). Content is the
// final concatenated string ready for SlotWorkspace; an empty Content
// means the walk-up found no instruction files and the slot should be
// treated as absent.
type Result struct {
	WorkingDir string
	GitRoot    string // empty when no .git was found between working_dir and filesystem root
	Files      []fileEntry
	Content    string
}

// WalkUp performs the AGENTS.md walk-up from workingDir up to the
// nearest git root (or filesystem root if no .git is found). It returns
// a Result with concatenated content in innermost-first order. An empty
// or non-existent workingDir returns a zero Result with no error — the
// caller treats Content=="" as "slot empty, skip on the wire".
//
// This function performs unbounded filesystem reads in the worst case
// (walk up to /), so callers on hot paths MUST go through Cache rather
// than calling WalkUp directly.
func WalkUp(workingDir string) (Result, error) {
	if workingDir == "" {
		return Result{}, nil
	}

	abs, err := filepath.Abs(workingDir)
	if err != nil {
		return Result{}, fmt.Errorf("workspace: abs working_dir: %w", err)
	}

	// Stat the starting directory. If it doesn't exist, return empty —
	// stale working_dir shouldn't error out the whole slot pipeline.
	info, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{WorkingDir: abs}, nil
		}
		return Result{}, fmt.Errorf("workspace: stat %s: %w", abs, err)
	}
	if !info.IsDir() {
		// working_dir resolved to a file (shouldn't happen in normal
		// operation). Walk from its parent.
		abs = filepath.Dir(abs)
	}

	out := Result{WorkingDir: abs}

	// Walk innermost-first: start at workingDir, climb to parent each
	// iteration, stop at the .git root or the filesystem root. We
	// inspect the .git marker AFTER reading the directory's instruction
	// files so the git-root directory's AGENTS.md (etc.) is included.
	current := abs
	for {
		entries := readDirEntries(current)
		out.Files = append(out.Files, entries...)

		// Check for .git at this level — if present, this is the git
		// root and we stop after reading. Use os.Lstat (no symlink
		// follow). Both .git directories (normal repo) and .git files
		// (worktree pointer) terminate the walk.
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			out.GitRoot = current
			break
		}

		parent := filepath.Dir(current)
		if parent == current {
			// Hit filesystem root without finding a .git. Walk
			// terminates here per the ticket spec ("git root OR
			// filesystem root").
			break
		}
		current = parent
	}

	out.Content = concatenate(out.Files)
	return out, nil
}

// readDirEntries returns the allowlisted instruction files at one
// directory level, in allowlist order. Files are read up to
// MaxFileBytes; oversized files are truncated with a trailing
// comment. Symlinks (os.Lstat reports Mode()&os.ModeSymlink) are
// skipped silently.
func readDirEntries(dir string) []fileEntry {
	out := make([]fileEntry, 0, len(allowlist))
	for _, name := range allowlist {
		info, eligible := isEligibleInstructionFile(dir, name)
		if !eligible {
			continue
		}
		full := filepath.Join(dir, name)
		content, truncated, rerr := readCapped(full)
		if rerr != nil {
			continue
		}
		if truncated {
			content = content + "\n\n<!-- workspace walk-up: content truncated at " + fmt.Sprintf("%d", MaxFileBytes) + " bytes -->"
		}
		out = append(out, fileEntry{
			Path:    full,
			Content: content,
			ModTime: info.ModTime(),
		})
	}
	return out
}

// isEligibleInstructionFile applies the same allow/skip logic
// readDirEntries uses, returning (info, true) when the file would be
// picked up by a walk and (nil, false) when it would be skipped. The
// helper exists so Cache.isStale's "new file appeared" sweep matches
// the same exclusion logic (symlink leaf, intermediate symlink for
// nested entries like .nanite/rules.md, directory at the path)
// readDirEntries enforces — otherwise the sweep can mark the cache
// stale on every call for excluded-but-present files and trigger
// perpetual re-walks.
//
// Note: this does NOT check for read errors at the file level — those
// only surface from readCapped during the actual walk. If a previously-
// readable file becomes unreadable, isStale's per-file mtime check
// reports it as stale via the os.Lstat error path; if a previously-
// unreadable file remains unreadable, it never appeared in cached
// Files and the sweep correctly treats it as "not new".
func isEligibleInstructionFile(dir, name string) (os.FileInfo, bool) {
	full := filepath.Join(dir, name)
	info, err := os.Lstat(full)
	if err != nil {
		return nil, false
	}
	// Reject symlinks for both the leaf file and (for nested
	// entries like .nanite/rules.md) any intermediate symlink.
	// filepath.EvalSymlinks would resolve everything; we want to
	// REFUSE symlinked instruction files entirely.
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, false
	}
	if info.IsDir() {
		return nil, false
	}
	// For nested entries the parent directory must also not be a
	// symlink. .nanite/rules.md is the only such entry today.
	if parent := filepath.Dir(name); parent != "." {
		parentInfo, perr := os.Lstat(filepath.Join(dir, parent))
		if perr != nil || parentInfo.Mode()&os.ModeSymlink != 0 {
			return nil, false
		}
	}
	return info, true
}

// readCapped reads up to MaxFileBytes from path. Returns (content,
// truncated, err). When truncated is true the content is exactly
// MaxFileBytes long and the caller should append a truncation marker.
//
// Uses io.ReadAll over an io.LimitReader so partial reads from
// os.File.Read (which is not guaranteed to fill the buffer or read the
// full file in one call) don't mis-detect truncation. Reading
// MaxFileBytes+1 lets us distinguish "fits within cap" from "exceeds
// cap" with one extra byte of slack.
func readCapped(path string) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return "", false, err
	}
	truncated := len(data) > MaxFileBytes
	if truncated {
		data = data[:MaxFileBytes]
	}
	return string(data), truncated, nil
}

// concatenate joins file entries with the opencode-style header and a
// blank-line separator. Empty input returns "".
func concatenate(files []fileEntry) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	for i, f := range files {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("Instructions from: ")
		b.WriteString(f.Path)
		b.WriteString("\n")
		b.WriteString(strings.TrimRight(f.Content, "\n"))
	}
	return b.String()
}

// Cache is a per-(session_id, working_dir) cache of WalkUp results.
// Concurrency-safe. The cache holds Result entries; on Refresh it
// verifies that every recorded file's on-disk mtime is unchanged and
// returns the cached entry on a hit. On any mtime mismatch (or a new
// allowlisted file appearing / a tracked file disappearing) the cache
// reruns WalkUp and stores the new Result.
//
// Cache lifetime is process-lifetime. The cache is keyed on
// (session_id, working_dir) so two sessions in the same working_dir
// each pay their own walk on the first call but share no state — this
// avoids cross-session cache poisoning if (hypothetically) per-session
// filters were ever added.
type Cache struct {
	mu      sync.Mutex
	entries map[cacheKey]Result
}

type cacheKey struct {
	SessionID  string
	WorkingDir string
}

// NewCache returns a fresh Cache.
func NewCache() *Cache {
	return &Cache{entries: make(map[cacheKey]Result)}
}

// Refresh returns the workspace walk-up Result for (sessionID,
// workingDir). On the first call for that key it runs WalkUp and
// caches the result. On subsequent calls it checks every cached file's
// on-disk mtime; if any drift is detected (mtime mismatch, file
// removed, or a new allowlisted file added to a previously-walked
// directory) the walk-up reruns and the entry is refreshed. The
// returned Result reflects current on-disk state at the moment Refresh
// returns.
//
// Refresh is the canonical entry point — call this on session start AND
// post-compaction (both are points where the slot store is rebuilt from
// scratch). For chat turns mid-session, calling Refresh is also safe and
// cheap (the mtime stat-loop is bounded by the number of cached
// instruction files, typically ≤4 per directory level).
//
// Errors from WalkUp are returned without caching the failed result —
// the next Refresh will retry.
func (c *Cache) Refresh(sessionID, workingDir string) (Result, error) {
	if workingDir == "" {
		return Result{}, nil
	}
	abs, err := filepath.Abs(workingDir)
	if err != nil {
		return Result{}, err
	}

	key := cacheKey{SessionID: sessionID, WorkingDir: abs}

	c.mu.Lock()
	cached, ok := c.entries[key]
	c.mu.Unlock()

	if ok && !c.isStale(cached) {
		return cached, nil
	}

	fresh, err := WalkUp(abs)
	if err != nil {
		return Result{}, err
	}

	c.mu.Lock()
	c.entries[key] = fresh
	c.mu.Unlock()

	return fresh, nil
}

// Invalidate drops any cached entry for (sessionID, workingDir). Useful
// when the caller knows the working_dir is changing or a session is
// being torn down. No-op when no entry exists.
func (c *Cache) Invalidate(sessionID, workingDir string) {
	if workingDir == "" {
		return
	}
	abs, err := filepath.Abs(workingDir)
	if err != nil {
		return
	}
	c.mu.Lock()
	delete(c.entries, cacheKey{SessionID: sessionID, WorkingDir: abs})
	c.mu.Unlock()
}

// isStale returns true when any cached file's mtime differs from
// on-disk, OR a new allowlisted file appeared at a previously-walked
// directory level, OR a previously-tracked file disappeared.
//
// Detecting "new file appeared" requires us to re-stat every directory
// the original walk visited. We reconstruct those directories from the
// cached Files paths and the cached WorkingDir/GitRoot range. This is
// cheaper than re-running the full walk because we skip file reads
// (stat-only).
func (c *Cache) isStale(cached Result) bool {
	// Per-file mtime check. If any cached file changed or vanished, stale.
	for _, f := range cached.Files {
		info, err := os.Lstat(f.Path)
		if err != nil {
			return true
		}
		if !info.ModTime().Equal(f.ModTime) {
			return true
		}
	}

	// "New file appeared at a previously-walked directory level" check.
	// Reconstruct the walk's directory list from WorkingDir up to and
	// including GitRoot (or filesystem root if GitRoot is empty). For
	// each directory, stat every allowlisted file; if any exists that
	// wasn't in the cached Files list, we're stale.
	tracked := make(map[string]struct{}, len(cached.Files))
	for _, f := range cached.Files {
		tracked[f.Path] = struct{}{}
	}

	current := cached.WorkingDir
	for {
		for _, name := range allowlist {
			full := filepath.Join(current, name)
			if _, ok := tracked[full]; ok {
				continue
			}
			// Use the same eligibility logic readDirEntries applies,
			// so excluded-but-present files (symlinks, nested entries
			// behind a symlinked parent, directories at the allowlist
			// path) never trigger stale-on-every-call.
			if _, ok := isEligibleInstructionFile(current, name); ok {
				return true
			}
		}

		if current == cached.GitRoot {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return false
}
