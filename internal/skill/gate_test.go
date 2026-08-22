package skill

// TASKS/skills/09's own Done-means, verified here:
//
//   - a granted skill with a matching approved hash executes successfully
//     within its declared capability bounds — a real sandbox.Apply call,
//     not mocked — and a test attempting an operation *outside* those
//     bounds confirms the sandbox actually blocks it (real OS-level
//     enforcement, not just the gate's own "would have refused" logic);
//   - a skill whose vendored content hash no longer matches the approved
//     hash is refused with a clear re-approval-required error, verified by
//     re-installing a real fixture with changed content and confirming the
//     previously-granted agent can no longer execute it until re-approved;
//   - a skill/agent pair with no grant row at all is refused outright (this
//     task's own resolution of the "no grant row" question — see gate.go's
//     package doc, "No-grant-row resolution");
//   - internal/skill contains exactly one call to sandbox.Apply.
//
// # Platform-dependence of the "real sandbox enforcement" tests
//
// requireSandboxTool below mirrors go-sandbox@v0.2.1's own integration-test
// convention (sandbox/integration_test.go's requireSandboxTool) — skip
// (not fail) when the platform's sandbox tool isn't installed, since a
// missing sandbox-exec/bwrap binary is an environment fact, not a bug in
// this file. On the machine this task was actually implemented and run
// against (darwin/arm64, sandbox-exec present at /usr/bin/sandbox-exec),
// every test below ran for real and passed — see this task's Work Log for
// the exact verification record, including the darwin-specific reasoning
// behind using an explicit FS.Deny grant (rather than bare omission from
// FS.Write) to prove the filesystem-write boundary, per gate.go's own
// package doc on go-sandbox's real, cross-platform-asymmetric enforcement
// behavior.
import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/skillvendor"
	"github.com/hollis-labs/nanite/internal/store"
)

func requireSandboxTool(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("sandbox-exec"); err != nil {
			t.Skip("sandbox-exec not found; skipping real-sandbox-enforcement test")
		}
	case "linux":
		if _, err := exec.LookPath("bwrap"); err != nil {
			t.Skip("bwrap not found; skipping real-sandbox-enforcement test")
		}
	default:
		t.Skipf("sandbox enforcement not supported on %s", runtime.GOOS)
	}
}

// ---------------------------------------------------------------------
// Fixture plumbing — mirrors resolver_test.go's newResolverTestStore /
// newResolverTestVendor / installFixture convention exactly (same
// package, same "don't add a test-only cross-package dependency on
// internal/skillinstall" reasoning).
// ---------------------------------------------------------------------

func newGateTestStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "gate-test.db")
	s, err := store.New(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

func newGateTestVendor(t *testing.T) *skillvendor.Store {
	t.Helper()
	v, err := skillvendor.New(filepath.Join(t.TempDir(), "vendor"))
	if err != nil {
		t.Fatalf("skillvendor.New: %v", err)
	}
	return v
}

// writeSkillFixture writes a minimal, real SKILL.md package to a fresh
// temp directory and returns its path. body distinguishes fixture
// versions for the hash-mismatch test — a real content-hash change on
// re-install, not a synthetic one.
func writeSkillFixture(t *testing.T, slug, body string) string {
	t.Helper()
	dir := t.TempDir()
	content := fmt.Sprintf("---\nname: Gate Test Skill\nslug: %s\ndescription: fixture skill for TASKS/skills/09 gate tests.\n---\n\n%s\n", slug, body)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture SKILL.md: %v", err)
	}
	return dir
}

// installGateFixture parses, vendors, and indexes a fresh skill package —
// this file's own minimal stand-in for internal/skillinstall's fuller
// pipeline, matching resolver_test.go's installFixture precedent exactly.
func installGateFixture(t *testing.T, idx *store.Store, vendor *skillvendor.Store, dir string) *store.Skill {
	t.Helper()
	def, files, err := ParsePackageDir(dir)
	if err != nil {
		t.Fatalf("ParsePackageDir(%s): %v", dir, err)
	}
	wr, err := vendor.Write(context.Background(), skillvendor.FileMap(files))
	if err != nil {
		t.Fatalf("vendor.Write(%s): %v", dir, err)
	}
	sk := def.ToStoreSkill()
	sk.ID = ""
	sk.ContentHash = wr.Address
	if err := idx.CreateSkill(context.Background(), sk); err != nil {
		t.Fatalf("CreateSkill(%s): %v", dir, err)
	}
	return sk
}

// reinstallGateFixture simulates a real re-install/re-sync of an
// already-indexed skill: re-parses and re-vendors dir (now with different
// content), then updates the existing index row's ContentHash/Version in
// place — mirroring internal/skillinstall's own upsertIndex re-sync path
// (same slug, same row, new address) without importing that package.
func reinstallGateFixture(t *testing.T, idx *store.Store, vendor *skillvendor.Store, existing *store.Skill, dir string) *store.Skill {
	t.Helper()
	_, files, err := ParsePackageDir(dir)
	if err != nil {
		t.Fatalf("ParsePackageDir(%s): %v", dir, err)
	}
	wr, err := vendor.Write(context.Background(), skillvendor.FileMap(files))
	if err != nil {
		t.Fatalf("vendor.Write(%s): %v", dir, err)
	}
	existing.ContentHash = wr.Address
	existing.Version++
	if err := idx.UpdateSkill(context.Background(), existing); err != nil {
		t.Fatalf("UpdateSkill(%s): %v", dir, err)
	}
	return existing
}

func makeGateTestAgent(t *testing.T, s *store.Store, slug string) *store.AgentProfile {
	t.Helper()
	a := &store.AgentProfile{Name: "Gate Test Agent " + slug, Slug: slug, SystemPrompt: "test"}
	if err := s.CreateAgent(context.Background(), a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return a
}

// newAuthorizedGate sets up a real store+vendor, installs a fresh fixture
// skill, creates an agent, and grants that agent an approved capability
// row for the fixture with capsJSON as capabilities_granted. Returns a
// ready-to-use Gate plus the agent ID and skill slug to build ExecRequests
// against.
func newAuthorizedGate(t *testing.T, capsJSON string) (gate *Gate, agentID, skillSlug string) {
	t.Helper()
	idx := newGateTestStore(t)
	vendor := newGateTestVendor(t)

	dir := writeSkillFixture(t, "gate-test-skill", "Body v1.")
	sk := installGateFixture(t, idx, vendor, dir)

	agent := makeGateTestAgent(t, idx, "gate-test-agent")

	if err := idx.InsertAgentKnownSkill(context.Background(), store.AgentKnownSkill{
		AgentID:             agent.ID,
		SkillName:           sk.Slug,
		ApprovedContentHash: sk.ContentHash,
		GrantedAt:           time.Now().UTC().Format(time.RFC3339),
		GrantedBy:           "test-operator",
		CapabilitiesGranted: capsJSON,
	}); err != nil {
		t.Fatalf("InsertAgentKnownSkill: %v", err)
	}

	return NewGate(idx, idx), agent.ID, sk.Slug
}

// ---------------------------------------------------------------------
// Grant-decision tests — no real sandbox execution involved, pure
// authorize() logic against a real *store.Store.
// ---------------------------------------------------------------------

func TestExecuteGated_NoGrantRow_Refused(t *testing.T) {
	idx := newGateTestStore(t)
	vendor := newGateTestVendor(t)
	dir := writeSkillFixture(t, "no-grant-skill", "Body.")
	sk := installGateFixture(t, idx, vendor, dir)
	agent := makeGateTestAgent(t, idx, "no-grant-agent")

	g := NewGate(idx, idx)
	req := ExecRequest{
		SkillSlug: sk.Slug, AgentID: agent.ID,
		Command: []string{"/bin/sh", "-c", "echo should-not-run"},
		WorkDir: t.TempDir(),
		Kind:    ExecKindMarker, Label: "echo",
	}
	_, err := g.ExecuteGated(context.Background(), req)
	if err == nil {
		t.Fatal("expected refusal with no grant row at all, got nil error")
	}
	var gerr *GrantRequiredError
	if !errors.As(err, &gerr) {
		t.Fatalf("expected *GrantRequiredError, got %T: %v", err, err)
	}
	if gerr.Reason != "no grant row exists" {
		t.Errorf("Reason: got %q, want %q", gerr.Reason, "no grant row exists")
	}
}

func TestExecuteGated_BareAssignmentGrant_Refused(t *testing.T) {
	idx := newGateTestStore(t)
	vendor := newGateTestVendor(t)
	dir := writeSkillFixture(t, "bare-assign-skill", "Body.")
	sk := installGateFixture(t, idx, vendor, dir)
	agent := makeGateTestAgent(t, idx, "bare-assign-agent")

	// Simulates AssignSkillToAgent's own bare INSERT (agent_id, skill_name
	// only) — no known-skill data, no grant-state columns.
	if err := idx.InsertAgentKnownSkill(context.Background(), store.AgentKnownSkill{
		AgentID: agent.ID, SkillName: sk.Slug,
	}); err != nil {
		t.Fatalf("InsertAgentKnownSkill: %v", err)
	}

	g := NewGate(idx, idx)
	req := ExecRequest{
		SkillSlug: sk.Slug, AgentID: agent.ID,
		Command: []string{"/bin/sh", "-c", "echo should-not-run"},
		WorkDir: t.TempDir(),
		Kind:    ExecKindMarker, Label: "echo",
	}
	_, err := g.ExecuteGated(context.Background(), req)
	if err == nil {
		t.Fatal("expected refusal for a bare-assignment grant row, got nil error")
	}
	var gerr *GrantRequiredError
	if !errors.As(err, &gerr) {
		t.Fatalf("expected *GrantRequiredError, got %T: %v", err, err)
	}
	if gerr.Reason != "grant row exists but has never been approved" {
		t.Errorf("Reason: got %q, want %q", gerr.Reason, "grant row exists but has never been approved")
	}
}

func TestExecuteGated_HashMismatch_ReapprovalRequired(t *testing.T) {
	idx := newGateTestStore(t)
	vendor := newGateTestVendor(t)

	dirV1 := writeSkillFixture(t, "reapproval-skill", "Body v1.")
	sk := installGateFixture(t, idx, vendor, dirV1)
	hash1 := sk.ContentHash

	agent := makeGateTestAgent(t, idx, "reapproval-agent")
	if err := idx.InsertAgentKnownSkill(context.Background(), store.AgentKnownSkill{
		AgentID: agent.ID, SkillName: sk.Slug,
		ApprovedContentHash: hash1,
		GrantedAt:           time.Now().UTC().Format(time.RFC3339),
		GrantedBy:           "test-operator",
	}); err != nil {
		t.Fatalf("InsertAgentKnownSkill: %v", err)
	}

	g := NewGate(idx, idx)
	req := ExecRequest{
		SkillSlug: sk.Slug, AgentID: agent.ID,
		Command: []string{"/bin/sh", "-c", "echo ok"},
		WorkDir: t.TempDir(),
		Kind:    ExecKindMarker, Label: "echo",
	}

	// Approved and current hash match: executes successfully.
	if _, err := g.ExecuteGated(context.Background(), req); err != nil {
		t.Fatalf("ExecuteGated before re-install: unexpected error: %v", err)
	}

	// Real re-install with genuinely different content -> new hash.
	dirV2 := writeSkillFixture(t, "reapproval-skill", "Body v2 — genuinely different content.")
	sk = reinstallGateFixture(t, idx, vendor, sk, dirV2)
	hash2 := sk.ContentHash
	if hash1 == hash2 {
		t.Fatal("expected re-install with changed content to produce a different content hash")
	}

	// Approved hash (hash1) no longer matches current hash (hash2):
	// refused, not silently allowed under the stale approval.
	_, err := g.ExecuteGated(context.Background(), req)
	if err == nil {
		t.Fatal("expected re-approval-required error after content changed, got nil")
	}
	var rerr *ReapprovalRequiredError
	if !errors.As(err, &rerr) {
		t.Fatalf("expected *ReapprovalRequiredError, got %T: %v", err, err)
	}
	if rerr.ApprovedHash != hash1 || rerr.CurrentHash != hash2 {
		t.Errorf("ReapprovalRequiredError hashes: got approved=%q current=%q, want approved=%q current=%q",
			rerr.ApprovedHash, rerr.CurrentHash, hash1, hash2)
	}

	// Re-approval (grant row updated to the new hash) restores execution.
	if err := idx.InsertAgentKnownSkill(context.Background(), store.AgentKnownSkill{
		AgentID: agent.ID, SkillName: sk.Slug,
		ApprovedContentHash: hash2,
		GrantedAt:           time.Now().UTC().Format(time.RFC3339),
		GrantedBy:           "test-operator",
	}); err != nil {
		t.Fatalf("InsertAgentKnownSkill (re-approval): %v", err)
	}
	if _, err := g.ExecuteGated(context.Background(), req); err != nil {
		t.Fatalf("ExecuteGated after re-approval: unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------
// Real sandbox-enforcement tests — a real sandbox.Apply call, real
// subprocess, real OS-level result. Each pairs a positive (within-grant)
// case with a negative (outside-grant, actually blocked) case, per this
// task's own Done-means.
// ---------------------------------------------------------------------

func TestExecuteGated_WriteWithinGrantSucceeds_OutsideGrantBlocked(t *testing.T) {
	requireSandboxTool(t)

	grantedDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(grantedDir): %v", err)
	}
	deniedDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(deniedDir): %v", err)
	}
	workDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(workDir): %v", err)
	}

	// Grants write access to grantedDir only, and explicitly denies
	// deniedDir. The explicit deny (rather than bare omission) is
	// deliberate — see gate.go's package doc on go-sandbox's real,
	// platform-asymmetric filesystem enforcement: omission alone does not
	// block a write on macOS's default-allow SBPL backend, but an
	// explicit FS.Deny entry does, on every platform this library
	// supports (a no-op on Linux, where the same outcome instead falls
	// out of deniedDir never being bound into the sandbox's mount
	// namespace at all).
	capsJSON := fmt.Sprintf(`{"fs":{"write":[%q],"deny":[%q]}}`, grantedDir, deniedDir)
	g, agentID, skillSlug := newAuthorizedGate(t, capsJSON)

	allowedTarget := filepath.Join(grantedDir, "allowed.txt")
	deniedTarget := filepath.Join(deniedDir, "denied.txt")

	script := fmt.Sprintf(
		`echo hi > %q && echo ALLOWED_OK; (echo hi > %q && echo DENIED_OK) || echo DENIED_BLOCKED`,
		allowedTarget, deniedTarget,
	)

	req := ExecRequest{
		SkillSlug: skillSlug, AgentID: agentID,
		Command: []string{"/bin/sh", "-c", script},
		WorkDir: workDir,
		Kind:    ExecKindMarker, Label: "fs-bounds-test",
	}
	res, err := g.ExecuteGated(context.Background(), req)
	if err != nil {
		t.Fatalf("ExecuteGated: %v (stdout=%q stderr=%q)", err, res.Stdout, res.Stderr)
	}

	if !strings.Contains(res.Stdout, "ALLOWED_OK") {
		t.Errorf("expected write to granted path to succeed; stdout=%q stderr=%q", res.Stdout, res.Stderr)
	}
	if _, statErr := os.Stat(allowedTarget); statErr != nil {
		t.Errorf("expected %s to exist on disk after granted write: %v", allowedTarget, statErr)
	}

	if !strings.Contains(res.Stdout, "DENIED_BLOCKED") {
		t.Errorf("expected write to denied path to be blocked by the sandbox, not just refused by the gate; stdout=%q stderr=%q", res.Stdout, res.Stderr)
	}
	if _, statErr := os.Stat(deniedTarget); !os.IsNotExist(statErr) {
		t.Errorf("expected %s to NOT exist on disk after a blocked write attempt (real OS-level proof), stat err = %v", deniedTarget, statErr)
	}
}

func TestExecuteGated_NetworkBlockedOutsideGrant_AllowedWithGrant(t *testing.T) {
	requireSandboxTool(t)
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not found")
	}

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	url := "http://" + ln.Addr().String()
	curlCmd := []string{"/bin/sh", "-c", "curl -sf --max-time 2 " + url}

	t.Run("blocked without network capability", func(t *testing.T) {
		g, agentID, skillSlug := newAuthorizedGate(t, `{}`)
		req := ExecRequest{
			SkillSlug: skillSlug, AgentID: agentID,
			Command: curlCmd, WorkDir: t.TempDir(),
			Kind: ExecKindMarker, Label: "network-test",
		}
		if _, err := g.ExecuteGated(context.Background(), req); err == nil {
			t.Fatal("expected sandboxed curl (no network capability granted) to fail, but it succeeded")
		}
	})

	t.Run("allowed with loopback capability granted", func(t *testing.T) {
		g, agentID, skillSlug := newAuthorizedGate(t, `{"network":{"allow_loopback":true}}`)
		req := ExecRequest{
			SkillSlug: skillSlug, AgentID: agentID,
			Command: curlCmd, WorkDir: t.TempDir(),
			Kind: ExecKindMarker, Label: "network-test",
		}
		if _, err := g.ExecuteGated(context.Background(), req); err != nil {
			t.Fatalf("expected sandboxed curl to succeed with loopback network capability granted: %v", err)
		}
	})
}

// TestFilterSecretEnv_StripsSecretsKeepsOrdinaryVars is a narrow unit test
// against filterSecretEnv itself, in addition to (not instead of) the
// end-to-end TestExecuteGated_EnvironmentSecretsFiltered_NotLeaked test
// below — this one pins the exact filtering behavior without depending on
// a real sandbox tool being installed, so it always runs regardless of
// platform.
func TestFilterSecretEnv_StripsSecretsKeepsOrdinaryVars(t *testing.T) {
	in := []string{
		"HOME=/home/test",
		"PATH=/usr/bin:/bin",
		"LANG=en_US.UTF-8",
		"API_KEY=super-secret",
		"MY_SECRET_VALUE=nope",
		"AUTH_TOKEN=nope",
		"DB_PASSWORD=nope",
		"SOME_CREDENTIAL_BLOB=nope",
		"malformed-entry-no-equals",
	}
	out := filterSecretEnv(in)

	wantKept := []string{"HOME=/home/test", "PATH=/usr/bin:/bin", "LANG=en_US.UTF-8"}
	for _, w := range wantKept {
		found := false
		for _, o := range out {
			if o == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %q to be kept, got %v", w, out)
		}
	}

	wantStripped := []string{"API_KEY", "MY_SECRET_VALUE", "AUTH_TOKEN", "DB_PASSWORD", "SOME_CREDENTIAL_BLOB"}
	for _, o := range out {
		eqIdx := strings.IndexByte(o, '=')
		key := o
		if eqIdx >= 0 {
			key = o[:eqIdx]
		}
		for _, w := range wantStripped {
			if key == w {
				t.Errorf("expected %q to be stripped, but it survived filtering: %v", w, out)
			}
		}
	}

	if len(out) != len(wantKept) {
		t.Errorf("expected exactly %d surviving entries (%v), got %d: %v", len(wantKept), wantKept, len(out), out)
	}
}

// TestExecuteGated_EnvironmentSecretsFiltered_NotLeaked is the regression
// test for TASKS/skills/09's "Fix required" section: a granted skill run
// under the bare default capability posture (no FS, no Network, nothing
// elevated) must NOT see a secret-shaped environment variable from the host
// process's own environment, via an ordinary "compute" marker/script that
// reads its own environment (“ !`env` “ / printenv-equivalent) — this is
// a real, unmocked test exercising the actual Gate.run/sandbox.Apply path
// (not a unit test against filterSecretEnv in isolation), matching this
// task's own established real-sandbox-test discipline.
func TestExecuteGated_EnvironmentSecretsFiltered_NotLeaked(t *testing.T) {
	requireSandboxTool(t)
	if _, err := exec.LookPath("env"); err != nil {
		t.Skip("env not found")
	}

	// A secret-shaped var (matches the "TOKEN" pattern) that must never
	// reach the sandboxed command's environment, and a plain, non-secret
	// var that must pass through unfiltered so a real skill script relying
	// on ordinary environment context isn't broken by this fix.
	t.Setenv("NANITE_TEST_SECRET_TOKEN", "should-not-leak")
	t.Setenv("NANITE_TEST_SAFE_VAR", "safe-value-should-pass-through")

	// Bare default posture — no FS, no Network, nothing elevated. The bug
	// this test guards against was unconditional: no capability grant
	// elevation was needed to trigger it.
	g, agentID, skillSlug := newAuthorizedGate(t, `{}`)
	req := ExecRequest{
		SkillSlug: skillSlug, AgentID: agentID,
		Command: []string{"/bin/sh", "-c", "env"},
		WorkDir: t.TempDir(),
		Kind:    ExecKindMarker, Label: "env-leak-test",
	}
	res, err := g.ExecuteGated(context.Background(), req)
	if err != nil {
		t.Fatalf("ExecuteGated: %v (stdout=%q stderr=%q)", err, res.Stdout, res.Stderr)
	}

	if strings.Contains(res.Stdout, "should-not-leak") {
		t.Errorf("secret-shaped env var value leaked into sandboxed command's environment: stdout=%q", res.Stdout)
	}
	if strings.Contains(res.Stdout, "NANITE_TEST_SECRET_TOKEN") {
		t.Errorf("secret-shaped env var name leaked into sandboxed command's environment: stdout=%q", res.Stdout)
	}

	// Non-secret pass-through: this fix must not become an allowlist that
	// breaks ordinary skill scripts relying on HOME/PATH/LANG/etc.
	if !strings.Contains(res.Stdout, "NANITE_TEST_SAFE_VAR=safe-value-should-pass-through") {
		t.Errorf("expected non-secret env var to pass through unfiltered; stdout=%q", res.Stdout)
	}
	for _, key := range []string{"HOME", "PATH", "LANG"} {
		if os.Getenv(key) == "" {
			continue
		}
		if !strings.Contains(res.Stdout, key+"=") {
			t.Errorf("expected ordinary env var %s to pass through unfiltered; stdout=%q", key, res.Stdout)
		}
	}
}

// ---------------------------------------------------------------------
// Structural proof: this package has exactly one sandbox.Apply call site
// (this file's Gate.run), matching this task's own Done-means grep check.
// ---------------------------------------------------------------------

func TestGate_ExactlyOneSandboxApplyCallInPackage(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	fset := token.NewFileSet()
	count := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		node, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		ast.Inspect(node, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "sandbox" && sel.Sel.Name == "Apply" {
				count++
			}
			return true
		})
	}
	if count != 1 {
		t.Fatalf("expected exactly one sandbox.Apply call in internal/skill's non-test files, got %d", count)
	}
}
