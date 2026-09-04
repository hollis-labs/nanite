package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	workflowapi "github.com/hollis-labs/nanite/internal/workflowapi"
	"github.com/hollis-labs/nanite/internal/workflowcompat"
)

func TestWorkflowExternalExecutionAdminRequiresAuthRedactsListAndForwardsAudit(t *testing.T) {
	a, mux := newTestAPI(t)
	admin := &recordingExternalExecutionAdmin{summaries: []workflowcompat.ExternalExecutionSummary{{
		ReceiptID: "receipt-1", RunID: "run-1", NodeID: "external-run",
		Engine: "langgraph", WorkflowName: "release", RequestDigest: "sha256:request",
		State: "ambiguous", Reason: "outcome unknown", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}}}
	a.SetWorkflowExternalExecutionAdmin(admin)
	a.SetWorkflowResponderAuthenticator(NewBasicWorkflowResponderAuthenticator("operator", "secret"))

	unauthorized := httptest.NewRequest(http.MethodGet, "/api/workflows/external-executions/ambiguous", nil)
	unauthorizedResponse := httptest.NewRecorder()
	mux.ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorizedResponse.Code)
	}

	list := httptest.NewRequest(http.MethodGet, "/api/workflows/external-executions/ambiguous?limit=5", nil)
	list.SetBasicAuth("operator", "secret")
	listResponse := httptest.NewRecorder()
	mux.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}
	body := listResponse.Body.String()
	for _, forbidden := range []string{"params", "session_id", "output"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("redacted list contains %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, "receipt-1") || admin.limit != 5 {
		t.Fatalf("list body=%s limit=%d", body, admin.limit)
	}

	resolve := httptest.NewRequest(http.MethodPost, "/api/workflows/external-executions/receipt-1/resolve",
		strings.NewReader(`{"action":"complete_success","output":"confirmed","reason":"checked provider audit log"}`))
	resolve.Header.Set("Content-Type", "application/json")
	resolve.Header.Set("Idempotency-Key", "operator-resolution-1")
	resolve.SetBasicAuth("operator", "secret")
	resolveResponse := httptest.NewRecorder()
	mux.ServeHTTP(resolveResponse, resolve)
	if resolveResponse.Code != http.StatusOK {
		t.Fatalf("resolve status=%d body=%s", resolveResponse.Code, resolveResponse.Body.String())
	}
	if admin.resolution.ReceiptID != "receipt-1" || admin.resolution.Action != "complete_success" ||
		admin.resolution.Output != "confirmed" || admin.resolution.Principal != "operator" ||
		admin.resolution.Reason != "checked provider audit log" || admin.resolution.IdempotencyKey != "operator-resolution-1" ||
		admin.resolution.ResolvedAt.IsZero() {
		t.Fatalf("forwarded resolution = %+v", admin.resolution)
	}
	var response map[string]any
	if err := json.Unmarshal(resolveResponse.Body.Bytes(), &response); err != nil || response["run_id"] != "run-1" {
		t.Fatalf("resolution response = %v, %v", response, err)
	}
}

type recordingExternalExecutionAdmin struct {
	summaries  []workflowcompat.ExternalExecutionSummary
	limit      int
	resolution workflowcompat.ExternalExecutionResolution
}

func (a *recordingExternalExecutionAdmin) ListAmbiguousExternalExecutions(_ context.Context, limit int) ([]workflowcompat.ExternalExecutionSummary, error) {
	a.limit = limit
	return a.summaries, nil
}

func (a *recordingExternalExecutionAdmin) ResolveExternalExecution(_ context.Context, resolution workflowcompat.ExternalExecutionResolution) (*workflowapi.RunRecord, error) {
	a.resolution = resolution
	return &workflowapi.RunRecord{Run: &workflowapi.RunState{
		RunID: "run-1", PipelineID: "release", Status: workflowapi.RunCompleted,
	}}, nil
}
