package service

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// fakeStepExecutor is the shared service-package test double for Nanite's
// execution boundary. Workflow sequencing tests live with workflowhost.
type fakeStepExecutor struct {
	mu          sync.Mutex
	llmCalls    []agentworkflow.LLMStepRequest
	toolCalls   []agentworkflow.ToolStepRequest
	verifyCalls []agentworkflow.VerifyRequest
	llmFunc     func(agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error)
	toolFunc    func(agentworkflow.ToolStepRequest) (agentworkflow.ToolStepResult, error)
	verifyFunc  func(agentworkflow.VerifyRequest) (agentworkflow.VerifyResult, error)
}

func (f *fakeStepExecutor) ExecuteLLMStep(_ context.Context, request agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
	f.mu.Lock()
	f.llmCalls = append(f.llmCalls, request)
	f.mu.Unlock()
	if f.llmFunc != nil {
		return f.llmFunc(request)
	}
	return agentworkflow.LLMStepResult{Text: "ok:" + request.StepID}, nil
}

func (f *fakeStepExecutor) ExecuteToolStep(_ context.Context, request agentworkflow.ToolStepRequest) (agentworkflow.ToolStepResult, error) {
	f.mu.Lock()
	f.toolCalls = append(f.toolCalls, request)
	f.mu.Unlock()
	if f.toolFunc != nil {
		return f.toolFunc(request)
	}
	return agentworkflow.ToolStepResult{Output: "ok:" + request.Tool}, nil
}

func (f *fakeStepExecutor) Verify(_ context.Context, request agentworkflow.VerifyRequest) (agentworkflow.VerifyResult, error) {
	f.mu.Lock()
	f.verifyCalls = append(f.verifyCalls, request)
	f.mu.Unlock()
	if f.verifyFunc != nil {
		return f.verifyFunc(request)
	}
	return agentworkflow.VerifyResult{Passed: true}, nil
}

func newTestWorkflowStore(t *testing.T) *store.Store {
	t.Helper()
	value, err := storetest.New(t, context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("create workflow test store: %v", err)
	}
	t.Cleanup(func() { value.Close(context.Background()) })
	return value
}

var _ agentworkflow.StepExecutor = (*fakeStepExecutor)(nil)
