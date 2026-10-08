package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestAgentV1BoundedHistoryStablePages(t *testing.T) {
	a, mux := newTestAPI(t)
	view := &store.Session{Provider: "anthropic"}
	if err := a.store.CreateSession(t.Context(), view); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		if err := a.store.CreateMessage(t.Context(), &store.Message{ID: id, SessionID: view.ID, Role: "assistant", Content: id}); err != nil {
			t.Fatal(err)
		}
	}
	type page struct {
		Items []MessageView `json:"items"`
		Older *string       `json:"older_cursor"`
	}
	get := func(path string) (int, page) {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		var out page
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	path := agentV1RoutePrefix + "/sessions/" + view.ID + "/messages?limit=2"
	code, first := get(path)
	if code != 200 || len(first.Items) != 2 || first.Items[0].ID != "d" || first.Items[1].ID != "e" || first.Older == nil {
		t.Fatalf("first=%d %+v", code, first)
	}
	// New messages must not shift older pages, including equal timestamps.
	if err := a.store.CreateMessage(t.Context(), &store.Message{ID: "f", SessionID: view.ID, Role: "assistant", Content: "f"}); err != nil {
		t.Fatal(err)
	}
	code, second := get(path + "&older_cursor=" + *first.Older)
	if code != 200 || len(second.Items) != 2 || second.Items[0].ID != "b" || second.Items[1].ID != "c" || second.Older == nil {
		t.Fatalf("second=%d %+v", code, second)
	}
	code, last := get(path + "&older_cursor=" + *second.Older)
	if code != 200 || len(last.Items) != 1 || last.Items[0].ID != "a" || last.Older != nil {
		t.Fatalf("last=%d %+v", code, last)
	}
	other := &store.Session{Provider: "anthropic"}
	if err := a.store.CreateSession(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	if code, _ := get(agentV1RoutePrefix + "/sessions/" + other.ID + "/messages?older_cursor=" + *first.Older); code != 400 {
		t.Errorf("cross-view cursor=%d", code)
	}
	if code, _ := get(path + "&older_cursor=" + *first.Older + "x"); code != 400 {
		t.Errorf("tampered cursor=%d", code)
	}
}

func TestAgentV1BoundedViewsAndInvalidLimits(t *testing.T) {
	a, mux := newTestAPI(t)
	for _, id := range []string{"view-a", "view-b", "view-c"} {
		if err := a.store.CreateSession(t.Context(), &store.Session{ID: id, Provider: "anthropic"}); err != nil {
			t.Fatal(err)
		}
	}
	type page struct {
		Items []SessionView `json:"items"`
		Next  *string       `json:"next_cursor"`
	}
	read := func(path string) (int, page) {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		var out page
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	code, first := read(agentV1RoutePrefix + "/sessions?limit=2")
	if code != 200 || len(first.Items) != 2 || first.Next == nil {
		t.Fatalf("first=%d %+v", code, first)
	}
	code, next := read(agentV1RoutePrefix + "/sessions?limit=2&cursor=" + *first.Next)
	if code != 200 || len(next.Items) != 1 || next.Next != nil || next.Items[0].ID == first.Items[0].ID || next.Items[0].ID == first.Items[1].ID {
		t.Fatalf("next=%d %+v", code, next)
	}
	for _, limit := range []string{"0", "-1", "201", "many"} {
		if code, _ := read(agentV1RoutePrefix + "/sessions?limit=" + limit); code != 400 {
			t.Errorf("limit=%s code=%d", limit, code)
		}
	}
	if code, _ := read(fmt.Sprintf("%s/sessions?include_archived=true&cursor=%s", agentV1RoutePrefix, *first.Next)); code != 400 {
		t.Errorf("changed query=%d", code)
	}
}
