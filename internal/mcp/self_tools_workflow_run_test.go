package mcp

// CW-20260815-0008: closes the second gap the CW-20260815-0005 conformance
// review found in the Worker → Reviewer → Gate workflow — no test exercised
// the real workflow_run self-tool. internal/service/workflow_definitions_
// smoke_test.go's "end-to-end" test calls WorkflowLauncher.Launch directly
// (its own comment discloses this), skipping exactly what callWorkflowRun
// (self_tools_workflow_run.go) adds over that: H1 caller-profile trust
// resolution (CallerProfileFromContext, not an LLM-suppliable arg — see
// tool_ctx.go) and envelope wrapping (dispatch.EnvelopeWrapper). This file
// exercises callWorkflowRun itself, against the real, shipped
// worker-reviewer-gate definition.
//
// The launcher is a stub (stubWorkflowLauncher, from
// self_tools_dispatch_workflow_test.go in this same package) rather than a
// real end-to-end WorkflowLauncher: internal/service already owns the real
// BuiltinWorkflowEngine/WorkflowStepExecutor/DurableAgentService wiring
// (proven end-to-end by the smoke test above) and itself imports
// internal/mcp (see dispatch_wiring.go) — internal/mcp importing back would
// be a cycle. callWorkflowRun is unexported, so only a test in this package
// can call it directly; the registry load below still guards against the
// shipped example drifting out from under this test.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/dispatch"
)

// exampleWorkflowDefinitionsDir locates examples/workflow-definitions/
// relative to this test file, regardless of the test runner's cwd. Mirrors
// internal/service/workflow_definitions_smoke_test.go's helper of the same
// name (duplicated, not shared — see the import-cycle note above).
func exampleWorkflowDefinitionsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..")
	return filepath.Join(repoRoot, "examples", "workflow-definitions")
}

// TestCallWorkflowRun_TrustResolutionAndEnvelopeWrap proves callWorkflowRun,
// given the real worker-reviewer-gate definition's name, pulls
// AgentProfileID from ctx (not args), forwards it plus params into the
// WorkflowLaunchRequest, and wraps the launcher's SpawnResult in a valid
// envelope.
func TestCallWorkflowRun_TrustResolutionAndEnvelopeWrap(t *testing.T) {
	dir := exampleWorkflowDefinitionsDir(t)
	registry, err := agentworkflow.LoadRegistryDir(dir)
	if err != nil {
		t.Fatalf("LoadRegistryDir(%s) = %v, want nil", dir, err)
	}
	wf, ok := registry.Get("worker-reviewer-gate")
	if !ok {
		t.Fatalf("workflow %q missing from registry (got %v)", "worker-reviewer-gate", registry.Names())
	}

	const wantSummary = "Workflow \"worker-reviewer-gate\" run run-1: waiting_on_gate.\n- worker (ok): reversed\n"
	launcher := &stubWorkflowLauncher{
		result: &dispatch.SpawnResult{Summary: wantSummary},
	}
	st := &SelfToolsTransport{WorkflowLauncher: launcher}

	ctx := WithCallerProfile(context.Background(), "profile-1")
	res, err := st.callWorkflowRun(ctx, map[string]any{
		"workflow_name": wf.Name,
		"params":        map[string]any{"task": "reverse the string \"abc\""},
	})
	if err != nil {
		t.Fatalf("callWorkflowRun: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success result, got error: %+v", res)
	}

	if launcher.calls != 1 {
		t.Fatalf("WorkflowLauncher.Launch calls = %d, want 1", launcher.calls)
	}
	if launcher.saw.WorkflowName != "worker-reviewer-gate" {
		t.Errorf("saw.WorkflowName = %q, want worker-reviewer-gate", launcher.saw.WorkflowName)
	}
	if launcher.saw.AgentProfileID != "profile-1" {
		t.Errorf("saw.AgentProfileID = %q, want profile-1 — must come from CallerProfileFromContext, not an LLM-suppliable arg",
			launcher.saw.AgentProfileID)
	}
	if task, _ := launcher.saw.Params["task"].(string); task != "reverse the string \"abc\"" {
		t.Errorf("saw.Params[task] = %v, want the forwarded task string", launcher.saw.Params["task"])
	}

	if len(res.Content) != 1 || res.Content[0].Type != "text" {
		t.Fatalf("res.Content = %+v, want a single text block", res.Content)
	}
	var env dispatch.Envelope
	if err := json.Unmarshal([]byte(res.Content[0].Text), &env); err != nil {
		t.Fatalf("result text is not a valid envelope: %v (text=%s)", err, res.Content[0].Text)
	}
	if env.Kind != "envelope" || env.Type != "report-card" {
		t.Errorf("envelope (Kind, Type) = (%q, %q), want (envelope, report-card) — DefaultEnvelopeWrapper's synthesized shape for a summary-only SpawnResult", env.Kind, env.Type)
	}
	if role, _ := env.Data["role"].(string); role != "workflow" {
		t.Errorf("envelope Data[role] = %v, want workflow", env.Data["role"])
	}
	if summary, _ := env.Data["summary"].(string); summary != wantSummary {
		t.Errorf("envelope Data[summary] = %q, want %q", summary, wantSummary)
	}
}

// TestCallWorkflowRun_NoCallerProfile_RejectsBeforeLaunch is the negative
// case for the same trust gate: callWorkflowRun.go checks AgentProfileID
// before ever calling the launcher. A session with no caller-profile
// context stamped (e.g. a malformed or bypassed service-layer call) must be
// rejected, not silently launched with empty identity.
func TestCallWorkflowRun_NoCallerProfile_RejectsBeforeLaunch(t *testing.T) {
	// A bare context.Background() is the only reachable "unresolved" state
	// via the public API; it exercises the AgentProfileID=="" check in
	// callWorkflowRun.
	launcher := &stubWorkflowLauncher{result: &dispatch.SpawnResult{Summary: "should not be reached"}}
	st := &SelfToolsTransport{WorkflowLauncher: launcher}

	res, err := st.callWorkflowRun(context.Background(), map[string]any{
		"workflow_name": "worker-reviewer-gate",
	})
	if err != nil {
		t.Fatalf("callWorkflowRun: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected an error result when caller profile is unresolved, got: %+v", res)
	}
	if launcher.calls != 0 {
		t.Fatalf("WorkflowLauncher.Launch calls = %d, want 0 — trust gate must reject before dispatch", launcher.calls)
	}
}

// TestCallWorkflowRun_MissingWorkflowName_RejectsBeforeLaunch guards the
// other required-arg check in callWorkflowRun, ahead of the trust gate.
func TestCallWorkflowRun_MissingWorkflowName_RejectsBeforeLaunch(t *testing.T) {
	launcher := &stubWorkflowLauncher{result: &dispatch.SpawnResult{Summary: "should not be reached"}}
	st := &SelfToolsTransport{WorkflowLauncher: launcher}

	ctx := WithCallerProfile(context.Background(), "profile-1")
	res, err := st.callWorkflowRun(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("callWorkflowRun: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected an error result for a missing workflow_name, got: %+v", res)
	}
	if launcher.calls != 0 {
		t.Fatalf("WorkflowLauncher.Launch calls = %d, want 0", launcher.calls)
	}
}

// TestCallWorkflowRun_MissingRequiredParam_RejectsBeforeLaunchWithClearError
// is the regression pin for CW-20260815-0022: the real incident was the
// Orchestrator calling workflow_run against worker-reviewer-gate without
// params.task, which the engine only caught deep inside step-config
// resolution with a generic "reference to unknown input" error. With
// WorkflowRegistry wired, the same omission must now be rejected before
// ever reaching the launcher, naming the missing key.
func TestCallWorkflowRun_MissingRequiredParam_RejectsBeforeLaunchWithClearError(t *testing.T) {
	dir := exampleWorkflowDefinitionsDir(t)
	registry, err := agentworkflow.LoadRegistryDir(dir)
	if err != nil {
		t.Fatalf("LoadRegistryDir(%s) = %v, want nil", dir, err)
	}

	launcher := &stubWorkflowLauncher{result: &dispatch.SpawnResult{Summary: "should not be reached"}}
	st := &SelfToolsTransport{WorkflowLauncher: launcher, WorkflowRegistry: registry}

	ctx := WithCallerProfile(context.Background(), "profile-1")
	res, err := st.callWorkflowRun(ctx, map[string]any{
		"workflow_name": "worker-reviewer-gate",
		// params.task deliberately omitted.
	})
	if err != nil {
		t.Fatalf("callWorkflowRun: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected an error result for a missing required param, got: %+v", res)
	}
	if launcher.calls != 0 {
		t.Fatalf("WorkflowLauncher.Launch calls = %d, want 0 — must reject before dispatch, not rely on the engine failing deep in the run", launcher.calls)
	}
	if len(res.Content) != 1 || res.Content[0].Type != "text" {
		t.Fatalf("res.Content = %+v, want a single text block", res.Content)
	}
	got := res.Content[0].Text
	if !strings.Contains(got, "task") {
		t.Errorf("error text = %q, want it to name the missing %q param", got, "task")
	}
	if !strings.Contains(got, "worker-reviewer-gate") {
		t.Errorf("error text = %q, want it to name the workflow", got)
	}
}

// TestCallWorkflowRun_RequiredParamSupplied_NoRegistryRegression proves the
// registry-backed pre-check doesn't reject the same call
// TestCallWorkflowRun_TrustResolutionAndEnvelopeWrap already proves
// succeeds — this time with WorkflowRegistry wired too, since that's how
// the container actually wires SelfToolsTransport in production.
func TestCallWorkflowRun_RequiredParamSupplied_NoRegistryRegression(t *testing.T) {
	dir := exampleWorkflowDefinitionsDir(t)
	registry, err := agentworkflow.LoadRegistryDir(dir)
	if err != nil {
		t.Fatalf("LoadRegistryDir(%s) = %v, want nil", dir, err)
	}

	launcher := &stubWorkflowLauncher{result: &dispatch.SpawnResult{Summary: "ok"}}
	st := &SelfToolsTransport{WorkflowLauncher: launcher, WorkflowRegistry: registry}

	ctx := WithCallerProfile(context.Background(), "profile-1")
	res, err := st.callWorkflowRun(ctx, map[string]any{
		"workflow_name": "worker-reviewer-gate",
		"params":        map[string]any{"task": "reverse the string \"abc\""},
	})
	if err != nil {
		t.Fatalf("callWorkflowRun: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %+v", res)
	}
	if launcher.calls != 1 {
		t.Fatalf("WorkflowLauncher.Launch calls = %d, want 1", launcher.calls)
	}
}

// TestCallWorkflowRun_UnknownWorkflowName_StillReachesLauncher proves the
// pre-check doesn't itself reject a workflow_name absent from the registry
// — that's WorkflowLauncher.Launch's job (it may know about
// non-built-in-engine sources this registry doesn't).
func TestCallWorkflowRun_UnknownWorkflowName_StillReachesLauncher(t *testing.T) {
	dir := exampleWorkflowDefinitionsDir(t)
	registry, err := agentworkflow.LoadRegistryDir(dir)
	if err != nil {
		t.Fatalf("LoadRegistryDir(%s) = %v, want nil", dir, err)
	}

	launcher := &stubWorkflowLauncher{result: &dispatch.SpawnResult{Summary: "ok"}}
	st := &SelfToolsTransport{WorkflowLauncher: launcher, WorkflowRegistry: registry}

	ctx := WithCallerProfile(context.Background(), "profile-1")
	_, err = st.callWorkflowRun(ctx, map[string]any{
		"workflow_name": "does-not-exist",
	})
	if err != nil {
		t.Fatalf("callWorkflowRun: %v", err)
	}
	if launcher.calls != 1 {
		t.Fatalf("WorkflowLauncher.Launch calls = %d, want 1 — an unregistered name must still reach the launcher", launcher.calls)
	}
}
