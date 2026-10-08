package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAgentClientMissingTokenIsDefinitiveRefusal(t *testing.T) {
	t.Setenv("NANITE_AUTH_TOKEN", "")
	t.Setenv("NANITE_AUTH_USER", "")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "" {
			t.Error("unexpected credentials")
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthenticated","message":"Bearer authentication is required."}}`))
	}))
	defer server.Close()
	_, err := newAgentClient(server.URL).SendTurn(t.Context(), "view", "hello")
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "unauthenticated") || strings.Contains(err.Error(), "uncertain") || calls.Load() != 1 {
		t.Fatalf("missing-token outcome=%v requests=%d", err, calls.Load())
	}
}

func TestAgentClientDoesNotFollowTokenRedirect(t *testing.T) {
	t.Setenv("NANITE_AUTH_TOKEN", "test-only-token")
	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationCalls.Add(1) }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-only-token" {
			t.Error("explicit operator token missing")
		}
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	_, err := newAgentClient(server.URL).SendTurn(t.Context(), "view", "hello")
	if err == nil || destinationCalls.Load() != 0 {
		t.Fatalf("redirect outcome=%v destination requests=%d", err, destinationCalls.Load())
	}
}
