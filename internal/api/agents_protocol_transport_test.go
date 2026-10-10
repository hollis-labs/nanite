package api

// Legacy protocol and transport inputs must not revive mutable profile writes.

import (
	"testing"
)

// Legacy protocol authoring is retired; the registered route must refuse without effects.
func TestHandleCreateAgent_SetsProtocolTransport(t *testing.T) {
	a, mux := newTestAPI(t)
	before := retiredAgentState(t, a)
	retiredAgentRequest(t, mux, "POST", "/api/agents", `{"name":"ACP Agent","slug":"acp-agent","protocol":"acp","transport":"tcp"}`)
	assertRetiredAgentState(t, a, before)
}

// Legacy protocol authoring is retired; the registered route must refuse without effects.
func TestHandleCreateAgent_InvalidProtocolRejected(t *testing.T) {
	a, mux := newTestAPI(t)
	before := retiredAgentState(t, a)
	for _, body := range []string{`{"protocol":"invalid","transport":"stdio"}`, `{"protocol":"acp","transport":"invalid"}`} {
		retiredAgentRequest(t, mux, "POST", "/api/agents", body)
		assertRetiredAgentState(t, a, before)
	}
}

// Legacy protocol authoring is retired; the registered route must refuse without effects.
func TestHandleUpdateAgent_SetsAndClearsProtocolTransport(t *testing.T) {
	a, mux := newTestAPI(t)
	p := retiredAgentHistory(t, a, "user")
	before := retiredAgentState(t, a)
	for _, body := range []string{`{"protocol":"acp","transport":"stdio"}`, `{"protocol":"","transport":""}`} {
		retiredAgentRequest(t, mux, "PUT", "/api/agents/"+p.ID, body)
		assertRetiredAgentState(t, a, before)
	}
}

// Legacy protocol authoring is retired; the registered route must refuse without effects.
func TestHandleUpdateAgent_ExistingAgentsUnaffected(t *testing.T) {
	a, mux := newTestAPI(t)
	p := retiredAgentHistory(t, a, "user")
	before := retiredAgentState(t, a)
	retiredAgentRequest(t, mux, "PUT", "/api/agents/"+p.ID, `{"description":"unrelated edit"}`)
	assertRetiredAgentState(t, a, before)
}
