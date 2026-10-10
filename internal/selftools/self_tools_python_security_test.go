package selftools

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	permissionlib "github.com/hollis-labs/substrate/harness/interception/permission"
)

type pythonDecisionChecker permissionlib.Decision

func (p pythonDecisionChecker) Check(context.Context, string, string, map[string]any, permissionlib.ToolMeta) permissionlib.CheckResult {
	return permissionlib.CheckResult{Decision: permissionlib.Decision(p)}
}

type pythonCheckerFunc func(context.Context, string, string, map[string]any, permissionlib.ToolMeta) permissionlib.CheckResult

func (f pythonCheckerFunc) Check(ctx context.Context, session, name string, args map[string]any, meta permissionlib.ToolMeta) permissionlib.CheckResult {
	return f(ctx, session, name, args, meta)
}

func TestPythonToolPumpCancellationAfterPermission(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := newStubDispatcher()
	calls := 0
	d.register("write", func(map[string]any) (any, error) { calls++; return "effect", nil })
	checker := pythonCheckerFunc(func(context.Context, string, string, map[string]any, permissionlib.ToolMeta) permissionlib.CheckResult {
		cancel()
		return permissionlib.CheckResult{Decision: permissionlib.DecisionAllow}
	})
	responses, _, err := runPythonPump(ctx, t, checker, d, sandboxToolRequest{ID: 1, Name: "write"})
	if err != nil || calls != 0 || len(responses) != 1 || responses[0].Error == "" {
		t.Fatalf("calls=%d responses=%+v err=%v", calls, responses, err)
	}
}

func TestPythonToolPumpRefusesRecursiveExecution(t *testing.T) {
	for _, name := range []string{"python_run", "code_task", "mcp__self__python_run"} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			d := newStubDispatcher()
			d.register(name, func(map[string]any) (any, error) { calls++; return "effect", nil })
			responses, _, err := runPythonPump(context.Background(), t, allowAllPermChecker{}, d, sandboxToolRequest{ID: 1, Name: name})
			if err != nil || calls != 0 || len(responses) != 1 || responses[0].ErrorCode != "execution_unavailable" {
				t.Fatalf("calls=%d responses=%+v err=%v", calls, responses, err)
			}
		})
	}
}

func TestPythonToolPumpRejectsUnboundOrOversizedRequests(t *testing.T) {
	for _, request := range []sandboxToolRequest{
		{ID: 2, Name: "write"},
		{ID: 1, Name: "write", Args: map[string]any{"body": strings.Repeat("x", 64*1024)}},
	} {
		calls := 0
		d := newStubDispatcher()
		d.register("write", func(map[string]any) (any, error) { calls++; return "effect", nil })
		responses, _, err := runPythonPump(context.Background(), t, allowAllPermChecker{}, d, request)
		if err == nil || calls != 0 || len(responses) != 0 {
			t.Fatalf("calls=%d responses=%+v err=%v", calls, responses, err)
		}
	}
}

func TestPythonSandboxBufferAcceptsDiscardedBytes(t *testing.T) {
	b := limitedSandboxBuffer{max: 3}
	if n, err := b.Write([]byte("abcdef")); n != 6 || err != nil || b.String() != "abc" {
		t.Fatalf("n=%d err=%v body=%q", n, err, b.String())
	}
}

type pythonBlockedOwnerFixture struct {
	entered  chan struct{}
	release  chan struct{}
	returned chan struct{}
}

func (*pythonBlockedOwnerFixture) AdmitPythonRun(context.Context, string) error { return nil }
func (d *pythonBlockedOwnerFixture) Dispatch(context.Context, string, string, map[string]any) (any, error) {
	close(d.entered)
	<-d.release
	close(d.returned)
	return "late result", nil
}

func TestPythonSandboxCancellationDoesNotWaitForHungOwner(t *testing.T) {
	d := &pythonBlockedOwnerFixture{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	var once sync.Once
	release := func() { once.Do(func() { close(d.release) }) }
	t.Cleanup(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		result *PythonRunResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := RunPythonSandbox(ctx, "private-test-session", `result = tool_call("hang", {})`, nil, 5, 0, allowAllPermChecker{}, d)
		done <- outcome{result, err}
	}()
	select {
	case <-d.entered:
	case result := <-done:
		t.Fatalf("runner ended before owner callback: %+v", result)
	case <-time.After(3 * time.Second):
		t.Fatal("owner callback not reached")
	}
	cancel()
	select {
	case result := <-done:
		if result.err != nil || result.result == nil || !strings.Contains(result.result.Error, "canceled") || len(result.result.ToolCalls) != 0 {
			t.Fatalf("cancellation outcome: result=%+v err=%v", result.result, result.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation waited indefinitely for a nonconforming owner")
	}
	release()
	<-d.returned
}

func TestPythonToolPumpRequiresExplicitAllow(t *testing.T) {
	for _, tc := range []struct {
		name       string
		permission PythonPermissionChecker
	}{
		{"missing", nil},
		{"ask", pythonDecisionChecker(permissionlib.DecisionAsk)},
		{"deny", pythonDecisionChecker(permissionlib.DecisionDeny)},
		{"unknown", pythonDecisionChecker("unknown")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			d := newStubDispatcher()
			d.register("write", func(map[string]any) (any, error) { calls++; return "effect", nil })
			responses, logs, err := runPythonPump(context.Background(), t, tc.permission, d, sandboxToolRequest{ID: 1, Name: "write"})
			if err != nil {
				t.Fatal(err)
			}
			if calls != 0 {
				t.Fatalf("unsupported authority caused %d dispatches", calls)
			}
			if len(responses) != 1 || responses[0].Error == "" || responses[0].Result != nil {
				t.Fatalf("responses: %#v", responses)
			}
			if len(logs) != 1 || logs[0].Status == "ok" {
				t.Fatalf("logs: %#v", logs)
			}
		})
	}
}

func runPythonPump(ctx context.Context, t *testing.T, permission PythonPermissionChecker, dispatcher PythonToolDispatcher, requests ...sandboxToolRequest) ([]sandboxToolResponse, []PythonToolCallLog, error) {
	t.Helper()
	r, input, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	output, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = input.Close(); _ = output.Close(); _ = w.Close() })
	go func() {
		defer func() { _ = input.Close() }()
		for _, request := range requests {
			if json.NewEncoder(input).Encode(request) != nil {
				return
			}
		}
	}()
	var logs []PythonToolCallLog
	var mu sync.Mutex
	done := make(chan error, 1)
	go func() {
		done <- pumpToolCalls(ctx, "host-session", r, w, permission, dispatcher, &logs, &mu)
		_ = w.Close()
	}()
	var responses []sandboxToolResponse
	decoder := json.NewDecoder(output)
	for {
		var response sandboxToolResponse
		if decoder.Decode(&response) != nil {
			break
		}
		responses = append(responses, response)
	}
	return responses, logs, <-done
}
