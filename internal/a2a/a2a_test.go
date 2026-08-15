package a2a_test

import (
	"testing"

	"github.com/hollis-labs/nanite/internal/a2a"
)

func TestNewAgentAddress(t *testing.T) {
	agentID := "agt_7xp4w9zq2a"
	addr := a2a.NewAgentAddress(agentID)

	expected := "msg://agent/nanite/agt_7xp4w9zq2a"
	if addr.URN() != expected {
		t.Errorf("NewAgentAddress(%q) = %q, want %q", agentID, addr.URN(), expected)
	}

	// Verify address can be parsed back
	parsed, err := a2a.ParseURN(addr.URN())
	if err != nil {
		t.Fatalf("ParseURN(%q): %v", addr.URN(), err)
	}

	if parsed.Kind != a2a.KindAgent {
		t.Errorf("parsed.Kind = %v, want %v", parsed.Kind, a2a.KindAgent)
	}
	if parsed.Authority != a2a.AuthorityNanite {
		t.Errorf("parsed.Authority = %q, want %q", parsed.Authority, a2a.AuthorityNanite)
	}
	if parsed.ID != agentID {
		t.Errorf("parsed.ID = %q, want %q", parsed.ID, agentID)
	}
}

func TestNewWorkflowAddress(t *testing.T) {
	workflowName := "research"
	addr := a2a.NewWorkflowAddress(workflowName)

	expected := "msg://workflow/nanite/research"
	if addr.URN() != expected {
		t.Errorf("NewWorkflowAddress(%q) = %q, want %q", workflowName, addr.URN(), expected)
	}

	// Verify address can be parsed back
	parsed, err := a2a.ParseURN(addr.URN())
	if err != nil {
		t.Fatalf("ParseURN(%q): %v", addr.URN(), err)
	}

	if parsed.Kind != a2a.KindWorkflow {
		t.Errorf("parsed.Kind = %v, want %v", parsed.Kind, a2a.KindWorkflow)
	}
	if parsed.Authority != a2a.AuthorityNanite {
		t.Errorf("parsed.Authority = %q, want %q", parsed.Authority, a2a.AuthorityNanite)
	}
	if parsed.ID != workflowName {
		t.Errorf("parsed.ID = %q, want %q", parsed.ID, workflowName)
	}
}

func TestJSONRPCEnvelope(t *testing.T) {
	// Test request envelope
	req := a2a.JSONRPCRequest{
		JSONRPC: a2a.JSONRPCVersion,
		Method:  "task.submit",
		Params: map[string]any{
			"target":  "msg://workflow/nanite/research",
			"message": "Research AI safety papers from 2024",
		},
		ID: "req_123",
	}

	if req.JSONRPC != "2.0" {
		t.Errorf("req.JSONRPC = %q, want \"2.0\"", req.JSONRPC)
	}

	// Test response envelope
	resp := a2a.JSONRPCResponse{
		JSONRPC: a2a.JSONRPCVersion,
		Result: map[string]any{
			"task_id": "task_abc",
			"state":   "submitted",
		},
		ID: "req_123",
	}

	if resp.Error != nil {
		t.Errorf("resp.Error should be nil for success response")
	}

	// Test error response
	errResp := a2a.JSONRPCResponse{
		JSONRPC: a2a.JSONRPCVersion,
		Error: &a2a.JSONRPCError{
			Code:    a2a.ErrTaskNotFound,
			Message: "Task not found",
		},
		ID: "req_123",
	}

	if errResp.Error.Code != a2a.ErrTaskNotFound {
		t.Errorf("errResp.Error.Code = %d, want %d", errResp.Error.Code, a2a.ErrTaskNotFound)
	}
}

func TestAgentCard(t *testing.T) {
	card := a2a.AgentCard{
		Name:        "Nanite",
		Description: "AI agent harness",
		URL:         "http://localhost:8090",
		Provider: a2a.Provider{
			Organization: "Hollis Labs",
		},
		Version: "0.1.0",
		Capabilities: a2a.Capabilities{
			Streaming:         false,
			PushNotifications: true,
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text", "json"},
		Skills: []a2a.Skill{
			{
				ID:          "research",
				Name:        "Research",
				Description: "Research a topic and summarize findings",
				Tags:        []string{"research", "analysis"},
				InputSchema: a2a.InputSchema{
					Type: "object",
					Properties: map[string]a2a.SchemaProperty{
						"topic": {
							Type:        "string",
							Description: "The topic to research",
						},
					},
					Required: []string{"topic"},
				},
			},
		},
	}

	if card.Name != "Nanite" {
		t.Errorf("card.Name = %q, want \"Nanite\"", card.Name)
	}
	if len(card.Skills) != 1 {
		t.Errorf("len(card.Skills) = %d, want 1", len(card.Skills))
	}
	if card.Skills[0].ID != "research" {
		t.Errorf("card.Skills[0].ID = %q, want \"research\"", card.Skills[0].ID)
	}
}

func TestTaskStates(t *testing.T) {
	states := []a2a.TaskState{
		a2a.TaskStateSubmitted,
		a2a.TaskStateWorking,
		a2a.TaskStateInputRequired,
		a2a.TaskStateCompleted,
		a2a.TaskStateFailed,
		a2a.TaskStateCanceled,
		a2a.TaskStateRejected,
		a2a.TaskStateAuthRequired,
	}

	expectedStates := []string{
		"submitted",
		"working",
		"input-required",
		"completed",
		"failed",
		"canceled",
		"rejected",
		"auth-required",
	}

	for i, state := range states {
		if string(state) != expectedStates[i] {
			t.Errorf("TaskState[%d] = %q, want %q", i, state, expectedStates[i])
		}
	}
}
