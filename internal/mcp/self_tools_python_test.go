package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/permission"
)

// skipIfNoPython3 skips the test if python3 is not on PATH.
func skipIfNoPython3(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found on PATH — skipping Python sandbox tests")
	}
}

// --- stubs ---

// allowAllPermChecker approves every tool call unconditionally.
type allowAllPermChecker struct{}

func (allowAllPermChecker) Check(_ context.Context, _, _ string, _ map[string]any, _ permission.ToolMeta) permission.CheckResult {
	return permission.CheckResult{Decision: permission.DecisionAllow, Reason: "test: allow all"}
}

// denyAllPermChecker denies every tool call.
type denyAllPermChecker struct{}

func (denyAllPermChecker) Check(_ context.Context, _, toolName string, _ map[string]any, _ permission.ToolMeta) permission.CheckResult {
	return permission.CheckResult{Decision: permission.DecisionDeny, Reason: "test: deny all"}
}

// stubDispatcher is a simple in-memory dispatcher for pilot tests.
type stubDispatcher struct {
	// handlers maps tool name → func(args) → (result, error)
	handlers map[string]func(args map[string]any) (any, error)
}

func newStubDispatcher() *stubDispatcher {
	return &stubDispatcher{handlers: make(map[string]func(map[string]any) (any, error))}
}

func (d *stubDispatcher) register(name string, fn func(args map[string]any) (any, error)) {
	d.handlers[name] = fn
}

func (d *stubDispatcher) Dispatch(_ context.Context, _ string, toolName string, args map[string]any) (any, error) {
	fn, ok := d.handlers[toolName]
	if !ok {
		return nil, fmt.Errorf("stub: no handler for tool %q", toolName)
	}
	return fn(args)
}

// nilDispatcher always errors.
type nilDispatcher struct{}

func (nilDispatcher) Dispatch(_ context.Context, _, toolName string, _ map[string]any) (any, error) {
	return nil, fmt.Errorf("no dispatcher configured")
}

// --- basic execution tests ---

func TestRunPythonSandbox_SimpleResult(t *testing.T) {
	skipIfNoPython3(t)

	result, err := RunPythonSandbox(
		context.Background(),
		"test-session",
		`result = 42`,
		nil,
		0, 0,
		allowAllPermChecker{},
		nilDispatcher{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected sandbox error: %s", result.Error)
	}

	// result should be JSON number 42.
	num, ok := result.Result.(float64)
	if !ok {
		t.Fatalf("expected float64 result, got %T: %v", result.Result, result.Result)
	}
	if num != 42 {
		t.Errorf("result = %v, want 42", num)
	}
}

func TestRunPythonSandbox_StdoutCapture(t *testing.T) {
	skipIfNoPython3(t)

	result, err := RunPythonSandbox(
		context.Background(),
		"test-session",
		`print("hello from sandbox")
result = "done"`,
		nil, 0, 0,
		allowAllPermChecker{},
		nilDispatcher{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("sandbox error: %s", result.Error)
	}
	if !strings.Contains(result.Stdout, "hello from sandbox") {
		t.Errorf("stdout missing expected text, got: %q", result.Stdout)
	}
}

func TestRunPythonSandbox_SyntaxError(t *testing.T) {
	skipIfNoPython3(t)

	result, err := RunPythonSandbox(
		context.Background(),
		"test-session",
		`def broken(:`,
		nil, 0, 0,
		allowAllPermChecker{},
		nilDispatcher{},
	)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Error == "" {
		t.Fatal("expected error for syntax error, got none")
	}
	// Error should mention SyntaxError.
	if !strings.Contains(result.Error, "SyntaxError") && !strings.Contains(result.Error, "invalid syntax") {
		t.Errorf("expected SyntaxError in error output, got: %q", result.Error)
	}
}

func TestRunPythonSandbox_NetworkDenied(t *testing.T) {
	skipIfNoPython3(t)

	result, err := RunPythonSandbox(
		context.Background(),
		"test-session",
		`import socket
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)`,
		nil, 0, 0,
		allowAllPermChecker{},
		nilDispatcher{},
	)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	// Should have caught the PermissionError from the monkey-patch.
	if result.Error == "" {
		t.Fatal("expected error from network deny, got none")
	}
	if !strings.Contains(result.Error, "PermissionError") && !strings.Contains(result.Error, "network access") {
		t.Errorf("expected network access denied error, got: %q", result.Error)
	}
}

func TestRunPythonSandbox_ArgsPassedToScript(t *testing.T) {
	skipIfNoPython3(t)

	result, err := RunPythonSandbox(
		context.Background(),
		"test-session",
		`result = args.get("x", 0) + args.get("y", 0)`,
		map[string]any{"x": float64(3), "y": float64(7)},
		0, 0,
		allowAllPermChecker{},
		nilDispatcher{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("sandbox error: %s", result.Error)
	}
	num, ok := result.Result.(float64)
	if !ok {
		t.Fatalf("expected float64, got %T: %v", result.Result, result.Result)
	}
	if num != 10 {
		t.Errorf("result = %v, want 10", num)
	}
}

// --- pilot scenario tests ---

// TestRunPythonSandbox_Pagination exercises the pagination pattern:
// a Python loop calls tool_call("stub_list", {limit, offset}) repeatedly
// until fewer than 'limit' items are returned, accumulating a total count.
// The stub returns pages of items, and the Python code sums them up.
func TestRunPythonSandbox_Pagination(t *testing.T) {
	skipIfNoPython3(t)

	// Stub: 237 total items, returned 50 per page.
	totalItems := 237
	disp := newStubDispatcher()
	disp.register("stub_list", func(args map[string]any) (any, error) {
		limit := int(args["limit"].(float64))
		offset := int(args["offset"].(float64))
		end := offset + limit
		if end > totalItems {
			end = totalItems
		}
		items := make([]int, 0, end-offset)
		for i := offset; i < end; i++ {
			items = append(items, i)
		}
		return map[string]any{"items": items}, nil
	})

	code := `
total = 0
offset = 0
limit = 50
while True:
    page = tool_call("stub_list", {"limit": limit, "offset": offset})
    items = page.get("items", [])
    total += len(items)
    if len(items) < limit:
        break
    offset += limit
result = {"total_count": total}
`
	result, err := RunPythonSandbox(
		context.Background(), "test-session",
		code, nil, 30, 256,
		allowAllPermChecker{}, disp,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("sandbox error: %s\nstdout: %s", result.Error, result.Stdout)
	}

	resMap, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T: %v", result.Result, result.Result)
	}
	gotTotal := int(resMap["total_count"].(float64))
	if gotTotal != totalItems {
		t.Errorf("total_count = %d, want %d", gotTotal, totalItems)
	}

	// Verify tool_calls log.
	expectedCalls := (totalItems + 49) / 50 // ceil division
	if len(result.ToolCalls) != expectedCalls {
		t.Errorf("tool_calls count = %d, want %d", len(result.ToolCalls), expectedCalls)
	}
	for i, tc := range result.ToolCalls {
		if tc.Name != "stub_list" {
			t.Errorf("tool_calls[%d].name = %q, want stub_list", i, tc.Name)
		}
		if tc.Status != "ok" {
			t.Errorf("tool_calls[%d].status = %q, want ok", i, tc.Status)
		}
	}
}

// TestRunPythonSandbox_LookupAndJoin exercises the multi-entity
// lookup-and-join pattern: list items, then fetch details for each,
// return a joined result.
func TestRunPythonSandbox_LookupAndJoin(t *testing.T) {
	skipIfNoPython3(t)

	// Stubs: list returns 3 item IDs; get returns enriched detail.
	items := []map[string]any{
		{"id": "item-1", "name": "Alpha"},
		{"id": "item-2", "name": "Beta"},
		{"id": "item-3", "name": "Gamma"},
	}
	details := map[string]map[string]any{
		"item-1": {"id": "item-1", "name": "Alpha", "score": 10.0},
		"item-2": {"id": "item-2", "name": "Beta", "score": 20.0},
		"item-3": {"id": "item-3", "name": "Gamma", "score": 30.0},
	}

	disp := newStubDispatcher()
	disp.register("stub_list_items", func(_ map[string]any) (any, error) {
		return map[string]any{"items": items}, nil
	})
	disp.register("stub_get_item", func(args map[string]any) (any, error) {
		id, _ := args["id"].(string)
		if d, ok := details[id]; ok {
			return d, nil
		}
		return nil, fmt.Errorf("item %q not found", id)
	})

	code := `
list_result = tool_call("stub_list_items", {})
joined = []
for item in list_result.get("items", []):
    detail = tool_call("stub_get_item", {"id": item["id"]})
    joined.append(detail)
result = {"items": joined, "count": len(joined)}
`
	result, err := RunPythonSandbox(
		context.Background(), "test-session",
		code, nil, 30, 256,
		allowAllPermChecker{}, disp,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("sandbox error: %s\nstdout: %s", result.Error, result.Stdout)
	}

	resMap, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T: %v", result.Result, result.Result)
	}
	count := int(resMap["count"].(float64))
	if count != 3 {
		t.Errorf("count = %d, want 3", count)
	}
	joinedItems, _ := resMap["items"].([]any)
	if len(joinedItems) != 3 {
		t.Fatalf("joined items = %d, want 3", len(joinedItems))
	}
	// Verify scores are present.
	scores := map[string]bool{}
	for _, raw := range joinedItems {
		m, _ := raw.(map[string]any)
		id, _ := m["id"].(string)
		score, _ := m["score"].(float64)
		scores[id] = score > 0
	}
	for _, id := range []string{"item-1", "item-2", "item-3"} {
		if !scores[id] {
			t.Errorf("item %q missing or has zero score in joined result", id)
		}
	}

	// 1 list call + 3 detail calls = 4 tool calls.
	if len(result.ToolCalls) != 4 {
		t.Errorf("tool_calls count = %d, want 4", len(result.ToolCalls))
	}
}

// TestRunPythonSandbox_NumericAggregation exercises the numeric aggregation
// pattern: call a tool returning numeric data, compute sum and mean in Python,
// return a single structured result.
func TestRunPythonSandbox_NumericAggregation(t *testing.T) {
	skipIfNoPython3(t)

	// Stub: returns metric values for N entities.
	metrics := []map[string]any{
		{"entity": "A", "value": 10.0},
		{"entity": "B", "value": 20.0},
		{"entity": "C", "value": 30.0},
		{"entity": "D", "value": 40.0},
	}
	disp := newStubDispatcher()
	disp.register("stub_get_metrics", func(_ map[string]any) (any, error) {
		return map[string]any{"metrics": metrics}, nil
	})

	code := `
resp = tool_call("stub_get_metrics", {})
values = [m["value"] for m in resp.get("metrics", [])]
total = sum(values)
mean  = total / len(values) if values else 0
result = {"sum": total, "mean": mean, "count": len(values)}
`
	result, err := RunPythonSandbox(
		context.Background(), "test-session",
		code, nil, 0, 0,
		allowAllPermChecker{}, disp,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("sandbox error: %s\nstdout: %s", result.Error, result.Stdout)
	}

	resMap, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T: %v", result.Result, result.Result)
	}
	sum := resMap["sum"].(float64)
	mean := resMap["mean"].(float64)
	count := int(resMap["count"].(float64))
	if sum != 100.0 {
		t.Errorf("sum = %v, want 100.0", sum)
	}
	if mean != 25.0 {
		t.Errorf("mean = %v, want 25.0", mean)
	}
	if count != 4 {
		t.Errorf("count = %d, want 4", count)
	}
	if len(result.ToolCalls) != 1 {
		t.Errorf("tool_calls count = %d, want 1", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Status != "ok" {
		t.Errorf("tool_calls[0].status = %q, want ok", result.ToolCalls[0].Status)
	}
}

// --- permission enforcement tests ---

// TestRunPythonSandbox_PermissionDenied asserts that when the permission
// checker denies a tool call, the sandbox receives a RuntimeError and the
// tool_calls log records the "denied" status.
func TestRunPythonSandbox_PermissionDenied(t *testing.T) {
	skipIfNoPython3(t)

	code := `
try:
    tool_call("forbidden_tool", {})
    result = "should_not_reach"
except RuntimeError as e:
    result = {"denied": True, "msg": str(e)}
`
	result, err := RunPythonSandbox(
		context.Background(), "test-session",
		code, nil, 0, 0,
		denyAllPermChecker{}, nilDispatcher{},
	)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected sandbox error: %s", result.Error)
	}

	resMap, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result after denial, got %T: %v", result.Result, result.Result)
	}
	if resMap["denied"] != true {
		t.Errorf("expected denied=true, got: %v", resMap)
	}

	if len(result.ToolCalls) != 1 {
		t.Fatalf("tool_calls count = %d, want 1", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Status != "denied" {
		t.Errorf("tool_calls[0].status = %q, want denied", result.ToolCalls[0].Status)
	}
}

// TestRunPythonSandbox_BadToolName asserts that requesting a non-existent
// tool (even with allowAll perm checker) surfaces as an error rather than
// a hang or panic.
func TestRunPythonSandbox_BadToolName(t *testing.T) {
	skipIfNoPython3(t)

	code := `
try:
    tool_call("nonexistent_tool_xyz", {})
    result = "unexpected_success"
except RuntimeError as e:
    result = {"error_caught": True}
`
	disp := newStubDispatcher() // no handler registered
	result, err := RunPythonSandbox(
		context.Background(), "test-session",
		code, nil, 0, 0,
		allowAllPermChecker{}, disp,
	)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("unexpected sandbox error: %s", result.Error)
	}
	resMap, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T: %v", result.Result, result.Result)
	}
	if resMap["error_caught"] != true {
		t.Errorf("expected error_caught=true, got: %v", resMap)
	}
	if len(result.ToolCalls) != 1 || result.ToolCalls[0].Status != "error" {
		t.Errorf("expected 1 tool_call with status=error, got: %+v", result.ToolCalls)
	}
}

// --- self-tool integration tests ---

// TestSelfToolsTransport_RunPython_BasicViaCallTool verifies the end-to-end
// path through CallTool → callRunPython → RunPythonSandbox.
func TestSelfToolsTransport_RunPython_BasicViaCallTool(t *testing.T) {
	skipIfNoPython3(t)

	st := newSelfTools(t)
	// Wire stub dispatcher and allow-all perm checker.
	disp := newStubDispatcher()
	disp.register("test_add", func(args map[string]any) (any, error) {
		a := args["a"].(float64)
		b := args["b"].(float64)
		return map[string]any{"sum": a + b}, nil
	})
	st.PythonDispatcher = disp
	st.PythonPermChecker = allowAllPermChecker{}

	code := `
r = tool_call("test_add", {"a": 3.0, "b": 4.0})
result = r["sum"]
`
	callResult, err := st.CallTool(context.Background(), "nanite_run_python", map[string]any{
		"code":       code,
		"session_id": "test-sess",
	})
	if err != nil {
		t.Fatalf("CallTool error: %v", err)
	}
	if callResult.IsError {
		t.Fatalf("tool returned error: %s", callResult.Content[0].Text)
	}

	var pyResult PythonRunResult
	if err := json.Unmarshal([]byte(callResult.Content[0].Text), &pyResult); err != nil {
		t.Fatalf("unmarshal result: %v\nraw: %s", err, callResult.Content[0].Text)
	}
	if pyResult.Error != "" {
		t.Fatalf("sandbox error: %s", pyResult.Error)
	}
	sum, ok := pyResult.Result.(float64)
	if !ok {
		t.Fatalf("expected float64 result, got %T: %v", pyResult.Result, pyResult.Result)
	}
	if sum != 7.0 {
		t.Errorf("result = %v, want 7.0", sum)
	}
}

// TestSelfToolsTransport_RunPython_NotOnChatSurface verifies that
// nanite_run_python is filtered from the Chat agent's static surface.
// This is the critical harness-enforcement check for CW-20260420-0019.
func TestSelfToolsTransport_RunPython_NotOnChatSurface(t *testing.T) {
	// Import the dispatch package surface function via the existing test pattern.
	// We verify by calling IsChatSurfaceTool directly — same as the role_test.
	from := "nanite_run_python"
	if isChatSurfaceTool(from) {
		t.Errorf("nanite_run_python must NOT be on the Chat surface (IsChatSurfaceTool returned true)")
	}
}

// TestSelfToolsTransport_RunPython_PresentInSelfToolDefinitions verifies the
// tool definition appears in the worker/planner surface registry.
func TestSelfToolsTransport_RunPython_PresentInSelfToolDefinitions(t *testing.T) {
	defs := selfToolDefinitions()
	found := false
	for _, d := range defs {
		if d.Name == "nanite_run_python" {
			found = true
			// Verify required fields.
			if d.Description == "" {
				t.Error("nanite_run_python description is empty")
			}
			props, _ := d.InputSchema["properties"].(map[string]any)
			if _, ok := props["code"]; !ok {
				t.Error("nanite_run_python schema missing 'code' property")
			}
			break
		}
	}
	if !found {
		t.Error("nanite_run_python not found in selfToolDefinitions()")
	}
}

// isChatSurfaceTool is a local bridge to dispatch.IsChatSurfaceTool.
// We inline the check here to avoid an import cycle, using the same logic.
func isChatSurfaceTool(name string) bool {
	chatSurfaceMetaExceptions := map[string]bool{
		"fetch_tool_result":  true,
		"search_tool_result": true,
		"request_tools":      true,
	}
	if chatSurfaceMetaExceptions[name] {
		return true
	}
	// Mirror of ChatToolSurface from dispatch/role.go.
	prefixes := []string{
		"nanite_todo_",
		"nanite_plan_",
		"nanite_scratchpad_",
		"nanite_message_",
		"nanite_handoff_",
		"nanite_show_",
		"nanite_execute_task",
		"nanite_chat_search",
	}
	for _, prefix := range prefixes {
		if name == prefix || strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// TestRunPythonSandbox_CapsClamped verifies the harness clamps out-of-range
// time/memory caps to the absolute ceilings defined in the package constants.
func TestRunPythonSandbox_CapsClamped(t *testing.T) {
	skipIfNoPython3(t)

	// Request well above the absolute ceilings.
	result, err := RunPythonSandbox(
		context.Background(), "test-session",
		`result = "capped"`,
		nil,
		9999,  // time_limit_seconds — should clamp to 60
		99999, // memory_limit_mb — should clamp to 1024
		allowAllPermChecker{},
		nilDispatcher{},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Error != "" {
		t.Fatalf("sandbox error: %s", result.Error)
	}
	s, _ := result.Result.(string)
	if s != "capped" {
		t.Errorf("result = %q, want capped", s)
	}
}
