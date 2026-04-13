// Regression tests for BLG-20260412-009 — managed-section atomicity.
//
// Coverage:
//   - WriteManagedSection leaves no partial file when the rename step fails
//     (target path is a non-empty directory).
//   - RemoveManagedSection leaves no partial file when the rename step fails.
//   - Concurrent WriteManagedSection calls never observe a torn file: the
//     final bytes are always the complete output of exactly one writer
//     (atomicity; last-writer-wins consistency is acceptable).
package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// TestWriteManagedSection_NoPartialFileOnRenameFailure asserts that when the
// final rename fails (target path is a non-empty directory), the target is
// not replaced with a partial file and no stray temp file is left behind.
func TestWriteManagedSection_NoPartialFileOnRenameFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only: relies on POSIX rename-over-non-empty-dir failure")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "CLAUDE.md")

	// Make the target a non-empty directory — rename over it will fail.
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "child"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteManagedSection(target, "body"); err == nil {
		t.Fatal("expected error writing managed section over non-empty directory")
	}

	// Target must still be a directory — no partial file replaced it.
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("target missing after failed write: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("target replaced by regular file after failed write")
	}

	// No stray temp files beside the target.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "CLAUDE.md" {
			t.Errorf("stray entry after failed write: %q", e.Name())
		}
	}
}

// TestRemoveManagedSection_NoPartialFileOnWriteFailure seeds a file with a
// managed block inside a directory we then chmod read-only. The rewrite
// path must fail (temp-file creation denied) and must leave the seeded
// file untouched — no partial write.
func TestRemoveManagedSection_NoPartialFileOnWriteFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only: relies on POSIX directory permission bits")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permissions")
	}
	readOnlyDir := t.TempDir()
	target := filepath.Join(readOnlyDir, "CLAUDE.md")
	seed := "# Project\n<!-- nanite:start -->\nbody\n<!-- nanite:end -->\n"
	if err := os.WriteFile(target, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(readOnlyDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnlyDir, 0o755) })

	if _, _, err := RemoveManagedSection(target); err == nil {
		t.Fatal("expected error writing in read-only dir")
	}

	data, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("original file missing after failed write: %v", readErr)
	}
	if string(data) != seed {
		t.Fatalf("target mutated on failed write:\ngot:  %q\nwant: %q", string(data), seed)
	}

	entries, _ := os.ReadDir(readOnlyDir)
	for _, e := range entries {
		if e.Name() != "CLAUDE.md" {
			t.Errorf("stray entry after failed write: %q", e.Name())
		}
	}
}

// TestWriteManagedSection_ConcurrentWritersNoTornFile fires two goroutines
// that both call WriteManagedSection on the same path. Neither writer is
// synchronized — last-writer-wins semantically is fine, but atomicity
// guarantees the final bytes are always the complete output of exactly
// one writer (never a half-A/half-B interleave).
func TestWriteManagedSection_ConcurrentWritersNoTornFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "CLAUDE.md")

	// Seed an empty file so both writers take the "append/replace" branch.
	if err := os.WriteFile(target, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	const iterations = 40
	bodyA := strings.Repeat("A", 2048)
	bodyB := strings.Repeat("B", 2048)

	for i := 0; i < iterations; i++ {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = WriteManagedSection(target, bodyA)
		}()
		go func() {
			defer wg.Done()
			_ = WriteManagedSection(target, bodyB)
		}()
		wg.Wait()

		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("iter %d: read: %v", i, err)
		}
		got := string(data)

		// File must contain exactly one complete managed block from exactly
		// one writer. It must not contain both bodies (interleave) and must
		// not be truncated mid-block.
		if !strings.Contains(got, managedStart) || !strings.Contains(got, managedEnd) {
			t.Fatalf("iter %d: file missing managed markers; got %q", i, truncateForLog(got))
		}
		hasA := strings.Contains(got, bodyA)
		hasB := strings.Contains(got, bodyB)
		if hasA && hasB {
			t.Fatalf("iter %d: torn file — contains both writers' bodies", i)
		}
		if !hasA && !hasB {
			t.Fatalf("iter %d: file contains neither writer's body: %q", i, truncateForLog(got))
		}
		// Count of start/end markers must be exactly 1 each — no appended
		// partial block from a second writer racing past our read.
		if n := strings.Count(got, managedStart); n != 1 {
			t.Fatalf("iter %d: expected 1 managedStart, got %d", i, n)
		}
		if n := strings.Count(got, managedEnd); n != 1 {
			t.Fatalf("iter %d: expected 1 managedEnd, got %d", i, n)
		}
	}
}

func truncateForLog(s string) string {
	const max = 200
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}
