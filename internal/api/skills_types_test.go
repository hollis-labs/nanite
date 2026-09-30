package api

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

// TestSkillViewJSONKeys pins SkillView to the key set store.Skill emitted
// when it was serialized directly. Zero values are marshaled deliberately:
// no key may disappear through omitempty.
func TestSkillViewJSONKeys(t *testing.T) {
	want := []string{
		"category", "content_hash", "declared_dependencies", "description",
		"enabled", "icon", "id", "input_schema", "installed_at", "name",
		"slug", "source_tier", "updated_at", "version",
	}
	if got := jsonKeys(t, skillToView(&store.Skill{})); !reflect.DeepEqual(got, want) {
		t.Fatalf("keys = %v\nwant   %v", got, want)
	}

	// InstallSkillResponse nests the view under "skill".
	raw, err := json.Marshal(InstallSkillResponse{Skill: skillToView(&store.Skill{})})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(raw, &outer); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var nested map[string]any
	if err := json.Unmarshal(outer["skill"], &nested); err != nil {
		t.Fatalf("unmarshal skill: %v", err)
	}
	if len(nested) != len(want) {
		t.Fatalf("InstallSkillResponse.skill has %d keys, want %d", len(nested), len(want))
	}
}

func TestSkillViewEmptyListMarshalsAsArray(t *testing.T) {
	for name, rows := range map[string][]store.Skill{"empty": {}, "nil": nil} {
		raw, err := json.Marshal(skillsToView(rows))
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		if string(raw) != "[]" {
			t.Fatalf("%s: marshaled %s, want []", name, raw)
		}
	}
}
