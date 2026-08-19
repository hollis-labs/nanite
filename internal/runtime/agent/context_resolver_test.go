package agent

// Phase 2 item 02 (TASKS/phase-2/02-port-forward-dynamic-resolver.md).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func TestResolveContextBlocks_Empty(t *testing.T) {
	out, err := ResolveContextBlocks(context.Background(), nil, "")
	if err != nil {
		t.Fatalf("ResolveContextBlocks(nil): %v", err)
	}
	if out != nil {
		t.Errorf("ResolveContextBlocks(nil) = %v, want nil", out)
	}
}

func TestResolveContextBlocks_Cmd(t *testing.T) {
	rows := []store.AgentContextResolver{
		{SlotName: "greeting", Kind: "cmd", Run: "printf hello-from-cmd", Timeout: "5s"},
	}
	out, err := ResolveContextBlocks(context.Background(), rows, "")
	if err != nil {
		t.Fatalf("ResolveContextBlocks: %v", err)
	}
	if got := out["greeting"]; got != "hello-from-cmd" {
		t.Errorf(`out["greeting"] = %q, want "hello-from-cmd"`, got)
	}
}

func TestResolveContextBlocks_CmdNonZeroExitAbortsWithNamedSlot(t *testing.T) {
	rows := []store.AgentContextResolver{
		{SlotName: "broken", Kind: "cmd", Run: "exit 3"},
	}
	_, err := ResolveContextBlocks(context.Background(), rows, "")
	if err == nil {
		t.Fatal("expected an error for a non-zero cmd exit, got nil")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Errorf("error %q does not name the offending slot", err.Error())
	}
}

func TestResolveContextBlocks_CmdBadTimeoutRejected(t *testing.T) {
	rows := []store.AgentContextResolver{
		{SlotName: "s1", Kind: "cmd", Run: "echo hi", Timeout: "not-a-duration"},
	}
	if _, err := ResolveContextBlocks(context.Background(), rows, ""); err == nil {
		t.Fatal("expected an error for a malformed timeout, got nil")
	}
}

func TestResolveContextBlocks_UnsupportedKindRejected(t *testing.T) {
	rows := []store.AgentContextResolver{
		{SlotName: "s1", Kind: "role_summary"},
	}
	if _, err := ResolveContextBlocks(context.Background(), rows, ""); err == nil {
		t.Fatal("expected an error for an unsupported resolver kind, got nil")
	}
}

func TestResolveContextBlocks_HTTPText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Test-Header"); got != "yes" {
			t.Errorf("request missing X-Test-Header, got %q", got)
		}
		w.Write([]byte("weather is sunny"))
	}))
	defer srv.Close()

	rows := []store.AgentContextResolver{
		{
			SlotName:    "weather",
			Kind:        "http",
			URL:         srv.URL,
			HeadersJSON: `{"X-Test-Header":"yes"}`,
		},
	}
	out, err := ResolveContextBlocks(context.Background(), rows, "")
	if err != nil {
		t.Fatalf("ResolveContextBlocks: %v", err)
	}
	if got := out["weather"]; got != "weather is sunny" {
		t.Errorf(`out["weather"] = %q, want "weather is sunny"`, got)
	}
}

func TestResolveContextBlocks_HTTPJSONWithJSONPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"forecast":{"today":"72F and sunny"}}`))
	}))
	defer srv.Close()

	rows := []store.AgentContextResolver{
		{
			SlotName:       "weather",
			Kind:           "http",
			URL:            srv.URL,
			ResponseFormat: "json",
			JSONPath:       "$.forecast.today",
		},
	}
	out, err := ResolveContextBlocks(context.Background(), rows, "")
	if err != nil {
		t.Fatalf("ResolveContextBlocks: %v", err)
	}
	if got := out["weather"]; got != "72F and sunny" {
		t.Errorf(`out["weather"] = %q, want "72F and sunny"`, got)
	}
}

func TestResolveContextBlocks_HTTPNon2xxAbortsWithNamedSlot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	rows := []store.AgentContextResolver{
		{SlotName: "broken-http", Kind: "http", URL: srv.URL},
	}
	_, err := ResolveContextBlocks(context.Background(), rows, "")
	if err == nil {
		t.Fatal("expected an error for a non-2xx HTTP response, got nil")
	}
	if !strings.Contains(err.Error(), "broken-http") {
		t.Errorf("error %q does not name the offending slot", err.Error())
	}
}

func TestResolveContextBlocks_MultipleSlots(t *testing.T) {
	rows := []store.AgentContextResolver{
		{SlotName: "a", Kind: "cmd", Run: "printf first"},
		{SlotName: "b", Kind: "cmd", Run: "printf second"},
	}
	out, err := ResolveContextBlocks(context.Background(), rows, "")
	if err != nil {
		t.Fatalf("ResolveContextBlocks: %v", err)
	}
	if out["a"] != "first" || out["b"] != "second" {
		t.Errorf("ResolveContextBlocks = %v, want a=first b=second", out)
	}
}
