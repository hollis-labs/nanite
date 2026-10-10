package api

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// agentProfileViewKeys is the wire contract of AgentProfileView: the keys
// the embedded store.AgentProfile emitted before the view became an explicit
// DTO, plus the five management keys. Adding a key here is a wire change;
// removing one can break the GUI (ui/src/lib/types.ts AgentProfile).
var agentProfileViewKeys = []string{
	"id", "name", "slug", "avatar", "system_prompt", "description", "modes",
	"default_model", "default_provider", "mcp_servers", "tool_permissions",
	"can_execute", "settings", "created_at", "updated_at", "agent_hash",
	"version", "tools", "directories", "constraints", "tags", "status",
	"source", "source_ref", "icon", "kind", "capabilities_json", "limits_json",
	"model_strategy", "imported_at", "origin_system", "format",
	"parent_dispatch_allowlist", "role_tools", "role_skills", "context_policy",
	"durable", "activation_mode", "class", "default_state", "consumer_id",
	"role_id", "model_id", "runtime_kind", "protocol", "transport", "plugin_id",
	"tether_managed", "tether_urn",
	"manage_class", "editable", "copy_to_managed", "revision", "persisted",
}

// populatedAgentProfile sets every field of a store.AgentProfile to a
// distinct non-zero value, so a translator that drops or swaps a field shows
// up as a value mismatch rather than two matching zero values.
func populatedAgentProfile(t *testing.T) store.AgentProfile {
	t.Helper()
	var p store.AgentProfile
	v := reflect.ValueOf(&p).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString(fmt.Sprintf("value-%d", i))
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Pointer:
			// Pinned runtime-only policy/host fields are deliberately excluded from
			// this legacy presentation DTO; populate them to exercise that boundary.
			f.Set(reflect.New(f.Type().Elem()))
		case reflect.Int:
			f.SetInt(int64(1000 + i))
		default:
			t.Fatalf("store.AgentProfile.%s has kind %s; teach populatedAgentProfile about it", v.Type().Field(i).Name, f.Kind())
		}
	}
	return p
}

func TestAgentProfileViewJSONKeys(t *testing.T) {
	p := populatedAgentProfile(t)
	view := agentProfileToView(&p, agentViewMeta{
		ManageClass:   "managed",
		Editable:      true,
		CopyToManaged: true,
		Revision:      "rev",
		Persisted:     true,
	})

	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal view: %v", err)
	}
	var got map[string]any
	if err = json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal view: %v", err)
	}

	gotKeys := make([]string, 0, len(got))
	for k := range got {
		gotKeys = append(gotKeys, k)
	}
	sort.Strings(gotKeys)
	wantKeys := append([]string(nil), agentProfileViewKeys...)
	sort.Strings(wantKeys)
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("AgentProfileView JSON keys changed\n got: %v\nwant: %v", gotKeys, wantKeys)
	}

	// Every profile key must carry the value the row itself serialized to,
	// which is what the embedded view emitted before it was an explicit DTO.
	rowRaw, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal row: %v", err)
	}
	var row map[string]any
	if err = json.Unmarshal(rowRaw, &row); err != nil {
		t.Fatalf("unmarshal row: %v", err)
	}
	meta := map[string]any{
		"manage_class": "managed", "editable": true, "copy_to_managed": true,
		"revision": "rev", "persisted": true,
	}
	for _, k := range agentProfileViewKeys {
		want, isMeta := meta[k]
		if !isMeta {
			want = row[k]
		}
		if !reflect.DeepEqual(got[k], want) {
			t.Errorf("key %q = %#v, want %#v", k, got[k], want)
		}
	}
}

func TestSessionAgentViewJSONKeys(t *testing.T) {
	views := sessionAgentsToView([]store.SessionAgent{{
		SessionID: "s1", AgentID: "a1", Mode: "default", JoinedAt: "2026-09-30T00:00:00Z", IsPrimary: true,
	}})
	raw, err := json.Marshal(views)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `[{"session_id":"s1","agent_id":"a1","mode":"default","joined_at":"2026-09-30T00:00:00Z","is_primary":true}]`
	if string(raw) != want {
		t.Fatalf("SessionAgentView JSON\n got: %s\nwant: %s", raw, want)
	}
}
