package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
	agentservice "github.com/hollis-labs/substrate/agent/service"

	chatstream "github.com/hollis-labs/libs/ui-go/chatstream"
	"github.com/hollis-labs/libs/ui-go/chatstream/conformance"
	"github.com/hollis-labs/libs/ui-go/chatstream/framing"
	"github.com/hollis-labs/nanite/internal/service"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

type cognitiveHTTPProvider struct {
	release <-chan struct{}
	started chan struct{}
	once    sync.Once
}

func (p *cognitiveHTTPProvider) StreamChat(ctx context.Context, _ llmtypes.ChatRequest) (<-chan llmtypes.StreamEvent, error) {
	ch := make(chan llmtypes.StreamEvent, 3)
	ch <- llmtypes.StreamEvent{Type: "delta", Content: "native answer"}
	p.once.Do(func() { close(p.started) })
	go func() {
		select {
		case <-p.release:
			ch <- llmtypes.StreamEvent{Type: "usage", Usage: &llmtypes.Usage{InputTokens: 5, OutputTokens: 2, StopReason: "end_turn"}}
			ch <- llmtypes.StreamEvent{Type: "done"}
		case <-ctx.Done():
		}
		close(ch)
	}()
	return ch, nil
}
func (p *cognitiveHTTPProvider) Complete(context.Context, llmtypes.ChatRequest) (string, error) {
	return "summary", nil
}
func (p *cognitiveHTTPProvider) Capabilities() llmtypes.ProviderCapabilities {
	return llmtypes.ProviderCapabilities{}
}

func TestAgentV1RealTurnHTTPReplayStatusCancelAndRestart(t *testing.T) {
	a, mux := newTestAPI(t)
	allowTestNativeModel(a)
	release := make(chan struct{})
	finish := sync.OnceFunc(func() { close(release) })
	defer finish()
	provider := &cognitiveHTTPProvider{release: release, started: make(chan struct{})}
	a.Services.Providers.Register("fixture", provider)
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		var reader io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, reader))
		return w
	}
	created := request("POST", agentV1RoutePrefix+"/sessions", agentV1CreateSessionRequest{DefinitionRef: a.Services.CognitiveViews.DefaultDefinitionRef})
	if created.Code != 201 {
		t.Fatal(created.Code, created.Body.String())
	}
	var view agentV1SessionResponse
	_ = json.Unmarshal(created.Body.Bytes(), &view)
	accepted := request("POST", view.RouteHints.Turns, agentV1TurnRequest{Content: []agentV1InputPart{{Kind: "text", Text: "question"}}, Delivery: "at_idle", DeltaMode: "live"})
	if accepted.Code != 202 {
		t.Fatal(accepted.Code, accepted.Body.String())
	}
	var turn agentV1TurnResponse
	_ = json.Unmarshal(accepted.Body.Bytes(), &turn)
	select {
	case <-provider.started:
	case <-time.After(5 * time.Second):
		t.Fatal("native provider not dispatched")
	}
	status := request("GET", turn.Links.Status, nil)
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"state":"working"`) {
		t.Fatal(status.Code, status.Body.String())
	}
	// Queued cancellation is scoped; it does not cancel the executing neighbor.
	queued := request("POST", view.RouteHints.Turns, agentV1TurnRequest{Content: []agentV1InputPart{{Kind: "text", Text: "queued"}}, Delivery: "at_idle"})
	var next agentV1TurnResponse
	_ = json.Unmarshal(queued.Body.Bytes(), &next)
	canceled := request("POST", next.Links.Cancel, nil)
	if canceled.Code != 200 {
		t.Fatal(canceled.Code, canceled.Body.String())
	}
	stillWorking := request("GET", turn.Links.Status, nil)
	if !strings.Contains(stillWorking.Body.String(), `"state":"working"`) {
		t.Fatal("neighbor was canceled", stillWorking.Body.String())
	}
	for _, operation := range []string{"retry", "agent-message", "chat/cancel"} {
		refused := request("POST", "/api/sessions/"+view.SessionViewID+"/"+operation, map[string]string{"from_session_id": view.SessionViewID, "content": "bypass"})
		if refused.Code != 422 {
			t.Fatalf("retained%s reached defined execution: %d %s", operation, refused.Code, refused.Body.String())
		}
	}
	finish()
	events := request("GET", turn.Links.Events, nil)
	if events.Code != 200 || events.Header().Get("X-Chat-Encoding") != "chatstream/v1" || events.Header().Get("Cache-Control") != "private, no-store, no-transform" {
		t.Fatal(events.Code, events.Header(), events.Body.String())
	}
	var canonical []chatstream.Event
	for frame, err := range framing.SSE(strings.NewReader(events.Body.String())) {
		if err != nil {
			t.Fatal(err)
		}
		var event chatstream.Event
		if err = json.Unmarshal(frame.Data, &event); err != nil {
			t.Fatal(err)
		}
		canonical = append(canonical, event)
	}
	if violations := conformance.Validate(canonical); len(violations) > 0 {
		t.Fatal(violations)
	}
	reduced, err := chatstream.Reduce(canonical, nil)
	if err != nil || reduced.Status != chatstream.StatusFinished {
		t.Fatal(reduced, err)
	}
	status = request("GET", turn.Links.Status, nil)
	var snapshot agentservice.Snapshot[store.Message]
	if err = json.Unmarshal(status.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.State != "completed" || snapshot.CommittedMessage == nil || snapshot.EventCheckpoint != reduced.LastSeq || snapshot.Message.LastSeq != snapshot.EventCheckpoint {
		t.Fatal(status.Body.String())
	}
	// Durable JSON commits outcome and event checkpoint together. A new manager
	// reopens the snapshot but has no claim to the lost in-process event journal.
	reopened, err := service.NewCognitiveTurns(a.store).Get(view.SessionViewID, turn.TurnID)
	if err != nil || reopened.State != "completed" || reopened.EventCheckpoint != snapshot.EventCheckpoint || reopened.Message.LastSeq != snapshot.Message.LastSeq {
		t.Fatal(reopened, err)
	}
	if _, err := service.NewCognitiveTurns(a.store).Get("wrong-owner", turn.TurnID); err == nil {
		t.Fatal("wrong owner found persisted turn")
	}
	replayRequest := httptest.NewRequest("GET", turn.Links.Events, nil)
	replayRequest.Header.Set("Last-Event-ID", "2")
	replay := httptest.NewRecorder()
	mux.ServeHTTP(replay, replayRequest)
	for frame, err := range framing.SSE(strings.NewReader(replay.Body.String())) {
		if err != nil {
			t.Fatal(err)
		}
		var event chatstream.Event
		_ = json.Unmarshal(frame.Data, &event)
		if event.Seq <= 2 {
			t.Fatal("duplicate replay", event)
		}
	}
	badCursor := httptest.NewRequest("GET", turn.Links.Events, nil)
	badCursor.Header.Set("Last-Event-ID", "-1")
	bad := httptest.NewRecorder()
	mux.ServeHTTP(bad, badCursor)
	if bad.Code != 400 {
		t.Fatal(bad.Code)
	}
	aheadRequest := httptest.NewRequest("GET", turn.Links.Events, nil)
	aheadRequest.Header.Set("Last-Event-ID", "999999")
	ahead := httptest.NewRecorder()
	mux.ServeHTTP(ahead, aheadRequest)
	if !strings.Contains(ahead.Body.String(), `"verb":"gap"`) {
		t.Fatal("missing explicit gap", ahead.Body.String())
	}
	// Repeated cancel cannot rewrite a committed completed outcome.
	final := request("POST", turn.Links.Cancel, nil)
	if !strings.Contains(final.Body.String(), `"state":"completed"`) {
		t.Fatal(final.Body.String())
	}
}
