package pluginapi_test

import (
	"encoding/json"
	"strings"
	"testing"

	sdkprocess "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/subprocess"
	"github.com/hollis-labs/nanite/pkg/pluginapi"
)

func reminderSeed() pluginapi.ReflexSeed {
	return pluginapi.ReflexSeed{ID: "check-before-answer", AgentSlug: "loom-weaver", Reminder: "Consult the wiki before answering.", Priority: 30, Trigger: pluginapi.ReflexPredicate{Kind: "AND", Clauses: []pluginapi.ReflexPredicate{{Kind: "text_regex_window", Scope: "user", Window: 2, Pattern: "(?i)architecture"}, {Kind: "tool_name_window", Window: 2, Mode: "none", Pattern: "^(wiki_|loom_)"}}}}
}
func reflexBlock() pluginapi.Block {
	return pluginapi.Block{Registers: pluginapi.Registrations{ReflexSeeds: []pluginapi.ReflexSeed{reminderSeed()}}}
}
func reflexCapability() sdkprocess.CapabilityRequest {
	raw, _ := json.Marshal(pluginapi.ReflexScope{SeedIDs: []string{"check-before-answer"}, AgentSlugs: []string{"loom-weaver"}})
	return sdkprocess.CapabilityRequest{Name: pluginapi.CapabilityReflexSeed, Metadata: raw}
}

func TestReflexSeedsRequireExactReviewedTargets(t *testing.T) {
	block := reflexBlock()
	capability := reflexCapability()
	if _, err := pluginapi.ReflexScopeFor(block, []sdkprocess.CapabilityRequest{capability}); err != nil {
		t.Fatal(err)
	}
	if _, err := pluginapi.ReflexScopeFor(block, nil); err == nil {
		t.Fatal("unapproved seed accepted")
	}
	capability.Optional = true
	if _, err := pluginapi.ReflexScopeFor(block, []sdkprocess.CapabilityRequest{capability}); err == nil {
		t.Fatal("optional authority accepted")
	}
	capability = reflexCapability()
	if _, err := pluginapi.ReflexScopeFor(block, []sdkprocess.CapabilityRequest{capability, capability}); err == nil {
		t.Fatal("duplicate scope accepted")
	}
	block.Registers.ReflexSeeds[0].AgentSlug = "other-agent"
	if _, err := pluginapi.ReflexScopeFor(block, []sdkprocess.CapabilityRequest{capability}); err == nil {
		t.Fatal("target widened")
	}
	for _, raw := range []string{`{"seed_ids":["check-before-answer"],"agent_slugs":["loom-weaver"],"system":true}`, `{"seed_ids":["check-before-answer"],"agent_slugs":["*"]}`, `{"seed_ids":["one","one"],"agent_slugs":["loom-weaver"]}`} {
		if _, err := pluginapi.DecodeReflexScope(json.RawMessage(raw)); err == nil {
			t.Fatal("invalid authority accepted")
		}
	}
}

func TestReflexPredicatesBoundEveryBranch(t *testing.T) {
	for _, change := range []func(*pluginapi.ReflexSeed){
		func(s *pluginapi.ReflexSeed) { s.Reminder = strings.Repeat("x", 8193) },
		func(s *pluginapi.ReflexSeed) { s.Priority = 101 },
		func(s *pluginapi.ReflexSeed) { s.Trigger.Clauses[1].Pattern = "[" },
		func(s *pluginapi.ReflexSeed) { s.Trigger.Clauses[0].Window = 21 },
		func(s *pluginapi.ReflexSeed) { s.Trigger.Clauses[1].Mode = "enforce" },
		func(s *pluginapi.ReflexSeed) { s.Trigger.Kind = "halt_session" },
	} {
		seed := reminderSeed()
		change(&seed)
		if seed.Validate() == nil {
			t.Fatal("invalid predicate accepted")
		}
	}
	seed := reminderSeed()
	for range 10 {
		seed.Trigger = pluginapi.ReflexPredicate{Kind: "AND", Clauses: []pluginapi.ReflexPredicate{seed.Trigger}}
	}
	if seed.Validate() == nil {
		t.Fatal("unbounded nesting accepted")
	}
	raw, err := pluginapi.EncodeBlock(reflexBlock())
	if err != nil {
		t.Fatal(err)
	}
	invalid := strings.Replace(string(raw), `"reminder":`, `"provenance_tier":"system","reminder":`, 1)
	if _, err := pluginapi.DecodeBlock(json.RawMessage(invalid)); err == nil {
		t.Fatal("system authority accepted")
	}
}

func TestHTTPRootAndSubtreeAreDistinctDeclarations(t *testing.T) {
	block := pluginapi.Block{Registers: pluginapi.Registrations{HTTPRoutes: []pluginapi.Route{{Method: "GET", Path: "bookmarks"}, {Method: "GET", Path: "bookmarks/"}, {Method: "DELETE", Path: "bookmarks/"}}}}
	if err := block.Validate(); err != nil {
		t.Fatal(err)
	}
	block.Registers.HTTPRoutes = append(block.Registers.HTTPRoutes, pluginapi.Route{Method: "GET", Path: "bookmarks/"})
	if block.Validate() == nil {
		t.Fatal("duplicate subtree route accepted")
	}
}
