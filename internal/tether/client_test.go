package tether

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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

// Mail arriving between the count and the tail fetch shifts the tail; List
// refetches at the new offset instead of skipping the oldest message.
func TestListRefetchesWhenMailArrives(t *testing.T) {
	var offsets []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset := r.URL.Query().Get("offset")
		offsets = append(offsets, offset)
		switch offset {
		case "":
			_, _ = w.Write([]byte(`{"total":60,"messages":[]}`))
		case "10":
			// Two new messages arrived: this page misses the two oldest.
			_, _ = w.Write([]byte(`{"total":62,"messages":[{"id":"third","to":"msg://agent/test/me","created_at":"2026-01-03T00:00:00Z"}]}`))
		default:
			_, _ = w.Write([]byte(`{"total":62,"messages":[{"id":"second","to":"msg://agent/test/me","created_at":"2026-01-02T00:00:00Z"},{"id":"first","to":"msg://agent/test/me","created_at":"2026-01-01T00:00:00Z"}]}`))
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
	if strings.Join(offsets, ",") != ",10,12" || len(messages) != 2 || messages[0].ID != "first" {
		t.Fatalf("offsets=%q messages=%v", offsets, messages)
	}
}

// A total that falls under one page refetches from the start rather than
// keeping a page fetched past the shrunken tail.
func TestListRefetchesWhenTotalShrinks(t *testing.T) {
	var offsets []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset := r.URL.Query().Get("offset")
		offsets = append(offsets, offset)
		if offset == "" && len(offsets) == 1 {
			_, _ = w.Write([]byte(`{"total":51,"messages":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"total":40,"messages":[{"id":"first","to":"msg://agent/test/me","created_at":"2026-01-01T00:00:00Z"}]}`))
	}))
	defer server.Close()
	c, err := New(Config{Endpoint: server.URL, CredentialFile: testCredential(t)})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := c.List(t.Context(), "msg://agent/test/me")
	if err != nil || strings.Join(offsets, ",") != ",1," || len(messages) != 1 {
		t.Fatalf("offsets=%q messages=%v err=%v", offsets, messages, err)
	}
}

func TestListGivesUpWhenTotalKeepsChanging(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"total":` + strconv.Itoa(50+calls) + `,"messages":[]}`))
	}))
	defer server.Close()
	c, err := New(Config{Endpoint: server.URL, CredentialFile: testCredential(t)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.List(t.Context(), "msg://agent/test/me"); err == nil || calls != listAttempts+1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

// Tether lists newest first; messages with equal timestamps keep that order
// reversed, so the older of the two comes out first.
func TestListOrdersEqualTimestampsByWirePosition(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"total":3,"messages":[{"id":"c","to":"msg://agent/test/me","created_at":"2026-01-01T00:00:00Z"},{"id":"b","to":"msg://agent/test/me","created_at":"2026-01-01T00:00:00Z"},{"id":"a","to":"msg://agent/test/me","created_at":"2026-01-01T00:00:00Z"}]}`))
	}))
	defer server.Close()
	c, err := New(Config{Endpoint: server.URL, CredentialFile: testCredential(t)})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := c.List(t.Context(), "msg://agent/test/me")
	if err != nil || len(messages) != 3 || messages[0].ID != "a" || messages[2].ID != "c" {
		t.Fatalf("messages=%v err=%v", messages, err)
	}
}
