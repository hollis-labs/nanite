package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/brand"
)

func agentServer(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Setenv(brand.Env("API_URL"), srv.URL)
}

// list follows the cursor to the end rather than printing the first page.
func TestAgentListFollowsEveryPage(t *testing.T) {
	var queries []string
	agentServer(t, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		if r.URL.Path != "/api/agent/v1/sessions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.URL.Query().Get("cursor") == "" {
			_, _ = w.Write([]byte(`{"items":[{"id":"a"}],"next_cursor":"c2"}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"id":"b"}],"next_cursor":null}`))
	})
	var out bytes.Buffer
	if err := runAgentSessionCommand([]string{"list", "--all"}, &out); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Items  []struct{ ID string } `json:"items"`
		Server string                `json:"server"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("%v: %s", err, out.String())
	}
	if len(got.Items) != 2 || got.Items[0].ID != "a" || got.Items[1].ID != "b" || got.Server == "" {
		t.Fatalf("got %+v", got)
	}
	if !strings.Contains(queries[0], "include_archived=true") || !strings.Contains(queries[1], "cursor=c2") {
		t.Fatalf("queries = %v", queries)
	}
}

func TestAgentStopCallsTheStopRoute(t *testing.T) {
	agentServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/api/agent/v1/sessions/s%201/stop" {
			t.Errorf("%s %s", r.Method, r.URL.EscapedPath())
		}
		_, _ = w.Write([]byte(`{"state":"ended","stopped":true}`))
	})
	var out bytes.Buffer
	if err := runAgentSessionCommand([]string{"stop", "s 1"}, &out); err != nil || !strings.Contains(out.String(), `"stopped":true`) {
		t.Fatalf("%v %s", err, out.String())
	}
}

func TestAgentStopReportsServerErrorsAndBadUsage(t *testing.T) {
	agentServer(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, `{"error":"nope"}`, http.StatusNotFound) })
	if err := runAgentSessionCommand([]string{"stop", "x"}, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v", err)
	}
	for _, args := range [][]string{{"stop"}, {"stop", "-x"}, {"list", "--bogus"}} {
		if err := runAgentSessionCommand(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("%v accepted", args)
		}
	}
}
