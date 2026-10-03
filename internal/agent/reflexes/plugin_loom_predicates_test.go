package reflexes

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/pkg/pluginapi"
	"github.com/hollis-labs/plugin-sdk/manifest"
)

// These behavioral checks consume the released plugin declarations and the
// actual host evaluator. No second core Loom seed implementation is retained.
func loomPilotTriggerSpec(t *testing.T, agentSlug, name string) (string, string) {
	t.Helper()
	published := os.Getenv("NANITE_LOOM_TEST_BUNDLE")
	if published == "" {
		t.Skip("requires verified loom/v0.1.0 release bundle")
	}
	root, err := os.OpenRoot(published) // #nosec G703 -- opt-in fixture selects an externally verified release directory.
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	raw, err := root.ReadFile("plugin.yaml")
	if err != nil {
		t.Fatal(err)
	}
	declaration, err := manifest.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	block, err := pluginapi.DecodeBlock(declaration.Nanite)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.ReplaceAll(name, "_", "-")
	if name == "capture_on_discovery" {
		id = strings.TrimPrefix(agentSlug, "loom-") + "-" + id
	}
	for _, seed := range block.Registers.ReflexSeeds {
		if seed.AgentSlug == agentSlug && seed.ID == id {
			trigger, marshalErr := json.Marshal(seed.Trigger)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			return "predicate", string(trigger)
		}
	}
	t.Fatal("released Loom seed missing", agentSlug, id)
	return "", ""
}

// TestCheckBeforeAnswer_FiresOnTopicQuestionWithoutWikiLookup proves the
// conjunction's positive case: a recent user turn touches Nanite
// wiki-bundle vocabulary AND Weaver hasn't called any wiki_*/loom_*
// tool yet this window.
func TestCheckBeforeAnswer_FiresOnTopicQuestionWithoutWikiLookup(t *testing.T) {
	kind, spec := loomPilotTriggerSpec(t, "loom-weaver", "check_before_answer")
	st := State{
		UserMessages: []MessageSignal{
			{Content: "How does the MCP trust tier system decide result-size ceilings?"},
			{Content: "What's the difference between the envelope system and a plain tool result?"},
		},
		Messages: []MessageSignal{
			{Content: "Let me think about that.", ToolNames: []string{"dev_read"}},
			{Content: "One moment.", ToolNames: nil},
		},
	}
	fired, err := EvaluateTrigger(kind, spec, st)
	if err != nil {
		t.Fatalf("EvaluateTrigger error: %v", err)
	}
	if !fired {
		t.Fatalf("check_before_answer MUST fire on a topic question with no prior wiki_*/loom_* call this window")
	}
}

// TestCheckBeforeAnswer_DoesNotFireOnceWikiToolsCalled proves the second
// conjunct actually gates re-firing: same topic question, but Weaver has
// already called a wiki_* tool in the window — no more nudging needed.
func TestCheckBeforeAnswer_DoesNotFireOnceWikiToolsCalled(t *testing.T) {
	kind, spec := loomPilotTriggerSpec(t, "loom-weaver", "check_before_answer")
	st := State{
		UserMessages: []MessageSignal{
			{Content: "How does the MCP trust tier system decide result-size ceilings?"},
			{Content: "What's the difference between the envelope system and a plain tool result?"},
		},
		Messages: []MessageSignal{
			{Content: "Searching the bundle now.", ToolNames: []string{"loom_page_search"}},
			{Content: "One moment.", ToolNames: nil},
		},
	}
	fired, err := EvaluateTrigger(kind, spec, st)
	if err != nil {
		t.Fatalf("EvaluateTrigger error: %v", err)
	}
	if fired {
		t.Fatalf("check_before_answer must NOT fire once a wiki_*/loom_* tool has been called this window")
	}
}

// TestCheckBeforeAnswer_DoesNotFireOnOffTopicQuestion proves the first
// conjunct is a real gate, not a rubber stamp: an off-topic question
// (no Nanite subsystem vocabulary) must not trip the reflex even though
// no wiki tool has been called.
func TestCheckBeforeAnswer_DoesNotFireOnOffTopicQuestion(t *testing.T) {
	kind, spec := loomPilotTriggerSpec(t, "loom-weaver", "check_before_answer")
	st := State{
		UserMessages: []MessageSignal{
			{Content: "What's a good recipe for weeknight dinners?"},
			{Content: "Any restaurant recommendations nearby?"},
		},
		Messages: []MessageSignal{
			{Content: "Sure, here are a few ideas.", ToolNames: nil},
			{Content: "Happy to help.", ToolNames: nil},
		},
	}
	fired, err := EvaluateTrigger(kind, spec, st)
	if err != nil {
		t.Fatalf("EvaluateTrigger error: %v", err)
	}
	if fired {
		t.Fatalf("check_before_answer must NOT fire on an off-topic question")
	}
}

// TestCaptureOnDiscovery_FiresOnDiscoveryLanguageWithToolActivity proves
// the positive case for both Weaver's and Curator's identical
// capture_on_discovery trigger spec: discovery-signaling language in
// the recent output AND real tool activity in the same window (not
// idle speculation).
func TestCaptureOnDiscovery_FiresOnDiscoveryLanguageWithToolActivity(t *testing.T) {
	for _, agentSlug := range []string{"loom-weaver", "loom-curator"} {
		t.Run(agentSlug, func(t *testing.T) {
			kind, spec := loomPilotTriggerSpec(t, agentSlug, "capture_on_discovery")
			st := State{
				Messages: []MessageSignal{
					{Content: "Turns out the plugin manifest schema changed in v0.3.0 — worth documenting.", ToolCalls: 2},
					{Content: "Checked the plugin.yaml reference while investigating.", ToolCalls: 1},
				},
			}
			fired, err := EvaluateTrigger(kind, spec, st)
			if err != nil {
				t.Fatalf("EvaluateTrigger error: %v", err)
			}
			if !fired {
				t.Fatalf("capture_on_discovery MUST fire on discovery language backed by tool activity")
			}
		})
	}
}

// TestCaptureOnDiscovery_DoesNotFireOnIdleSpeculation proves the
// tool_calls_window conjunct actually blocks pure idle speculation —
// discovery language with zero tool calls in the window must not fire.
func TestCaptureOnDiscovery_DoesNotFireOnIdleSpeculation(t *testing.T) {
	for _, agentSlug := range []string{"loom-weaver", "loom-curator"} {
		t.Run(agentSlug, func(t *testing.T) {
			kind, spec := loomPilotTriggerSpec(t, agentSlug, "capture_on_discovery")
			st := State{
				Messages: []MessageSignal{
					{Content: "Turns out this might be worth documenting, I think.", ToolCalls: 0},
					{Content: "Just speculating here, nothing checked yet.", ToolCalls: 0},
				},
			}
			fired, err := EvaluateTrigger(kind, spec, st)
			if err != nil {
				t.Fatalf("EvaluateTrigger error: %v", err)
			}
			if fired {
				t.Fatalf("capture_on_discovery must NOT fire on idle speculation with zero tool calls")
			}
		})
	}
}

// TestCaptureOnDiscovery_DoesNotFireWithoutDiscoveryLanguage proves the
// regex conjunct is a real gate: ordinary tool-heavy work with no
// discovery-signaling language must not fire.
func TestCaptureOnDiscovery_DoesNotFireWithoutDiscoveryLanguage(t *testing.T) {
	kind, spec := loomPilotTriggerSpec(t, "loom-weaver", "capture_on_discovery")
	st := State{
		Messages: []MessageSignal{
			{Content: "Compiled the wiki_page blueprint for path nanite/envelopes.md.", ToolCalls: 2},
			{Content: "Wrote the page directly; confidence_tier was high.", ToolCalls: 1},
		},
	}
	fired, err := EvaluateTrigger(kind, spec, st)
	if err != nil {
		t.Fatalf("EvaluateTrigger error: %v", err)
	}
	if fired {
		t.Fatalf("capture_on_discovery must NOT fire without discovery-signaling language")
	}
}
