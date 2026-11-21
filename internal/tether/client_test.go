package tether

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestClientRejectsWrongMailboxAndDoesNotFollowRedirects(t *testing.T) {
	var destination atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destination.Add(1); w.WriteHeader(204) }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/messages/list") {
			_, _ = w.Write([]byte(`{"messages":[{"id":"one","to":"msg://agent/test/another"}]}`))
			return
		}
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	c, err := New(Config{Endpoint: origin.URL, Credential: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.List(t.Context(), "msg://agent/test/me"); err == nil {
		t.Fatal("foreign mailbox accepted")
	}
	if err = c.MarkRead(t.Context(), "one", "msg://agent/test/me"); err == nil {
		t.Fatal("redirect accepted")
	}
	if destination.Load() != 0 {
		t.Fatal("credential redirect followed")
	}
}
