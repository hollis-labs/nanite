package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestAgentV1ClientContextAdmissionRefusesBeforeEffects(t *testing.T) {
	a, mux := newTestAPI(t)
	session := &store.Session{Provider: "fixture", Model: "model"}
	if err := a.store.CreateSession(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	accepted := 0
	a.Services.Chat = &agentChatStub{handleMessageFn: func(context.Context, string, string) (string, error) { accepted++; return "turn", nil }}
	contexts := []string{`null`, `{"Version":1,"view":{"route":"/docs"}}`, `{"version":1,"view":{"Route":"/docs"}}`, `{"version":1,"view":{"route":"/docs","actor":"secret"}}`, `{"version":1,"version":1,"view":{"route":"/docs"}}`, `{"version":1,"view":{"route":"/docs","search":"` + strings.Repeat("x", 1025) + `"}}`}
	for _, descriptor := range contexts {
		w := httptest.NewRecorder()
		body := `{"content":[{"kind":"text","text":"question"}],"delivery":"at_idle","client_context":` + descriptor + `}`
		mux.ServeHTTP(w, httptest.NewRequest("POST", agentV1RoutePrefix+"/sessions/"+session.ID+"/turns", strings.NewReader(body)))
		if w.Code != 400 || accepted != 0 || strings.Contains(w.Body.String(), "secret") {
			t.Fatal(w.Code, w.Body.String(), accepted)
		}
	}
	for _, body := range []string{
		`{"content":[{"kind":"text","text":"question"}],"delivery":"at_idle","Client_Context":{"version":1,"view":{"route":"/docs"}}}`,
		`{"content":[{"kind":"text","text":"question"}],"delivery":"at_idle","client_context":{},"client_context":{"version":1,"view":{"route":"/docs"}}}`,
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", agentV1RoutePrefix+"/sessions/"+session.ID+"/turns", strings.NewReader(body)))
		if w.Code != 400 || accepted != 0 {
			t.Fatal(w.Code, accepted)
		}
	}
	for _, suffix := range []string{`,"client_context":{"version":1,"view":{"route":"/docs"}}`, ""} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", agentV1RoutePrefix+"/sessions/"+session.ID+"/turns", strings.NewReader(`{"content":[{"kind":"text","text":"question"}],"delivery":"at_idle"`+suffix+`}`)))
		if w.Code != 202 || strings.Contains(w.Body.String(), "/docs") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if accepted != 2 {
		t.Fatal(accepted)
	}
	var rows int
	if err := a.store.DB.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&rows); err != nil || rows != 0 {
		t.Fatal(rows, err)
	}
}
