package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Regression tests for audit 2026-04-10-mcp-client-transport finding 02
// (unbounded response body → memory DoS). Both transports must refuse
// payloads above their configured cap rather than buffering them.

func TestReadLineBounded_AllowsUpToLimit(t *testing.T) {
	payload := strings.Repeat("x", 1024) + "\n"
	r := bufio.NewReader(strings.NewReader(payload))
	got, err := readLineBounded(r, 2048)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1024 {
		t.Fatalf("expected 1024 bytes, got %d", len(got))
	}
}

func TestReadLineBounded_RejectsOverLimit(t *testing.T) {
	payload := strings.Repeat("x", 4096) + "\n"
	r := bufio.NewReader(strings.NewReader(payload))
	_, err := readLineBounded(r, 1024)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("expected 'exceeded' in error, got %q", err.Error())
	}
}

func TestReadLineBounded_ExactBoundary(t *testing.T) {
	payload := strings.Repeat("x", 1024) + "\n"
	r := bufio.NewReader(strings.NewReader(payload))
	got, err := readLineBounded(r, 1024)
	if err != nil {
		t.Fatalf("unexpected error at exact boundary: %v", err)
	}
	if len(got) != 1024 {
		t.Fatalf("expected 1024 bytes, got %d", len(got))
	}
}

func TestHTTPTransport_ResponseBodyCapped(t *testing.T) {
	// Server emits a well-formed JSON-RPC reply whose Result field is a
	// huge string, exceeding maxHTTPResponseBytes. MaxBytesReader must
	// surface a decode error instead of letting the host buffer the full
	// payload.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		big := strings.Repeat("a", maxHTTPResponseBytes+1024)
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":%q}]}}`, big)
	}))
	defer srv.Close()

	tr := NewHTTPTransport(srv.URL)
	_, err := tr.CallTool(context.Background(), "whatever", nil)
	if err == nil {
		t.Fatal("expected error on oversized body, got nil")
	}
	// MaxBytesReader reports "http: request body too large" (when used as
	// a request-side reader) or our decode error ("decode response") once
	// the limit is hit; accept either since both signal the cap engaged.
	if !strings.Contains(err.Error(), "decode response") && !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected decode/too-large error, got %q", err.Error())
	}
}

func TestHTTPTransport_SmallResponseOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      1,
			Result:  json.RawMessage(`{"content":[{"type":"text","text":"ok"}],"isError":false}`),
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	tr := NewHTTPTransport(srv.URL)
	res, err := tr.CallTool(context.Background(), "t", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Content) != 1 || res.Content[0].Text != "ok" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestHTTPTransport_ErrorBodyCapped(t *testing.T) {
	// Non-OK response with an oversized body; the transport must bound
	// the error string at maxHTTPErrorBodyBytes.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(strings.Repeat("e", maxHTTPErrorBodyBytes*4)))
	}))
	defer srv.Close()

	tr := NewHTTPTransport(srv.URL)
	_, err := tr.CallTool(context.Background(), "t", nil)
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
	if len(err.Error()) > maxHTTPErrorBodyBytes+256 {
		t.Fatalf("error body not capped: len=%d", len(err.Error()))
	}
}
