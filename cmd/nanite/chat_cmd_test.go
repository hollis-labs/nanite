package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunChatTurnCancelTargetsAcceptedTurn(t *testing.T) {
	var cancels atomic.Int32
	started := make(chan struct{})
	var target atomic.Value
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/agent/v1/sessions/{id}/turns", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(agentTurnResponse{SessionID: r.PathValue("id"), MessageID: "msg-1", TurnID: "accepted-turn", StreamURL: "/api/agent/v1/sessions/view/turns/accepted-turn/events"})
	})
	mux.HandleFunc("GET /api/agent/v1/sessions/{id}/turns/{turnId}/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	})
	mux.HandleFunc("POST /api/agent/v1/sessions/{id}/turns/{turnId}/cancel", func(w http.ResponseWriter, r *http.Request) {
		target.Store(r.PathValue("id") + "/" + r.PathValue("turnId"))
		cancels.Add(1)
		json.NewEncoder(w).Encode(agentCancelResponse{SessionID: r.PathValue("id"), Status: "canceled"})
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runChatTurn(ctx, newAgentClient(server.URL), "view", "question") }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("events not opened")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel blocked")
	}
	if cancels.Load() != 1 || target.Load() != "view/accepted-turn" {
		t.Fatal(cancels.Load(), target.Load())
	}
}
func TestRunChatTurnEOFIsUncertainOutcomeAndNeverResubmits(t *testing.T) {
	var posts, gets atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			json.NewEncoder(w).Encode(agentTurnResponse{SessionID: "view", MessageID: "msg-1", TurnID: "msg-1", StreamURL: "/api/agent/v1/sessions/view/turns/msg-1/events"})
			return
		}
		gets.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		writeAgentCanonicalFixture(w, false)
	}))
	defer server.Close()
	err := runChatTurn(t.Context(), newAgentClient(server.URL), "view", "question")
	if err == nil || !strings.Contains(err.Error(), "without a terminal") || !strings.Contains(err.Error(), "inspect turn msg-1") || posts.Load() != 1 || gets.Load() != 1 {
		t.Fatal(err, posts.Load(), gets.Load())
	}
}
func TestRunChatTurnMalformedWireRefusesLegacyEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			json.NewEncoder(w).Encode(agentTurnResponse{SessionID: "view", MessageID: "msg-1", TurnID: "msg-1", StreamURL: "/api/agent/v1/sessions/view/turns/msg-1/events"})
			return
		}
		fmt.Fprint(w, "data: {\"type\":\"stream_end\"}\n\n")
	}))
	defer server.Close()
	if err := runChatTurn(t.Context(), newAgentClient(server.URL), "view", "question"); err == nil || !strings.Contains(err.Error(), "malformed canonical") {
		t.Fatal(err)
	}
}
