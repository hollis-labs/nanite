package permission

// Lineage-aware grant lookup tests (CW-fix-prompt-worker-grant-lineage).
//
// Workers spawned via dispatch are different sessions from the parent
// chat that issued the explicit-mention path grant. Without lineage,
// the worker's bucket is empty and dev_* tool calls hit a permission
// miss. RegisterLineage stamps a (childSession → parentSession) pointer
// at spawn time; LookupPath walks the chain on a miss against the
// worker's own bucket.

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// TestLineage_DirectAncestorHit covers the common case: a worker session
// has an empty bucket and registered lineage to a parent that holds the
// grant. LookupPath returns ancestor_session + the parent session ID.
func TestLineage_DirectAncestorHit(t *testing.T) {
	g := NewPathGrants()
	g.RegisterFromUserMessage("parent", "see /tmp/foo.txt")
	g.RegisterLineage("worker", "parent")

	matched, kind, via := g.LookupPath("worker", "/tmp/foo.txt")
	if !matched {
		t.Fatalf("expected lineage hit; got matched=false")
	}
	if kind != LookupKindAncestorSession {
		t.Errorf("kind = %q, want %q", kind, LookupKindAncestorSession)
	}
	if via != "parent" {
		t.Errorf("via = %q, want %q", via, "parent")
	}

	// IsPathAllowed should agree with LookupPath via the same lineage walk.
	if !g.IsPathAllowed("worker", "/tmp/foo.txt") {
		t.Error("IsPathAllowed should accept the lineage hit")
	}
}

// TestLineage_NoLineage_StillRejects locks the opt-in invariant: a
// worker with no registered lineage does not see any other session's
// grants. Lineage is something dispatch wires explicitly — a stray
// session ID never picks up grants from elsewhere.
func TestLineage_NoLineage_StillRejects(t *testing.T) {
	g := NewPathGrants()
	g.RegisterFromUserMessage("parent", "see /tmp/foo.txt")
	// No RegisterLineage call.

	matched, kind, via := g.LookupPath("orphan-worker", "/tmp/foo.txt")
	if matched {
		t.Fatalf("orphan worker should not see parent's grant; got matched=true (kind=%q via=%q)", kind, via)
	}
	if kind != LookupKindNone {
		t.Errorf("kind = %q, want %q", kind, LookupKindNone)
	}
	if via != "" {
		t.Errorf("via = %q, want \"\"", via)
	}
	if g.IsPathAllowed("orphan-worker", "/tmp/foo.txt") {
		t.Error("IsPathAllowed should reject without lineage")
	}
}

// TestLineage_DepthCap regression-locks the bounded walk. Build a chain
// longer than the cap and confirm LookupPath bails cleanly + emits the
// "lineage walk capped" warn-level log instead of spinning.
func TestLineage_DepthCap(t *testing.T) {
	// Capture slog so we can assert the warn fires.
	prev := slog.Default()
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	g := NewPathGrants()
	// Build a chain n hops deep; the deepest session holds the grant.
	chainDepth := lineageMaxHops + 2
	deepest := fmt.Sprintf("s%d", chainDepth)
	g.RegisterFromUserMessage(deepest, "see /tmp/foo.txt")
	for i := 0; i < chainDepth; i++ {
		g.RegisterLineage(fmt.Sprintf("s%d", i), fmt.Sprintf("s%d", i+1))
	}

	matched, _, _ := g.LookupPath("s0", "/tmp/foo.txt")
	if matched {
		t.Fatalf("expected lookup at hop 0 to give up at the depth cap; got matched=true")
	}
	if !strings.Contains(buf.String(), "path-grant lineage walk capped") {
		t.Errorf("expected lineage-cap warn log; got: %s", buf.String())
	}

	// A lookup that starts inside the cap window (e.g. at hop chainDepth-1)
	// still resolves — depth-bound is per-call, not global.
	near := fmt.Sprintf("s%d", chainDepth-1)
	if !g.IsPathAllowed(near, "/tmp/foo.txt") {
		t.Errorf("expected lookup from %q to resolve within the cap", near)
	}
}

// TestLineage_ClearedAfterSpawn locks the cleanup invariant: once
// ClearLineage runs (spawn-finish defer), the worker session can no
// longer reach the parent's grants. Without this, the lineage map would
// leak entries indefinitely and stale workers could still resolve.
func TestLineage_ClearedAfterSpawn(t *testing.T) {
	g := NewPathGrants()
	g.RegisterFromUserMessage("parent", "see /tmp/foo.txt")
	g.RegisterLineage("worker", "parent")

	if !g.IsPathAllowed("worker", "/tmp/foo.txt") {
		t.Fatalf("setup: lineage hit should resolve before clear")
	}
	g.ClearLineage("worker")
	if g.IsPathAllowed("worker", "/tmp/foo.txt") {
		t.Errorf("after ClearLineage, worker should no longer reach parent's grant")
	}
}

// TestLineage_OwnBucketBeatsAncestor ensures that a worker that owns a
// grant for the candidate path is classified as a literal/ancestor hit
// against its own bucket — NOT as ancestor_session — so the diagnostic
// log preserves the meaningful distinction between "this session owns
// it" and "inherited via lineage."
func TestLineage_OwnBucketBeatsAncestor(t *testing.T) {
	g := NewPathGrants()
	// Parent holds /foo; worker holds /bar; worker has lineage to parent.
	g.RegisterFromUserMessage("parent", "see /foo/file.txt")
	g.RegisterFromUserMessage("worker", "see /bar/file.txt")
	g.RegisterLineage("worker", "parent")

	// Lookup of /bar/file.txt resolves on the worker's own bucket — not
	// via lineage — so via must be empty.
	matched, kind, via := g.LookupPath("worker", "/bar/file.txt")
	if !matched {
		t.Fatalf("worker should resolve own-bucket grant")
	}
	if kind != LookupKindLiteral {
		t.Errorf("kind = %q, want literal (own-bucket hit)", kind)
	}
	if via != "" {
		t.Errorf("via = %q, want \"\" (own-bucket hits do not surface via session id)", via)
	}

	// Lookup of /foo/file.txt resolves only via the parent — lineage hit.
	matched, kind, via = g.LookupPath("worker", "/foo/file.txt")
	if !matched {
		t.Fatalf("worker should resolve parent's grant via lineage")
	}
	if kind != LookupKindAncestorSession {
		t.Errorf("kind = %q, want ancestor_session", kind)
	}
	if via != "parent" {
		t.Errorf("via = %q, want \"parent\"", via)
	}
}

// TestLineage_RegisterLineage_NilSafe matches the rest of the package's
// nil-safe disposition: callers may invoke against a nil store without
// panic.
func TestLineage_RegisterLineage_NilSafe(t *testing.T) {
	var g *PathGrants
	g.RegisterLineage("child", "parent") // must not panic
	g.ClearLineage("child")              // must not panic
}

// TestLineage_RegisterLineage_EmptyIDs is a no-op guard: empty session
// IDs are silently ignored so callers don't need to pre-validate.
func TestLineage_RegisterLineage_EmptyIDs(t *testing.T) {
	g := NewPathGrants()
	g.RegisterFromUserMessage("parent", "see /tmp/foo.txt")
	g.RegisterLineage("", "parent")
	g.RegisterLineage("worker", "")

	if g.IsPathAllowed("", "/tmp/foo.txt") {
		t.Error("empty child ID should not register lineage")
	}
	if g.IsPathAllowed("worker", "/tmp/foo.txt") {
		t.Error("empty parent ID should not register lineage")
	}
}

// TestLineage_BestSessionDir_WalksAncestor (CW-20260504-0003) — a worker
// session whose own bucket is empty inherits the parent's most-specific
// existing-dir grant via the lineage walk. Mirror of LookupPath's
// lineage walk; required for dev_bash to derive a sensible default
// working_dir for the worker.
func TestLineage_BestSessionDir_WalksAncestor(t *testing.T) {
	tmp := t.TempDir() // exists on disk as a directory
	g := NewPathGrants()
	g.RegisterFromUserMessage("parent", "see "+tmp)
	g.RegisterLineage("worker", "parent")

	// Worker's own bucket is empty.
	if got := g.BucketSize("worker"); got != 0 {
		t.Fatalf("setup: expected empty worker bucket, got %d", got)
	}
	// BestSessionDir for the worker walks lineage and returns the
	// parent's tempdir grant (which exists as a directory).
	if got := g.BestSessionDir("worker"); got != tmp {
		t.Errorf("BestSessionDir(worker) = %q, want %q (via lineage)", got, tmp)
	}
	// Sanity: parent itself returns the same.
	if got := g.BestSessionDir("parent"); got != tmp {
		t.Errorf("BestSessionDir(parent) = %q, want %q (own bucket)", got, tmp)
	}
}

// TestLineage_BestSessionDir_OwnBeatsAncestor — when both worker and
// parent have grants for an existing directory, the worker's own
// most-specific grant wins (preserves the existing "longest wins"
// semantics). Lineage candidates are unioned but the sort still picks
// the longest existing-dir.
func TestLineage_BestSessionDir_OwnBeatsAncestor(t *testing.T) {
	parentDir := t.TempDir()                              // shorter path
	workerDir := t.TempDir() + "/deeper-grant-fixture"    // longer path
	if err := os.MkdirAll(workerDir, 0o755); err != nil { // ensure exists as dir
		t.Fatalf("mkdir: %v", err)
	}

	g := NewPathGrants()
	g.RegisterFromUserMessage("parent", "see "+parentDir)
	g.RegisterFromUserMessage("worker", "see "+workerDir)
	g.RegisterLineage("worker", "parent")

	if got := g.BestSessionDir("worker"); got != workerDir {
		t.Errorf("BestSessionDir(worker) = %q, want %q (own bucket, longer)", got, workerDir)
	}
}

// TestLineage_BestSessionDir_NoLineage_StaysEmpty — a worker without
// registered lineage doesn't accidentally pick up another session's
// default. Lineage is opt-in; without a stamp BestSessionDir falls
// through to an empty result.
func TestLineage_BestSessionDir_NoLineage_StaysEmpty(t *testing.T) {
	tmp := t.TempDir()
	g := NewPathGrants()
	g.RegisterFromUserMessage("parent", "see "+tmp)
	// No RegisterLineage call.

	if got := g.BestSessionDir("orphan-worker"); got != "" {
		t.Errorf("BestSessionDir(orphan-worker) = %q, want \"\" (no lineage)", got)
	}
}

// TestLineage_BestSessionDir_DepthCap — the lineage walk in
// BestSessionDir is bounded by the same depth cap as LookupPath;
// candidates beyond the cap are not considered.
func TestLineage_BestSessionDir_DepthCap(t *testing.T) {
	tmp := t.TempDir()
	g := NewPathGrants()
	chainDepth := lineageMaxHops + 2
	deepest := fmt.Sprintf("s%d", chainDepth)
	g.RegisterFromUserMessage(deepest, "see "+tmp)
	for i := 0; i < chainDepth; i++ {
		g.RegisterLineage(fmt.Sprintf("s%d", i), fmt.Sprintf("s%d", i+1))
	}

	// Lookup from s0 should hit the cap before reaching deepest.
	if got := g.BestSessionDir("s0"); got != "" {
		t.Errorf("BestSessionDir(s0) = %q, want \"\" (deepest grant beyond depth cap)", got)
	}
}

// TestLineage_BestSessionDir_ClearedAfterSpawn — once ClearLineage runs
// (spawn-finish defer), the worker's BestSessionDir no longer reaches
// the parent. Cleanup invariant identical to LookupPath's.
func TestLineage_BestSessionDir_ClearedAfterSpawn(t *testing.T) {
	tmp := t.TempDir()
	g := NewPathGrants()
	g.RegisterFromUserMessage("parent", "see "+tmp)
	g.RegisterLineage("worker", "parent")

	if got := g.BestSessionDir("worker"); got != tmp {
		t.Fatalf("setup: BestSessionDir(worker) = %q, want %q before clear", got, tmp)
	}
	g.ClearLineage("worker")
	if got := g.BestSessionDir("worker"); got != "" {
		t.Errorf("after ClearLineage, BestSessionDir(worker) = %q, want \"\"", got)
	}
}
