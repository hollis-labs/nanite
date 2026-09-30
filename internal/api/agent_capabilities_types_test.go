package api

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// jsonKeys marshals v and returns its top-level object keys, sorted.
func jsonKeys(t *testing.T, v any) []string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestAgentCapabilityViewJSONKeys pins each view to the key set the store row
// emitted when it was serialized directly, which is what ui/src/lib/types.ts
// reads. Zero values are marshaled deliberately: no key may disappear through
// omitempty.
func TestAgentCapabilityViewJSONKeys(t *testing.T) {
	cases := []struct {
		name string
		view any
		want []string
	}{
		{"known tool", knownToolToView(&store.AgentKnownTool{}), []string{
			"activation_count", "added_at", "agent_id", "last_used_at", "pinned",
			"reason", "sort_order", "tool_name", "ttl_seconds",
		}},
		{"known skill", knownSkillToView(&store.AgentKnownSkill{}), []string{
			"activation_count", "added_at", "agent_id", "approved_content_hash",
			"capabilities_granted", "granted_at", "granted_by", "last_used_at",
			"pinned", "reason", "skill_name", "ttl_seconds",
		}},
		{"procedure", procedureToView(&store.AgentProcedure{}), []string{
			"agent_id", "body", "created_at", "name", "scope", "updated_at",
		}},
		{"knowledge seed", knowledgeSeedToView(&store.AgentKnowledgeSeed{}), []string{
			"agent_id", "applied_at", "body", "created_at", "namespace", "seed_key", "tags_json",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := jsonKeys(t, tc.view); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("keys = %v\nwant   %v", got, tc.want)
			}
		})
	}
}

// TestAgentCapabilityEmptyListsMarshalAsArray: the store lists return an empty
// slice, never nil, so an empty list has always reached the UI as [].
func TestAgentCapabilityEmptyListsMarshalAsArray(t *testing.T) {
	cases := map[string]any{
		"known tools":     knownToolsToView([]store.AgentKnownTool{}),
		"known skills":    knownSkillsToView([]store.AgentKnownSkill{}),
		"procedures":      proceduresToView([]store.AgentProcedure{}),
		"knowledge seeds": knowledgeSeedsToView([]store.AgentKnowledgeSeed{}),
		"nil known tools": knownToolsToView(nil),
	}
	for name, v := range cases {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		if string(raw) != "[]" {
			t.Fatalf("%s: marshaled %s, want []", name, raw)
		}
	}
}
