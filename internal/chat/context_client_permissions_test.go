package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/permission"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestAssembleSlotSources_PermissionsSlot_EmptyWhenNothingConfigured asserts
// that the SlotPermissions block is empty when the ContextClient has no
// PathGrants wired and no DevToolsAllowedPaths configured. The slot is
// silently skipped — the header alone is not worth burning tokens on.
// This is the default for tests that don't exercise path-permission
// surfaces. CW-20260512-0118 (SP-20260512-0010 W2).
func TestAssembleSlotSources_PermissionsSlot_EmptyWhenNothingConfigured(t *testing.T) {
	cb, s := newTestBroker(t)
	if err := s.CreateWorkspace(&store.Workspace{ID: "ws1", Name: "Test"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess := &store.Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	agent := &store.AgentProfile{Name: "T", Slug: "test"}
	if err := s.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	sources, err := cb.AssembleSlotSources(context.Background(), sess, agent, nil, &store.Workspace{Name: "WS"}, nil)
	if err != nil {
		t.Fatalf("AssembleSlotSources: %v", err)
	}
	if sources.Permissions != "" {
		t.Errorf("expected empty Permissions slot when no path constraints configured; got:\n%s", sources.Permissions)
	}
}

// TestAssembleSlotSources_PermissionsSlot_BinaryAllowList asserts that the
// SlotPermissions block renders the binary-scoped allow-list when the
// ContextClient has DevToolsAllowedPaths configured. This is the typical
// chat-profile shape — the agent sees its READ roots up front.
func TestAssembleSlotSources_PermissionsSlot_BinaryAllowList(t *testing.T) {
	cb, s := newTestBroker(t)
	cb.DevToolsAllowedPaths = []string{
		"/Users/u/Projects-apps/nanite",
		"/Users/u/Projects-apps/agent-workspaces",
	}

	if err := s.CreateWorkspace(&store.Workspace{ID: "ws1", Name: "Test"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess := &store.Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Slug intentionally empty: this test exercises the GENERIC scope-qualifier
	// path (`agent.Slug == "" || agent.Slug == "default"` in context_client.go).
	// "default" itself would now collide with the seed row from migration 060
	// (CW-20260512-0111, internal profiles file source-of-truth).
	agent := &store.AgentProfile{Name: "default"}
	if err := s.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	sources, err := cb.AssembleSlotSources(context.Background(), sess, agent, nil, &store.Workspace{Name: "WS"}, nil)
	if err != nil {
		t.Fatalf("AssembleSlotSources: %v", err)
	}
	if sources.Permissions == "" {
		t.Fatal("expected non-empty Permissions slot when AllowedPaths configured")
	}
	if !strings.Contains(sources.Permissions, "## Path access") {
		t.Errorf("expected '## Path access' header; got:\n%s", sources.Permissions)
	}
	if !strings.Contains(sources.Permissions, "/Users/u/Projects-apps/nanite/") {
		t.Errorf("expected nanite path in summary; got:\n%s", sources.Permissions)
	}
	if !strings.Contains(sources.Permissions, "this session's scope") {
		t.Errorf("default-slug agent should render generic scope qualifier; got:\n%s", sources.Permissions)
	}
}

// TestAssembleSlotSources_PermissionsSlot_SubagentScope asserts that a non-
// default agent slug renders the subagent scope qualifier in the closing
// refusal hook. The c160 fabrication chain dispatched a `researcher` →
// `worker`-profile subagent; the rendered summary needs to identify the
// scope it's bound to so the agent can refuse referring to "this
// researcher subagent's scope" rather than the generic phrasing.
func TestAssembleSlotSources_PermissionsSlot_SubagentScope(t *testing.T) {
	cb, s := newTestBroker(t)
	cb.DevToolsAllowedPaths = []string{"/Users/u/Projects-apps/nanite"}

	if err := s.CreateWorkspace(&store.Workspace{ID: "ws1", Name: "Test"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess := &store.Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	agent := &store.AgentProfile{Name: "researcher", Slug: "researcher-test"}
	if err := s.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	sources, err := cb.AssembleSlotSources(context.Background(), sess, agent, nil, &store.Workspace{Name: "WS"}, nil)
	if err != nil {
		t.Fatalf("AssembleSlotSources: %v", err)
	}
	if !strings.Contains(sources.Permissions, "this researcher-test subagent's scope") {
		t.Errorf("expected researcher-scoped qualifier; got:\n%s", sources.Permissions)
	}
}

// TestAssembleSlotSources_PermissionsSlot_SessionGrants asserts that
// session-scoped explicit-mention grants render under "session grants"
// when the ContextClient has a PathGrants instance wired.
func TestAssembleSlotSources_PermissionsSlot_SessionGrants(t *testing.T) {
	cb, s := newTestBroker(t)
	cb.PathGrants = permission.NewPathGrants()

	if err := s.CreateWorkspace(&store.Workspace{ID: "ws1", Name: "Test"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess := &store.Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Empty Slug avoids the migration-060 seed-row UNIQUE collision (see
	// BinaryAllowList test above); behaviorally identical to slug "default"
	// for the renderer's generic-phrasing branch.
	agent := &store.AgentProfile{Name: "default"}
	if err := s.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// Stage an explicit grant on the session.
	cb.PathGrants.RegisterFromUserMessage(sess.ID, "look at /tmp/explicit-grant/file.go")

	sources, err := cb.AssembleSlotSources(context.Background(), sess, agent, nil, &store.Workspace{Name: "WS"}, nil)
	if err != nil {
		t.Fatalf("AssembleSlotSources: %v", err)
	}
	if !strings.Contains(sources.Permissions, "You have explicit access to (session grants):") {
		t.Errorf("expected session-grants section; got:\n%s", sources.Permissions)
	}
	if !strings.Contains(sources.Permissions, "/tmp/explicit-grant/file.go") {
		t.Errorf("expected granted path in summary; got:\n%s", sources.Permissions)
	}
}

// TestAssembleSlotSources_PermissionsSlot_LineageGrants asserts that a
// subagent child session sees its parent's path grants rendered as
// "inherited from parent session" — this is the c160 repro shape: a
// worker spawned from a chat that mentioned a path should see that path
// in its summary, not have to discover it via fabrication.
func TestAssembleSlotSources_PermissionsSlot_LineageGrants(t *testing.T) {
	cb, s := newTestBroker(t)
	cb.PathGrants = permission.NewPathGrants()

	if err := s.CreateWorkspace(&store.Workspace{ID: "ws1", Name: "Test"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	parentSess := &store.Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(parentSess); err != nil {
		t.Fatalf("CreateSession parent: %v", err)
	}
	childSess := &store.Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(childSess); err != nil {
		t.Fatalf("CreateSession child: %v", err)
	}
	agent := &store.AgentProfile{Name: "researcher", Slug: "researcher-test"}
	if err := s.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// User in parent thread mentioned a path; spawned researcher should
	// see it through lineage.
	cb.PathGrants.RegisterFromUserMessage(parentSess.ID, "research /tmp/parent-research-area/notes.md")
	cb.PathGrants.RegisterLineage(childSess.ID, parentSess.ID)

	sources, err := cb.AssembleSlotSources(context.Background(), childSess, agent, nil, &store.Workspace{Name: "WS"}, nil)
	if err != nil {
		t.Fatalf("AssembleSlotSources: %v", err)
	}
	if !strings.Contains(sources.Permissions, "You inherit these from the parent session:") {
		t.Errorf("expected lineage section; got:\n%s", sources.Permissions)
	}
	if !strings.Contains(sources.Permissions, "/tmp/parent-research-area/notes.md") {
		t.Errorf("expected parent-mentioned path in lineage; got:\n%s", sources.Permissions)
	}
	if !strings.Contains(sources.Permissions, "(from parent session)") {
		t.Errorf("expected provenance tag '(from parent session)'; got:\n%s", sources.Permissions)
	}
}

// TestAssembleSlotSources_PermissionsSlot_c160_RegressionRepro is the
// load-bearing acceptance test for CW-20260512-0118. Stages the exact
// shape of the c160 turn-16 reproduction: a researcher subagent is
// dispatched, its parent chat mentioned a target path, but the researcher
// is configured for a different workspace and the target is outside its
// scope.
//
// Before this ticket: the researcher saw nothing about its constraints
// in the prompt and fabricated 8.1KB of analysis against training-data
// priors. After this ticket: the researcher sees a SlotPermissions block
// that names its scope and tells it explicitly to "return a failure
// naming the path rather than synthesizing an answer".
//
// The test asserts the prompt SHAPE the fix produces. The behavior shift
// (the LLM actually refusing) is the runtime outcome, but the structural
// invariant — the constraint is visible in the prompt — is what we test
// here. Composes with CW-20260512-0095 (PR #144) which detects fabrication
// at runtime as the downstream backstop; this ticket is the upstream
// PREVENTION layer.
func TestAssembleSlotSources_PermissionsSlot_c160_RegressionRepro(t *testing.T) {
	cb, s := newTestBroker(t)
	cb.PathGrants = permission.NewPathGrants()
	// Researcher's workspace allow-list — nanite only, NOT Fragments Engine.
	cb.DevToolsAllowedPaths = []string{"/Users/u/Projects-apps/nanite"}

	if err := s.CreateWorkspace(&store.Workspace{ID: "ws1", Name: "Test"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	parentSess := &store.Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(parentSess); err != nil {
		t.Fatalf("CreateSession parent: %v", err)
	}
	childSess := &store.Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(childSess); err != nil {
		t.Fatalf("CreateSession child: %v", err)
	}
	researcher := &store.AgentProfile{Name: "researcher", Slug: "researcher-test"}
	if err := s.CreateAgent(researcher); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// Parent chat mentioned the nanite path (in scope) — that's what the
	// child inherits via lineage. The Fragments Engine path was NOT
	// mentioned (out of scope) — and there's no allow-list entry for it.
	cb.PathGrants.RegisterFromUserMessage(parentSess.ID, "research /Users/u/Projects-apps/nanite/internal/")
	cb.PathGrants.RegisterLineage(childSess.ID, parentSess.ID)

	sources, err := cb.AssembleSlotSources(context.Background(), childSess, researcher, nil, &store.Workspace{Name: "WS"}, nil)
	if err != nil {
		t.Fatalf("AssembleSlotSources: %v", err)
	}

	// The summary MUST appear in the rendered slot.
	if sources.Permissions == "" {
		t.Fatal("c160 regression: SlotPermissions empty — researcher will fabricate")
	}

	// The researcher's scope MUST be named, so the closing refusal hook
	// points to the right qualifier.
	if !strings.Contains(sources.Permissions, "this researcher-test subagent's scope") {
		t.Errorf("c160 regression: researcher scope not surfaced; got:\n%s", sources.Permissions)
	}

	// The inherited path MUST appear (the agent should know it has access
	// to what the parent granted). path_grants stores cleaned absolute
	// paths (no trailing /), so the row reads as the literal granted dir.
	if !strings.Contains(sources.Permissions, "/Users/u/Projects-apps/nanite/internal") {
		t.Errorf("c160 regression: inherited parent path missing; got:\n%s", sources.Permissions)
	}

	// The closing refusal hook MUST be present. This is the load-bearing
	// fix — agents read this immediately before deciding whether to
	// attempt a tool call against an inaccessible path.
	if !strings.Contains(sources.Permissions, "return a failure naming the path") {
		t.Errorf("c160 regression: refusal hook missing from summary; got:\n%s", sources.Permissions)
	}

	// Fragments Engine path MUST NOT appear in any "you can access" or
	// "inherited" section — it was never granted. The agent reading this
	// summary should see no permission to touch it. The closing implicit-
	// deny clause ("any path not listed above is outside scope") covers
	// the gap.
	if strings.Contains(sources.Permissions, "Fragments Engine") {
		t.Errorf("c160 regression: Fragments Engine path leaked into summary; got:\n%s", sources.Permissions)
	}
}

// TestAssembleSlotSources_PermissionsSlot_W3ForwardedDeniesRendered is the
// W2 ↔ W3 integration acceptance (CW-20260512-0119, SP-20260512-0010 W3).
//
// Stages a subagent child session whose PathGrants store has a derived
// RuleSet registered (the shape the subagent runner produces at spawn
// time). The rendered SlotPermissions block MUST surface the forwarded
// denies under "You CANNOT access (explicitly denied)" with the parent's
// Source tag preserved + the "(via parent)" provenance suffix.
//
// This closes the visibility loop opened by W2's reserved hook:
// SlotPermissions now displays parent-forwarded denies so the agent
// reads "denied by parent profile (via parent)" immediately before
// deciding whether to attempt a tool call.
func TestAssembleSlotSources_PermissionsSlot_W3ForwardedDeniesRendered(t *testing.T) {
	cb, s := newTestBroker(t)
	cb.PathGrants = permission.NewPathGrants()

	if err := s.CreateWorkspace(&store.Workspace{ID: "ws1", Name: "Test"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	childSess := &store.Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(childSess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	researcher := &store.AgentProfile{Name: "researcher", Slug: "researcher-test"}
	if err := s.CreateAgent(researcher); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// Stage the derivation the runner would have done at spawn time.
	parentRules := &permission.RuleSet{
		Rules: []permission.Rule{
			{
				Tool:     "dev_read",
				Pattern:  "/Users/u/sensitive/**",
				Behavior: permission.DecisionDeny,
				Source:   "parent profile.yaml",
			},
		},
	}
	derived, err := permission.DeriveSubagentRuleSet(permission.DerivationInput{
		Parent: parentRules,
	})
	if err != nil {
		t.Fatalf("DeriveSubagentRuleSet: %v", err)
	}
	cb.PathGrants.RegisterDerivedRules(childSess.ID, derived)

	sources, err := cb.AssembleSlotSources(context.Background(), childSess, researcher, nil, &store.Workspace{Name: "WS"}, nil)
	if err != nil {
		t.Fatalf("AssembleSlotSources: %v", err)
	}

	if !strings.Contains(sources.Permissions, "You CANNOT access (explicitly denied):") {
		t.Errorf("expected forwarded denies under explicit-deny section; got:\n%s", sources.Permissions)
	}
	if !strings.Contains(sources.Permissions, "/Users/u/sensitive/**") {
		t.Errorf("expected forwarded deny pattern in rendered output; got:\n%s", sources.Permissions)
	}
	// Parent Source tag preserved (and W3 provenance suffix appended).
	if !strings.Contains(sources.Permissions, "parent profile.yaml (via parent)") {
		t.Errorf("expected parent Source tag preserved with (via parent) suffix; got:\n%s", sources.Permissions)
	}
	// Researcher scope qualifier present.
	if !strings.Contains(sources.Permissions, "this researcher-test subagent's scope") {
		t.Errorf("expected researcher scope in closing refusal hook; got:\n%s", sources.Permissions)
	}
}

// TestAssembleSlotSources_PermissionsSlot_DeterministicAcrossTurns asserts
// that two consecutive calls with identical inputs produce byte-identical
// Permissions slot content — the cache-key invariant the slot
// infrastructure depends on. Permission summaries that vary across turns
// would force a cache miss every turn, defeating the SlotPermissions
// purpose.
func TestAssembleSlotSources_PermissionsSlot_DeterministicAcrossTurns(t *testing.T) {
	cb, s := newTestBroker(t)
	cb.DevToolsAllowedPaths = []string{"/a", "/b"}
	cb.PathGrants = permission.NewPathGrants()

	if err := s.CreateWorkspace(&store.Workspace{ID: "ws1", Name: "Test"}); err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	sess := &store.Session{WorkspaceID: "ws1"}
	if err := s.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Empty Slug — same reason as BinaryAllowList test (migration-060 seed
	// collision with slug "default"). Generic-phrasing branch still fires.
	agent := &store.AgentProfile{Name: "default"}
	if err := s.CreateAgent(agent); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	cb.PathGrants.RegisterFromUserMessage(sess.ID, "look at /tmp/some/path.go")

	a, err := cb.AssembleSlotSources(context.Background(), sess, agent, nil, &store.Workspace{Name: "WS"}, nil)
	if err != nil {
		t.Fatalf("AssembleSlotSources (a): %v", err)
	}
	b, err := cb.AssembleSlotSources(context.Background(), sess, agent, nil, &store.Workspace{Name: "WS"}, nil)
	if err != nil {
		t.Fatalf("AssembleSlotSources (b): %v", err)
	}
	if a.Permissions != b.Permissions {
		t.Errorf("non-deterministic Permissions slot:\na:\n%s\nb:\n%s", a.Permissions, b.Permissions)
	}
}
