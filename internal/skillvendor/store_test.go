package skillvendor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStoreForTest(t *testing.T) *Store {
	t.Helper()
	root := filepath.Join(t.TempDir(), "vendor")
	s, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func samplePackage() FileMap {
	return FileMap{
		"SKILL.md":            []byte("# Sample Skill\n\ninstructions here\n"),
		"scripts/run.sh":      []byte("#!/bin/sh\necho hi\n"),
		"references/notes.md": []byte("reference material\n"),
		"assets/logo.txt":     []byte("not really a logo"),
	}
}

func TestAddress_DeterministicRegardlessOfMapOrder(t *testing.T) {
	// Build the same logical tree via two different map literals (Go map
	// iteration order is randomized at runtime) and confirm both hash to
	// the same address.
	a := FileMap{
		"SKILL.md":       []byte("body"),
		"scripts/run.sh": []byte("#!/bin/sh\n"),
	}
	b := FileMap{
		"scripts/run.sh": []byte("#!/bin/sh\n"),
		"SKILL.md":       []byte("body"),
	}
	addrA, err := Address(a)
	if err != nil {
		t.Fatalf("Address(a): %v", err)
	}
	addrB, err := Address(b)
	if err != nil {
		t.Fatalf("Address(b): %v", err)
	}
	if addrA != addrB {
		t.Errorf("expected identical addresses for identical trees; got %q and %q", addrA, addrB)
	}
	if err := ValidateAddress(addrA); err != nil {
		t.Errorf("generated address failed ValidateAddress: %v", err)
	}
}

func TestAddress_DifferentContentDifferentAddress(t *testing.T) {
	a := FileMap{"SKILL.md": []byte("version 1")}
	b := FileMap{"SKILL.md": []byte("version 2")}
	addrA, _ := Address(a)
	addrB, _ := Address(b)
	if addrA == addrB {
		t.Errorf("different content should produce different addresses; both = %q", addrA)
	}
}

func TestAddress_DifferentPathsDifferentAddress(t *testing.T) {
	// Same bytes, different relative path -> different address. A skill's
	// tree shape is part of its identity, not just its byte content.
	a := FileMap{"SKILL.md": []byte("same bytes")}
	b := FileMap{"references/SKILL.md": []byte("same bytes")}
	addrA, _ := Address(a)
	addrB, _ := Address(b)
	if addrA == addrB {
		t.Errorf("different paths should produce different addresses; both = %q", addrA)
	}
}

func TestAddress_RejectsEmptyPackage(t *testing.T) {
	_, err := Address(FileMap{})
	if !errors.Is(err, ErrEmptyPackage) {
		t.Errorf("expected ErrEmptyPackage, got %v", err)
	}
}

func TestAddress_RejectsInvalidPaths(t *testing.T) {
	cases := []FileMap{
		{"": []byte("x")},
		{"/abs/path": []byte("x")},
		{"../escape": []byte("x")},
		{"scripts/../../escape": []byte("x")},
	}
	for _, fm := range cases {
		if _, err := Address(fm); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Address(%v): expected ErrInvalidPath, got %v", fm, err)
		}
	}
}

func TestAddress_RejectsNormalizationCollision(t *testing.T) {
	fm := FileMap{
		"a/b.txt":   []byte("one"),
		"./a/b.txt": []byte("two"),
	}
	if _, err := Address(fm); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("expected ErrInvalidPath on normalization collision, got %v", err)
	}
}

func TestValidateAddress_RejectsForeignFormats(t *testing.T) {
	cases := []string{
		"",
		"skl-vendor-",
		"skl-vendor-tooshort",
		"art-stash-0123456789abcdef",  // a real stash.go-shaped ID, wrong family
		"skl-vendor-0123456789ABCDEF", // uppercase hex not produced by this pkg
	}
	for _, addr := range cases {
		if err := ValidateAddress(addr); !errors.Is(err, ErrInvalidAddress) {
			t.Errorf("ValidateAddress(%q): expected ErrInvalidAddress, got %v", addr, err)
		}
	}
}

func TestStore_WriteAndPath_RoundTrip(t *testing.T) {
	s := newStoreForTest(t)
	pkg := samplePackage()

	res, err := s.Write(context.Background(), pkg)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if res.Reused {
		t.Error("first write should not report Reused=true")
	}
	if err := ValidateAddress(res.Address); err != nil {
		t.Errorf("returned address failed validation: %v", err)
	}

	dir, err := s.Path(res.Address)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	for relPath, want := range pkg {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(relPath)))
		if err != nil {
			t.Fatalf("read %s: %v", relPath, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s: content mismatch, got %q want %q", relPath, got, want)
		}
	}

	// Confined under the store root.
	rootAbs, _ := filepath.Abs(s.Root())
	if real, evalErr := filepath.EvalSymlinks(rootAbs); evalErr == nil {
		rootAbs = real
	}
	dirAbs, _ := filepath.Abs(dir)
	if real, evalErr := filepath.EvalSymlinks(dirAbs); evalErr == nil {
		dirAbs = real
	}
	rel, err := filepath.Rel(rootAbs, dirAbs)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Errorf("vendored dir %q not under store root %q (rel=%q err=%v)", dirAbs, rootAbs, rel, err)
	}
}

func TestStore_ReadFiles_MatchesInput(t *testing.T) {
	s := newStoreForTest(t)
	pkg := samplePackage()

	res, err := s.Write(context.Background(), pkg)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := s.ReadFiles(res.Address)
	if err != nil {
		t.Fatalf("ReadFiles: %v", err)
	}
	if len(got) != len(pkg) {
		t.Fatalf("ReadFiles returned %d files, want %d", len(got), len(pkg))
	}
	for relPath, want := range pkg {
		gotContent, ok := got[relPath]
		if !ok {
			t.Errorf("ReadFiles missing %s", relPath)
			continue
		}
		if string(gotContent) != string(want) {
			t.Errorf("%s: content mismatch", relPath)
		}
	}
}

func TestStore_Write_SameTreeSameAddress_ByteForByte(t *testing.T) {
	s := newStoreForTest(t)
	pkg := samplePackage()

	res1, err := s.Write(context.Background(), pkg)
	if err != nil {
		t.Fatalf("first write: %v", err)
	}

	// Re-derive the same logical tree via a fresh map (new backing
	// storage, same paths/bytes) and write it again.
	pkg2 := FileMap{}
	for k, v := range pkg {
		cp := make([]byte, len(v))
		copy(cp, v)
		pkg2[k] = cp
	}
	res2, err := s.Write(context.Background(), pkg2)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}

	if res1.Address != res2.Address {
		t.Errorf("identical trees should produce identical addresses; got %q and %q", res1.Address, res2.Address)
	}
	if res2.Reused == false {
		t.Error("re-writing identical content should report Reused=true")
	}
}

func TestStore_Write_IsSafeNoOpOnSecondCall(t *testing.T) {
	s := newStoreForTest(t)
	pkg := samplePackage()

	res1, err := s.Write(context.Background(), pkg)
	if err != nil {
		t.Fatalf("first write: %v", err)
	}
	dir, err := s.Path(res1.Address)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	before, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}

	res2, err := s.Write(context.Background(), pkg)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if !res2.Reused {
		t.Error("second write of identical content should be Reused=true")
	}
	after, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if before.ModTime() != after.ModTime() {
		t.Error("second write should not have touched the address directory (mtime changed)")
	}
}

func TestStore_Write_ConcurrentIdenticalContent_ConvergesWithoutError(t *testing.T) {
	s := newStoreForTest(t)
	pkg := samplePackage()

	const goroutines = 8
	type outcome struct {
		res WriteResult
		err error
	}
	results := make(chan outcome, goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func() {
			<-start
			// Independent map per goroutine (fresh backing arrays) so this
			// genuinely exercises "two callers with equal-but-distinct
			// FileMaps", not the same map instance shared across
			// goroutines.
			local := FileMap{}
			for k, v := range pkg {
				cp := make([]byte, len(v))
				copy(cp, v)
				local[k] = cp
			}
			res, err := s.Write(context.Background(), local)
			results <- outcome{res: res, err: err}
		}()
	}
	close(start)

	addrs := map[string]bool{}
	var reusedCount int
	for i := 0; i < goroutines; i++ {
		o := <-results
		if o.err != nil {
			t.Errorf("goroutine %d: unexpected error: %v", i, o.err)
			continue
		}
		addrs[o.res.Address] = true
		if o.res.Reused {
			reusedCount++
		}
	}
	if len(addrs) != 1 {
		t.Errorf("expected all goroutines to converge on one address, got %d distinct: %v", len(addrs), addrs)
	}
	if reusedCount != goroutines-1 {
		t.Errorf("expected exactly %d goroutines to report Reused=true (all but the winner), got %d", goroutines-1, reusedCount)
	}

	// Exactly one address directory should exist under root (plus the
	// .staging scratch dir, which is not itself a published address).
	entries, err := os.ReadDir(s.Root())
	if err != nil {
		t.Fatalf("ReadDir root: %v", err)
	}
	var addressDirs int
	for _, e := range entries {
		if e.Name() == stagingDirName {
			continue
		}
		addressDirs++
	}
	if addressDirs != 1 {
		t.Errorf("expected exactly 1 published address directory, got %d", addressDirs)
	}
}

func TestStore_Path_ReturnsErrAddressMissing(t *testing.T) {
	s := newStoreForTest(t)
	// A well-formed address that was never written.
	fake := AddressPrefix + "0000000000000000"
	_, err := s.Path(fake)
	if !errors.Is(err, ErrAddressMissing) {
		t.Errorf("expected ErrAddressMissing, got %v", err)
	}
}

func TestStore_Path_RejectsForeignAddressFormat(t *testing.T) {
	s := newStoreForTest(t)
	_, err := s.Path("not-a-real-address")
	if !errors.Is(err, ErrInvalidAddress) {
		t.Errorf("expected ErrInvalidAddress, got %v", err)
	}
}

func TestStore_DiskLoss_RowPresentDirectoryMissing(t *testing.T) {
	// Simulates task 04's real scenario: the index row references an
	// address, but the vendored directory was deleted out-of-band (e.g.
	// operator wiped the data directory). Path/ReadFiles must surface a
	// real, typed error — never silently succeed or panic.
	s := newStoreForTest(t)
	res, err := s.Write(context.Background(), samplePackage())
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	dir, err := s.Path(res.Address)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("simulate disk loss: %v", err)
	}

	if _, err := s.Path(res.Address); !errors.Is(err, ErrAddressMissing) {
		t.Errorf("Path after disk loss: expected ErrAddressMissing, got %v", err)
	}
	if _, err := s.ReadFiles(res.Address); !errors.Is(err, ErrAddressMissing) {
		t.Errorf("ReadFiles after disk loss: expected ErrAddressMissing, got %v", err)
	}
}

func TestStore_Corruption_ContentMutatedAfterWrite_DetectedOnRead(t *testing.T) {
	// The store's own write path never mutates a written address in
	// place, but this proves the *detection* half of the immutability
	// contract: if something else (a bug, an operator, disk bit-rot)
	// mutates a vendored file after the fact, the next read must catch it
	// rather than silently serving corrupted content.
	s := newStoreForTest(t)
	res, err := s.Write(context.Background(), samplePackage())
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	dir, err := s.Path(res.Address)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("tampered content"), 0o644); err != nil {
		t.Fatalf("simulate tampering: %v", err)
	}

	if _, err := s.Path(res.Address); !errors.Is(err, ErrCorrupted) {
		t.Errorf("Path after tampering: expected ErrCorrupted, got %v", err)
	}
	if _, err := s.ReadFiles(res.Address); !errors.Is(err, ErrCorrupted) {
		t.Errorf("ReadFiles after tampering: expected ErrCorrupted, got %v", err)
	}
}

func TestStore_Corruption_DetectedOnReWrite(t *testing.T) {
	// If the on-disk content at an address has been tampered with,
	// Write's own idempotency fast-path must also detect it (rather than
	// happily reporting Reused=true over corrupted bytes) — this is the
	// "never a silent overwrite" half of the immutability contract from
	// the task's Done-means: attempting to write at an address whose
	// on-disk bytes no longer match produces a real, explicit error.
	s := newStoreForTest(t)
	pkg := samplePackage()
	res, err := s.Write(context.Background(), pkg)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	dir, err := s.Path(res.Address)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("tampered"), 0o644); err != nil {
		t.Fatalf("simulate tampering: %v", err)
	}

	if _, err := s.Write(context.Background(), pkg); !errors.Is(err, ErrCorrupted) {
		t.Errorf("re-Write over tampered address: expected ErrCorrupted, got %v", err)
	}
}

func TestStore_NeverSilentlyOverwritesDifferentContentAtSameAddress(t *testing.T) {
	// By construction, two different FileMaps always hash to different
	// addresses, so there is no API-level way to "write different content
	// at an existing address" through Write itself — Done-means' first
	// acceptable outcome (a new, different address) always holds. This
	// test pins that guarantee directly.
	s := newStoreForTest(t)
	first := samplePackage()
	res1, err := s.Write(context.Background(), first)
	if err != nil {
		t.Fatalf("first write: %v", err)
	}

	second := samplePackage()
	second["SKILL.md"] = []byte("meaningfully different body")
	res2, err := s.Write(context.Background(), second)
	if err != nil {
		t.Fatalf("second write: %v", err)
	}

	if res1.Address == res2.Address {
		t.Fatalf("different content produced the same address: %q", res1.Address)
	}

	// Both addresses remain independently readable with their own
	// original content — proves the first write's directory was never
	// touched by the second.
	dir1, err := s.Path(res1.Address)
	if err != nil {
		t.Fatalf("Path(res1): %v", err)
	}
	got1, err := os.ReadFile(filepath.Join(dir1, "SKILL.md"))
	if err != nil {
		t.Fatalf("read res1 SKILL.md: %v", err)
	}
	if string(got1) != string(first["SKILL.md"]) {
		t.Errorf("first address's content was mutated: got %q", got1)
	}
}

func TestStore_Delete_RemovesAddress(t *testing.T) {
	s := newStoreForTest(t)
	res, err := s.Write(context.Background(), samplePackage())
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := s.Delete(res.Address); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Path(res.Address); !errors.Is(err, ErrAddressMissing) {
		t.Errorf("expected ErrAddressMissing after Delete, got %v", err)
	}
}

func TestStore_Delete_NoErrorOnMissingAddress(t *testing.T) {
	s := newStoreForTest(t)
	fake := AddressPrefix + "0000000000000000"
	if err := s.Delete(fake); err != nil {
		t.Errorf("Delete on never-written address should be a no-op, got %v", err)
	}
}

func TestStore_Delete_RejectsForeignAddressFormat(t *testing.T) {
	s := newStoreForTest(t)
	if err := s.Delete("not-a-real-address"); !errors.Is(err, ErrInvalidAddress) {
		t.Errorf("expected ErrInvalidAddress, got %v", err)
	}
}

func TestNew_RejectsEmptyRoot(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Error("expected error for empty root")
	}
}

func TestNew_CreatesRootDirectory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "nested", "vendor")
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("precondition: root should not exist yet, stat err=%v", err)
	}
	if _, err := New(root); err != nil {
		t.Fatalf("New: %v", err)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		t.Errorf("expected New to create root directory, stat err=%v", err)
	}
}

func TestStore_Write_RejectsEmptyPackage(t *testing.T) {
	s := newStoreForTest(t)
	if _, err := s.Write(context.Background(), FileMap{}); !errors.Is(err, ErrEmptyPackage) {
		t.Errorf("expected ErrEmptyPackage, got %v", err)
	}
}

func TestStore_Write_RejectsContextAlreadyCanceled(t *testing.T) {
	s := newStoreForTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Write(ctx, samplePackage()); err == nil {
		t.Error("expected error for already-canceled context")
	}
}
