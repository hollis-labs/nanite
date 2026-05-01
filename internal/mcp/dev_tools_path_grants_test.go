package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/permission"
)

// TestDevTools_PathGrants_EndToEnd is the trust-agent integration smoke
// test (CW-20260430-0009). Exercises the full path:
//
//   user message contains "/some/explicit/path.go"
//   → grant registered into PathGrants
//   → ctx stamped via permission.WithPathGrants
//   → DevToolsTransport.resolveAllowed accepts the path even though it
//     lies outside the static AllowedPaths list
//
// The static list is set to a *different* tmp dir to confirm the static
// gate alone rejects, and the grant is the only thing letting the call
// through.
func TestDevTools_PathGrants_EndToEnd(t *testing.T) {
	// Two tmp dirs: one that is in the static allow-list, one that the
	// user "explicitly mentions" in their message. The mentioned one is
	// NOT in AllowedPaths.
	staticDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mentionedDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// Write a file in the mentioned dir.
	mentionedFile := filepath.Join(mentionedDir, "report.go")
	if err := os.WriteFile(mentionedFile, []byte("package report\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	dt := NewDevToolsTransport([]string{staticDir})

	// Step 1: bare ctx, mentioned path should be REJECTED — only the
	// static dir is allowed.
	if _, err := dt.resolveAllowed(context.Background(), mentionedFile); err == nil {
		t.Fatalf("expected rejection on bare ctx for mentioned path %q", mentionedFile)
	}

	// Step 2: simulate "user message contains <path>" by registering the
	// path with PathGrants and stamping ctx.
	grants := permission.NewPathGrants()
	registered := grants.RegisterFromUserMessage("session-A", "please read "+mentionedFile)
	if len(registered) == 0 {
		t.Fatalf("RegisterFromUserMessage returned no grants — parser missed %q", mentionedFile)
	}

	ctx := permission.WithPathGrants(context.Background(), "session-A", grants)

	// Step 3: the call should now succeed for the mentioned file.
	resolved, err := dt.resolveAllowed(ctx, mentionedFile)
	if err != nil {
		t.Fatalf("resolveAllowed with grant ctx: %v", err)
	}
	if !strings.HasSuffix(resolved, "report.go") {
		t.Errorf("resolved = %q, want suffix report.go", resolved)
	}

	// Step 4: a sibling file under the mentioned-file's parent dir
	// should ALSO be allowed (Q2 — parent grant).
	sibling := filepath.Join(mentionedDir, "other.go")
	if err := os.WriteFile(sibling, []byte("package report\n"), 0o644); err != nil {
		t.Fatalf("write sibling: %v", err)
	}
	if _, err := dt.resolveAllowed(ctx, sibling); err != nil {
		t.Errorf("sibling under mentioned file's parent should be allowed: %v", err)
	}

	// Step 5: a file in a totally unrelated dir should still be rejected
	// — grants do not leak across directories.
	unrelatedDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(unrelatedDir, "nope.go")
	if err := os.WriteFile(unrelated, []byte(""), 0o644); err != nil {
		t.Fatalf("write unrelated: %v", err)
	}
	if _, err := dt.resolveAllowed(ctx, unrelated); err == nil {
		t.Errorf("unrelated path should be rejected; got resolved successfully")
	}

	// Step 6: a fresh session should NOT inherit grants from session-A.
	bareCtx := permission.WithPathGrants(context.Background(), "session-B", grants)
	if _, err := dt.resolveAllowed(bareCtx, mentionedFile); err == nil {
		t.Errorf("session-B should not inherit session-A grants")
	}
}

// TestDevTools_PathGrants_StaticListStillRejects confirms that absent any
// session grant on ctx, the static AllowedPaths list still rejects paths
// outside it (regression guard — the trust-agent change must NOT widen
// the default surface).
func TestDevTools_PathGrants_StaticListStillRejects(t *testing.T) {
	staticDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	other, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dt := NewDevToolsTransport([]string{staticDir})

	if _, err := dt.resolveAllowed(context.Background(), filepath.Join(other, "x.go")); err == nil {
		t.Error("path outside static list should still be rejected on bare ctx")
	}
}

// TestDevTools_PathGrants_DevReadFlow exercises the path-grant fallback
// through the public CallTool surface (dev_read), confirming the
// integration is wired all the way through CallTool → callRead →
// resolveAllowed → tryResolveViaSessionGrant.
func TestDevTools_PathGrants_DevReadFlow(t *testing.T) {
	staticDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mentionedDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mentionedFile := filepath.Join(mentionedDir, "doc.txt")
	if err := os.WriteFile(mentionedFile, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dt := NewDevToolsTransport([]string{staticDir})

	// Without grant, dev_read fails.
	res, err := dt.CallTool(context.Background(), "dev_read", map[string]any{"path": mentionedFile})
	if err != nil {
		t.Fatalf("CallTool without grant: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected dev_read to fail without a path grant")
	}

	// With grant, dev_read succeeds.
	grants := permission.NewPathGrants()
	grants.RegisterFromUserMessage("s1", "see "+mentionedFile)
	ctx := permission.WithPathGrants(context.Background(), "s1", grants)
	res, err = dt.CallTool(ctx, "dev_read", map[string]any{"path": mentionedFile})
	if err != nil {
		t.Fatalf("CallTool with grant: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected dev_read to succeed with path grant; got error result: %+v", res)
	}
}
