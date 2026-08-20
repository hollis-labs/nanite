package selftools

import (
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/mcp"
)

// findSelfTool returns the named tool from selfToolDefinitions() or fails.
func findSelfTool(t *testing.T, name string) mcp.Tool {
	t.Helper()
	for _, d := range selfToolDefinitions() {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("%s not registered in selfToolDefinitions()", name)
	return mcp.Tool{}
}

// TestSubagentSpawnPromptArg_GuidesCaller is the CW-20260516-0067 regression
// guard. The subagent_spawn `prompt` arg was once described only as "Initial
// prompt for the subagent", which gave callers no guidance and let agents
// forward the user's raw orchestration message — making the worker spawn its
// own subagents (the c226 fork chain). The arg description must now tell the
// caller to compose a focused, self-contained worker brief and to never
// forward the user's raw message or orchestration instructions.
func TestSubagentSpawnPromptArg_GuidesCaller(t *testing.T) {
	tool := findSelfTool(t, "subagent_spawn")

	props, ok := tool.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("subagent_spawn schema has no properties map: %v", tool.InputSchema)
	}
	promptProp, ok := props["prompt"].(map[string]any)
	if !ok {
		t.Fatalf("subagent_spawn schema has no prompt property: %v", props)
	}
	desc, _ := promptProp["description"].(string)

	if strings.TrimSpace(desc) == "Initial prompt for the subagent." {
		t.Fatal("prompt arg description was not expanded past the bare placeholder")
	}
	for _, want := range []string{"worker", "NEVER", "orchestrat"} {
		if !strings.Contains(desc, want) {
			t.Errorf("prompt arg description missing guidance phrase %q; got: %q", want, desc)
		}
	}
}

// TestSubagentSpawnTool_DescriptionHasPromptHygiene confirms the tool-level
// description also carries the prompt-composition guidance, so the cue is
// visible whether the caller reads the tool blurb or the arg schema.
func TestSubagentSpawnTool_DescriptionHasPromptHygiene(t *testing.T) {
	tool := findSelfTool(t, "subagent_spawn")
	for _, want := range []string{"worker, not an orchestrator", "NEVER"} {
		if !strings.Contains(tool.Description, want) {
			t.Errorf("subagent_spawn tool description missing prompt-hygiene phrase %q", want)
		}
	}
}
