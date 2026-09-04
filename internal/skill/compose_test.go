package skill

// TASKS/skills/07-implement-inline-fork-composition-semantics.md's own
// Done-means:
//   - a real inline-composed skill (two installed test packages, one
//     declaring the other as an inline dependency) materializes with
//     the nested content spliced in correctly;
//   - a real fork-composed skill delegates to an actual subagent/fork
//     invocation (not mocked) and folds the result back correctly;
//   - a provenance chain is available for a real multi-level composed
//     materialization, showing the full root -> nested -> ... path.
//
// Install-time cycle/recursion-limit rejection (this same task's other
// half) is covered in internal/skillinstall/dependency_graph_test.go,
// not here — this file is the Materializer's composition semantics
// only.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/subagent"
)

// composeFixturesDir reuses the same testdata root resolver_test.go
// already established for this package (resolverFixturesDir) — kept as
// its own named constant here purely for this file's own readability,
// not because the directory differs.
const composeFixturesDir = resolverFixturesDir

// parseFixtureDef parses a fixture package directory's own Definition,
// standing in for "the root skill's already-parsed Definition" a real
// caller (a future skill_get self-tool, task 11) would have obtained by
// reading its own vendored copy — MaterializeSkill's own contract starts
// from an already-parsed Definition, not a raw path.
func parseFixtureDef(t *testing.T, name string) Definition {
	t.Helper()
	def, _, err := ParsePackageDir(filepath.Join(composeFixturesDir, name))
	if err != nil {
		t.Fatalf("ParsePackageDir(%s): %v", name, err)
	}
	return *def
}

func TestMaterializeSkill_Inline_SplicesContentAndSubstitutesParameters(t *testing.T) {
	idx := newResolverTestStore(t)
	vendor := newResolverTestVendor(t)

	installFixture(t, idx, vendor, filepath.Join(composeFixturesDir, "compose-inline-child"))
	installFixture(t, idx, vendor, filepath.Join(composeFixturesDir, "compose-inline-parent"))

	parent := parseFixtureDef(t, "compose-inline-parent")

	result, err := MaterializeSkill(context.Background(), MaterializerDeps{
		Index:  idx,
		Vendor: vendor,
	}, parent, MaterializeInput{
		StaticArgs: map[string]string{"target": "widget-42"},
	})
	if err != nil {
		t.Fatalf("MaterializeSkill: %v", err)
	}

	if !strings.Contains(result.Content, "Parent body: follow the composed child's instructions below.") {
		t.Errorf("Content missing the parent's own body:\n%s", result.Content)
	}
	if !strings.Contains(result.Content, "## Composed skill: compose-inline-child (inline)") {
		t.Errorf("Content missing the inline composition header:\n%s", result.Content)
	}
	if !strings.Contains(result.Content, "operate on widget-42") {
		t.Errorf("Content missing the substituted {{target}} parameter value:\n%s", result.Content)
	}
	if strings.Contains(result.Content, "{{target}}") {
		t.Errorf("Content still contains an unsubstituted {{target}} placeholder:\n%s", result.Content)
	}

	wantChain := ProvenanceChain{
		{Slug: "compose-inline-parent", Kind: "root"},
		{Slug: "compose-inline-child", Kind: "inline"},
	}
	if len(result.Chain) != len(wantChain) {
		t.Fatalf("Chain = %v, want %v", result.Chain, wantChain)
	}
	for i := range wantChain {
		if result.Chain[i] != wantChain[i] {
			t.Errorf("Chain[%d] = %+v, want %+v", i, result.Chain[i], wantChain[i])
		}
	}
}

func TestMaterializeSkill_Inline_MissingRequiredParameter_FailsWithCompositionError(t *testing.T) {
	idx := newResolverTestStore(t)
	vendor := newResolverTestVendor(t)

	installFixture(t, idx, vendor, filepath.Join(composeFixturesDir, "compose-inline-child"))
	installFixture(t, idx, vendor, filepath.Join(composeFixturesDir, "compose-inline-parent"))

	parent := parseFixtureDef(t, "compose-inline-parent")

	// No StaticArgs supplied — compose-inline-child's required "target"
	// parameter has no static value and no ResolverSlot binding.
	_, err := MaterializeSkill(context.Background(), MaterializerDeps{
		Index:  idx,
		Vendor: vendor,
	}, parent, MaterializeInput{})
	if err == nil {
		t.Fatal("expected a materialization failure for the child's missing required parameter")
	}
	var cerr *CompositionError
	if !errors.As(err, &cerr) {
		t.Fatalf("expected a *CompositionError, got %T: %v", err, err)
	}
	if cerr.Skill != "compose-inline-child" {
		t.Errorf("CompositionError.Skill = %q, want %q (should attribute the failure to the child, not the parent)", cerr.Skill, "compose-inline-child")
	}
	var perr *MissingSkillParameterError
	if !errors.As(cerr, &perr) {
		t.Fatalf("expected the underlying error to be a *MissingSkillParameterError, got %T: %v", cerr.Err, cerr.Err)
	}
}

// capturingRunner is a real subagent.Runner test double that records
// every prompt it's invoked with and returns a genuinely structured
// ResultJSON that deliberately does NOT echo the prompt back verbatim
// (not the {"summary": ...} fallback shape either) — used where a test
// needs to assert both exactly what a fork-delegated subagent received
// (via prompts) and that only the delegated invocation's own, distinct
// result folds back into the parent's materialized output — never the
// raw instructional text it was given, and never subagent.EchoRunner's
// own reflect-the-prompt-back behavior, which would make "did the raw
// text leak through" and "did the runner's result get folded back"
// indistinguishable from each other.
type capturingRunner struct {
	mu      sync.Mutex
	prompts []string
}

func (r *capturingRunner) Run(_ context.Context, run *subagent.Run) (*subagent.Result, error) {
	r.mu.Lock()
	r.prompts = append(r.prompts, run.Prompt)
	r.mu.Unlock()
	payload, _ := json.Marshal(map[string]any{
		"outcome":       "delegated work completed",
		"prompt_length": len(run.Prompt),
	})
	return &subagent.Result{Summary: "captured", ResultJSON: string(payload)}, nil
}

func TestMaterializeSkill_Fork_DelegatesToRealSubagentAndFoldsBackResult(t *testing.T) {
	idx := newResolverTestStore(t)
	vendor := newResolverTestVendor(t)

	installFixture(t, idx, vendor, filepath.Join(composeFixturesDir, "compose-fork-child"))
	installFixture(t, idx, vendor, filepath.Join(composeFixturesDir, "compose-fork-parent"))

	runner := &capturingRunner{}
	svc := subagent.NewService(idx.DB, runner, nil, nil, nil)

	parent := parseFixtureDef(t, "compose-fork-parent")

	result, err := MaterializeSkill(context.Background(), MaterializerDeps{
		Index:    idx,
		Vendor:   vendor,
		Subagent: svc,
	}, parent, MaterializeInput{
		ParentSessionID: "sess-compose-fork-test",
		ParentAgentID:   "agent-compose-fork-test",
		ForkRole:        "fork-composed-worker",
	})
	if err != nil {
		t.Fatalf("MaterializeSkill: %v", err)
	}

	// The real subagent machinery actually ran: the fork child's own
	// materialized content became the delegated subagent's Prompt.
	if len(runner.prompts) != 1 {
		t.Fatalf("expected exactly one real subagent dispatch, got %d", len(runner.prompts))
	}
	if !strings.Contains(runner.prompts[0], "Fork child body:") {
		t.Errorf("delegated subagent Prompt missing the fork child's own materialized content: %q", runner.prompts[0])
	}

	// Only the delegated invocation's RESULT folds back — not its raw
	// instructional text, and not a full transcript.
	if strings.Contains(result.Content, "this text becomes the delegated subagent's own task prompt") {
		t.Errorf("Content leaked the fork child's raw instructional text verbatim — fork must fold back only the result:\n%s", result.Content)
	}
	if !strings.Contains(result.Content, "delegated work completed") {
		t.Errorf("Content missing the delegated subagent's real structured result:\n%s", result.Content)
	}
	if !strings.Contains(result.Content, "## Composed skill: compose-fork-child (fork)") {
		t.Errorf("Content missing the fork composition header:\n%s", result.Content)
	}
	if !strings.Contains(result.Content, "Parent body: delegate the composed child skill to its own agent turn.") {
		t.Errorf("Content missing the parent's own body:\n%s", result.Content)
	}

	wantChain := ProvenanceChain{
		{Slug: "compose-fork-parent", Kind: "root"},
		{Slug: "compose-fork-child", Kind: "fork"},
	}
	if len(result.Chain) != len(wantChain) {
		t.Fatalf("Chain = %v, want %v", result.Chain, wantChain)
	}
	for i := range wantChain {
		if result.Chain[i] != wantChain[i] {
			t.Errorf("Chain[%d] = %+v, want %+v", i, result.Chain[i], wantChain[i])
		}
	}
}

func TestMaterializeSkill_Fork_NoSubagentConfigured_FailsClearly(t *testing.T) {
	idx := newResolverTestStore(t)
	vendor := newResolverTestVendor(t)

	installFixture(t, idx, vendor, filepath.Join(composeFixturesDir, "compose-fork-child"))
	installFixture(t, idx, vendor, filepath.Join(composeFixturesDir, "compose-fork-parent"))

	parent := parseFixtureDef(t, "compose-fork-parent")

	_, err := MaterializeSkill(context.Background(), MaterializerDeps{
		Index:  idx,
		Vendor: vendor,
		// Subagent intentionally left nil.
	}, parent, MaterializeInput{
		ParentSessionID: "sess-1",
		ForkRole:        "worker",
	})
	if err == nil {
		t.Fatal("expected a clear failure when fork composition is needed but no SubagentDispatcher is configured")
	}
	var cerr *CompositionError
	if !errors.As(err, &cerr) {
		t.Fatalf("expected a *CompositionError, got %T: %v", err, err)
	}
	if !errors.Is(cerr, ErrForkNotConfigured) {
		t.Errorf("expected the underlying error to wrap ErrForkNotConfigured, got: %v", cerr.Err)
	}
}

func TestMaterializeSkill_Fork_MissingParentSessionID_FailsClearly(t *testing.T) {
	idx := newResolverTestStore(t)
	vendor := newResolverTestVendor(t)

	installFixture(t, idx, vendor, filepath.Join(composeFixturesDir, "compose-fork-child"))
	installFixture(t, idx, vendor, filepath.Join(composeFixturesDir, "compose-fork-parent"))

	runner := &capturingRunner{}
	svc := subagent.NewService(idx.DB, runner, nil, nil, nil)
	parent := parseFixtureDef(t, "compose-fork-parent")

	_, err := MaterializeSkill(context.Background(), MaterializerDeps{
		Index:    idx,
		Vendor:   vendor,
		Subagent: svc,
	}, parent, MaterializeInput{
		ForkRole: "worker", // ParentSessionID intentionally omitted.
	})
	if err == nil {
		t.Fatal("expected a clear failure for fork composition with no ParentSessionID")
	}
	if len(runner.prompts) != 0 {
		t.Error("no subagent should have been dispatched without a ParentSessionID")
	}
}

// TestMaterializeSkill_MultiLevelProvenanceChain_CrossesInlineAndFork is
// this task's Done-means "provenance chain... for a real multi-level
// composed materialization" requirement: root (inline-composes mid) ->
// mid (fork-composed by root, itself inline-composes leaf) -> leaf.
// Proves both that mid's OWN inline composition of leaf happens before
// mid is forked (the delegated subagent's prompt contains leaf's
// content) and that the full root -> mid -> leaf chain is reported.
func TestMaterializeSkill_MultiLevelProvenanceChain_CrossesInlineAndFork(t *testing.T) {
	idx := newResolverTestStore(t)
	vendor := newResolverTestVendor(t)

	installFixture(t, idx, vendor, filepath.Join(composeFixturesDir, "compose-multilevel-leaf"))
	installFixture(t, idx, vendor, filepath.Join(composeFixturesDir, "compose-multilevel-mid"))
	installFixture(t, idx, vendor, filepath.Join(composeFixturesDir, "compose-multilevel-root"))

	runner := &capturingRunner{}
	svc := subagent.NewService(idx.DB, runner, nil, nil, nil)
	root := parseFixtureDef(t, "compose-multilevel-root")

	result, err := MaterializeSkill(context.Background(), MaterializerDeps{
		Index:    idx,
		Vendor:   vendor,
		Subagent: svc,
	}, root, MaterializeInput{
		ParentSessionID: "sess-multilevel",
		ForkRole:        "worker",
	})
	if err != nil {
		t.Fatalf("MaterializeSkill: %v", err)
	}

	// The mid skill's own recursive materialization (inline-composing
	// the leaf) happened BEFORE it was forked — the delegated subagent's
	// prompt carries the leaf's content, proving fork delegates the
	// nested skill's already-fully-materialized content, not just its
	// bare, uncomposed body.
	if len(runner.prompts) != 1 {
		t.Fatalf("expected exactly one real subagent dispatch (for the mid-level fork), got %d", len(runner.prompts))
	}
	if !strings.Contains(runner.prompts[0], "Mid body") {
		t.Errorf("delegated subagent prompt missing the mid skill's own body: %q", runner.prompts[0])
	}
	if !strings.Contains(runner.prompts[0], "Leaf body.") {
		t.Errorf("delegated subagent prompt missing the leaf's inline-composed content (mid must materialize its own dependency before being forked): %q", runner.prompts[0])
	}

	wantChain := ProvenanceChain{
		{Slug: "compose-multilevel-root", Kind: "root"},
		{Slug: "compose-multilevel-mid", Kind: "fork"},
		{Slug: "compose-multilevel-leaf", Kind: "inline"},
	}
	if len(result.Chain) != len(wantChain) {
		t.Fatalf("Chain = %v, want %v", result.Chain, wantChain)
	}
	for i := range wantChain {
		if result.Chain[i] != wantChain[i] {
			t.Errorf("Chain[%d] = %+v, want %+v", i, result.Chain[i], wantChain[i])
		}
	}
	t.Logf("full materialized output:\n%s", result.Content)
}

// gatedSettingsReader forces SubagentApprovalRequired=true so
// subagent.Service's approval-gate predicate fires — mirrors
// internal/selftools's own established gated-test construction pattern
// (self_tools_subagent_envelope_test.go's gatedSettingsReader),
// reproduced here (rather than exported cross-package) purely for this
// file's own DI need.
type gatedSettingsReader struct{ us store.UserSettings }

func (g gatedSettingsReader) GetUserSettings(ctx context.Context) (*store.UserSettings, error) {
	cp := g.us
	return &cp, nil
}

// gatedApprovalEmitter records emit calls and returns a stable envelope
// id, letting Spawn's gated path complete without a real approval
// substrate.
type gatedApprovalEmitter struct{ count int }

func (e *gatedApprovalEmitter) Emit(_ context.Context, _, _ string, _ []byte) (string, error) {
	e.count++
	return fmt.Sprintf("env-%d", e.count), nil
}

// gatedNotCalledRunner fails the test if Run is ever invoked — the
// gated path must accept the spawn and stop at StatusRequested without
// ever reaching the runner.
type gatedNotCalledRunner struct{ t *testing.T }

func (r *gatedNotCalledRunner) Run(_ context.Context, _ *subagent.Run) (*subagent.Result, error) {
	r.t.Fatal("runner must not be invoked while the fork subagent spawn is pending approval")
	return nil, nil
}

// TestMaterializeSkill_Fork_PendingApproval_ReturnsDistinguishableError
// reproduces, as a real regression test, the fresh-reviewer-found
// production-blocking bug directly: constructed the same way the
// reviewer's own scratch test was (a real *subagent.Service with
// SubagentApprovalRequired: true, mirroring internal/selftools's own
// gated-test pattern), Spawn's gated path inserts the run as
// StatusRequested and returns *before ever reaching* ModeSync's
// blocking-exec branch — a deterministic control-flow path under the
// documented production default, not a rare race. runFork must surface
// this as a distinguishable *ForkPendingApprovalError a caller can
// errors.As against, never the old opaque "did not terminate
// synchronously" message.
func TestMaterializeSkill_Fork_PendingApproval_ReturnsDistinguishableError(t *testing.T) {
	idx := newResolverTestStore(t)
	vendor := newResolverTestVendor(t)

	installFixture(t, idx, vendor, filepath.Join(composeFixturesDir, "compose-fork-child"))
	installFixture(t, idx, vendor, filepath.Join(composeFixturesDir, "compose-fork-parent"))

	runner := &gatedNotCalledRunner{t: t}
	emitter := &gatedApprovalEmitter{}
	settings := gatedSettingsReader{us: store.UserSettings{SubagentApprovalRequired: true}}
	svc := subagent.NewService(idx.DB, runner, nil, emitter, settings)

	parent := parseFixtureDef(t, "compose-fork-parent")

	_, err := MaterializeSkill(context.Background(), MaterializerDeps{
		Index:    idx,
		Vendor:   vendor,
		Subagent: svc,
	}, parent, MaterializeInput{
		ParentSessionID: "sess-pending-approval",
		ParentAgentID:   "agent-pending-approval",
		ForkRole:        "fork-composed-worker",
	})
	if err == nil {
		t.Fatal("expected a materialization failure for a fork subagent pending human approval")
	}

	var pendingErr *ForkPendingApprovalError
	if !errors.As(err, &pendingErr) {
		t.Fatalf("expected a *ForkPendingApprovalError in the error chain, got %T: %v", err, err)
	}
	if pendingErr.RunID == "" {
		t.Error("ForkPendingApprovalError.RunID must not be empty")
	}
	if pendingErr.EnvelopeInstanceID == "" {
		t.Error("ForkPendingApprovalError.EnvelopeInstanceID must not be empty (the approval envelope actually emitted)")
	}
	if pendingErr.Status != subagent.StatusRequested {
		t.Errorf("ForkPendingApprovalError.Status = %q, want %q", pendingErr.Status, subagent.StatusRequested)
	}
	if pendingErr.Slug != "compose-fork-child" {
		t.Errorf("ForkPendingApprovalError.Slug = %q, want %q", pendingErr.Slug, "compose-fork-child")
	}
	if strings.Contains(err.Error(), "did not terminate synchronously") {
		t.Errorf("error still uses the old generic, indistinguishable phrasing: %v", err)
	}
}

// TestForkResultText_PartialCaptureShape_FoldsBackVerbatim is the
// second regression test the fresh-reviewer fix calls for: a genuinely
// structured partial-capture ResultJSON
// ({"partial":true,"summary":...,"envelope":{...}}, internal/subagent's
// own documented shape for a run cut mid-task but with real captured
// state) must fold back verbatim — envelope/tools fields intact — not
// be reduced to just its top-level `summary` string the way a prior
// draft's discriminator (any JSON with a non-empty `summary`) would
// have done.
func TestForkResultText_PartialCaptureShape_FoldsBackVerbatim(t *testing.T) {
	raw := `{"partial":true,"summary":"cut short mid-task","envelope":{"kind":"envelope","version":1,"type":"list-card","data":{"title":"Plan","items":[{"label":"do the thing"}]}},"tools":{"calls":3}}`
	run := &subagent.Run{Status: subagent.StatusCompleted, ResultJSON: raw}

	result, ok := forkResultText(run)
	if !ok {
		t.Fatal("forkResultText returned ok=false for a genuine partial-capture ResultJSON")
	}
	if result != raw {
		t.Errorf("forkResultText = %q, want the full partial-capture JSON verbatim: %q", result, raw)
	}
	if !strings.Contains(result, `"envelope"`) || !strings.Contains(result, `"tools"`) {
		t.Errorf("forkResultText dropped the envelope/tools fields, keeping only the truncated summary: %q", result)
	}
}

// TestForkResultText_PlainSummaryShape_StillExtractsSummary is a
// companion guard confirming the fix didn't regress the original,
// non-partial {"summary": ...} fallback shape (structuredResultJSON's
// own wrapper for a runner that returned only a Result.Summary) — that
// shape has no `partial` key at all, so it must still take the
// plain-summary extraction path.
func TestForkResultText_PlainSummaryShape_StillExtractsSummary(t *testing.T) {
	run := &subagent.Run{Status: subagent.StatusCompleted, ResultJSON: `{"summary":"the child's reply"}`}

	result, ok := forkResultText(run)
	if !ok {
		t.Fatal("forkResultText returned ok=false for a plain {\"summary\":...} ResultJSON")
	}
	if result != "the child's reply" {
		t.Errorf("forkResultText = %q, want just the extracted summary %q", result, "the child's reply")
	}
}

// Sanity: *store.Store really does satisfy every narrow interface this
// file's production code depends on — a compile-time guard, not a
// runtime assertion.
var (
	_ AgentContextResolverStore = (*store.Store)(nil)
	_ SkillIndexStore           = (*store.Store)(nil)
	_ SubagentDispatcher        = (*subagent.Service)(nil)
)
