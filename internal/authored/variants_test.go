package authored

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseVariant(t *testing.T) {
	v, err := ParseVariant("lead", []byte("---\ndescription: Runs a council.\ntools:\n  allow: [read_file, \"mcp__*\"]\n  deny: [mcp__danger]\nprovider: small\nmodel: m\nbudgets:\n  steps: 12\n  tokens: 50000\n  wall: 5m\n  cost: 0.5\nlast_n: 4\nresult_cap: 3000\ncan_spawn: true\n---\n  You lead.\n"))
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "lead" || v.Description != "Runs a council." || v.Prompt != "You lead." || v.Provider != "small" || v.Model != "m" ||
		v.Budgets != (Budgets{Steps: 12, Tokens: 50000, Wall: 5 * time.Minute, Cost: 0.5}) || v.LastN != 4 || v.ResultCap != 3000 || !v.CanSpawn ||
		strings.Join(v.Allow, ",") != "read_file,mcp__*" || strings.Join(v.Deny, ",") != "mcp__danger" {
		t.Fatalf("%+v", v)
	}
	if v, err := ParseVariant("plain", []byte("Just a body.")); err != nil || v.Prompt != "Just a body." {
		t.Fatalf("no frontmatter: %+v %v", v, err)
	}
	for _, tc := range []struct{ name, body, want string }{
		{"Bad", "Body", "invalid variant name"},
		{"chat", "Body", "invalid variant name"},
		{"worker", "Body", "built-in variant"},
		{"typo", "---\ncan_spawm: true\n---\nBody", "can_spawm"},
		{"empty", "---\nmodel: x\n---\n\n", "body"},
		{"open", "---\nmodel: x\nBody", "unterminated"},
		{"wall", "---\nbudgets:\n  wall: soon\n---\nBody", "budgets.wall"},
		{"neg", "---\nresult_cap: -1\n---\nBody", "result_cap must not be negative"},
		{"many", "---\nlast_n: 51\n---\nBody", "at most 50"},
		{"glob", "---\ntools:\n  deny: [\"[\"]\n---\nBody", "invalid tool pattern"},
		{"lookout", "---\nread_only: true\ncan_spawn: true\n---\nBody", "cannot be combined"},
		{"nomodel", "---\nprovider: small\n---\nBody", "needs a model"},
	} {
		if _, err := ParseVariant(tc.name, []byte(tc.body)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %v, want %q", tc.name, err, tc.want)
		}
	}
}

func TestLoadVariants(t *testing.T) {
	dir := t.TempDir()
	if v, err := LoadVariants(dir); err != nil || len(v) != 0 {
		t.Fatalf("no variants directory: %v %v", v, err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "variants"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"good.md": "Body", "bad.md": "---\nlast_n: -1\n---\nBody", "notes.txt": "ignored"} {
		if err := os.WriteFile(filepath.Join(dir, "variants", name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	v, err := LoadVariants(dir)
	if err == nil || !strings.Contains(err.Error(), "variants/bad.md") {
		t.Fatalf("bad variant: %v", err)
	}
	if len(v) != 1 || v["good"].Prompt != "Body" {
		t.Fatalf("variants %+v", v)
	}
}

// TestLoadVariantsStaysInTheAgentDirectory: a variant symlinked to a file
// outside the agent directory is an error, not read.
func TestLoadVariantsStaysInTheAgentDirectory(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "variants"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside, "secret.md")
	if err := os.WriteFile(target, []byte("Outside body"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "variants", "escape.md")); err != nil {
		t.Fatal(err)
	}
	v, err := LoadVariants(dir)
	if err == nil || !strings.Contains(err.Error(), "variants/escape.md") || len(v) != 0 {
		t.Fatalf("symlink out of the agent directory: %v %+v", err, v)
	}
}
