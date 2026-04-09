package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteManagedSection_NewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")

	if err := WriteManagedSection(path, "agent content here"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read file: %v", err)
	}
	got := string(data)

	if !strings.Contains(got, managedStart) {
		t.Errorf("missing managedStart marker")
	}
	if !strings.Contains(got, managedEnd) {
		t.Errorf("missing managedEnd marker")
	}
	if !strings.Contains(got, managedNotice) {
		t.Errorf("missing managedNotice")
	}
	if !strings.Contains(got, "agent content here") {
		t.Errorf("missing content")
	}
}

func TestWriteManagedSection_PreservesExistingUserContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")

	userContent := "# My Project\n\nThis is user content.\n"
	if err := os.WriteFile(path, []byte(userContent), 0o644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if err := WriteManagedSection(path, "managed content"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read file: %v", err)
	}
	got := string(data)

	if !strings.Contains(got, userContent) {
		t.Errorf("user content was not preserved; got:\n%s", got)
	}
	if !strings.Contains(got, "managed content") {
		t.Errorf("managed content missing")
	}
	// User content should appear before managed section.
	userIdx := strings.Index(got, "# My Project")
	startIdx := strings.Index(got, managedStart)
	if userIdx > startIdx {
		t.Errorf("user content should appear before managed section")
	}
}

func TestWriteManagedSection_ReplacesExistingManagedSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")

	initial := "# Header\n\nBefore content.\n\n" +
		managedStart + "\n" +
		managedNotice + "\n\n" +
		"old managed content\n\n" +
		managedEnd + "\n\n" +
		"After content.\n"

	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if err := WriteManagedSection(path, "new managed content"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read file: %v", err)
	}
	got := string(data)

	if strings.Contains(got, "old managed content") {
		t.Errorf("old managed content should have been replaced")
	}
	if !strings.Contains(got, "new managed content") {
		t.Errorf("new managed content missing")
	}
	if !strings.Contains(got, "# Header") {
		t.Errorf("content before managed section not preserved")
	}
	if !strings.Contains(got, "After content.") {
		t.Errorf("content after managed section not preserved")
	}
}

func TestWriteManagedSection_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")

	if err := WriteManagedSection(path, "same content"); err != nil {
		t.Fatalf("first write failed: %v", err)
	}

	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read after first write: %v", err)
	}

	if err := WriteManagedSection(path, "same content"); err != nil {
		t.Fatalf("second write failed: %v", err)
	}

	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read after second write: %v", err)
	}

	if string(first) != string(second) {
		t.Errorf("writes are not idempotent\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

func TestReadManagedSection_ReadsContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")

	content := "# Header\n\n" +
		managedStart + "\n" +
		managedNotice + "\n\n" +
		"agent generated content\n\n" +
		managedEnd + "\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	got, err := ReadManagedSection(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(got, "agent generated content") {
		t.Errorf("expected content not found; got: %q", got)
	}
	if strings.Contains(got, managedStart) {
		t.Errorf("result should not contain start marker")
	}
	if strings.Contains(got, managedEnd) {
		t.Errorf("result should not contain end marker")
	}
	if strings.Contains(got, managedNotice) {
		t.Errorf("result should not contain notice line")
	}
}

func TestReadManagedSection_NoMarkers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CLAUDE.md")

	if err := os.WriteFile(path, []byte("# Just user content\n"), 0o644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	got, err := ReadManagedSection(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got != "" {
		t.Errorf("expected empty string, got: %q", got)
	}
}
