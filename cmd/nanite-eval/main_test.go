package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfflineRunWritesResults(t *testing.T) {
	out := t.TempDir()
	if err := run(context.Background(), []string{"-scenario", "code-search", "-out", out}, nil); err != nil {
		t.Fatal(err)
	}
	md, err := os.ReadFile(filepath.Join(out, "results.md")) //nolint:gosec // the test's own temp dir
	if err != nil || !strings.Contains(string(md), "| code-search | subtask | yes |") {
		t.Fatalf("results.md: %v\n%s", err, md)
	}
	var results []map[string]any
	data, err := os.ReadFile(filepath.Join(out, "results.json")) //nolint:gosec // the test's own temp dir
	if err != nil || json.Unmarshal(data, &results) != nil || len(results) != 2 {
		t.Fatalf("results.json: %v\n%s", err, data)
	}
}

func TestBadFlags(t *testing.T) {
	for _, args := range [][]string{{"-mode", "both"}, {"-scenario", "nope"}} {
		if err := run(context.Background(), args, nil); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
}
