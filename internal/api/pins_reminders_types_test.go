package api

import (
	"net/http"
	"testing"
)

func TestRetiredAgentBuilderUnknownTargetRefusesBeforeLookup(t *testing.T) {
	_, mux := newTestAPI(t)
	requireRetiredAPI(t, mcpDo(mux, "POST", "/api/agent-builder/dry-run", `{"schema_version":1,"mode":"update_profile","profile":{"id":"no-such-agent","name":"x","system_prompt":"x"}}`))
}

func TestLoomCoreCallbackRouteRetired(t *testing.T) {
	_, mux := newTestAPI(t)
	w := mcpDo(mux, "POST", "/api/loom/curator-wake", `{"generator":"g","fragment":{"id":"frag-1"}}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("retired callback: %d %s", w.Code, w.Body.String())
	}
}
