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

// TestDevTools_PathGrants_C127TildeMention_NoHOME is the regression
// guard for CW-20260502-0014 (Glass-8). Reproduces the c127 acceptance-
// smoke failure shape: nanite-api-service runs under launchd with no
// HOME in its environment, the user mentions "~/Projects-apps", and the
// agent invokes a dev_* tool with a tilde-prefixed path arg. Without
// the HomeDir fallback, the literal tilde flows through resolveAllowed
// and the path-grant store never registers the mention.
//
// This test mirrors the live failure exactly:
//
//   - $HOME unset (launchd default-environment posture)
//   - AllowedPaths empty (fresh chat session, no project root configured)
//   - User message contains "~/Projects-apps"
//   - Agent's tool call passes the literal "~/Projects-apps" string
//
// Pre-fix: resolveAllowed produces an EscapeError with Attempt = literal
// "~/Projects-apps" and Cause = "no allowed paths configured for this
// session", matching c127 verbatim.
//
// Post-fix: HomeDir falls back to user.Current(), absolutize and
// expandHome both expand to the same canonical path, the parent-dir
// grant matches, and resolveAllowed accepts the call.
func TestDevTools_PathGrants_C127TildeMention_NoHOME(t *testing.T) {
	prev, hadHome := os.LookupEnv("HOME")
	t.Cleanup(func() {
		if hadHome {
			os.Setenv("HOME", prev)
		} else {
			os.Unsetenv("HOME")
		}
	})
	os.Unsetenv("HOME")

	home, err := permission.HomeDir()
	if err != nil {
		t.Skipf("HomeDir fallback unavailable on this platform: %v", err)
	}
	expandedDir := filepath.Join(home, "Projects-apps")

	// Ingest-side: simulate chat.HandleMessage's path-grant parser firing
	// for the c127 user message.
	grants := permission.NewPathGrants()
	registered := grants.RegisterFromUserMessage(
		"c127",
		"Quick test — list the contents of ~/Projects-apps, just top level, first 10 entries.",
	)
	if len(registered) == 0 {
		t.Fatal("RegisterFromUserMessage returned no grants on HOME-less env — absolutize fell through")
	}
	foundExpanded := false
	for _, p := range registered {
		if p == expandedDir {
			foundExpanded = true
		}
	}
	if !foundExpanded {
		t.Errorf("registered grants %v do not include expanded path %q", registered, expandedDir)
	}

	// Tool-call side: AllowedPaths empty (c127's environment), ctx stamped,
	// agent passes the literal "~/Projects-apps". expandHome must agree
	// with absolutize on the canonical form.
	dt := NewDevToolsTransport(nil)
	ctx := permission.WithPathGrants(context.Background(), "c127", grants)

	resolved, err := dt.resolveAllowed(ctx, "~/Projects-apps")
	if err != nil {
		t.Fatalf("resolveAllowed(~/Projects-apps) failed after HOME-less fallback: %v", err)
	}
	if !strings.HasSuffix(resolved, "Projects-apps") {
		t.Errorf("resolved = %q, want suffix Projects-apps", resolved)
	}
}

// TestDevTools_TildeAcceptanceInDescriptions guards the LLM-facing
// instruction surface that prevents the c140/c141 username-hallucination
// regression. Background: when the user's message contains ~/Projects-apps
// and the dev_* tool description says "must start with /", Sonnet 4 has
// been observed to convert the tilde into /Users/<fabricated-name>/...
// instead of passing it verbatim. The descriptions must (a) explicitly
// advertise that ~/ paths are accepted, (b) tell the agent to pass them
// VERBATIM, and (c) warn against substituting a username. If any of the
// six dev_* tools loses this language, this test goes red — the smoke
// failure mode it prevents is non-deterministic and very expensive to
// re-discover via live chat.
func TestDevTools_TildeAcceptanceInDescriptions(t *testing.T) {
	dt := NewDevToolsTransport(nil)
	tools, err := dt.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) == 0 {
		t.Fatal("ListTools returned no tools")
	}
	expectedNames := map[string]bool{
		"dev_read": true, "dev_grep": true, "dev_write": true,
		"dev_glob": true, "dev_edit": true, "dev_bash": true,
	}
	for _, tool := range tools {
		if !expectedNames[tool.Name] {
			continue
		}
		desc := tool.Description
		// (a) tilde acceptance advertised
		if !strings.Contains(desc, "~/") {
			t.Errorf("%s description missing ~/ acceptance language: %s", tool.Name, desc)
		}
		// (b) "VERBATIM" caps to make the instruction stick under
		//     non-deterministic sampling
		if !strings.Contains(desc, "VERBATIM") {
			t.Errorf("%s description missing VERBATIM nudge: %s", tool.Name, desc)
		}
		// (c) explicit anti-fabrication guidance
		if !strings.Contains(desc, "do NOT substitute") {
			t.Errorf("%s description missing anti-fabrication guidance: %s", tool.Name, desc)
		}
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
