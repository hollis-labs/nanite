package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/permission"
)

// An API session's host-registered roots authorize its file and shell tools,
// make the first the default working directory, and nothing else.
func TestDevTools_SessionRootsBoundAnAPISession(t *testing.T) {
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	extra, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	inWork := work + "/a.txt"
	inExtra := extra + "/b.txt"
	nope := outside + "/c.txt"
	for _, f := range []string{inWork, inExtra, nope} {
		if writeErr := os.WriteFile(f, []byte("x"), 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}

	grants := permission.NewPathGrants()
	grants.RegisterRoots("sess-api", work, extra)
	ctx := permission.WithPathGrants(context.Background(), "sess-api", grants)
	dt := NewDevToolsTransport(nil)

	for _, ok := range []string{inWork, inExtra} {
		if _, resolveErr := dt.resolveAllowed(ctx, ok); resolveErr != nil {
			t.Errorf("%s inside a registered root refused: %v", ok, resolveErr)
		}
	}
	if _, err = dt.resolveAllowed(ctx, nope); err == nil {
		t.Error("a path outside the registered roots was allowed")
	}
	if _, err = dt.resolveAllowed(permission.WithPathGrants(context.Background(), "other", grants), inWork); err == nil {
		t.Error("another session saw this session's roots")
	}

	// dev_bash with no working_dir runs in the work root, not in a later root.
	res, err := dt.CallTool(ctx, "dev_bash", map[string]any{"command": "pwd"})
	if err != nil || res.IsError {
		t.Fatalf("dev_bash: %v %+v", err, res)
	}
	if got := strings.TrimSpace(res.Content[0].Text); !strings.HasSuffix(got, filepath.Base(work)) {
		t.Errorf("default working dir = %q, want the work root %q", got, work)
	}
}

// A host directory that contains a protected one must not carry the dev tools
// into it: every file-touching tool refuses the protected path, a symlink to
// it included, and dev_bash is withheld (the Reviewer's #546 probe).
func TestDevTools_ProtectedDescendantOfAGrantedAncestor(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	control := base + "/control"
	if err = os.MkdirAll(control, 0o750); err != nil {
		t.Fatal(err)
	}
	secret := control + "/state.txt"
	open := base + "/open.txt"
	for _, f := range []string{secret, open} {
		if err = os.WriteFile(f, []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := base + "/link"
	if err = os.Symlink(control, link); err != nil {
		t.Fatal(err)
	}

	grants := permission.NewPathGrants()
	grants.RegisterRoots("sess-api", base)
	grants.RegisterProtected("sess-api", []string{control}, "not available: protected "+control)
	ctx := permission.WithPathGrants(context.Background(), "sess-api", grants)
	dt := NewDevToolsTransport(nil)

	for _, target := range []string{secret, link + "/state.txt"} {
		for tool, args := range map[string]map[string]any{
			"dev_read":  {"path": target},
			"dev_write": {"path": target, "content": "overwritten"},
			"dev_edit":  {"path": target, "old_string": "original", "new_string": "edited"},
		} {
			res, callErr := dt.CallTool(ctx, tool, args)
			if callErr != nil || !res.IsError {
				t.Errorf("%s on %s: err=%v res=%+v, want a refusal", tool, target, callErr, res)
			}
		}
	}
	//nolint:gosec // G304: a fixture path the test created.
	if data, readErr := os.ReadFile(secret); readErr != nil || string(data) != "original" {
		t.Fatalf("protected file changed: %q %v", data, readErr)
	}

	// Searches over the granted ancestor skip the protected directory.
	for tool, args := range map[string]map[string]any{
		"dev_grep": {"pattern": "original", "directory": base},
		"dev_glob": {"pattern": "**/*.txt", "directory": base},
	} {
		res, callErr := dt.CallTool(ctx, tool, args)
		if callErr != nil || res.IsError {
			t.Fatalf("%s: %v %+v", tool, callErr, res)
		}
		out := res.Content[0].Text
		if strings.Contains(out, "state.txt") || !strings.Contains(out, "open.txt") {
			t.Errorf("%s output = %q, want open.txt and not the protected state.txt", tool, out)
		}
	}
	if res, readErr := dt.CallTool(ctx, "dev_read", map[string]any{"path": open}); readErr != nil || res.IsError {
		t.Errorf("an unprotected sibling was refused: %v %+v", readErr, res)
	}

	res, err := dt.CallTool(ctx, "dev_bash", map[string]any{"command": "cat " + secret, "working_dir": base})
	if err != nil || !res.IsError || !strings.Contains(res.Content[0].Text, control) {
		t.Fatalf("dev_bash = %+v %v, want a refusal naming %s", res, err, control)
	}
}

// A spawned child (and its child) uses the parent's grants, so it carries the
// parent's protection too.
func TestDevTools_ProtectionFollowsSessionLineage(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	control := base + "/control"
	if err = os.MkdirAll(control, 0o750); err != nil {
		t.Fatal(err)
	}
	secret := control + "/state.txt"
	if err = os.WriteFile(secret, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}

	grants := permission.NewPathGrants()
	grants.RegisterRoots("parent", base)
	grants.RegisterProtected("parent", []string{control}, "not available: protected "+control)
	grants.RegisterLineage("child", "parent")
	grants.RegisterLineage("grandchild", "child")
	dt := NewDevToolsTransport(nil)

	for _, session := range []string{"parent", "child", "grandchild"} {
		ctx := permission.WithPathGrants(context.Background(), session, grants)
		for tool, args := range map[string]map[string]any{
			"dev_read":  {"path": secret},
			"dev_write": {"path": secret, "content": "overwritten"},
			"dev_edit":  {"path": secret, "old_string": "original", "new_string": "edited"},
			"dev_bash":  {"command": "echo x > " + control + "/new.txt", "working_dir": base},
		} {
			res, callErr := dt.CallTool(ctx, tool, args)
			if callErr != nil || !res.IsError {
				t.Errorf("%s as %s: err=%v res=%+v, want a refusal", tool, session, callErr, res)
			}
		}
	}
	//nolint:gosec // G304: a fixture path the test created.
	if data, readErr := os.ReadFile(secret); readErr != nil || string(data) != "original" {
		t.Fatalf("protected file changed: %q %v", data, readErr)
	}
	if _, statErr := os.Stat(control + "/new.txt"); statErr == nil {
		t.Fatal("dev_bash wrote into the protected directory")
	}
}
