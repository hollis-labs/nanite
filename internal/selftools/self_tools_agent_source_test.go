package selftools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// Fresh host IDs select immutable content; they never become launch identities.
func TestAgentSourceResolve_RoundTrip(t *testing.T) {
	st := newSelfTools(t)
	db := fixtureStore(st)
	data := []byte("---\nschema_version: \"2\"\ndefinition_id: def:source-fixture\nrevision: \"1\"\nname: source-fixture\ndescription: Private test.\nbehavior:\n  purpose: Read immutable source.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nFixture content.\n")
	pin, err := db.InstallAgentDefinition(t.Context(), data, nil)
	if err != nil {
		t.Fatal(err)
	}
	host, err := db.CreateAgentHostSettings(t.Context(), store.AgentHostSettings{Title: "Recon Agent", Slug: "recon-agent", DefinitionRef: pin, Enabled: true, Source: "operator", Settings: store.NativeHostSettings{Version: "1", Runtime: "api"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{host.Slug, host.ID} {
		res, err := st.CallTool(t.Context(), agentSourceResolveToolName, map[string]any{"agent": ref})
		if err != nil || res.IsError {
			t.Fatalf("resolve: %v %+v", err, res)
		}
		var out agentSourceResolveResult
		if err = json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
			t.Fatal(err)
		}
		if out.DefinitionRef != pin || out.Artifact != string(data) || out.ID != host.ID || out.HostSettingsRef.Revision != host.Revision {
			t.Fatalf("source mismatch: %+v", out)
		}
		if strings.Contains(res.Content[0].Text, "agent_spec") {
			t.Fatal("local host identifier presented as launch identity")
		}
	}
}

// TestAgentSourceResolve_NotFound confirms an unknown ref yields a clean
// tool-level error rather than a transport failure.
func TestAgentSourceResolve_NotFound(t *testing.T) {
	st := newSelfTools(t)
	res, err := st.CallTool(t.Context(), agentSourceResolveToolName, map[string]any{
		"agent": "no-such-agent",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected a tool-level error for an unknown agent")
	}
	if !strings.Contains(res.Content[0].Text, "no host settings found") {
		t.Errorf("error text = %q, want a not-found message", res.Content[0].Text)
	}
}

// TestAgentSourceResolve_Registered confirms the resolver tool is in the
// self-tool definitions list so it is discoverable as an MCP tool.
func TestAgentSourceResolve_Registered(t *testing.T) {
	found := false
	for _, tool := range selfToolDefinitions() {
		if tool.Name == agentSourceResolveToolName {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("%q not present in selfToolDefinitions()", agentSourceResolveToolName)
	}
}
