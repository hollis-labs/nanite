package pluginapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func TestClientLoopbackWireAndToolError(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/tools/call" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("wrong tool-call wire request")
		}
		var call pluginapi.ToolCall
		if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
			t.Error(err)
		}
		if call.SessionID != "s-123" || call.Name != "bookmark_get" || call.Args["id"] != "missing" {
			t.Error("lost session or tool arguments")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"not found"}],"isError":true}`))
	}))
	defer s.Close()
	c, err := pluginapi.NewClient(s.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := c.CallTool(context.Background(), pluginapi.ToolCall{SessionID: "s-123", Name: "bookmark_get", Args: map[string]any{"id": "missing"}})
	if err != nil || !result.IsError || len(result.Content) != 1 {
		t.Fatalf("got %+v, %v", result, err)
	}
}

func TestClientRefusesUnsafeOrigins(t *testing.T) {
	for _, u := range []string{"http://localhost:8090", "http://example.org", "http://192.168.1.1:8090", "file:///tmp/host", "http://user:secret@127.0.0.1", "http://127.0.0.1/api", "http://127.0.0.1?query", "http://127.0.0.1?", "http://127.0.0.1#fragment"} {
		if _, err := pluginapi.NewClient(u, nil); err == nil {
			t.Fatalf("accepted %s", u)
		}
	}
	for _, u := range []string{"http://127.0.0.1:8090", "http://[::1]:8090/"} {
		if _, err := pluginapi.NewClient(u, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClientFailuresDoNotRetryOrLeakBodies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"transport error", 500, `{"error":"secret-value"}`},
		{"malformed JSON", 200, `{`},
		{"missing content", 200, `{}`},
		{"oversize", 200, strings.Repeat(" ", (4<<20)+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer s.Close()
			c, err := pluginapi.NewClient(s.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.CallTool(context.Background(), pluginapi.ToolCall{Name: "bookmark_delete"})
			if err == nil || strings.Contains(err.Error(), "secret-value") || calls != 1 {
				t.Fatalf("calls=%d, err=%v", calls, err)
			}
		})
	}
}

func TestClientRefusesRedirectAndHonorsCancellation(t *testing.T) {
	forwarded := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded = true }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer s.Close()
	c, err := pluginapi.NewClient(s.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CallTool(context.Background(), pluginapi.ToolCall{Name: "test"}); err == nil || forwarded {
		t.Fatal("followed host redirect")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.CallTool(ctx, pluginapi.ToolCall{Name: "test"}); err == nil {
		t.Fatal("ignored cancellation")
	}
}
