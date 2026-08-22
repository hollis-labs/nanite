package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/config"
	"github.com/hollis-labs/nanite/internal/contextbroker"
	"github.com/hollis-labs/nanite/internal/store"
)

// newStasherForTest constructs an ArtifactStasher wired against a temp
// SQLite store + temp filesystem artifacts root. Returns the stasher,
// the store (for follow-up assertions), and the artifacts root path.
func newStasherForTest(t *testing.T) (contextbroker.SlotStasher, *store.Store, string) {
	t.Helper()
	tmp := t.TempDir()
	dbPath := tmp + "/stash.db"
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })

	artifactsRoot := tmp + "/artifacts"
	if err := os.MkdirAll(artifactsRoot, 0o755); err != nil {
		t.Fatalf("mkdir artifacts root: %v", err)
	}

	cfg := config.DefaultAppConfig()
	cfg.Artifacts.StorageDir = artifactsRoot

	stasher, err := NewArtifactStasher(ArtifactStasherConfig{
		Store:     s,
		AppConfig: cfg,
	})
	if err != nil {
		t.Fatalf("NewArtifactStasher: %v", err)
	}
	return stasher, s, artifactsRoot
}

// newSessionForStashTest inserts a session row so the artifact FK is
// satisfied. Returns the session ID.
func newSessionForStashTest(t *testing.T, s *store.Store, id string) {
	t.Helper()
	if err := s.CreateSession(context.Background(), &store.Session{ID: id}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
}

func TestArtifactStasher_StashAndRetrieve(t *testing.T) {
	// End-to-end: stash a slot's content, verify (a) artifact_id is
	// returned, (b) artifact row exists in the store with the right
	// origin, (c) the file on disk contains the original bytes verbatim.
	stasher, s, root := newStasherForTest(t)
	newSessionForStashTest(t, s, "sess-1")

	content := strings.Repeat("body line\n", 1000) // ~10K bytes
	res, err := stasher.StashSlot(context.Background(), contextbroker.StashRequest{
		SessionID: "sess-1",
		SlotName:  "memory",
		Content:   content,
		Tokens:    2500,
	})
	if err != nil {
		t.Fatalf("StashSlot: %v", err)
	}
	if res.ArtifactID == "" {
		t.Fatal("expected non-empty ArtifactID")
	}
	if !strings.HasPrefix(res.ArtifactID, "art-stash-") {
		t.Errorf("artifact_id should start with art-stash-, got %q", res.ArtifactID)
	}
	if res.Reused {
		t.Error("first stash should not report Reused=true")
	}

	// Row exists.
	row, err := s.GetArtifact(context.Background(), res.ArtifactID)
	if err != nil {
		t.Fatalf("GetArtifact: %v", err)
	}
	if row.Origin != SlotStashOrigin {
		t.Errorf("artifact origin = %q, want %q", row.Origin, SlotStashOrigin)
	}
	if row.SessionID != "sess-1" {
		t.Errorf("artifact session = %q, want sess-1", row.SessionID)
	}
	if row.SizeBytes != int64(len(content)) {
		t.Errorf("artifact size = %d, want %d", row.SizeBytes, len(content))
	}

	// File on disk matches.
	disk, err := os.ReadFile(row.StoragePath)
	if err != nil {
		t.Fatalf("read stash file: %v", err)
	}
	if string(disk) != content {
		t.Errorf("stash file content mismatch (got %d bytes, want %d)", len(disk), len(content))
	}

	// Confined under the artifacts root. Resolve symlinks on both
	// sides because macOS's TempDir lives under /var → /private/var.
	rootAbs, _ := filepath.Abs(root)
	if real, evalErr := filepath.EvalSymlinks(rootAbs); evalErr == nil {
		rootAbs = real
	}
	targetAbs, _ := filepath.Abs(row.StoragePath)
	if real, evalErr := filepath.EvalSymlinks(targetAbs); evalErr == nil {
		targetAbs = real
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Errorf("stash path %q not under artifacts root %q (rel=%q err=%v)", targetAbs, rootAbs, rel, err)
	}
}

func TestArtifactStasher_Idempotent(t *testing.T) {
	// Re-stashing the same (session, slot, content) tuple returns the
	// same artifact_id with Reused=true and does NOT create a duplicate
	// row. This is the cache-stability contract the pointer envelope
	// relies on across turns.
	stasher, s, _ := newStasherForTest(t)
	newSessionForStashTest(t, s, "sess-idem")

	content := strings.Repeat("stable ", 500)
	req := contextbroker.StashRequest{
		SessionID: "sess-idem",
		SlotName:  "context",
		Content:   content,
		Tokens:    500,
	}

	res1, err := stasher.StashSlot(context.Background(), req)
	if err != nil {
		t.Fatalf("first stash: %v", err)
	}
	res2, err := stasher.StashSlot(context.Background(), req)
	if err != nil {
		t.Fatalf("second stash: %v", err)
	}

	if res1.ArtifactID != res2.ArtifactID {
		t.Errorf("re-stash should return same ID; got %q then %q", res1.ArtifactID, res2.ArtifactID)
	}
	if res1.Reused {
		t.Error("first stash should not be reused")
	}
	if !res2.Reused {
		t.Error("second stash should be reused=true")
	}

	// Confirm exactly one row exists for this session.
	rows, err := s.ListArtifacts(context.Background(), "sess-idem")
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected exactly 1 artifact row after idempotent re-stash, got %d", len(rows))
	}
}

func TestArtifactStasher_DifferentContentDifferentID(t *testing.T) {
	// Content-addressed: different content → different ID. Pointer
	// envelopes for different bodies must not collide.
	stasher, s, _ := newStasherForTest(t)
	newSessionForStashTest(t, s, "sess-diff")

	resA, err := stasher.StashSlot(context.Background(), contextbroker.StashRequest{
		SessionID: "sess-diff", SlotName: "context",
		Content: strings.Repeat("A", 1000), Tokens: 250,
	})
	if err != nil {
		t.Fatalf("stash A: %v", err)
	}
	resB, err := stasher.StashSlot(context.Background(), contextbroker.StashRequest{
		SessionID: "sess-diff", SlotName: "context",
		Content: strings.Repeat("B", 1000), Tokens: 250,
	})
	if err != nil {
		t.Fatalf("stash B: %v", err)
	}
	if resA.ArtifactID == resB.ArtifactID {
		t.Errorf("different content should produce different artifact_ids; both = %q", resA.ArtifactID)
	}
}

func TestArtifactStasher_DifferentSlotsDifferentIDs(t *testing.T) {
	// Same content in different slot positions → different IDs. Keeps
	// the positional cache math intact when two slots happen to hold
	// identical bytes (rare but possible during compaction).
	stasher, s, _ := newStasherForTest(t)
	newSessionForStashTest(t, s, "sess-slot")

	content := strings.Repeat("same content ", 200)
	resA, err := stasher.StashSlot(context.Background(), contextbroker.StashRequest{
		SessionID: "sess-slot", SlotName: "memory", Content: content, Tokens: 500,
	})
	if err != nil {
		t.Fatalf("stash memory: %v", err)
	}
	resB, err := stasher.StashSlot(context.Background(), contextbroker.StashRequest{
		SessionID: "sess-slot", SlotName: "context", Content: content, Tokens: 500,
	})
	if err != nil {
		t.Fatalf("stash context: %v", err)
	}
	if resA.ArtifactID == resB.ArtifactID {
		t.Errorf("same content in different slots should produce different IDs; both = %q", resA.ArtifactID)
	}
}

func TestArtifactStasher_RejectsEmptySession(t *testing.T) {
	stasher, _, _ := newStasherForTest(t)
	_, err := stasher.StashSlot(context.Background(), contextbroker.StashRequest{
		SessionID: "", SlotName: "memory", Content: "x", Tokens: 1,
	})
	if err == nil {
		t.Error("expected error on empty SessionID")
	}
}

func TestArtifactStasher_RejectsEmptyContent(t *testing.T) {
	stasher, s, _ := newStasherForTest(t)
	newSessionForStashTest(t, s, "sess-empty")
	_, err := stasher.StashSlot(context.Background(), contextbroker.StashRequest{
		SessionID: "sess-empty", SlotName: "memory", Content: "", Tokens: 0,
	})
	if err == nil {
		t.Error("expected error on empty Content")
	}
}

func TestArtifactStasher_NilStoreRejected(t *testing.T) {
	_, err := NewArtifactStasher(ArtifactStasherConfig{Store: nil})
	if err == nil {
		t.Error("expected error when Store is nil")
	}
}

// TestArtifactStasher_DeterministicIDMatchesPackageFn confirms the
// stasher's ID-generation contract matches contextbroker.DeterministicArtifactID
// exactly — so the broker (which embeds the same function in its
// pointer-format docstring) and the stasher agree on shape.
func TestArtifactStasher_DeterministicIDMatchesPackageFn(t *testing.T) {
	stasher, s, _ := newStasherForTest(t)
	newSessionForStashTest(t, s, "sess-canon")

	content := "canonical body"
	res, err := stasher.StashSlot(context.Background(), contextbroker.StashRequest{
		SessionID: "sess-canon", SlotName: "context", Content: content, Tokens: 5,
	})
	if err != nil {
		t.Fatalf("stash: %v", err)
	}
	want := contextbroker.DeterministicArtifactID("sess-canon", "context", content)
	if res.ArtifactID != want {
		t.Errorf("stasher ID = %q, want canonical %q", res.ArtifactID, want)
	}
}

// TestArtifactStasher_AtomicityOnFSWriteFailure forces an FS write
// failure (by giving the stasher an unwritable artifacts root) and
// confirms no DB row was created — the artifact row would otherwise
// reference a non-existent file. This is the load-bearing atomicity
// guarantee for the broker's pointer-stash flow.
func TestArtifactStasher_AtomicityOnFSWriteFailure(t *testing.T) {
	tmp := t.TempDir()
	s, err := store.New(context.Background(), tmp+"/stash.db")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { s.Close(context.Background()) })
	newSessionForStashTest(t, s, "sess-fs-fail")

	// Use a path that ResolveUnder will accept but MkdirAll cannot
	// create (a regular file's name as the root).
	blockerFile := tmp + "/not-a-dir"
	if err := os.WriteFile(blockerFile, []byte("blocker"), 0o644); err != nil {
		t.Fatalf("create blocker: %v", err)
	}

	cfg := config.DefaultAppConfig()
	cfg.Artifacts.StorageDir = blockerFile

	stasher, err := NewArtifactStasher(ArtifactStasherConfig{Store: s, AppConfig: cfg})
	if err != nil {
		t.Fatalf("NewArtifactStasher: %v", err)
	}
	_, err = stasher.StashSlot(context.Background(), contextbroker.StashRequest{
		SessionID: "sess-fs-fail", SlotName: "memory", Content: "x", Tokens: 1,
	})
	if err == nil {
		t.Fatal("expected error when artifacts root is unwritable")
	}

	// No DB row should have been created for this content.
	id := contextbroker.DeterministicArtifactID("sess-fs-fail", "memory", "x")
	if row, _ := s.GetArtifact(context.Background(), id); row != nil {
		t.Errorf("DB row leaked despite FS failure: %+v", row)
	}
}

// TestArtifactStasher_RecoversFromMissingFile is the reviewer-feedback
// hardening (CW-20260512-0110 Comment 1): when the DB row exists but
// the referenced file is missing on disk (manual deletion, partial
// cleanup, half-state from a prior crash), the idempotency fast-path
// MUST re-write the content rather than silently returning Reused=true.
// Otherwise the broker would emit a pointer envelope that resolves to
// a non-existent file at dev_read time — breaking the atomicity
// contract.
func TestArtifactStasher_RecoversFromMissingFile(t *testing.T) {
	stasher, s, _ := newStasherForTest(t)
	newSessionForStashTest(t, s, "sess-resurrect")

	content := strings.Repeat("recoverable body\n", 50)
	req := contextbroker.StashRequest{
		SessionID: "sess-resurrect",
		SlotName:  "memory",
		Content:   content,
		Tokens:    250,
	}

	// First stash — happy path.
	res1, err := stasher.StashSlot(context.Background(), req)
	if err != nil {
		t.Fatalf("first stash: %v", err)
	}

	// Manually delete the on-disk file to simulate disk-loss.
	row, err := s.GetArtifact(context.Background(), res1.ArtifactID)
	if err != nil {
		t.Fatalf("GetArtifact: %v", err)
	}
	if err := os.Remove(row.StoragePath); err != nil {
		t.Fatalf("remove stash file: %v", err)
	}
	// Sanity: the file is actually gone.
	if _, err := os.Stat(row.StoragePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stash file should be missing after Remove, got err=%v", err)
	}

	// Re-stash — must detect missing file and re-write the content.
	res2, err := stasher.StashSlot(context.Background(), req)
	if err != nil {
		t.Fatalf("recovery stash: %v", err)
	}
	if res2.ArtifactID != res1.ArtifactID {
		t.Errorf("recovery stash should return same ID; got %q want %q", res2.ArtifactID, res1.ArtifactID)
	}
	if !res2.Reused {
		t.Errorf("recovery stash should report Reused=true (row already exists)")
	}

	// File should be back on disk with the original bytes.
	disk, err := os.ReadFile(row.StoragePath)
	if err != nil {
		t.Fatalf("read resurrected stash file: %v", err)
	}
	if string(disk) != content {
		t.Errorf("resurrected stash content mismatch (got %d bytes, want %d)", len(disk), len(content))
	}
}

// TestArtifactStasher_RecoveryFailsCleanlyOnStatError covers the
// reviewer-feedback edge (CW-20260512-0110 Comment 1) where Stat returns
// a non-not-exist error (e.g. permission denied). The stasher MUST
// surface this as a real error so the broker falls back to inline
// shipping rather than emitting an unverifiable pointer.
func TestArtifactStasher_RecoveryFailsCleanlyOnStatError(t *testing.T) {
	if os.Getuid() == 0 {
		// Root bypasses Unix permission bits — skip rather than yield
		// a false negative in CI containers that run as root.
		t.Skip("test requires non-root user to enforce Unix permission bits")
	}
	stasher, s, root := newStasherForTest(t)
	newSessionForStashTest(t, s, "sess-stat-err")

	content := "stat-err body"
	req := contextbroker.StashRequest{
		SessionID: "sess-stat-err",
		SlotName:  "memory",
		Content:   content,
		Tokens:    3,
	}
	if _, err := stasher.StashSlot(context.Background(), req); err != nil {
		t.Fatalf("first stash: %v", err)
	}

	// Strip execute bit from the session directory so Stat on a file
	// inside it returns EACCES rather than NotExist.
	sessionDir := filepath.Join(root, "sess-stat-err")
	if err := os.Chmod(sessionDir, 0o000); err != nil {
		t.Fatalf("chmod sessionDir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sessionDir, 0o755) })

	_, err := stasher.StashSlot(context.Background(), req)
	if err == nil {
		t.Fatal("expected error when Stat on existing stash file fails with non-not-exist error")
	}
}

// TestArtifactStasher_ConcurrentStashes_NoInlineFallback is the
// reviewer-feedback hardening (CW-20260512-0110 Comment 2): two
// concurrent goroutines stashing the same (session, slot, content)
// tuple MUST both succeed without falling back to inline shipping. The
// deterministic artifact_id means exactly one INSERT wins; the loser
// must detect the conflict, re-read the row, and return Reused=true
// rather than bubbling the constraint error up to the broker.
func TestArtifactStasher_ConcurrentStashes_NoInlineFallback(t *testing.T) {
	stasher, s, _ := newStasherForTest(t)
	newSessionForStashTest(t, s, "sess-concurrent")

	content := strings.Repeat("concurrent body\n", 200)
	req := contextbroker.StashRequest{
		SessionID: "sess-concurrent",
		SlotName:  "memory",
		Content:   content,
		Tokens:    500,
	}

	const goroutines = 8
	type outcome struct {
		res contextbroker.StashResult
		err error
	}
	results := make(chan outcome, goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func() {
			<-start
			res, err := stasher.StashSlot(context.Background(), req)
			results <- outcome{res: res, err: err}
		}()
	}
	close(start)

	var (
		ids       = map[string]bool{}
		successes int
		errs      []error
	)
	for i := 0; i < goroutines; i++ {
		o := <-results
		if o.err != nil {
			errs = append(errs, o.err)
			continue
		}
		successes++
		ids[o.res.ArtifactID] = true
	}
	if len(errs) > 0 {
		t.Errorf("expected no errors from concurrent stashes (broker would fall back to inline shipping), got %d errors: %v", len(errs), errs)
	}
	if successes != goroutines {
		t.Errorf("expected %d successful stashes, got %d", goroutines, successes)
	}
	if len(ids) != 1 {
		t.Errorf("concurrent stashes should all return the same artifact_id; got %d distinct IDs: %v", len(ids), ids)
	}
	// Exactly one DB row regardless of how many goroutines raced.
	rows, err := s.ListArtifacts(context.Background(), "sess-concurrent")
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("expected exactly 1 artifact row after concurrent stashes, got %d", len(rows))
	}
}

// TestArtifactStasher_ConcurrentStashes_WithMissingFile combines the
// Comment 1 + Comment 2 recovery paths: a row already exists but its
// FS file is missing, and a concurrent goroutine arrives during the
// resurrection window. Both callers should converge on Reused=true
// with the file restored on disk.
func TestArtifactStasher_ConcurrentStashes_WithMissingFile(t *testing.T) {
	stasher, s, _ := newStasherForTest(t)
	newSessionForStashTest(t, s, "sess-conc-miss")

	content := strings.Repeat("recovery race body\n", 80)
	req := contextbroker.StashRequest{
		SessionID: "sess-conc-miss",
		SlotName:  "memory",
		Content:   content,
		Tokens:    400,
	}

	// Seed: stash once so the row exists.
	first, err := stasher.StashSlot(context.Background(), req)
	if err != nil {
		t.Fatalf("seed stash: %v", err)
	}
	row, err := s.GetArtifact(context.Background(), first.ArtifactID)
	if err != nil {
		t.Fatalf("GetArtifact: %v", err)
	}
	// Delete the file to force every subsequent caller into the
	// disk-loss recovery branch.
	if err := os.Remove(row.StoragePath); err != nil {
		t.Fatalf("remove stash file: %v", err)
	}

	const goroutines = 6
	type outcome struct {
		res contextbroker.StashResult
		err error
	}
	results := make(chan outcome, goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func() {
			<-start
			res, err := stasher.StashSlot(context.Background(), req)
			results <- outcome{res: res, err: err}
		}()
	}
	close(start)

	for i := 0; i < goroutines; i++ {
		o := <-results
		if o.err != nil {
			t.Errorf("goroutine %d failed: %v", i, o.err)
			continue
		}
		if o.res.ArtifactID != first.ArtifactID {
			t.Errorf("goroutine %d returned id %q, want %q", i, o.res.ArtifactID, first.ArtifactID)
		}
		if !o.res.Reused {
			t.Errorf("goroutine %d should report Reused=true (row exists), got false", i)
		}
	}

	// File must be back on disk with original bytes.
	disk, err := os.ReadFile(row.StoragePath)
	if err != nil {
		t.Fatalf("read recovered stash file: %v", err)
	}
	if string(disk) != content {
		t.Errorf("recovered stash content mismatch (got %d bytes, want %d)", len(disk), len(content))
	}
}

// Sanity guard against future code refactors: ErrStashUnavailable is the
// canonical sentinel for "no stash backend wired" — the decider falls
// back to ActionShip when it sees this. Confirm the production stasher
// does NOT return this sentinel under normal conditions (it only fires
// for NopStasher).
func TestArtifactStasher_DoesNotReturnUnavailable(t *testing.T) {
	stasher, s, _ := newStasherForTest(t)
	newSessionForStashTest(t, s, "sess-not-unavailable")

	_, err := stasher.StashSlot(context.Background(), contextbroker.StashRequest{
		SessionID: "sess-not-unavailable", SlotName: "memory", Content: "x", Tokens: 1,
	})
	if errors.Is(err, contextbroker.ErrStashUnavailable) {
		t.Error("production stasher should never return ErrStashUnavailable")
	}
}
