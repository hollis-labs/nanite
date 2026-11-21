package tether

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	c, err := New(Config{Endpoint: origin.URL, CredentialFile: testCredential(t)})
	if err != nil {
		t.Fatal(err)
	}
	if messages, err := c.List(t.Context(), "msg://agent/test/me"); err != nil || len(messages) != 0 {
		t.Fatalf("foreign mail not skipped: %v %v", messages, err)
	}
	if err = c.MarkRead(t.Context(), "one", "msg://agent/test/me"); err == nil {
		t.Fatal("redirect accepted")
	}
	if destination.Load() != 0 {
		t.Fatal("credential redirect followed")
	}
}

func testCredential(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestCredentialAndTransportRequirements(t *testing.T) {
	path := testCredential(t)
	if _, err := New(Config{Endpoint: "http://example.com", CredentialFile: path}); err == nil {
		t.Fatal("remote HTTP allowed")
	}
	if err := os.Chmod(path, 0644); err != nil { // #nosec G302 -- intentionally insecure fixture must be rejected.
		t.Fatal(err)
	}
	if _, err := New(Config{Endpoint: "https://example.com", CredentialFile: path}); err == nil {
		t.Fatal("public credential allowed")
	}
}

func TestListDrainsOldestPage(t *testing.T) {
	var offsets []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offsets = append(offsets, r.URL.Query().Get("offset"))
		if r.URL.Query().Get("offset") == "" {
			_, _ = w.Write([]byte(`{"total":100,"messages":[{"id":"latest","to":"msg://agent/test/me","created_at":"2026-01-03T00:00:00Z"}]}`))
		} else {
			_, _ = w.Write([]byte(`{"total":100,"messages":[{"id":"second","to":"msg://agent/test/me","created_at":"2026-01-02T00:00:00Z"},{"id":"first","to":"msg://agent/test/me","created_at":"2026-01-01T00:00:00Z"}]}`))
		}
	}))
	defer server.Close()
	c, err := New(Config{Endpoint: server.URL, CredentialFile: testCredential(t)})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := c.List(t.Context(), "msg://agent/test/me")
	if err != nil {
		t.Fatal(err)
	}
	if len(offsets) != 2 || offsets[1] != "50" || len(messages) != 2 || messages[0].ID != "first" {
		t.Fatalf("offsets=%v messages=%v", offsets, messages)
	}
}
