package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// helper: make a fake .git marker so WalkUp terminates at the desired root.
func mkgit(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkgit: %v", err)
	}
}

func mkfile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdirall: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writefile %s: %v", path, err)
	}
}

// TestWalkUp_SameAgentDifferentDirsDifferentContent — Acceptance test from
// the ticket: same agent in ~/Projects/A/ vs ~/Projects/B/ (each with an
// AGENTS.md) produces different workspace-slot content.
func TestWalkUp_SameAgentDifferentDirsDifferentContent(t *testing.T) {
	root := t.TempDir()

	dirA := filepath.Join(root, "A")
	dirB := filepath.Join(root, "B")
	mkgit(t, dirA)
	mkgit(t, dirB)
	mkfile(t, filepath.Join(dirA, "AGENTS.md"), "Project A rules.\n")
	mkfile(t, filepath.Join(dirB, "AGENTS.md"), "Project B rules.\n")

	resA, err := WalkUp(dirA)
	if err != nil {
		t.Fatalf("walkup A: %v", err)
	}
	resB, err := WalkUp(dirB)
	if err != nil {
		t.Fatalf("walkup B: %v", err)
	}

	if resA.Content == "" || resB.Content == "" {
		t.Fatalf("expected non-empty content for both projects; A=%q B=%q", resA.Content, resB.Content)
	}
	if resA.Content == resB.Content {
		t.Fatalf("expected different content for projects A and B; both=%q", resA.Content)
	}
	if !strings.Contains(resA.Content, "Project A rules") {
		t.Fatalf("A content missing Project A rules: %q", resA.Content)
	}
	if !strings.Contains(resB.Content, "Project B rules") {
		t.Fatalf("B content missing Project B rules: %q", resB.Content)
	}
}

// TestWalkUp_MultiLevelConcatenatedInnermostFirst — Acceptance test:
// multiple AGENTS.md files between working_dir and git root are
// concatenated with path headers, innermost first (per opencode
// convention).
func TestWalkUp_MultiLevelConcatenatedInnermostFirst(t *testing.T) {
	root := t.TempDir()
	gitRoot := filepath.Join(root, "repo")
	level1 := filepath.Join(gitRoot, "level1")
	level2 := filepath.Join(level1, "level2")

	mkgit(t, gitRoot)
	mkfile(t, filepath.Join(gitRoot, "AGENTS.md"), "ROOT-RULES")
	mkfile(t, filepath.Join(level1, "AGENTS.md"), "L1-RULES")
	mkfile(t, filepath.Join(level2, "AGENTS.md"), "L2-RULES")

	res, err := WalkUp(level2)
	if err != nil {
		t.Fatalf("walkup: %v", err)
	}

	idxL2 := strings.Index(res.Content, "L2-RULES")
	idxL1 := strings.Index(res.Content, "L1-RULES")
	idxRoot := strings.Index(res.Content, "ROOT-RULES")

	if idxL2 < 0 || idxL1 < 0 || idxRoot < 0 {
		t.Fatalf("expected all three rules sets; content=%q", res.Content)
	}
	if !(idxL2 < idxL1 && idxL1 < idxRoot) {
		t.Fatalf("expected innermost-first order (L2<L1<ROOT) but got L2=%d L1=%d ROOT=%d", idxL2, idxL1, idxRoot)
	}

	// Each block must carry the `Instructions from:` header pointing at
	// the absolute source path.
	wantHeaders := []string{
		"Instructions from: " + filepath.Join(level2, "AGENTS.md"),
		"Instructions from: " + filepath.Join(level1, "AGENTS.md"),
		"Instructions from: " + filepath.Join(gitRoot, "AGENTS.md"),
	}
	for _, h := range wantHeaders {
		if !strings.Contains(res.Content, h) {
			t.Errorf("missing header %q in content:\n%s", h, res.Content)
		}
	}

	if res.GitRoot != gitRoot {
		t.Errorf("expected GitRoot=%q got %q", gitRoot, res.GitRoot)
	}
}

// TestWalkUp_EmptyTree — Acceptance: empty working_dir tree (no
// allowlisted files between working_dir and git root) → workspace slot
// empty.
func TestWalkUp_EmptyTree(t *testing.T) {
	root := t.TempDir()
	gitRoot := filepath.Join(root, "repo")
	leaf := filepath.Join(gitRoot, "a", "b", "c")
	mkgit(t, gitRoot)
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatalf("mkdirall: %v", err)
	}

	res, err := WalkUp(leaf)
	if err != nil {
		t.Fatalf("walkup: %v", err)
	}
	if res.Content != "" {
		t.Fatalf("expected empty content, got %q", res.Content)
	}
	if len(res.Files) != 0 {
		t.Fatalf("expected zero files, got %d", len(res.Files))
	}
	if res.GitRoot != gitRoot {
		t.Errorf("expected GitRoot=%q got %q", gitRoot, res.GitRoot)
	}
}

// TestWalkUp_DoesNotRecurseIntoSubdirs — Acceptance: an AGENTS.md in a
// SIBLING directory (not on the walk path) should NOT appear in the
// slot.
func TestWalkUp_DoesNotRecurseIntoSubdirs(t *testing.T) {
	root := t.TempDir()
	gitRoot := filepath.Join(root, "repo")
	walkDir := filepath.Join(gitRoot, "walk")
	sibling := filepath.Join(gitRoot, "sibling")

	mkgit(t, gitRoot)
	mkfile(t, filepath.Join(walkDir, "AGENTS.md"), "WALK-RULES")
	mkfile(t, filepath.Join(sibling, "AGENTS.md"), "SIBLING-RULES-NOT-INCLUDED")
	// Add a deep grandchild of walkDir with its own AGENTS.md — also
	// should not appear (we walk UP, not down).
	mkfile(t, filepath.Join(walkDir, "grandchild", "AGENTS.md"), "GRANDCHILD-NOT-INCLUDED")

	res, err := WalkUp(walkDir)
	if err != nil {
		t.Fatalf("walkup: %v", err)
	}
	if !strings.Contains(res.Content, "WALK-RULES") {
		t.Errorf("missing WALK-RULES: %q", res.Content)
	}
	if strings.Contains(res.Content, "SIBLING-RULES-NOT-INCLUDED") {
		t.Errorf("sibling AGENTS.md leaked in: %q", res.Content)
	}
	if strings.Contains(res.Content, "GRANDCHILD-NOT-INCLUDED") {
		t.Errorf("grandchild AGENTS.md leaked in: %q", res.Content)
	}
}

// TestWalkUp_AllowlistPriorityOrderWithinDir — within one directory,
// allowlisted files are concatenated in the documented priority order
// (AGENTS.md → CLAUDE.md → NANITE.md → .nanite/rules.md). All present
// files are included; none shadow each other.
func TestWalkUp_AllowlistPriorityOrderWithinDir(t *testing.T) {
	root := t.TempDir()
	mkgit(t, root)
	mkfile(t, filepath.Join(root, "AGENTS.md"), "FILE-AGENTS")
	mkfile(t, filepath.Join(root, "CLAUDE.md"), "FILE-CLAUDE")
	mkfile(t, filepath.Join(root, "NANITE.md"), "FILE-NANITE")
	mkfile(t, filepath.Join(root, ".nanite", "rules.md"), "FILE-RULES")

	res, err := WalkUp(root)
	if err != nil {
		t.Fatalf("walkup: %v", err)
	}

	idxAgents := strings.Index(res.Content, "FILE-AGENTS")
	idxClaude := strings.Index(res.Content, "FILE-CLAUDE")
	idxNanite := strings.Index(res.Content, "FILE-NANITE")
	idxRules := strings.Index(res.Content, "FILE-RULES")

	if idxAgents < 0 || idxClaude < 0 || idxNanite < 0 || idxRules < 0 {
		t.Fatalf("missing one of the allowlisted files: AGENTS=%d CLAUDE=%d NANITE=%d RULES=%d content=%q",
			idxAgents, idxClaude, idxNanite, idxRules, res.Content)
	}
	if !(idxAgents < idxClaude && idxClaude < idxNanite && idxNanite < idxRules) {
		t.Errorf("allowlist order wrong: AGENTS=%d CLAUDE=%d NANITE=%d RULES=%d",
			idxAgents, idxClaude, idxNanite, idxRules)
	}
}

// TestWalkUp_SkipsSymlinks — symlinked instruction files at any level
// are skipped silently (security).
func TestWalkUp_SkipsSymlinks(t *testing.T) {
	root := t.TempDir()
	mkgit(t, root)
	// Real file outside the workspace.
	realFile := filepath.Join(t.TempDir(), "secret-AGENTS.md")
	if err := os.WriteFile(realFile, []byte("SECRET-DO-NOT-LEAK"), 0o644); err != nil {
		t.Fatalf("write real: %v", err)
	}
	// Symlink at the workspace level pointing at the real file.
	linkPath := filepath.Join(root, "AGENTS.md")
	if err := os.Symlink(realFile, linkPath); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	res, err := WalkUp(root)
	if err != nil {
		t.Fatalf("walkup: %v", err)
	}
	if strings.Contains(res.Content, "SECRET-DO-NOT-LEAK") {
		t.Errorf("symlinked file content leaked: %q", res.Content)
	}
}

// TestWalkUp_StopsAtFilesystemRootWithoutGit — when no .git is found
// between working_dir and the filesystem root, the walk terminates at
// the filesystem root naturally (no error). We can't easily simulate /
// in a unit test, but we can verify the walk reaches up several levels
// past the temp root without crashing.
func TestWalkUp_StopsAtFilesystemRootWithoutGit(t *testing.T) {
	root := t.TempDir()
	leaf := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatalf("mkdirall: %v", err)
	}
	mkfile(t, filepath.Join(leaf, "AGENTS.md"), "LEAF-RULES")
	// No .git anywhere — walk will climb past tempdir to /.
	res, err := WalkUp(leaf)
	if err != nil {
		t.Fatalf("walkup: %v", err)
	}
	if !strings.Contains(res.Content, "LEAF-RULES") {
		t.Errorf("missing leaf rules: %q", res.Content)
	}
	if res.GitRoot != "" {
		t.Errorf("expected empty GitRoot when no .git found, got %q", res.GitRoot)
	}
}

// TestWalkUp_NonExistentWorkingDir — stale working_dir returns empty
// without error.
func TestWalkUp_NonExistentWorkingDir(t *testing.T) {
	res, err := WalkUp("/this/path/should/not/exist/anywhere/" + t.Name())
	if err != nil {
		t.Fatalf("expected nil err for non-existent dir, got %v", err)
	}
	if res.Content != "" {
		t.Errorf("expected empty content, got %q", res.Content)
	}
}

// TestWalkUp_EmptyWorkingDir — empty string returns empty Result.
func TestWalkUp_EmptyWorkingDir(t *testing.T) {
	res, err := WalkUp("")
	if err != nil {
		t.Fatalf("walkup: %v", err)
	}
	if res.Content != "" || res.WorkingDir != "" {
		t.Errorf("expected zero Result, got %+v", res)
	}
}

// TestWalkUp_TruncatesOversizedFiles — files over MaxFileBytes are
// truncated with a marker, not refused.
func TestWalkUp_TruncatesOversizedFiles(t *testing.T) {
	root := t.TempDir()
	mkgit(t, root)
	// Make an oversized AGENTS.md (MaxFileBytes + 100 bytes).
	big := strings.Repeat("a", MaxFileBytes+100)
	mkfile(t, filepath.Join(root, "AGENTS.md"), big)

	res, err := WalkUp(root)
	if err != nil {
		t.Fatalf("walkup: %v", err)
	}
	if !strings.Contains(res.Content, "content truncated at") {
		t.Errorf("expected truncation marker, got: %q", res.Content[:200])
	}
}

// TestCache_RefreshHitsCacheOnSecondCall — caching path: same
// (session_id, working_dir) returns the cached Result without
// re-walking. Verify by mutating a file AFTER the first read but
// BEFORE the second; if the cache hit, the second call should return
// the OLD content. Then verify mtime invalidation by touching the file
// (changing mtime), at which point the cache must refresh.
func TestCache_RefreshHitsCacheOnSecondCall(t *testing.T) {
	root := t.TempDir()
	mkgit(t, root)
	path := filepath.Join(root, "AGENTS.md")
	mkfile(t, path, "V1")

	cache := NewCache()

	first, err := cache.Refresh("sess-1", root)
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if !strings.Contains(first.Content, "V1") {
		t.Fatalf("first content missing V1: %q", first.Content)
	}

	// Overwrite the file content but ALSO restore its mtime so the
	// cache's mtime check passes. This isolates the "cache hit"
	// behavior from the invalidation behavior tested next.
	origInfo, _ := os.Stat(path)
	if err := os.WriteFile(path, []byte("V2-but-mtime-restored"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := os.Chtimes(path, origInfo.ModTime(), origInfo.ModTime()); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	second, err := cache.Refresh("sess-1", root)
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if !strings.Contains(second.Content, "V1") {
		t.Errorf("expected cached V1 content on second call (mtime unchanged), got %q", second.Content)
	}
	if strings.Contains(second.Content, "V2-but-mtime-restored") {
		t.Errorf("cache did not hit; rewrote content leaked through: %q", second.Content)
	}
}

// TestCache_PostCompactionRefreshAfterMtimeChange — Acceptance test:
// post-compaction refreshes the slot when an AGENTS.md mtime changes.
// "Post-compaction" in this layer is just "Refresh called again"; the
// cache must observe the mtime drift and reread.
func TestCache_PostCompactionRefreshAfterMtimeChange(t *testing.T) {
	root := t.TempDir()
	mkgit(t, root)
	path := filepath.Join(root, "AGENTS.md")
	mkfile(t, path, "V1")

	cache := NewCache()
	first, err := cache.Refresh("sess-1", root)
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if !strings.Contains(first.Content, "V1") {
		t.Fatalf("first V1 missing: %q", first.Content)
	}

	// Rewrite + bump mtime forward.
	if err := os.WriteFile(path, []byte("V2"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	newer := time.Now().Add(10 * time.Second)
	if err := os.Chtimes(path, newer, newer); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	second, err := cache.Refresh("sess-1", root)
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if !strings.Contains(second.Content, "V2") {
		t.Errorf("expected refreshed V2 content after mtime change, got %q", second.Content)
	}
	if strings.Contains(second.Content, "V1") {
		t.Errorf("V1 leaked through stale cache: %q", second.Content)
	}
}

// TestCache_InvalidationOnNewFileAppearance — a new allowlisted file
// appearing at a previously-walked directory level should invalidate
// the cache.
func TestCache_InvalidationOnNewFileAppearance(t *testing.T) {
	root := t.TempDir()
	mkgit(t, root)
	mkfile(t, filepath.Join(root, "AGENTS.md"), "ONLY-V1")

	cache := NewCache()
	first, err := cache.Refresh("sess-1", root)
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if strings.Contains(first.Content, "CLAUDE") {
		t.Fatalf("unexpected CLAUDE content in V1: %q", first.Content)
	}

	// Add a NEW allowlisted file to the same directory.
	mkfile(t, filepath.Join(root, "CLAUDE.md"), "CLAUDE-RULES")

	second, err := cache.Refresh("sess-1", root)
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if !strings.Contains(second.Content, "CLAUDE-RULES") {
		t.Errorf("new file did not refresh cache: %q", second.Content)
	}
}

// TestCache_InvalidationOnFileRemoval — a previously-tracked file
// vanishing should invalidate the cache.
func TestCache_InvalidationOnFileRemoval(t *testing.T) {
	root := t.TempDir()
	mkgit(t, root)
	path := filepath.Join(root, "AGENTS.md")
	mkfile(t, path, "RULES")

	cache := NewCache()
	first, err := cache.Refresh("sess-1", root)
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if !strings.Contains(first.Content, "RULES") {
		t.Fatalf("first missing RULES: %q", first.Content)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}

	second, err := cache.Refresh("sess-1", root)
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if second.Content != "" {
		t.Errorf("expected empty content after file removal, got %q", second.Content)
	}
}

// TestCache_SeparateSessionsIndependentEntries — two sessions sharing
// the same working_dir each get their own cached entry. This avoids
// any future per-session filter from leaking across sessions.
func TestCache_SeparateSessionsIndependentEntries(t *testing.T) {
	root := t.TempDir()
	mkgit(t, root)
	mkfile(t, filepath.Join(root, "AGENTS.md"), "RULES")

	cache := NewCache()
	if _, err := cache.Refresh("sess-A", root); err != nil {
		t.Fatalf("sess-A: %v", err)
	}
	if _, err := cache.Refresh("sess-B", root); err != nil {
		t.Fatalf("sess-B: %v", err)
	}

	cache.mu.Lock()
	got := len(cache.entries)
	cache.mu.Unlock()
	if got != 2 {
		t.Errorf("expected 2 cache entries (one per session), got %d", got)
	}
}

// TestCache_Invalidate — explicit invalidation drops the cached entry.
func TestCache_Invalidate(t *testing.T) {
	root := t.TempDir()
	mkgit(t, root)
	mkfile(t, filepath.Join(root, "AGENTS.md"), "RULES")

	cache := NewCache()
	if _, err := cache.Refresh("sess-1", root); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	cache.Invalidate("sess-1", root)
	cache.mu.Lock()
	got := len(cache.entries)
	cache.mu.Unlock()
	if got != 0 {
		t.Errorf("expected cache empty after Invalidate, got %d entries", got)
	}
}
