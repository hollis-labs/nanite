package service

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	sdkplugin "github.com/hollis-labs/libs/plugin-mcp/plugin-sdk"
	"github.com/hollis-labs/nanite/internal/chat"
	pluginpkg "github.com/hollis-labs/nanite/internal/plugin"
	permissionlib "github.com/hollis-labs/substrate/harness/interception/permission"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

func displayToolDefinition(name string) llmtypes.ToolDefinition {
	return llmtypes.ToolDefinition{Name: name, InputSchema: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []any{"value"}}}
}

func TestToolDisplaySchemaOwnershipAndCollisions(t *testing.T) {
	original := displayToolDefinition("fixture_tool")
	before, err := json.Marshal(original.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	tools := []llmtypes.ToolDefinition{original}
	normalizeToolInputSchemas(tools)
	normalizeToolInputSchemas(tools)
	after, err := json.Marshal(original.InputSchema)
	if err != nil || string(before) != string(after) {
		t.Fatal("schema assembly mutated the canonical schema", err)
	}
	input := map[string]any{"value": "actual argument", "toolAction": "Reading records", "toolSummary": "Record contents"}
	clean, labels := splitToolDisplayInput(tools, llmtypes.ToolUseBlock{Name: original.Name, Input: input})
	if !reflect.DeepEqual(clean, map[string]any{"value": "actual argument"}) || labels.action != "Reading records" || labels.summary != "Record contents" || input["toolAction"] != "Reading records" {
		t.Fatal("display split changed provider history or execution arguments", clean, labels, input)
	}
	encoded, err := json.Marshal(tools[0].InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if decodeErr := json.Unmarshal(encoded, &wire); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	prop := wire["properties"].(map[string]any)["toolAction"].(map[string]any)
	if prop["type"] != "string" || strings.Contains(string(encoded), "owned") {
		t.Fatal("private provenance leaked into schema wire", string(encoded))
	}
	untrusted := []llmtypes.ToolDefinition{{Name: original.Name, InputSchema: wire}}
	untouched, unowned := splitToolDisplayInput(untrusted, llmtypes.ToolUseBlock{Name: original.Name, Input: input})
	if !reflect.DeepEqual(untouched, input) || unowned != (toolDisplayLabels{}) {
		t.Fatal("serialized lookalike acquired harness ownership")
	}

	collision := displayToolDefinition("collision")
	collision.InputSchema["properties"].(map[string]any)["toolAction"] = map[string]any{"type": "object"}
	tools = []llmtypes.ToolDefinition{collision}
	normalizeToolInputSchemas(tools)
	native := map[string]any{"contract": "native argument"}
	input = map[string]any{"value": "v", "toolAction": native, "toolSummary": "Optional summary"}
	clean, labels = splitToolDisplayInput(tools, llmtypes.ToolUseBlock{Name: "collision", Input: input})
	if !reflect.DeepEqual(clean["toolAction"], native) || labels.action != "" || labels.summary != "Optional summary" {
		t.Fatal("native reserved property changed", clean, labels)
	}

	for _, key := range []string{"patternProperties", "$ref", "allOf", "propertyNames", "dependentRequired", "dependencies", "unevaluatedProperties", "minProperties", "maxProperties", "required label", "additionalProperties", "freeform", "nil"} {
		t.Run(key, func(t *testing.T) {
			def := displayToolDefinition("ambiguous")
			switch key {
			case "freeform":
				delete(def.InputSchema, "properties")
			case "nil":
				def.InputSchema = nil
			case "additionalProperties":
				def.InputSchema[key] = true
			case "required label":
				def.InputSchema["required"] = []any{"toolAction"}
			default:
				def.InputSchema[key] = map[string]any{}
			}
			defs := []llmtypes.ToolDefinition{def}
			normalizeToolInputSchemas(defs)
			args := map[string]any{"toolAction": native, "toolSummary": 7}
			got, display := splitToolDisplayInput(defs, llmtypes.ToolUseBlock{Name: "ambiguous", Input: args})
			if !reflect.DeepEqual(got, args) || display != (toolDisplayLabels{}) {
				t.Fatal("ambiguous argument namespace was narrowed", got, display)
			}
		})
	}
}

type displayToolProbe struct {
	stubToolService
	schema             map[string]any
	input              map[string]any
	calls, schemaReads int
}

func (p *displayToolProbe) GetToolSchema(string) map[string]any { p.schemaReads++; return p.schema }
func (p *displayToolProbe) GetToolMeta(context.Context, string) (ToolMetaInfo, bool) {
	return ToolMetaInfo{IsReadOnly: true}, true
}
func (p *displayToolProbe) Execute(_ context.Context, _, _ string, input map[string]any) (*ToolResult, error) {
	p.calls++
	p.input = input
	return &ToolResult{Output: "ok"}, nil
}

func (p *displayToolProbe) HandleRequestTools(_ context.Context, _ string, input map[string]any) ([]llmtypes.ToolDefinition, string, error) {
	p.input = input
	return []llmtypes.ToolDefinition{displayToolDefinition("loaded_tool")}, "loaded", nil
}

type displayPreHook struct{ input map[string]any }

func (*displayPreHook) EventTypes() []string { return []string{pluginpkg.EventToolExecuting} }
func (*displayPreHook) PluginID() string     { return "private-display-fixture" }
func (h *displayPreHook) Handle(_ context.Context, event sdkplugin.Event) error {
	h.input, _ = event.Data["tool_input"].(map[string]any)
	return nil
}

func TestToolDisplayExecutionAndApprovalIsolation(t *testing.T) {
	f := newCharacterizationFixture(t, nil, "fixture_tool") // Explicit private prior binding, not issuer creation.
	actor, resolveErr := f.svc.agents.GetBySlug(t.Context(), "characterization-agent")
	if resolveErr != nil {
		t.Fatal(resolveErr)
	}
	def := displayToolDefinition("fixture_tool")
	defs := []llmtypes.ToolDefinition{def}
	normalizeToolInputSchemas(defs)
	probe := &displayToolProbe{schema: def.InputSchema}
	f.svc.tools = probe
	f.svc.permissions = permissionlib.NewEngine(permissionlib.ModeDefault, &permissionlib.RuleSet{Rules: []permissionlib.Rule{{Tool: "fixture_tool", Pattern: "Running action", Behavior: permissionlib.DecisionDeny}}})
	hook := &displayPreHook{}
	host := pluginpkg.NewHost(http.NewServeMux(), pluginpkg.NewLogger("display-fixture"))
	f.svc.pluginHost = host
	if err := host.RegisterEventHook(hook.EventTypes(), hook); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"value": "actual argument", "toolAction": "Running action", "toolSummary": "Action result"}
	tu := llmtypes.ToolUseBlock{ID: "display-call", Name: "fixture_tool", Input: input}
	loop := newLoopState(chat.AgentConstraints{}, nil, false)
	ch := make(chan chat.StreamEvent, 32)
	plans := f.svc.preCheckTools(t.Context(), f.session, actor.ID, []llmtypes.ToolUseBlock{tu}, loop, ch, &ToolSelection{}, defs)
	if len(plans) != 1 || plans[0].status != toolPlanReady {
		t.Fatal("display fields failed canonical validation", plans)
	}
	result := f.svc.executeToolBatch(t.Context(), plans, loop, actor.ID, ch, f.session)
	want := map[string]any{"value": "actual argument"}
	if len(result) != 1 || result[0].isError || probe.calls != 1 || !reflect.DeepEqual(probe.input, want) || !reflect.DeepEqual(hook.input, want) || input["toolAction"] != "Running action" {
		t.Fatal("metadata reached a hook/transport or mutated provider input", probe.input, hook.input, result)
	}
	event := <-ch
	if event.Type != "tool_call" || event.ToolAction != "Running action" || event.ToolSummary != "Action result" {
		t.Fatal("display labels were not emitted", event)
	}

	f.svc.permissions = permissionlib.NewEngine(permissionlib.ModeDefault, &permissionlib.RuleSet{Rules: []permissionlib.Rule{{Tool: "fixture_tool", Behavior: permissionlib.DecisionAsk}}}, permissionlib.WithApprovalTimeout(time.Millisecond))
	loop = newLoopState(chat.AgentConstraints{}, nil, false)
	ch = make(chan chat.StreamEvent, 32)
	plans = f.svc.preCheckTools(t.Context(), f.session, actor.ID, []llmtypes.ToolUseBlock{tu}, loop, ch, &ToolSelection{}, defs)
	if plans[0].status != toolPlanDenied {
		t.Fatal("approval timeout became execution authority")
	}
	approval := <-ch
	var payload chat.ApprovalRequestPayload
	if approval.Type != "approval_request" || json.Unmarshal([]byte(approval.Data), &payload) != nil || !reflect.DeepEqual(payload.Input, want) {
		t.Fatal("display metadata changed approval subject", approval)
	}
	denied := <-ch
	if denied.Type != "tool_call" || denied.ToolAction != "Running action" || probe.calls != 1 {
		t.Fatal("denied display emitted authority or lost labels", denied)
	}
}

func TestToolDisplayNativeRefusalAndLocalTools(t *testing.T) {
	def := displayToolDefinition("chat_get")
	defs := []llmtypes.ToolDefinition{def}
	normalizeToolInputSchemas(defs)
	probe := &displayToolProbe{schema: def.InputSchema}
	svc := &chatServiceImpl{tools: probe}
	loop := newLoopState(chat.AgentConstraints{}, nil, false)
	ch := make(chan chat.StreamEvent, 16)
	ctx := context.WithValue(t.Context(), cognitiveTurnContextKey{}, true)
	tu := llmtypes.ToolUseBlock{ID: "native-read", Name: "chat_get", Input: map[string]any{"value": "claimed session", "toolAction": "Reading prose"}}
	plans := svc.preCheckTools(ctx, "actual native view", "claimed actor", []llmtypes.ToolUseBlock{tu}, loop, ch, &ToolSelection{}, defs)
	if plans[0].status != toolPlanDenied || probe.calls != 0 || probe.schemaReads != 0 {
		t.Fatal("native missing actor reached a transcript/tool reader")
	}
	if event := <-ch; event.ToolAction != "Reading prose" || event.Type != "tool_call" {
		t.Fatal("refusal display labels lost", event)
	}

	for _, name := range []string{"scratchpad_write", "fetch_tool_result"} {
		t.Run(name, func(t *testing.T) {
			tool := displayToolDefinition(name)
			tools := []llmtypes.ToolDefinition{tool}
			normalizeToolInputSchemas(tools)
			call := llmtypes.ToolUseBlock{ID: name, Name: name, Input: map[string]any{"key": "k", "value": "v", "toolAction": "Local action", "toolSummary": "Local result"}}
			input, display := splitToolDisplayInput(tools, call)
			call.Input = input
			events := make(chan chat.StreamEvent, 8)
			local := newLoopState(chat.AgentConstraints{}, nil, false)
			svc.executeSingleTool(t.Context(), call, input, local, "", "s", events, nil, display)
			if event := <-events; event.ToolAction != "Local action" || event.ToolSummary != "Local result" {
				t.Fatal("local display missing", event)
			}
			if probe.calls != 0 {
				t.Fatal("local tool reached transport")
			}
		})
	}
}

func TestToolDisplayRequestToolsPreservesProviderHistory(t *testing.T) {
	probe := &displayToolProbe{}
	svc := &chatServiceImpl{tools: probe, streams: NewStreamManager()}
	tools := []llmtypes.ToolDefinition{displayToolDefinition("request_tools")}
	normalizeToolInputSchemas(tools)
	input := map[string]any{"intent": "find a reader", "toolAction": "Finding tools", "toolSummary": "Reader discovery"}
	call := llmtypes.ToolUseBlock{ID: "request-tools-call", Name: "request_tools", Input: input}
	run := &runState{loop: newLoopState(chat.AgentConstraints{}, nil, false), tools: tools}
	events := make(chan chat.StreamEvent, 16)
	svc.settleToolTurn(t.Context(), "host-session", "host-message", &turnSetup{selection: &ToolSelection{}}, run, providerTurn{toolUseBlocks: []llmtypes.ToolUseBlock{call}}, events)
	if !reflect.DeepEqual(probe.input, map[string]any{"intent": "find a reader"}) {
		t.Fatal("discovery received display arguments", probe.input)
	}
	event := <-events
	if event.ToolAction != "Finding tools" || event.ToolSummary != "Reader discovery" {
		t.Fatal("discovery display missing", event)
	}
	if len(run.chatMessages) < 1 || len(run.chatMessages[0].ContentBlocks) != 1 || run.chatMessages[0].ContentBlocks[0].Input == nil || !reflect.DeepEqual(*run.chatMessages[0].ContentBlocks[0].Input, input) || input["toolAction"] != "Finding tools" {
		t.Fatal("provider tool-use history changed", run.chatMessages)
	}
	loadedInput, display := splitToolDisplayInput(run.tools, llmtypes.ToolUseBlock{Name: "loaded_tool", Input: map[string]any{"value": "v", "toolAction": "Loaded action"}})
	if !reflect.DeepEqual(loadedInput, map[string]any{"value": "v"}) || display.action != "Loaded action" {
		t.Fatal("new discovery schemas lack private display ownership", loadedInput, display)
	}
}

func TestToolDisplayOptionalWireAndNestedInputIsolation(t *testing.T) {
	legacy, err := json.Marshal(chat.StreamEvent{Type: "tool_call", Tool: "legacy"})
	if err != nil || strings.Contains(string(legacy), "tool_action") || strings.Contains(string(legacy), "tool_summary") {
		t.Fatal("optional wire fields changed legacy event", string(legacy), err)
	}
	tools := []llmtypes.ToolDefinition{displayToolDefinition("nested")}
	normalizeToolInputSchemas(tools)
	nested := map[string]any{"items": []any{map[string]any{"literal": "native"}}}
	input := map[string]any{"value": nested, "toolAction": "Display only"}
	clean, display := splitToolDisplayInput(tools, llmtypes.ToolUseBlock{Name: "nested", Input: input})
	clean["value"].(map[string]any)["items"].([]any)[0].(map[string]any)["literal"] = "callback-local"
	if nested["items"].([]any)[0].(map[string]any)["literal"] != "native" {
		t.Fatal("callback mutation reached provider history")
	}
	event := toolCallDisplayEvent(llmtypes.ToolUseBlock{ID: "call", Name: "nested", Input: clean}, display)
	wire, err := json.Marshal(event)
	if err != nil || !strings.Contains(string(wire), `"tool_action":"Display only"`) || strings.Contains(string(wire), "tool_summary") {
		t.Fatal("additive display wire incorrect", string(wire), err)
	}
}

// These are already admitted private execution plans. This standalone test
// checks scheduling/display isolation, not actor enrollment or app authority.
type displayParallelProbe struct {
	stubToolService
	mu      sync.Mutex
	started int
	barrier chan struct{}
	inputs  []map[string]any
}

func (*displayParallelProbe) GetToolSchema(string) map[string]any { return nil }

func (p *displayParallelProbe) Execute(ctx context.Context, _, _ string, input map[string]any) (*ToolResult, error) {
	p.mu.Lock()
	p.inputs = append(p.inputs, input)
	p.started++
	if p.started == 2 {
		close(p.barrier)
	}
	p.mu.Unlock()
	select {
	case <-p.barrier:
		return &ToolResult{Output: "parallel result"}, nil
	case <-ctx.Done():
		return &ToolResult{Output: "canceled", IsError: true}, ctx.Err()
	}
}

func TestToolDisplayParallelExecutionIsolation(t *testing.T) {
	probe := &displayParallelProbe{barrier: make(chan struct{})}
	svc := &chatServiceImpl{tools: probe, streams: NewStreamManager()}
	tools := []llmtypes.ToolDefinition{displayToolDefinition("parallel_tool")}
	normalizeToolInputSchemas(tools)
	var plans []toolPlan
	for _, id := range []string{"first", "second"} {
		tu := llmtypes.ToolUseBlock{ID: id, Name: "parallel_tool", Input: map[string]any{"value": id, "toolAction": "Action " + id}}
		input, display := splitToolDisplayInput(tools, tu)
		tu.Input = input
		plans = append(plans, toolPlan{tu: tu, execInput: input, display: display, status: toolPlanReady, concurrent: true})
	}
	events := make(chan chat.StreamEvent, 16)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	results := svc.executeToolBatch(ctx, plans, newLoopState(chat.AgentConstraints{}, nil, false), "", events, "host-session")
	if len(results) != 2 || results[0].isError || results[1].isError || probe.started != 2 {
		t.Fatal("parallel execution failed", results)
	}
	close(events)
	seen := map[string]string{}
	for event := range events {
		if event.Type == "tool_call" {
			seen[event.ToolID] = event.ToolAction
		}
	}
	if !reflect.DeepEqual(seen, map[string]string{"first": "Action first", "second": "Action second"}) {
		t.Fatal("parallel labels crossed tool calls", seen)
	}
	for _, input := range probe.inputs {
		if _, exists := input["toolAction"]; exists {
			t.Fatal("parallel dispatch received display arguments", input)
		}
	}

}
