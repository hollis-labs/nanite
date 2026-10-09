package pluginapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
)

func TestAgentToolEffectsFailClosed(t *testing.T) {
	for _, test := range []struct {
		effect            string
		read, destructive bool
	}{{ToolEffectRead, true, false}, {ToolEffectWrite, false, false}, {ToolEffectDestructive, false, true}} {
		read, destructive, err := ToolEffectHints(test.effect)
		if err != nil || read != test.read || destructive != test.destructive {
			t.Fatalf("effect %s: %v %v %v", test.effect, read, destructive, err)
		}
	}
	for _, effect := range []string{"", "READ", "read_write", "read ", "custom.effect"} {
		if _, _, checkErr := ToolEffectHints(effect); checkErr == nil {
			t.Fatalf("accepted %q", effect)
		}
	}
	valid := manifest.Tool{Name: "bookmarks_list", Description: "List bookmarks", Effect: ToolEffectRead, InputSchema: json.RawMessage(`{"type":"object"}`)}
	if checkErr := ValidateAgentTools([]manifest.Tool{valid}); checkErr != nil {
		t.Fatal(checkErr)
	}
	for _, name := range []string{"", "foo.bar", "foo bar", strings.Repeat("a", 65)} {
		bad := valid
		bad.Name = name
		if checkErr := ValidateAgentTools([]manifest.Tool{bad}); checkErr == nil {
			t.Fatalf("accepted name %q", name)
		}
	}
	bad := valid
	bad.Effect = "unknown"
	if checkErr := ValidateAgentTools([]manifest.Tool{bad}); checkErr == nil {
		t.Fatal("unknown effect accepted")
	}
	bad = valid
	bad.Description = strings.Repeat("x", 32*1024+1)
	if checkErr := ValidateAgentTools([]manifest.Tool{bad}); checkErr == nil {
		t.Fatal("oversized description accepted")
	}
	bad = valid
	bad.InputSchema = json.RawMessage(strings.Repeat("x", 256*1024+1))
	if checkErr := ValidateAgentTools([]manifest.Tool{bad}); checkErr == nil {
		t.Fatal("oversized schema accepted")
	}
	if checkErr := ValidateAgentTools(make([]manifest.Tool, MaxAgentTools+1)); checkErr == nil {
		t.Fatal("unbounded declarations accepted")
	}
}
