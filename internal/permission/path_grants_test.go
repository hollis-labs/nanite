package permission

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExtractPathMentions_StrictPrefixOnly exercises Q1 of the locked
// design — only ^~/, ^/, ^./ tokens auto-grant.
func TestExtractPathMentions_StrictPrefixOnly(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want []string
	}{
		{
			name: "tilde slash mention",
			msg:  "please read ~/foo/bar.go",
			want: []string{"~/foo/bar.go"},
		},
		{
			name: "absolute path mention",
			msg:  "see /tmp/log.txt for details",
			want: []string{"/tmp/log.txt"},
		},
		{
			name: "dot slash mention",
			msg:  "look at ./internal/foo",
			want: []string{"./internal/foo"},
		},
		{
			name: "multiple mentions",
			msg:  "compare ~/a.txt and /etc/b.txt and ./c.txt",
			want: []string{"~/a.txt", "/etc/b.txt", "./c.txt"},
		},
		{
			name: "no false positive on bare filename",
			msg:  "read config.yaml please",
			want: nil,
		},
		{
			name: "no false positive on project name",
			msg:  "in nanite the chat agent does X",
			want: nil,
		},
		{
			name: "no false positive on URL",
			msg:  "check https://example.com/foo and http://x.com/bar",
			want: nil,
		},
		{
			name: "no false positive on file:// URL",
			msg:  "open file:///tmp/foo",
			want: nil,
		},
		{
			name: "no false positive on bare slash",
			msg:  "say / out loud",
			want: nil,
		},
		{
			name: "no false positive on network share",
			msg:  "mount //server/share now",
			want: nil,
		},
		{
			name: "trailing punctuation stripped",
			msg:  "see ~/foo. and /tmp/bar, and ./baz; thanks",
			want: []string{"~/foo", "/tmp/bar", "./baz"},
		},
		{
			name: "markdown backticks stripped",
			msg:  "open `~/foo/bar.go` now",
			want: []string{"~/foo/bar.go"},
		},
		{
			name: "parens stripped",
			msg:  "the file (/tmp/log.txt) please",
			want: []string{"/tmp/log.txt"},
		},
		{
			name: "tool name not matched (no slash)",
			msg:  "call dev_read on a path",
			want: nil,
		},
		{
			name: "dedup same token",
			msg:  "~/foo and ~/foo again",
			want: []string{"~/foo"},
		},
		{
			name: "empty input",
			msg:  "",
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractPathMentions(tc.msg)
			if !equalStringSlices(got, tc.want) {
				t.Errorf("ExtractPathMentions(%q) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}

// TestPathGrants_RegisterAndCheck covers Q2 (literal + parent-dir grant)
// and the basic auto-grant flow.
func TestPathGrants_RegisterAndCheck(t *testing.T) {
	g := NewPathGrants()
	tmp := t.TempDir()
	subFile := filepath.Join(tmp, "subdir", "file.txt")
	if err := os.MkdirAll(filepath.Dir(subFile), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(subFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("writefile: %v", err)
	}

	msg := "please read " + subFile + " for me"
	granted := g.RegisterFromUserMessage("s1", msg)
	if len(granted) == 0 {
		t.Fatalf("expected grants; got none")
	}

	// Q2: literal path is granted.
	if !g.IsPathAllowed("s1", subFile) {
		t.Errorf("literal path not granted: %s", subFile)
	}
	// Q2: parent dir is granted.
	if !g.IsPathAllowed("s1", filepath.Dir(subFile)) {
		t.Errorf("parent dir not granted: %s", filepath.Dir(subFile))
	}
	// Q2: a sibling file under the parent dir IS reachable (single-level
	// descent — parent-dir grant covers everything under it).
	sibling := filepath.Join(filepath.Dir(subFile), "other.txt")
	if !g.IsPathAllowed("s1", sibling) {
		t.Errorf("sibling under granted parent dir not reachable: %s", sibling)
	}
	// Q2: grandparent should NOT be granted (no recursion above the
	// literal mention).
	if g.IsPathAllowed("s1", filepath.Dir(filepath.Dir(subFile))) {
		t.Errorf("grandparent should not be granted")
	}
}

// TestPathGrants_SessionIsolation covers Q3 — grants are scoped per
// session and don't leak across.
func TestPathGrants_SessionIsolation(t *testing.T) {
	g := NewPathGrants()
	g.RegisterFromUserMessage("s1", "/tmp/foo.txt")

	if !g.IsPathAllowed("s1", "/tmp/foo.txt") {
		t.Error("s1 grant missing")
	}
	if g.IsPathAllowed("s2", "/tmp/foo.txt") {
		t.Error("s1 grant should not leak to s2")
	}
}

// TestPathGrants_SessionPersists covers Q3 — once granted in a session,
// the grant persists across multiple checks (no nag-again).
func TestPathGrants_SessionPersists(t *testing.T) {
	g := NewPathGrants()
	g.RegisterFromUserMessage("s1", "/tmp/foo.txt")

	for i := 0; i < 5; i++ {
		if !g.IsPathAllowed("s1", "/tmp/foo.txt") {
			t.Errorf("grant should persist across calls; iteration %d failed", i)
		}
	}
}

// TestPathGrants_Clear ensures session-end cleanup wipes grants.
func TestPathGrants_Clear(t *testing.T) {
	g := NewPathGrants()
	g.RegisterFromUserMessage("s1", "/tmp/foo.txt")
	g.Clear("s1")
	if g.IsPathAllowed("s1", "/tmp/foo.txt") {
		t.Error("Clear should wipe grants for s1")
	}
}

// TestPathGrants_NilSafe ensures every operation is safe on a nil
// receiver, matching the rest of the permission package's nil-safe
// disposition.
func TestPathGrants_NilSafe(t *testing.T) {
	var g *PathGrants
	if got := g.RegisterFromUserMessage("s1", "/tmp/foo.txt"); got != nil {
		t.Errorf("nil receiver should return nil, got %v", got)
	}
	if g.IsPathAllowed("s1", "/tmp/foo.txt") {
		t.Error("nil receiver should always return false")
	}
	g.Clear("s1") // must not panic
}

// TestPathGrants_TildeExpansion ensures the parser and the lookup agree
// on the canonical form of a ~/ path.
func TestPathGrants_TildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("UserHomeDir unavailable: %v", err)
	}
	g := NewPathGrants()
	g.RegisterFromUserMessage("s1", "edit ~/myproject/foo.go now")

	expanded := filepath.Join(home, "myproject", "foo.go")
	if !g.IsPathAllowed("s1", expanded) {
		t.Errorf("expanded path %s should be allowed", expanded)
	}
	// Sibling under parent dir should also be allowed (parent grant).
	sibling := filepath.Join(home, "myproject", "bar.go")
	if !g.IsPathAllowed("s1", sibling) {
		t.Errorf("sibling %s under parent dir should be allowed", sibling)
	}
	// Outside the parent dir should NOT be allowed.
	outside := filepath.Join(home, "otherproject", "baz.go")
	if g.IsPathAllowed("s1", outside) {
		t.Errorf("outside path %s should not be allowed", outside)
	}
}

// TestPathGrants_ContextHelpers covers WithPathGrants /
// PathGrantsFromContext round-trip.
func TestPathGrants_ContextHelpers(t *testing.T) {
	g := NewPathGrants()
	g.RegisterFromUserMessage("s1", "/tmp/foo.txt")

	ctx := WithPathGrants(context.Background(), "s1", g)
	gotID, gotChecker := PathGrantsFromContext(ctx)
	if gotID != "s1" {
		t.Errorf("session ID = %q, want s1", gotID)
	}
	if gotChecker == nil {
		t.Fatal("checker should not be nil")
	}
	if !gotChecker.IsPathAllowed("s1", "/tmp/foo.txt") {
		t.Error("checker should accept /tmp/foo.txt")
	}

	// nil-safe: bare context returns ("", nil).
	gotID, gotChecker = PathGrantsFromContext(context.Background())
	if gotID != "" || gotChecker != nil {
		t.Errorf("bare context should return zero values, got (%q, %v)", gotID, gotChecker)
	}

	// nil-safe: WithPathGrants with empty sessionID returns ctx unchanged.
	ctx2 := WithPathGrants(context.Background(), "", g)
	if id, c := PathGrantsFromContext(ctx2); id != "" || c != nil {
		t.Errorf("empty sessionID should be ignored")
	}
}

// TestPathGrants_NoFalsePositiveOnBareToolName ensures the parser
// doesn't grant tool names like "dev_read" that contain no slash.
func TestPathGrants_NoFalsePositiveOnBareToolName(t *testing.T) {
	g := NewPathGrants()
	granted := g.RegisterFromUserMessage("s1", "call dev_read for me")
	if len(granted) != 0 {
		t.Errorf("tool name should not auto-grant, got %v", granted)
	}
}

// TestPathGrants_DotSlashCanonicalization ensures ./X resolves under
// the current working directory.
func TestPathGrants_DotSlashCanonicalization(t *testing.T) {
	g := NewPathGrants()
	cwd, err := os.Getwd()
	if err != nil {
		t.Skipf("Getwd unavailable: %v", err)
	}
	granted := g.RegisterFromUserMessage("s1", "look at ./internal/foo")
	if len(granted) == 0 {
		t.Fatal("expected grant for ./internal/foo")
	}
	expected := filepath.Join(cwd, "internal", "foo")
	if !g.IsPathAllowed("s1", expected) {
		t.Errorf("expected %s to be allowed; grants = %v", expected, g.ListGrants("s1"))
	}
}

// equalStringSlices does an order-sensitive equality check (returns
// true when both are nil/empty).
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// noopDevNullCleaner is a sanity check that this file doesn't depend on
// stray helpers.
var _ = strings.TrimSpace
