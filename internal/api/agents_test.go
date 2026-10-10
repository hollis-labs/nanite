package api

import (
	"testing"
)

// Source metadata cannot revive the retired mutable profile route or enroll an actor.
func TestHandleCreateAgent_DefaultsSourceToUser(t *testing.T) {
	a, mux := newTestAPI(t)
	before := retiredAgentState(t, a)
	retiredAgentRequest(t, mux, "POST", "/api/agents", `{"id":"claimed-profile","name":"Source Default","slug":"source-default","system_prompt":"test"}`)
	assertRetiredAgentState(t, a, before)
}

// Source metadata cannot revive the retired mutable profile route or enroll an actor.
func TestHandleCreateAgent_RejectsInternalSource(t *testing.T) {
	a, mux := newTestAPI(t)
	before := retiredAgentState(t, a)
	retiredAgentRequest(t, mux, "POST", "/api/agents", `{"source":"internal","name":"Internal","slug":"internal-reject","system_prompt":"test"}`)
	assertRetiredAgentState(t, a, before)
}

// Source metadata cannot revive the retired mutable profile route or enroll an actor.
func TestHandleUpdateAgent_InternalRejectedAsHarnessOwned(t *testing.T) {
	a, mux := newTestAPI(t)
	p := retiredAgentHistory(t, a, "internal")
	before := retiredAgentState(t, a)
	retiredAgentRequest(t, mux, "PUT", "/api/agents/"+p.ID, `{"name":"Updated Name","source":"user","source_ref":"/claimed.md"}`)
	assertRetiredAgentState(t, a, before)
}
