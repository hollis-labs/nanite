package contextbroker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPCCSource_Fetch(t *testing.T) {
	// Create temp PCC directory structure.
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "mentat")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write sample PCC files.
	files := map[string]string{
		"00_project.md":      "# Mentat\nMentat is the meta-agent.",
		"01_conventions.md":  "# Conventions\nUse Go conventions.",
		"02_architecture.md": "# Architecture\nGo backend + React frontend.",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(projectDir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	src := NewPCCSource(tmpDir)
	items, err := src.Fetch(context.Background(), Intent{
		Type:  IntentWriteCode,
		Scope: "mentat",
	}, 10000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(items) != 3 {
		t.Errorf("expected 3 items, got %d", len(items))
	}

	// write_code should rank conventions highest.
	if len(items) > 0 && items[0].Relevance < 0.8 {
		t.Errorf("expected first item to have high relevance for write_code, got %.2f", items[0].Relevance)
	}
}

func TestPCCSource_NoScope(t *testing.T) {
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "mentat")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "00_project.md"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	src := NewPCCSource(tmpDir)
	items, err := src.Fetch(context.Background(), Intent{Type: IntentCustom}, 10000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// With no scope, should auto-detect the first project dir.
	if len(items) != 1 {
		t.Errorf("expected 1 item from auto-detected project, got %d", len(items))
	}
}

func TestPCCSource_BudgetRespected(t *testing.T) {
	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "mentat")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write a large file that exceeds budget.
	bigContent := make([]byte, 2000) // ~500 tokens
	for i := range bigContent {
		bigContent[i] = 'x'
	}
	if err := os.WriteFile(filepath.Join(projectDir, "00_project.md"), bigContent, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "01_conventions.md"), []byte("small"), 0644); err != nil {
		t.Fatal(err)
	}

	src := NewPCCSource(tmpDir)
	items, err := src.Fetch(context.Background(), Intent{
		Type:  IntentCustom,
		Scope: "mentat",
	}, 10) // Very tight budget — only 10 tokens
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only the small file should fit in the budget.
	if len(items) > 1 {
		t.Errorf("expected at most 1 item with tight budget, got %d", len(items))
	}
}
