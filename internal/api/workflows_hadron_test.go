package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/agentworkflow"
	"github.com/hollis-labs/nanite/internal/store"
	workflowapi "github.com/hollis-labs/nanite/internal/workflowapi"
	"github.com/hollis-labs/nanite/internal/workflowcompat"
	"github.com/hollis-labs/nanite/internal/workflowhost"
)

const hadronAPITestSource = `workflow:
  name: Hadron API Pilot
  version: 1.0.0
steps:
  - id: direct-tool
    kind: nanite-tool
    kind_version: v1
    config:
      product_step_id: direct-tool
      product_kind: tool
      agent_id: api-agent
      tool: api_echo
      args:
        message: hello
    outputs:
      result:
        type: object
`

const callbackAPITestSource = `workflow:
  name: API callback resume
  version: 1.0.0
steps:
  - id: callback
    kind: nanite-external-callback
    kind_version: v1
    config:
      product_step_id: callback
      product_kind: gate
      authority_ref: callback-principal
    outputs:
      result:
        type: object
`

const approvalAPITestSource = `workflow:
  name: API approval resume
  version: 1.0.0
steps:
  - id: approval
    kind: nanite-approval
    kind_version: v1
    config:
      product_step_id: approval
      product_kind: gate
      authority_ref: approval-principal
    outputs:
      result:
        type: object
`

type hadronAPITestExecutor struct{}

func (hadronAPITestExecutor) ExecuteLLMStep(context.Context, agentworkflow.LLMStepRequest) (agentworkflow.LLMStepResult, error) {
	return agentworkflow.LLMStepResult{}, nil
}

func (hadronAPITestExecutor) ExecuteToolStep(context.Context, agentworkflow.ToolStepRequest) (agentworkflow.ToolStepResult, error) {
	return agentworkflow.ToolStepResult{Output: `{"echo":"hello"}`}, nil
}

func (hadronAPITestExecutor) Verify(context.Context, agentworkflow.VerifyRequest) (agentworkflow.VerifyResult, error) {
	return agentworkflow.VerifyResult{}, nil
}

func newTestAPIWithSharedSurface(t *testing.T) (*API, *http.ServeMux, *workflowhost.Engine) {
	t.Helper()
	a, mux := newTestAPI(t)
	state, err := workflowhost.NewWorkflowStateStore(a.Services.Store)
	if err != nil {
		t.Fatalf("NewWorkflowStateStore: %v", err)
	}
	engine := newAPITestWorkflowHost(t, a.Services.Store)
	coordinator, err := workflowcompat.NewCutoverCoordinator(a.Services.Store)
	if err != nil {
		t.Fatalf("NewCutoverCoordinator: %v", err)
	}
	if _, prepareErr := coordinator.PrepareSharedStartup(t.Context(), workflowcompat.CutoverRequest{
		Owner: "api-test", Token: "api-test-boot",
	}); prepareErr != nil {
		t.Fatalf("PrepareSharedStartup: %v", prepareErr)
	}
	executor := hadronAPITestExecutor{}
	surface, err := workflowcompat.NewSharedSurface(a.Services.Store, state, engine, executor)
	if err != nil {
		t.Fatalf("NewSharedSurface: %v", err)
	}
	a.SetWorkflowSurface(surface)
	a.SetWorkflowResponderAuthenticator(NewBasicWorkflowResponderAuthenticator("workflow-responder", "api-test-password"))
	return a, mux, engine
}

func TestWorkflowAPIExplicitHadronSelectionProjectsDurableStateWithoutMixingLegacy(t *testing.T) {
	a, mux, _ := newTestAPIWithSharedSurface(t)
	legacyStarted := time.Now().UTC().Add(-time.Minute)
	legacyCompleted := legacyStarted.Add(time.Second)
	if err := a.Services.Store.CreateWorkflowRun(t.Context(), &store.WorkflowRunRow{
		ID: "legacy-run", DefinitionName: "Legacy Only", Status: "completed",
		StartedAt: legacyStarted, CompletedAt: legacyCompleted,
	}); err != nil {
		t.Fatalf("CreateWorkflowRun legacy fixture: %v", err)
	}
	// The removed process-local store must not participate in any route.
	a.Services.RunStore.Add(workflowapi.PipelineInfo{ID: "legacy-only", Name: "Legacy Only", StepCount: 1}, &workflowapi.RunState{
		PipelineID: "legacy-only", RunID: "process-local-only", Status: workflowapi.RunCompleted,
		StepStates: map[string]*workflowapi.StepState{}, StartedAt: legacyStarted, CompletedAt: legacyStarted,
	})

	response := workflowAPIRequest(t, mux, http.MethodPost, "/api/workflows/runs?engine=hadron&locator=pilot.workflow.yaml", hadronAPITestSource, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("Hadron POST status=%d body=%s", response.Code, response.Body.String())
	}
	var created struct {
		RunID      string `json:"run_id"`
		PipelineID string `json:"pipeline_id"`
		Status     string `json:"status"`
	}
	decodeWorkflowAPIResponse(t, response, &created)
	if created.RunID == "" || created.PipelineID == "" || created.Status != "completed" {
		t.Fatalf("Hadron POST response=%+v", created)
	}

	defaultList := workflowAPIRequest(t, mux, http.MethodGet, "/api/workflows/runs", "", nil)
	var defaultRuns []*workflowapi.RunRecord
	decodeWorkflowAPIResponse(t, defaultList, &defaultRuns)
	if len(defaultRuns) != 1 || defaultRuns[0].Run.RunID != created.RunID {
		t.Fatalf("default shared list = %+v", defaultRuns)
	}

	hadronList := workflowAPIRequest(t, mux, http.MethodGet, "/api/workflows/runs?engine=hadron", "", nil)
	var durableRuns []*workflowapi.RunRecord
	decodeWorkflowAPIResponse(t, hadronList, &durableRuns)
	if len(durableRuns) != 1 || durableRuns[0].Run.RunID != created.RunID {
		t.Fatalf("Hadron list mixed legacy rows: %+v", durableRuns)
	}

	get := workflowAPIRequest(t, mux, http.MethodGet, "/api/workflows/runs/"+created.RunID+"?engine=hadron", "", nil)
	var record workflowapi.RunRecord
	decodeWorkflowAPIResponse(t, get, &record)
	step := record.Run.StepStates["direct-tool"]
	if record.Pipeline.ID != created.PipelineID || record.Pipeline.Name != "Hadron API Pilot" || record.Pipeline.StepCount != 1 || record.Run.Status != workflowapi.RunCompleted {
		t.Fatalf("durable run projection=%+v", record)
	}
	if step == nil || step.Status != workflowapi.StepCompleted || step.Attempts != 1 || step.Output == nil {
		t.Fatalf("durable step projection=%+v", step)
	}
	wantEventTypes := []string{"pipeline.started", "step.started", "step.completed", "pipeline.completed"}
	if got := workflowEventTypes(record.Events); !containsOrderedStrings(got, wantEventTypes) {
		t.Fatalf("durable events=%v, want ordered subsequence %v", got, wantEventTypes)
	}

	var engineKind, revisionID string
	if err := a.Services.Store.DB.QueryRow(`
SELECT engine_kind, definition_revision_id
FROM workflow_runs WHERE id=?`, created.RunID).Scan(&engineKind, &revisionID); err != nil {
		t.Fatalf("load workflow engine kind: %v", err)
	}
	if engineKind != store.WorkflowEngineIdentityShared.Kind {
		t.Fatalf("engine_kind=%q, want %q", engineKind, store.WorkflowEngineIdentityShared.Kind)
	}
	revision, err := a.Services.Store.GetWorkflowRunDefinitionRevision(t.Context(), created.RunID)
	if err != nil || revision.RevisionID != revisionID || revision.DefinitionName != "Hadron API Pilot" {
		t.Fatalf("run definition revision = %+v id=%q, %v", revision, revisionID, err)
	}

	legacyThroughHadron := workflowAPIRequest(t, mux, http.MethodGet, "/api/workflows/runs/legacy-run?engine=hadron", "", nil)
	if legacyThroughHadron.Code != http.StatusNotFound {
		t.Fatalf("legacy row through Hadron status=%d body=%s", legacyThroughHadron.Code, legacyThroughHadron.Body.String())
	}
	durableThroughDefault := workflowAPIRequest(t, mux, http.MethodGet, "/api/workflows/runs/"+created.RunID, "", nil)
	if durableThroughDefault.Code != http.StatusOK {
		t.Fatalf("durable row through default status=%d body=%s", durableThroughDefault.Code, durableThroughDefault.Body.String())
	}
	legacyList := workflowAPIRequest(t, mux, http.MethodGet, "/api/workflows/runs?engine=legacy", "", nil)
	var legacyRuns []*workflowapi.RunRecord
	decodeWorkflowAPIResponse(t, legacyList, &legacyRuns)
	if len(legacyRuns) != 1 || legacyRuns[0].Run.RunID != "legacy-run" {
		t.Fatalf("terminal legacy history = %+v", legacyRuns)
	}
	legacyGet := workflowAPIRequest(t, mux, http.MethodGet, "/api/workflows/runs/legacy-run?engine=legacy", "", nil)
	if legacyGet.Code != http.StatusOK {
		t.Fatalf("terminal legacy GET status=%d body=%s", legacyGet.Code, legacyGet.Body.String())
	}

	badEngine := workflowAPIRequest(t, mux, http.MethodGet, "/api/workflows/runs?engine=other", "", nil)
	if badEngine.Code != http.StatusBadRequest || badEngine.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("bad engine response status=%d content-type=%q body=%s", badEngine.Code, badEngine.Header().Get("Content-Type"), badEngine.Body.String())
	}
}

func TestWorkflowAPIHadronSSEIsFutureOnlyByDefaultAndResumesFromDurableCursor(t *testing.T) {
	_, mux, _ := newTestAPIWithSharedSurface(t)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	prior := postHadronAPIRun(t, server.URL)

	streamCtx, cancelStream := context.WithCancel(t.Context())
	defer cancelStream()
	request, err := http.NewRequestWithContext(streamCtx, http.MethodGet, server.URL+"/api/workflows/events?engine=hadron", nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("open future-only SSE: %v", err)
	}
	defer stream.Body.Close()
	if stream.StatusCode != http.StatusOK || stream.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("SSE response status=%d content-type=%q", stream.StatusCode, stream.Header.Get("Content-Type"))
	}

	current := postHadronAPIRun(t, server.URL)
	first := readWorkflowSSEEvent(t, stream.Body)
	if first.Event.RunID != current.RunID {
		t.Fatalf("future-only stream replayed prior run %q: event=%+v prior=%q", prior.RunID, first, prior.RunID)
	}
	if first.Cursor <= 0 {
		t.Fatalf("future-only SSE cursor=%d", first.Cursor)
	}
	cancelStream()
	_ = stream.Body.Close()

	resumeCtx, cancelResume := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancelResume()
	resumeRequest, err := http.NewRequestWithContext(resumeCtx, http.MethodGet, server.URL+"/api/workflows/events?engine=hadron&run_id="+current.RunID, nil)
	if err != nil {
		t.Fatal(err)
	}
	resumeRequest.Header.Set("Last-Event-ID", strconv.FormatInt(first.Cursor, 10))
	resumed, err := server.Client().Do(resumeRequest)
	if err != nil {
		t.Fatalf("resume SSE: %v", err)
	}
	defer resumed.Body.Close()
	next := readWorkflowSSEEvent(t, resumed.Body)
	if next.Cursor <= first.Cursor || next.Event.RunID != current.RunID {
		t.Fatalf("resumed SSE event=%+v, first cursor=%d", next, first.Cursor)
	}
}

type hadronAPIPostResponse struct {
	RunID      string `json:"run_id"`
	PipelineID string `json:"pipeline_id"`
	Status     string `json:"status"`
}

func postHadronAPIRun(t *testing.T, baseURL string) hadronAPIPostResponse {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+"/api/workflows/runs?engine=hadron", strings.NewReader(hadronAPITestSource))
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST Hadron run: %v", err)
	}
	defer response.Body.Close()
	var result hadronAPIPostResponse
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("POST Hadron run status=%d body=%s", response.StatusCode, body)
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode POST Hadron run: %v", err)
	}
	return result
}

type parsedWorkflowSSEEvent struct {
	Cursor int64
	Type   string
	Event  workflowapi.Event
}

func readWorkflowSSEEvent(t *testing.T, body io.Reader) parsedWorkflowSSEEvent {
	t.Helper()
	reader := bufio.NewReader(body)
	var result parsedWorkflowSSEEvent
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE event: %v", err)
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		switch {
		case strings.HasPrefix(line, "id: "):
			result.Cursor, err = strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "id: ")), 10, 64)
			if err != nil {
				t.Fatalf("parse SSE id: %v", err)
			}
		case strings.HasPrefix(line, "event: "):
			result.Type = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		case strings.HasPrefix(line, "data: "):
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &result.Event); err != nil {
				t.Fatalf("decode SSE data: %v", err)
			}
		case line == "":
			if result.Cursor != 0 && result.Type != "" {
				return result
			}
		}
	}
}

func workflowAPIRequest(t *testing.T, handler http.Handler, method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func decodeWorkflowAPIResponse(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("API status=%d body=%s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode API response: %v body=%s", err, response.Body.String())
	}
}

func workflowEventTypes(events []workflowapi.Event) []string {
	result := make([]string, len(events))
	for i := range events {
		result[i] = events[i].Type
	}
	return result
}

func containsOrderedStrings(got, want []string) bool {
	index := 0
	for _, value := range got {
		if index < len(want) && value == want[index] {
			index++
		}
	}
	return index == len(want)
}

func TestWorkflowAPIHadronSelectionRequiresProductionSurface(t *testing.T) {
	_, mux := newTestAPI(t)
	response := workflowAPIRequest(t, mux, http.MethodGet, "/api/workflows/runs?engine=hadron", "", nil)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unwired Hadron status=%d body=%s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("unwired Hadron content-type=%q", got)
	}
}

func TestWorkflowAPIQueriesExactSharedAndPilotIdentitiesButRejectsMixedPairs(t *testing.T) {
	a, mux, _ := newTestAPIWithSharedSurface(t)
	created := workflowAPIRequest(t, mux, http.MethodPost, "/api/workflows/runs?locator=identity.workflow.yaml", hadronAPITestSource, nil)
	var result hadronAPIPostResponse
	decodeWorkflowAPIResponse(t, created, &result)
	revision, err := a.Services.Store.GetWorkflowRunDefinitionRevision(t.Context(), result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	pilotRevision := revision
	pilotRevision.RevisionID += "-pilot"
	pilotRevision.Engine = store.WorkflowEngineIdentityPilot
	pilotRevision.RegisteredBy = "pre-extraction-pilot"
	pilotRevision.CreatedAt = pilotRevision.CreatedAt.Add(time.Nanosecond)
	if _, err := a.Services.Store.CreateWorkflowDefinitionRevision(t.Context(), pilotRevision); err != nil {
		t.Fatalf("create exact pilot revision: %v", err)
	}
	if _, err := a.Services.Store.DB.ExecContext(t.Context(), `
UPDATE workflow_runs
SET engine_kind = ?, engine_contract_version = ?, definition_revision_id = ?
WHERE id = ?`, store.WorkflowEngineIdentityPilot.Kind, store.WorkflowEngineIdentityPilot.ContractVersion,
		pilotRevision.RevisionID, result.RunID); err != nil {
		t.Fatalf("stamp exact pilot identity: %v", err)
	}
	get := workflowAPIRequest(t, mux, http.MethodGet, "/api/workflows/runs/"+result.RunID, "", nil)
	if get.Code != http.StatusOK {
		t.Fatalf("exact pilot GET status=%d body=%s", get.Code, get.Body.String())
	}

	if _, err := a.Services.Store.DB.ExecContext(t.Context(), `
UPDATE workflow_runs SET engine_contract_version = 'v0.1.0' WHERE id = ?`, result.RunID); err != nil {
		t.Fatal(err)
	}
	mixed := workflowAPIRequest(t, mux, http.MethodGet, "/api/workflows/runs/"+result.RunID, "", nil)
	if mixed.Code != http.StatusNotFound {
		t.Fatalf("mixed engine pair GET status=%d body=%s", mixed.Code, mixed.Body.String())
	}
	list := workflowAPIRequest(t, mux, http.MethodGet, "/api/workflows/runs", "", nil)
	var rows []*workflowapi.RunRecord
	decodeWorkflowAPIResponse(t, list, &rows)
	if len(rows) != 0 {
		t.Fatalf("mixed engine pair leaked into list: %+v", rows)
	}
}

func TestWorkflowAPIHadronRejectsInvalidSource(t *testing.T) {
	_, mux, _ := newTestAPIWithSharedSurface(t)
	response := workflowAPIRequest(t, mux, http.MethodPost, "/api/workflows/runs?engine=hadron", "not: [valid", nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid Hadron source status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestWorkflowAPIHadronCancelProjectsClosedLegacyStatus(t *testing.T) {
	a, mux, engine := newTestAPIWithSharedSurface(t)
	definition := agentworkflow.WorkflowDefinition{
		Name: "API cancellation", Engine: agentworkflow.EngineHadron,
		Steps: []agentworkflow.StepDefinition{{ID: "approval", Kind: agentworkflow.StepKindGate, Config: map[string]any{
			"correlation": "api-approval", "authority_ref": "release-manager",
		}}},
	}
	waiting, err := engine.Run(t.Context(), definition, agentworkflow.WorkflowInput{}, hadronAPITestExecutor{})
	if err != nil {
		t.Fatalf("create waiting durable run: %v", err)
	}
	waitingResponse := workflowAPIRequest(t, mux, http.MethodGet, "/api/workflows/runs/"+waiting.RunID+"?engine=hadron", "", nil)
	var waitingRecord workflowapi.RunRecord
	decodeWorkflowAPIResponse(t, waitingResponse, &waitingRecord)
	if waitingRecord.Run.Status != workflowapi.RunRunning || waitingRecord.Run.StepStates["approval"] == nil || waitingRecord.Run.StepStates["approval"].Status != workflowapi.StepRunning {
		t.Fatalf("waiting state escaped closed compatibility statuses: %+v", waitingRecord.Run)
	}
	response := workflowAPIRequest(t, mux, http.MethodPost, fmt.Sprintf("/api/workflows/runs/%s/cancel?engine=hadron", waiting.RunID), "", nil)
	var status map[string]string
	decodeWorkflowAPIResponse(t, response, &status)
	if status["status"] != "canceled" {
		t.Fatalf("cancel response=%v", status)
	}
	run, err := a.Services.Store.GetWorkflowRun(t.Context(), waiting.RunID)
	if err != nil || run.Status != "canceled" {
		t.Fatalf("durable canceled run=%+v err=%v", run, err)
	}
}

func TestWorkflowAPIAuthenticatedCallbackAndApprovalAreIdempotent(t *testing.T) {
	tests := []struct {
		name      string
		source    string
		stepID    string
		principal string
		pathPart  string
	}{
		{name: "callback", source: callbackAPITestSource, stepID: "callback", principal: "callback-principal", pathPart: "callbacks"},
		{name: "approval", source: approvalAPITestSource, stepID: "approval", principal: "approval-principal", pathPart: "approvals"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			a, mux, _ := newTestAPIWithSharedSurface(t)
			a.SetWorkflowResponderAuthenticator(NewBasicWorkflowResponderAuthenticator(test.principal, "api-test-password"))
			created := workflowAPIRequest(t, mux, http.MethodPost, "/api/workflows/runs?locator="+test.name+".workflow.yaml", test.source, nil)
			var waiting hadronAPIPostResponse
			decodeWorkflowAPIResponse(t, created, &waiting)
			if waiting.Status != "running" {
				t.Fatalf("waiting launch = %+v", waiting)
			}
			path := fmt.Sprintf("/api/workflows/runs/%s/%s/%s", waiting.RunID, test.pathPart, test.stepID)
			body := `{"payload":{"accepted":true}}`

			unauthenticated := workflowAPIRequest(t, mux, http.MethodPost, path, body, map[string]string{"Idempotency-Key": test.name + "-1"})
			if unauthenticated.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated response status=%d body=%s", unauthenticated.Code, unauthenticated.Body.String())
			}
			missingKey := workflowAuthenticatedRequest(t, mux, path, body, test.principal, "api-test-password", "")
			if missingKey.Code != http.StatusBadRequest {
				t.Fatalf("missing idempotency key status=%d body=%s", missingKey.Code, missingKey.Body.String())
			}
			wrongPassword := workflowAuthenticatedRequest(t, mux, path, body, "workflow-responder", "wrong-password", test.name+"-1")
			if wrongPassword.Code != http.StatusUnauthorized {
				t.Fatalf("wrong password status=%d body=%s", wrongPassword.Code, wrongPassword.Body.String())
			}
			// Authentication and workflow authority are separate checks. For
			// this branch temporarily authenticate the immutable wait's wrong
			// principal to prove the host still rejects it.
			a.SetWorkflowResponderAuthenticator(NewBasicWorkflowResponderAuthenticator("wrong-principal", "api-test-password"))
			wrongPrincipal := workflowAuthenticatedRequest(t, mux, path, body, "wrong-principal", "api-test-password", test.name+"-1")
			if wrongPrincipal.Code != http.StatusForbidden {
				t.Fatalf("wrong principal status=%d body=%s", wrongPrincipal.Code, wrongPrincipal.Body.String())
			}
			a.SetWorkflowResponderAuthenticator(NewBasicWorkflowResponderAuthenticator(test.principal, "api-test-password"))
			accepted := workflowAuthenticatedRequest(t, mux, path, body, test.principal, "api-test-password", test.name+"-1")
			var completed hadronAPIPostResponse
			decodeWorkflowAPIResponse(t, accepted, &completed)
			if completed.RunID != waiting.RunID || completed.Status != "completed" {
				t.Fatalf("accepted response = %+v", completed)
			}
			replayed := workflowAuthenticatedRequest(t, mux, path, body, test.principal, "api-test-password", test.name+"-1")
			var replayResult hadronAPIPostResponse
			decodeWorkflowAPIResponse(t, replayed, &replayResult)
			if replayResult != completed {
				t.Fatalf("idempotent replay = %+v, want %+v", replayResult, completed)
			}

			var responderRef, idempotencyKey string
			if err := a.Services.Store.DB.QueryRowContext(t.Context(), `
SELECT json_extract(record_json, '$.resolution.responder.reference'),
       json_extract(record_json, '$.resolution.idempotency_key')
FROM workflow_waits WHERE run_id = ?`, waiting.RunID).Scan(&responderRef, &idempotencyKey); err != nil {
				t.Fatalf("load persisted responder provenance: %v", err)
			}
			if responderRef != test.principal || idempotencyKey != test.name+"-1" {
				t.Fatalf("persisted responder=%q idempotency=%q", responderRef, idempotencyKey)
			}
		})
	}
}

func workflowAuthenticatedRequest(t *testing.T, handler http.Handler, target, body, user, password, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	request.SetBasicAuth(user, password)
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
