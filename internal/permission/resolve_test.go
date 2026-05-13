package permission

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestResolve_globPatternPerSession is the acceptance criterion from
// CW-20260512-0120: the same profile (same RuleSet) resolves to
// session-specific absolute paths depending on the working_dir.
func TestResolve_globPatternPerSession(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()

	profile := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "./**", Behavior: DecisionAllow},
		},
	}

	resolvedA, err := profile.Resolve(dirA)
	if err != nil {
		t.Fatalf("resolve for session A: %v", err)
	}
	resolvedB, err := profile.Resolve(dirB)
	if err != nil {
		t.Fatalf("resolve for session B: %v", err)
	}

	canonicalA, _ := canonicalizeWorkingDir(dirA)
	canonicalB, _ := canonicalizeWorkingDir(dirB)

	wantA := filepath.Join(canonicalA, "**")
	wantB := filepath.Join(canonicalB, "**")

	if resolvedA.Rules[0].Pattern != wantA {
		t.Errorf("session A: got %q, want %q", resolvedA.Rules[0].Pattern, wantA)
	}
	if resolvedB.Rules[0].Pattern != wantB {
		t.Errorf("session B: got %q, want %q", resolvedB.Rules[0].Pattern, wantB)
	}
	if resolvedA.Rules[0].Pattern == resolvedB.Rules[0].Pattern {
		t.Errorf("same profile resolved to identical paths across sessions: %q", resolvedA.Rules[0].Pattern)
	}

	// Profile itself MUST NOT have been mutated — Resolve is a pure function.
	if profile.Rules[0].Pattern != "./**" {
		t.Errorf("Resolve mutated input profile: pattern is now %q", profile.Rules[0].Pattern)
	}
}

// TestResolve_subdirGlob covers the `./generated/**` case from the ticket
// acceptance: nested workspace-relative globs resolve correctly.
func TestResolve_subdirGlob(t *testing.T) {
	dir := t.TempDir()

	rs := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_write", Pattern: "./generated/**", Behavior: DecisionAllow},
		},
	}

	resolved, err := rs.Resolve(dir)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	canonical, _ := canonicalizeWorkingDir(dir)
	want := filepath.Join(canonical, "generated") + string(filepath.Separator) + "**"
	if resolved.Rules[0].Pattern != want {
		t.Errorf("got %q, want %q", resolved.Rules[0].Pattern, want)
	}
}

// TestResolve_absolutePatternsUntouched is the ticket's "Absolute patterns
// untouched" acceptance criterion: any pattern that doesn't start with `./`
// passes through verbatim.
func TestResolve_absolutePatternsUntouched(t *testing.T) {
	dir := t.TempDir()

	rs := &RuleSet{
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "/Users/someone/elsewhere/**", Behavior: DecisionAllow},
			{Tool: "dev_edit", Pattern: "/srv/data/file.txt", Behavior: DecisionAllow},
			// Shell command substring — not a path pattern at all; must pass through.
			{Tool: "shell", Pattern: "rm -rf", Behavior: DecisionDeny},
			// Bare tool-name glob with empty Pattern; nothing to resolve.
			{Tool: "memory_*", Pattern: "", Behavior: DecisionAllow},
		},
	}

	resolved, err := rs.Resolve(dir)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(resolved.Rules) != 4 {
		t.Fatalf("expected 4 rules, got %d", len(resolved.Rules))
	}
	for i := range rs.Rules {
		if resolved.Rules[i].Pattern != rs.Rules[i].Pattern {
			t.Errorf("rule[%d] pattern was rewritten: %q -> %q", i, rs.Rules[i].Pattern, resolved.Rules[i].Pattern)
		}
	}
}

// TestResolve_traversalRejected covers the "reject `./..` and any escape
// shape at resolution time" sharp edge from the ticket.
func TestResolve_traversalRejected(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name    string
		pattern string
	}{
		{"dotdot", "./.."},
		{"dotdot_slash", "./../"},
		{"dotdot_sibling", "./../sibling/"},
		{"dotdot_sibling_glob", "./../sibling/**"},
		{"deep_traversal", "./a/../../escape/**"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rs := &RuleSet{Rules: []Rule{
				{Tool: "dev_read", Pattern: tc.pattern, Behavior: DecisionAllow},
			}}
			_, err := rs.Resolve(dir)
			if err == nil {
				t.Fatalf("expected ErrPatternEscapesWorkingDir for pattern %q, got nil", tc.pattern)
			}
			if !errors.Is(err, ErrPatternEscapesWorkingDir) {
				t.Errorf("pattern %q: error %v is not ErrPatternEscapesWorkingDir", tc.pattern, err)
			}
		})
	}
}

// TestResolve_symlinkCanonicalization covers the "resolve symlinks to
// canonical paths" sharp edge. A working_dir reached via a symlink must
// resolve through to the underlying real directory — otherwise a
// symlinked working_dir could grant access to anywhere on disk via
// `./**`.
func TestResolve_symlinkCanonicalization(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require admin on windows")
	}

	realDir := t.TempDir()
	// macOS prepends /private/ via symlink for /var and /tmp. Resolve the
	// real dir up-front so the assertion compares against the actual
	// canonical path that EvalSymlinks will produce.
	realCanonical, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatalf("realDir EvalSymlinks: %v", err)
	}

	linkParent := t.TempDir()
	linkPath := filepath.Join(linkParent, "linked-workspace")
	if err := os.Symlink(realCanonical, linkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}

	rs := &RuleSet{Rules: []Rule{
		{Tool: "dev_read", Pattern: "./**", Behavior: DecisionAllow},
	}}

	resolved, err := rs.Resolve(linkPath)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	want := filepath.Join(realCanonical, "**")
	if resolved.Rules[0].Pattern != want {
		t.Errorf("symlink not canonicalized: got %q, want %q", resolved.Rules[0].Pattern, want)
	}
	// Defensive: the resolved pattern MUST NOT contain the symlink prefix.
	if strings.Contains(resolved.Rules[0].Pattern, linkPath) {
		t.Errorf("resolved pattern still references the symlink: %q", resolved.Rules[0].Pattern)
	}
}

// TestResolve_emptyWorkingDirRejected covers the safety net: a missing
// working_dir would otherwise resolve `./**` to `/`, granting the agent
// the whole filesystem.
func TestResolve_emptyWorkingDirRejected(t *testing.T) {
	rs := &RuleSet{Rules: []Rule{
		{Tool: "dev_read", Pattern: "./**", Behavior: DecisionAllow},
	}}
	_, err := rs.Resolve("")
	if err == nil {
		t.Fatal("expected ErrEmptyWorkingDir, got nil")
	}
	if !errors.Is(err, ErrEmptyWorkingDir) {
		t.Errorf("got %v, want ErrEmptyWorkingDir", err)
	}
}

// TestResolve_emptyWorkingDirOKWhenNoRelative checks the back-compat
// promise: if no rule uses `./`, the resolver tolerates an empty
// working_dir (it has nothing to do).
func TestResolve_emptyWorkingDirOKWhenNoRelative(t *testing.T) {
	rs := &RuleSet{Rules: []Rule{
		{Tool: "dev_read", Pattern: "/abs/path/**", Behavior: DecisionAllow},
		{Tool: "shell", Pattern: "rm", Behavior: DecisionDeny},
	}}
	resolved, err := rs.Resolve("")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(resolved.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(resolved.Rules))
	}
}

// TestResolve_nilRuleSet exercises the nil-safe path.
func TestResolve_nilRuleSet(t *testing.T) {
	var rs *RuleSet
	resolved, err := rs.Resolve("/tmp")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if resolved != nil {
		t.Errorf("expected nil resolved, got %#v", resolved)
	}
}

// TestResolve_dotAloneResolvesToWorkingDir documents the bare-`.` and
// bare-`./` corner case: a pattern equal to "." or "./" resolves to the
// working_dir itself with no glob suffix.
func TestResolve_dotAloneResolvesToWorkingDir(t *testing.T) {
	dir := t.TempDir()
	canonical, _ := canonicalizeWorkingDir(dir)

	cases := []string{".", "./"}
	for _, pattern := range cases {
		t.Run(pattern, func(t *testing.T) {
			rs := &RuleSet{Rules: []Rule{
				{Tool: "dev_read", Pattern: pattern, Behavior: DecisionAllow},
			}}
			resolved, err := rs.Resolve(dir)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if resolved.Rules[0].Pattern != canonical {
				t.Errorf("got %q, want %q", resolved.Rules[0].Pattern, canonical)
			}
		})
	}
}

// TestResolve_preservesModeAndSource verifies that Resolve carries the
// Mode and per-Rule Source tag through unchanged, so downstream
// diagnostic surfaces (permission summary, deny-forwarding trace) can
// trace a resolved pattern back to its origin.
func TestResolve_preservesModeAndSource(t *testing.T) {
	dir := t.TempDir()
	rs := &RuleSet{
		Mode: ModeAcceptEdits,
		Rules: []Rule{
			{Tool: "dev_read", Pattern: "./**", Behavior: DecisionAllow, Source: "test:profile.yaml"},
			{Tool: "dev_edit", Pattern: "/abs/**", Behavior: DecisionAllow, Source: "test:profile.yaml"},
		},
	}
	resolved, err := rs.Resolve(dir)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if resolved.Mode != ModeAcceptEdits {
		t.Errorf("mode lost: got %s, want %s", resolved.Mode, ModeAcceptEdits)
	}
	for i := range resolved.Rules {
		if resolved.Rules[i].Source != "test:profile.yaml" {
			t.Errorf("rule[%d] source lost: got %q", i, resolved.Rules[i].Source)
		}
	}
}

// TestResolvePattern_singleRule exercises the per-Rule entry point used by
// callers that want to resolve incrementally (e.g. when streaming rules
// from a YAML decoder).
func TestResolvePattern_singleRule(t *testing.T) {
	dir := t.TempDir()
	canonical, _ := canonicalizeWorkingDir(dir)

	in := Rule{Tool: "dev_read", Pattern: "./src/**", Behavior: DecisionAllow}
	out, err := in.ResolvePattern(canonical)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := filepath.Join(canonical, "src") + string(filepath.Separator) + "**"
	if out.Pattern != want {
		t.Errorf("got %q, want %q", out.Pattern, want)
	}
	// Receiver unchanged.
	if in.Pattern != "./src/**" {
		t.Errorf("ResolvePattern mutated receiver: %q", in.Pattern)
	}
}

// TestHasWorkspaceRelative spot-checks the predicate used by the Resolve
// fast path.
func TestHasWorkspaceRelative(t *testing.T) {
	cases := []struct {
		name string
		rs   *RuleSet
		want bool
	}{
		{"nil", nil, false},
		{"empty", &RuleSet{}, false},
		{"all absolute", &RuleSet{Rules: []Rule{
			{Pattern: "/abs/**"},
			{Pattern: ""},
		}}, false},
		{"one relative", &RuleSet{Rules: []Rule{
			{Pattern: "/abs/**"},
			{Pattern: "./**"},
		}}, true},
		{"dot alone", &RuleSet{Rules: []Rule{
			{Pattern: "."},
		}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.rs.HasWorkspaceRelative(); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSplitGlob exercises the literal/glob split that underpins the
// containment check for `./` patterns.
func TestSplitGlob(t *testing.T) {
	cases := []struct {
		in              string
		wantLiteral     string
		wantGlobSuffix  string
	}{
		{"", "", ""},
		{"**", "", "**"},
		{"generated/**", "generated", "**"},
		{"generated/foo.go", "generated/foo.go", ""},
		{"src/*.go", "src", "*.go"},
		{"deep/nested/path/*.md", "deep/nested/path", "*.md"},
		{"a/b/[abc]*", "a/b", "[abc]*"},
		{"foo?bar", "", "foo?bar"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			lit, glob := splitGlob(tc.in)
			if lit != tc.wantLiteral || glob != tc.wantGlobSuffix {
				t.Errorf("got (%q, %q), want (%q, %q)", lit, glob, tc.wantLiteral, tc.wantGlobSuffix)
			}
		})
	}
}
