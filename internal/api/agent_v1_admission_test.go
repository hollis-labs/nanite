package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

func TestAgentV1TurnValidationPrecedesAdmission(t *testing.T) {
	a, mux := newTestAPI(t)
	view := &store.Session{Provider: "anthropic"}
	if err := a.store.CreateSession(t.Context(), view); err != nil {
		t.Fatal(err)
	}
	calls := 0
	a.Services.Chat = &agentChatStub{handleMessageFn: func(context.Context, string, string) (string, error) {
		calls++
		return "", service.ErrCognitiveQueueFull
	}}
	for _, test := range []struct {
		body string
		want int
	}{
		{`{"content":"legacy","delivery":"at_idle"}`, 400},
		{`{"content":[{"kind":"text","text":"hello"}],"delivery":"interrupt"}`, 422},
		{`{"content":[{"kind":"image","text":"hello"}],"delivery":"at_idle"}`, 422},
		{`{"content":[{"kind":"text","text":" "}],"delivery":"at_idle"}`, 400},
		{`{"content":[{"kind":"text","text":"hello"}],"delivery":"at_idle","effort":"hihg"}`, 400},
		{`{"content":[{"kind":"text","text":"hello","url":"removed"}],"delivery":"at_idle"}`, 400},
		{`{"content":[{"kind":"text","text":"hello"}],"delivery":"at_idle","cycle_kind":"chat"}`, 400},
		{`{"content":[{"kind":"text","text":"hello"}],"delivery":"at_idle"}`, 429},
	} {
		before := calls
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, agentV1RoutePrefix+"/sessions/"+view.ID+"/turns", bytes.NewBufferString(test.body)))
		if w.Code != test.want {
			t.Fatalf("body=%s status=%d want=%d response=%s", test.body, w.Code, test.want, w.Body.String())
		}
		if test.want != 429 && calls != before {
			t.Fatal("invalid request entered admission")
		}
		if test.want == 429 && !strings.Contains(w.Body.String(), `"code":"queue_full"`) {
			t.Fatalf("queue error=%s", w.Body.String())
		}
	}
}
