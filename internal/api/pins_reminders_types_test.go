package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAgentBuilderDryRun_UpdateUnknownProfile(t *testing.T) {
	_, mux := newTestAPI(t)
	w := mcpDo(mux, "POST", "/api/agent-builder/dry-run", `{"schema_version":1,"mode":"update_profile","profile":{"id":"no-such-agent","name":"x","system_prompt":"x"}}`)
	var resp AgentBuilderDryRunResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || w.Code != http.StatusOK {
		t.Fatalf("dry-run: %d %s", w.Code, w.Body.String())
	}
	found := false
	for _, e := range resp.Errors {
		if e == `profile "no-such-agent" not found` {
			found = true
		}
	}
	if !found || resp.Valid {
		t.Fatalf("errors = %v valid=%v, want profile not found", resp.Errors, resp.Valid)
	}
}

func TestLoomCoreCallbackRouteRetired(t *testing.T) {
	_, mux := newTestAPI(t)
	w := mcpDo(mux, "POST", "/api/loom/curator-wake", `{"generator":"g","fragment":{"id":"frag-1"}}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("retired callback: %d %s", w.Code, w.Body.String())
	}
}
