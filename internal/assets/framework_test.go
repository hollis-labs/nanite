package assets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	v := Version()
	if v == "" {
		t.Fatal("Version() returned empty string")
	}
	if !strings.HasPrefix(v, "2.") {
		t.Errorf("Version() = %q, want prefix 2.", v)
	}
}

func TestFile(t *testing.T) {
	data, err := File("VERSION")
	if err != nil {
		t.Fatalf("File(VERSION): %v", err)
	}
	if len(data) == 0 {
		t.Fatal("File(VERSION) returned empty bytes")
	}
	if _, err := File("does/not/exist.txt"); err == nil {
		t.Error("File(nonexistent) should have returned error")
	}
}

func TestExtractTo_EmptyTarget(t *testing.T) {
	dir := t.TempDir()
	report, err := ExtractTo(dir, ExtractOptions{})
	if err != nil {
		t.Fatalf("ExtractTo: %v", err)
	}
	if report.Created == 0 {
		t.Error("expected Created > 0")
	}
	if report.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0 (empty target)", report.Skipped)
	}

	// Spot-check: VERSION file should exist and match embedded.
	extracted, err := os.ReadFile(filepath.Join(dir, "VERSION"))
	if err != nil {
		t.Fatalf("read extracted VERSION: %v", err)
	}
	embedded, _ := File("VERSION")
	if string(extracted) != string(embedded) {
		t.Errorf("VERSION mismatch: extracted=%q embedded=%q", extracted, embedded)
	}
}

func TestExtractTo_SkipsModified(t *testing.T) {
	dir := t.TempDir()
	// First extract populates.
	if _, err := ExtractTo(dir, ExtractOptions{}); err != nil {
		t.Fatalf("first ExtractTo: %v", err)
	}
	// User modifies one file.
	modPath := filepath.Join(dir, "VERSION")
	if err := os.WriteFile(modPath, []byte("99.0.0-custom\n"), 0o644); err != nil {
		t.Fatalf("write mod: %v", err)
	}
	// Second extract should skip VERSION.
	report, err := ExtractTo(dir, ExtractOptions{})
	if err != nil {
		t.Fatalf("second ExtractTo: %v", err)
	}
	if report.Skipped == 0 {
		t.Error("expected Skipped > 0 after user modification")
	}
	// Verify the modified file is unchanged.
	after, _ := os.ReadFile(modPath)
	if string(after) != "99.0.0-custom\n" {
		t.Errorf("modified VERSION was overwritten: %q", after)
	}
}

func TestExtractTo_ForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	if _, err := ExtractTo(dir, ExtractOptions{}); err != nil {
		t.Fatal(err)
	}
	modPath := filepath.Join(dir, "VERSION")
	if err := os.WriteFile(modPath, []byte("99.0.0-custom\n"), 0o644); err != nil {
		t.Fatalf("write mod: %v", err)
	}

	if _, err := ExtractTo(dir, ExtractOptions{Force: true}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(modPath)
	embedded, _ := File("VERSION")
	if string(after) != string(embedded) {
		t.Errorf("Force=true did not overwrite: got %q want %q", after, embedded)
	}
}
