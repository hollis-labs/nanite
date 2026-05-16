package bootprofile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubstitute_HappyPath(t *testing.T) {
	got, err := Substitute("hello {{name}} on {{project}}!", Vars{"name": "world", "project": "nanite"})
	if err != nil {
		t.Fatalf("Substitute: %v", err)
	}
	if want := "hello world on nanite!"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSubstitute_NoTokens(t *testing.T) {
	got, err := Substitute("plain text no braces", nil)
	if err != nil {
		t.Fatalf("Substitute: %v", err)
	}
	if got != "plain text no braces" {
		t.Fatalf("unexpected mutation: %q", got)
	}
}

// TestSubstitute_UnknownVariableErrors pins the design decision that
// unknown variables produce an error (rather than silently leaving the
// `{{var}}` literal in the output). See slots.go for the rationale.
func TestSubstitute_UnknownVariableErrors(t *testing.T) {
	_, err := Substitute("hi {{unknown}}", Vars{"known": "x"})
	if err == nil {
		t.Fatal("expected error for unknown variable")
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("error %q should mention the missing variable name", err)
	}
}

func TestSubstitute_DottedKeys(t *testing.T) {
	got, err := Substitute("alias={{identity.lineage_alias}}", Vars{"identity.lineage_alias": "a.b.c"})
	if err != nil {
		t.Fatalf("Substitute: %v", err)
	}
	if got != "alias=a.b.c" {
		t.Fatalf("got %q", got)
	}
}

func TestSubstitute_MultipleMissingAreReported(t *testing.T) {
	_, err := Substitute("{{a}} {{b}} {{c}}", Vars{"b": "B"})
	if err == nil {
		t.Fatal("expected error")
	}
	// dedupe + alphabetical order is part of the contract
	if !strings.Contains(err.Error(), "a, c") {
		t.Fatalf("error %q should list missing vars in sorted order", err)
	}
}

func TestResolveSlot_TextWithVars(t *testing.T) {
	src := SlotSource{Type: "text", Content: "role={{role}}"}
	got, req, err := resolveSlot("agent", src, "", Vars{"role": "backend"})
	if err != nil {
		t.Fatalf("resolveSlot: %v", err)
	}
	if req != nil {
		t.Fatalf("text slot should not produce a requirement, got %+v", req)
	}
	if got != "role=backend" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveSlot_StaticFile(t *testing.T) {
	root := t.TempDir()
	body := "static body {{role}}"
	if err := os.WriteFile(filepath.Join(root, "f.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	src := SlotSource{Type: "static", Path: "f.md"}
	got, _, err := resolveSlot("agent", src, root, Vars{"role": "backend"})
	if err != nil {
		t.Fatalf("resolveSlot: %v", err)
	}
	if !strings.Contains(got, "static body backend") {
		t.Fatalf("expected template-substituted body, got %q", got)
	}
}

func TestResolveSlot_StaticDirGlob(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "things")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.md"), []byte("B"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("A"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignore.txt"), []byte("X"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := SlotSource{Type: "static", Path: "things"}
	got, _, err := resolveSlot("skills", src, root, nil)
	if err != nil {
		t.Fatalf("resolveSlot: %v", err)
	}
	// sorted: a.md before b.md
	aIdx := strings.Index(got, "### a.md")
	bIdx := strings.Index(got, "### b.md")
	if aIdx < 0 || bIdx < 0 {
		t.Fatalf("expected both files in output, got %q", got)
	}
	if aIdx > bIdx {
		t.Fatalf("expected a.md to appear before b.md, got %q", got)
	}
	if strings.Contains(got, "ignore.txt") {
		t.Fatalf("non-glob file leaked into output: %q", got)
	}
}

func TestResolveSlot_DeferredTypesProduceRequirements(t *testing.T) {
	cases := []struct {
		name   string
		src    SlotSource
		expect Requirement
	}{
		{
			name:   "cmd",
			src:    SlotSource{Type: "cmd", Run: "echo hi", Timeout: "5s"},
			expect: Requirement{Slot: "recap", Type: "cmd", Run: "echo hi", Timeout: "5s"},
		},
		{
			name:   "http",
			src:    SlotSource{Type: "http", URL: "https://x.example", ResponseFormat: "vanta_recall"},
			expect: Requirement{Slot: "memory", Type: "http", URL: "https://x.example", ResponseFormat: "vanta_recall"},
		},
		{
			name:   "role_summary",
			src:    SlotSource{Type: "role_summary", Path: "~/.nanite/roles/x.md"},
			expect: Requirement{Slot: "agent", Type: "role_summary", Path: "~/.nanite/roles/x.md"},
		},
		{
			name:   "skill_index",
			src:    SlotSource{Type: "skill_index", Limit: 8},
			expect: Requirement{Slot: "skills", Type: "skill_index", Limit: 8},
		},
	}
	slotNameFor := map[string]string{
		"cmd":          "recap",
		"http":         "memory",
		"role_summary": "agent",
		"skill_index":  "skills",
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, req, err := resolveSlot(slotNameFor[tc.name], tc.src, "", nil)
			if err != nil {
				t.Fatalf("resolveSlot: %v", err)
			}
			if got != "" {
				t.Fatalf("deferred slot should not produce content, got %q", got)
			}
			if req == nil {
				t.Fatal("expected a Requirement")
			}
			if *req != tc.expect {
				t.Fatalf("got %+v, want %+v", *req, tc.expect)
			}
		})
	}
}

func TestResolveSlot_UnknownType(t *testing.T) {
	_, _, err := resolveSlot("x", SlotSource{Type: "bogus"}, "", nil)
	if err == nil {
		t.Fatal("expected error for unknown slot type")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("error %q should mention the bad type name", err)
	}
}

func TestResolveSlot_MissingType(t *testing.T) {
	_, _, err := resolveSlot("x", SlotSource{}, "", nil)
	if err == nil {
		t.Fatal("expected error for missing type")
	}
	if !strings.Contains(err.Error(), "missing 'type' field") {
		t.Fatalf("error %q should mention missing type", err)
	}
}

// TestResolvePath_RelativeWithEmptyRootErrors pins the PR #169 round 1
// fix: a relative slot path with no catalogRoot must return an
// explicit error rather than silently joining onto the process cwd
// (which would make compile non-reproducible).
func TestResolvePath_RelativeWithEmptyRootErrors(t *testing.T) {
	_, err := resolvePath("relative/path.md", "")
	if err == nil {
		t.Fatal("expected error for relative path with empty catalog root")
	}
	if !strings.Contains(err.Error(), "catalog root unset") {
		t.Fatalf("error %q should mention catalog root unset", err)
	}
}

// TestResolvePath_AbsoluteWithEmptyRootOK pins that an absolute path
// works regardless of catalogRoot — the empty-root rejection above
// applies only to relative paths.
func TestResolvePath_AbsoluteWithEmptyRootOK(t *testing.T) {
	got, err := resolvePath("/tmp/abs.md", "")
	if err != nil {
		t.Fatalf("absolute path with empty root unexpectedly failed: %v", err)
	}
	if got != "/tmp/abs.md" {
		t.Fatalf("absolute path got mangled: %q", got)
	}
}

// TestResolveStatic_RelativeWithEmptyRootErrors checks the error
// propagates through resolveStatic. Before PR #169 round 1 this
// case silently rebased onto cwd.
func TestResolveStatic_RelativeWithEmptyRootErrors(t *testing.T) {
	_, err := resolveStatic("rules", SlotSource{Type: "static", Path: "rules.md"}, "")
	if err == nil {
		t.Fatal("expected error for static slot with empty catalog root")
	}
	if !strings.Contains(err.Error(), "catalog root unset") {
		t.Fatalf("error %q should mention catalog root unset", err)
	}
}
