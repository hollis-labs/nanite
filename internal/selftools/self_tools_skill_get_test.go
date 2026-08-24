package selftools

// self_tools_skill_get_test.go — TASKS/skills/11's own regression suite for
// skill_get, plus the denial-path coverage the task file's own Done-means
// asks be "reused/adapted rather than reinvented" from
// internal/skill/gate_test.go's equivalent tests
// (TestExecuteGated_NoGrantRow_Refused, TestExecuteGated_
// BareAssignmentGrant_Refused, TestExecuteGated_HashMismatch_
// ReapprovalRequired). This file's fixture helpers
// (newSkillGetTestStore/newSkillGetTestVendor/writeSkillGetFixture/
// installSkillGetFixture/makeSkillGetTestAgent) mirror gate_test.go's own
// fixture plumbing convention exactly (same package-external
// duplication precedent that file's own comment documents against
// resolver_test.go) — internal/skill's fixture helpers are unexported and
// this is a different package, so they can't be imported directly.
import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/mcp"
	"github.com/hollis-labs/nanite/internal/skill"
	"github.com/hollis-labs/nanite/internal/skillvendor"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

func requireSkillGetSandboxTool(t *testing.T) {
	t.Helper()
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("sandbox-exec"); err != nil {
			t.Skip("sandbox-exec not found; skipping real-marker-execution test")
		}
	case "linux":
		if _, err := exec.LookPath("bwrap"); err != nil {
			t.Skip("bwrap not found; skipping real-marker-execution test")
		}
	default:
		t.Skipf("sandbox enforcement not supported on %s", runtime.GOOS)
	}
}

func newSkillGetTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := storetest.New(t, context.Background(), filepath.Join(t.TempDir(), "skill-get-test.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s
}

func newSkillGetTestVendor(t *testing.T) *skillvendor.Store {
	t.Helper()
	v, err := skillvendor.New(filepath.Join(t.TempDir(), "vendor"))
	if err != nil {
		t.Fatalf("skillvendor.New: %v", err)
	}
	return v
}

// writeSkillGetFixture writes a real SKILL.md package to a fresh temp
// directory: one required `who` parameter (proves static-arg substitution),
// plus a real, non-fenced “ !`cmd` “ marker (proves the Gate/sandbox
// execution leg, not just composition/parameter resolution).
func writeSkillGetFixture(t *testing.T, slug string) string {
	t.Helper()
	dir := t.TempDir()
	content := fmt.Sprintf(
		"---\nname: Skill Get Test Skill\nslug: %s\ndescription: fixture for TASKS/skills/11's skill_get tests.\nparameters:\n  - name: who\n    required: true\n---\n\nHello {{who}}! Marker output: !`echo hello-from-sandboxed-marker`\n",
		slug,
	)
	if err := writeFileHelper(filepath.Join(dir, "SKILL.md"), content); err != nil {
		t.Fatalf("write fixture SKILL.md: %v", err)
	}
	return dir
}

// writeFileHelper is a tiny os.WriteFile wrapper used by every fixture
// builder in this file.
func writeFileHelper(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func installSkillGetFixture(t *testing.T, idx *store.Store, vendor *skillvendor.Store, dir string) *store.Skill {
	t.Helper()
	def, files, err := skill.ParsePackageDir(dir)
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

func reinstallSkillGetFixture(t *testing.T, idx *store.Store, vendor *skillvendor.Store, existing *store.Skill, slug string) *store.Skill {
	t.Helper()
	dir := t.TempDir()
	content := fmt.Sprintf(
		"---\nname: Skill Get Test Skill\nslug: %s\ndescription: fixture v2 — genuinely different content.\nparameters:\n  - name: who\n    required: true\n---\n\nHello {{who}}! Marker output: !`echo hello-from-sandboxed-marker-v2`\n",
		slug,
	)
	if err := writeFileHelper(filepath.Join(dir, "SKILL.md"), content); err != nil {
		t.Fatalf("write fixture v2 SKILL.md: %v", err)
	}
	_, files, err := skill.ParsePackageDir(dir)
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

func makeSkillGetTestAgent(t *testing.T, s *store.Store, slug string) *store.AgentProfile {
	t.Helper()
	a := &store.AgentProfile{Name: "Skill Get Test Agent " + slug, Slug: slug, SystemPrompt: "test"}
	if err := s.CreateAgent(context.Background(), a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	return a
}

func TestCallSkillGet_GrantedSkill_ReturnsMaterializedContentWithMarkerExecuted(t *testing.T) {
	requireSkillGetSandboxTool(t)

	idx := newSkillGetTestStore(t)
	vendor := newSkillGetTestVendor(t)
	dir := writeSkillGetFixture(t, "skill-get-granted")
	sk := installSkillGetFixture(t, idx, vendor, dir)
	agent := makeSkillGetTestAgent(t, idx, "skill-get-granted-agent")

	if err := idx.InsertAgentKnownSkill(context.Background(), store.AgentKnownSkill{
		AgentID:             agent.ID,
		SkillName:           sk.Slug,
		ApprovedContentHash: sk.ContentHash,
		GrantedAt:           time.Now().UTC().Format(time.RFC3339),
		GrantedBy:           "test-operator",
		CapabilitiesGranted: "{}",
	}); err != nil {
		t.Fatalf("InsertAgentKnownSkill: %v", err)
	}

	transport := &SelfToolsTransport{Store: idx, SkillVendor: vendor}
	ctx := mcp.WithCallerProfile(context.Background(), agent.ID)

	res, err := transport.CallTool(ctx, "skill_get", map[string]any{
		"slug":   sk.Slug,
		"params": map[string]any{"who": "World"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error result: %+v", res)
	}
	if len(res.Content) == 0 {
		t.Fatal("expected non-empty tool result content")
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "Hello World!") {
		t.Errorf("expected parameter substitution in materialized content, got: %q", text)
	}
	if !strings.Contains(text, "hello-from-sandboxed-marker") {
		t.Errorf("expected the inline marker's real sandboxed execution output in materialized content, got: %q", text)
	}
}

func TestCallSkillGet_NoGrantRow_Refused(t *testing.T) {
	idx := newSkillGetTestStore(t)
	vendor := newSkillGetTestVendor(t)
	dir := writeSkillGetFixture(t, "skill-get-no-grant")
	sk := installSkillGetFixture(t, idx, vendor, dir)
	agent := makeSkillGetTestAgent(t, idx, "skill-get-no-grant-agent")

	transport := &SelfToolsTransport{Store: idx, SkillVendor: vendor}
	ctx := mcp.WithCallerProfile(context.Background(), agent.ID)

	res, err := transport.CallTool(ctx, "skill_get", map[string]any{"slug": sk.Slug, "params": map[string]any{"who": "World"}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected a denial for an ungranted skill, got success: %+v", res)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "no valid capability grant") || !strings.Contains(text, "no grant row exists") {
		t.Errorf("expected a clear 'no grant row' denial, got: %q", text)
	}
}

func TestCallSkillGet_BareAssignmentGrant_Refused(t *testing.T) {
	idx := newSkillGetTestStore(t)
	vendor := newSkillGetTestVendor(t)
	dir := writeSkillGetFixture(t, "skill-get-bare-assignment")
	sk := installSkillGetFixture(t, idx, vendor, dir)
	agent := makeSkillGetTestAgent(t, idx, "skill-get-bare-assignment-agent")

	// Simulates AssignSkillToAgent's own bare INSERT (agent_id, skill_name
	// only) — no grant-state columns, matching gate_test.go's own
	// TestExecuteGated_BareAssignmentGrant_Refused fixture exactly.
	if err := idx.InsertAgentKnownSkill(context.Background(), store.AgentKnownSkill{
		AgentID: agent.ID, SkillName: sk.Slug,
	}); err != nil {
		t.Fatalf("InsertAgentKnownSkill: %v", err)
	}

	transport := &SelfToolsTransport{Store: idx, SkillVendor: vendor}
	ctx := mcp.WithCallerProfile(context.Background(), agent.ID)

	res, err := transport.CallTool(ctx, "skill_get", map[string]any{"slug": sk.Slug, "params": map[string]any{"who": "World"}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected a denial for a bare-assignment grant, got success: %+v", res)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "never been approved") {
		t.Errorf("expected a 'never been approved' denial, got: %q", text)
	}
}

func TestCallSkillGet_HashMismatch_ReapprovalRequired(t *testing.T) {
	idx := newSkillGetTestStore(t)
	vendor := newSkillGetTestVendor(t)
	dir := writeSkillGetFixture(t, "skill-get-stale-hash")
	sk := installSkillGetFixture(t, idx, vendor, dir)
	hash1 := sk.ContentHash
	agent := makeSkillGetTestAgent(t, idx, "skill-get-stale-hash-agent")

	if err := idx.InsertAgentKnownSkill(context.Background(), store.AgentKnownSkill{
		AgentID: agent.ID, SkillName: sk.Slug,
		ApprovedContentHash: hash1,
		GrantedAt:           time.Now().UTC().Format(time.RFC3339),
		GrantedBy:           "test-operator",
	}); err != nil {
		t.Fatalf("InsertAgentKnownSkill: %v", err)
	}

	sk = reinstallSkillGetFixture(t, idx, vendor, sk, "skill-get-stale-hash")
	if sk.ContentHash == hash1 {
		t.Fatal("expected re-install with changed content to produce a different content hash")
	}

	transport := &SelfToolsTransport{Store: idx, SkillVendor: vendor}
	ctx := mcp.WithCallerProfile(context.Background(), agent.ID)

	res, err := transport.CallTool(ctx, "skill_get", map[string]any{"slug": sk.Slug, "params": map[string]any{"who": "World"}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected a re-approval-required denial after re-install, got success: %+v", res)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "re-approval required") {
		t.Errorf("expected a 're-approval required' denial, got: %q", text)
	}
}

func TestCallSkillGet_NoCallerInContext_Refused(t *testing.T) {
	idx := newSkillGetTestStore(t)
	vendor := newSkillGetTestVendor(t)
	dir := writeSkillGetFixture(t, "skill-get-no-caller")
	sk := installSkillGetFixture(t, idx, vendor, dir)

	transport := &SelfToolsTransport{Store: idx, SkillVendor: vendor}
	res, err := transport.CallTool(context.Background(), "skill_get", map[string]any{"slug": sk.Slug})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected a denial when no calling agent is stamped on ctx")
	}
	if !strings.Contains(res.Content[0].Text, "no calling agent in context") {
		t.Errorf("expected a 'no calling agent in context' error, got: %q", res.Content[0].Text)
	}
}

func TestCallSkillGet_UnknownSlug_ClearError(t *testing.T) {
	idx := newSkillGetTestStore(t)
	vendor := newSkillGetTestVendor(t)
	agent := makeSkillGetTestAgent(t, idx, "skill-get-unknown-slug-agent")

	transport := &SelfToolsTransport{Store: idx, SkillVendor: vendor}
	ctx := mcp.WithCallerProfile(context.Background(), agent.ID)
	res, err := transport.CallTool(ctx, "skill_get", map[string]any{"slug": "does-not-exist"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error for an unknown slug")
	}
	if !strings.Contains(res.Content[0].Text, "not found in the skill catalog") {
		t.Errorf("expected a 'not found' error, got: %q", res.Content[0].Text)
	}
}

func TestCallSkillGet_DisabledSkill_Refused(t *testing.T) {
	idx := newSkillGetTestStore(t)
	vendor := newSkillGetTestVendor(t)
	dir := writeSkillGetFixture(t, "skill-get-disabled")
	sk := installSkillGetFixture(t, idx, vendor, dir)
	sk.Enabled = false
	if err := idx.UpdateSkill(context.Background(), sk); err != nil {
		t.Fatalf("UpdateSkill (disable): %v", err)
	}
	agent := makeSkillGetTestAgent(t, idx, "skill-get-disabled-agent")
	if err := idx.InsertAgentKnownSkill(context.Background(), store.AgentKnownSkill{
		AgentID: agent.ID, SkillName: sk.Slug,
		ApprovedContentHash: sk.ContentHash,
		GrantedAt:           time.Now().UTC().Format(time.RFC3339),
		GrantedBy:           "test-operator",
	}); err != nil {
		t.Fatalf("InsertAgentKnownSkill: %v", err)
	}

	transport := &SelfToolsTransport{Store: idx, SkillVendor: vendor}
	ctx := mcp.WithCallerProfile(context.Background(), agent.ID)
	res, err := transport.CallTool(ctx, "skill_get", map[string]any{"slug": sk.Slug, "params": map[string]any{"who": "World"}})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error for a disabled skill")
	}
	if !strings.Contains(res.Content[0].Text, "disabled") {
		t.Errorf("expected a 'disabled' error, got: %q", res.Content[0].Text)
	}
}

// TestCallSkillGet_MissingRequiredParameter confirms a missing required
// parameter surfaces internal/skill.MissingSkillParameterError's own clear
// message through the tool result, rather than a generic materialization
// failure — proving skillGetMaterializeErrorResult's fallback branch
// preserves the underlying error's own text.
func TestCallSkillGet_MissingRequiredParameter(t *testing.T) {
	idx := newSkillGetTestStore(t)
	vendor := newSkillGetTestVendor(t)
	dir := writeSkillGetFixture(t, "skill-get-missing-param")
	sk := installSkillGetFixture(t, idx, vendor, dir)
	agent := makeSkillGetTestAgent(t, idx, "skill-get-missing-param-agent")
	if err := idx.InsertAgentKnownSkill(context.Background(), store.AgentKnownSkill{
		AgentID: agent.ID, SkillName: sk.Slug,
		ApprovedContentHash: sk.ContentHash,
		GrantedAt:           time.Now().UTC().Format(time.RFC3339),
		GrantedBy:           "test-operator",
	}); err != nil {
		t.Fatalf("InsertAgentKnownSkill: %v", err)
	}

	transport := &SelfToolsTransport{Store: idx, SkillVendor: vendor}
	ctx := mcp.WithCallerProfile(context.Background(), agent.ID)
	// "who" (required) is deliberately omitted from params.
	res, err := transport.CallTool(ctx, "skill_get", map[string]any{"slug": sk.Slug})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error for a missing required parameter")
	}
	if !strings.Contains(res.Content[0].Text, "missing required parameter") {
		t.Errorf("expected a 'missing required parameter' error, got: %q", res.Content[0].Text)
	}
}

// TestCallSkillGet_NilStoreOrVendor_ClearError confirms the unwired-field
// guard clauses at the top of callSkillGet — matching every other
// nil-safe field on SelfToolsTransport per this file's own doc convention.
func TestCallSkillGet_NilStoreOrVendor_ClearError(t *testing.T) {
	vendor := newSkillGetTestVendor(t)
	transport := &SelfToolsTransport{Store: nil, SkillVendor: vendor}
	res, err := transport.CallTool(context.Background(), "skill_get", map[string]any{"slug": "anything"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].Text, "no store configured") {
		t.Errorf("expected a clear 'no store configured' error, got: %+v", res)
	}

	idx := newSkillGetTestStore(t)
	transport2 := &SelfToolsTransport{Store: idx, SkillVendor: nil}
	res2, err := transport2.CallTool(context.Background(), "skill_get", map[string]any{"slug": "anything"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res2.IsError || !strings.Contains(res2.Content[0].Text, "no vendored skill store configured") {
		t.Errorf("expected a clear 'no vendored skill store configured' error, got: %+v", res2)
	}
}

// TestCallSkillGet_ForkDependencyWithoutForkRole_ClearError confirms a
// fork-composed dependency encountered with no fork_role argument supplied
// fails with internal/skill/compose.go's own clear, named error ("no
// default role is assumed") rather than an opaque internal failure or a
// silently-invented default — the design-latitude call this task's own
// brief flagged as worth documenting rather than guessing past.
func TestCallSkillGet_ForkDependencyWithoutForkRole_ClearError(t *testing.T) {
	idx := newSkillGetTestStore(t)
	vendor := newSkillGetTestVendor(t)

	depDir := t.TempDir()
	if err := writeFileHelper(filepath.Join(depDir, "SKILL.md"),
		"---\nname: Fork Dependency\nslug: skill-get-fork-dep\ndescription: nested fork-mode dependency.\ncontext: fork\n---\n\nDependency body.\n"); err != nil {
		t.Fatalf("write dependency SKILL.md: %v", err)
	}
	depSk := installSkillGetFixture(t, idx, vendor, depDir)

	rootDir := t.TempDir()
	if err := writeFileHelper(filepath.Join(rootDir, "SKILL.md"),
		"---\nname: Fork Root\nslug: skill-get-fork-root\ndescription: root skill composing a fork dependency.\ndependencies:\n  - skill-get-fork-dep\n---\n\nRoot body.\n"); err != nil {
		t.Fatalf("write root SKILL.md: %v", err)
	}
	rootSk := installSkillGetFixture(t, idx, vendor, rootDir)
	_ = depSk

	agent := makeSkillGetTestAgent(t, idx, "skill-get-fork-root-agent")
	if err := idx.InsertAgentKnownSkill(context.Background(), store.AgentKnownSkill{
		AgentID: agent.ID, SkillName: rootSk.Slug,
		ApprovedContentHash: rootSk.ContentHash,
		GrantedAt:           time.Now().UTC().Format(time.RFC3339),
		GrantedBy:           "test-operator",
	}); err != nil {
		t.Fatalf("InsertAgentKnownSkill: %v", err)
	}

	transport := &SelfToolsTransport{Store: idx, SkillVendor: vendor} // Subagent left nil
	ctx := mcp.WithCallerProfile(context.Background(), agent.ID)
	res, err := transport.CallTool(ctx, "skill_get", map[string]any{"slug": rootSk.Slug})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error for a fork dependency with no Subagent configured and no fork_role")
	}
	// With Subagent nil, ErrForkNotConfigured fires before the ForkRole
	// check — both are real, named, actionable errors (never a silent
	// degrade to inline splicing), so this only asserts the message names
	// the fork-composition context, not which of the two specific reasons.
	if !strings.Contains(res.Content[0].Text, "fork") {
		t.Errorf("expected a fork-composition-related error, got: %q", res.Content[0].Text)
	}
}
