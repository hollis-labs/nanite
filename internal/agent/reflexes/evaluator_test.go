package reflexes

import (
	"encoding/json"
	"strings"
	"testing"
)

// fixtureEchoState returns a synthetic State matching the FU-13
// cache-miss singleton-race attractor (the "echo" signature):
//
//   - 3 consecutive turns with input_tokens=3, cache_read=0
//   - identical byte-identical bodies
//   - zero tool calls
//
// This MUST trip the drift_detector_echo reflex.
func fixtureEchoState() State {
	body := "Awaiting state. Mailbox empty. No probes."
	signals := []MessageSignal{
		{MessageID: "m3", Content: body, InputTokens: 3, OutputTokens: 18, CacheRead: 0, ToolCalls: 0},
		{MessageID: "m2", Content: body, InputTokens: 3, OutputTokens: 18, CacheRead: 0, ToolCalls: 0},
		{MessageID: "m1", Content: body, InputTokens: 3, OutputTokens: 18, CacheRead: 0, ToolCalls: 0},
	}
	return State{SessionID: "s-echo", AgentID: "a-1", Messages: signals}
}

// fixtureHealthyCompressionState returns a synthetic State matching
// healthy idle compression. Output may be shorter than prior, but
// cache_read is in the millions (multi-iteration within-pass cache
// hits), input_tokens are 40-90, and tool calls are >0. This MUST NOT
// trip the echo detector — same surface symptom, different cause.
func fixtureHealthyCompressionState() State {
	bodies := []string{
		"Three healthy turns, varying content.",
		"Saw nothing new; brief verdict.",
		"Latest report after multiple tool calls.",
	}
	signals := []MessageSignal{
		{MessageID: "m3", Content: bodies[2], InputTokens: 72, OutputTokens: 25, CacheRead: 1_500_000, ToolCalls: 3},
		{MessageID: "m2", Content: bodies[1], InputTokens: 45, OutputTokens: 12, CacheRead: 1_200_000, ToolCalls: 2},
		{MessageID: "m1", Content: bodies[0], InputTokens: 85, OutputTokens: 41, CacheRead: 1_900_000, ToolCalls: 4},
	}
	return State{SessionID: "s-compress", AgentID: "a-1", Messages: signals}
}

// fixtureSuperlativeState returns 3 consecutive turns with:
//   - zero tool calls
//   - output growing by ≥1.5× each step (10 → 20 → 40)
//   - escalating-language pattern in at least one turn
func fixtureSuperlativeState() State {
	signals := []MessageSignal{
		{MessageID: "m3", Content: "Beyond Emergency: an absolute crisis of unprecedented scope.", InputTokens: 5, OutputTokens: 40, CacheRead: 0, ToolCalls: 0},
		{MessageID: "m2", Content: "An emergency response is needed; deploying overwhelming force.", InputTokens: 5, OutputTokens: 20, CacheRead: 0, ToolCalls: 0},
		{MessageID: "m1", Content: "Note an issue, will continue.", InputTokens: 5, OutputTokens: 10, CacheRead: 0, ToolCalls: 0},
	}
	return State{SessionID: "s-super", AgentID: "a-2", Messages: signals}
}

// fixtureSingleSuperlativeState: only ONE turn has the regex; the
// other two pass with no growth. This must NOT trigger the runaway
// detector — conjunction over N=3 keeps single-pass matches harmless.
func fixtureSingleSuperlativeState() State {
	signals := []MessageSignal{
		{MessageID: "m3", Content: "Status quo.", InputTokens: 5, OutputTokens: 10, CacheRead: 0, ToolCalls: 0},
		{MessageID: "m2", Content: "Beyond Emergency, this is a maximum crisis!", InputTokens: 5, OutputTokens: 12, CacheRead: 0, ToolCalls: 0},
		{MessageID: "m1", Content: "Note an issue.", InputTokens: 5, OutputTokens: 11, CacheRead: 0, ToolCalls: 0},
	}
	return State{SessionID: "s-super-single", Messages: signals}
}

// seedTriggerSpec extracts the trigger_spec JSON for a named base seed.
func seedTriggerSpec(t *testing.T, classTag, name string) (string, string) {
	t.Helper()
	for _, s := range BaseSeeds() {
		if s.ClassTag == classTag && s.Name == name {
			b, err := json.Marshal(s.TriggerSpec)
			if err != nil {
				t.Fatalf("marshal trigger: %v", err)
			}
			return s.TriggerKind, string(b)
		}
	}
	t.Fatalf("no seed matching class=%s name=%s", classTag, name)
	return "", ""
}

// TestDriftDetector_FiresOnDegenerate — critical FU-30 contract test.
//
// The echo signature MUST fire the drift_detector_echo reflex.
func TestDriftDetector_FiresOnDegenerate(t *testing.T) {
	kind, spec := seedTriggerSpec(t, "process", "drift_detector_echo")
	fired, err := EvaluateTrigger(kind, spec, fixtureEchoState())
	if err != nil {
		t.Fatalf("EvaluateTrigger error: %v", err)
	}
	if !fired {
		t.Fatalf("drift_detector_echo MUST fire on the echo signature; got fired=false")
	}
}

// TestDriftDetector_DoesNotFireOnHealthyCompression — critical FU-30
// contract test. Healthy idle compression shares the surface symptom
// (short output) but differs in cache_read + tool_calls + input_tokens.
// The reflex MUST NOT fire on this state.
func TestDriftDetector_DoesNotFireOnHealthyCompression(t *testing.T) {
	kind, spec := seedTriggerSpec(t, "process", "drift_detector_echo")
	fired, err := EvaluateTrigger(kind, spec, fixtureHealthyCompressionState())
	if err != nil {
		t.Fatalf("EvaluateTrigger error: %v", err)
	}
	if fired {
		t.Fatalf("drift_detector_echo MUST NOT fire on healthy compression; got fired=true (conjunction violated)")
	}
}

// TestRunawaySuperlativeDetector_FiresOnRegexMatch — critical FU-30
// contract test. Conjunctive: zero tool calls + output growth + regex
// match. All three together → fire.
func TestRunawaySuperlativeDetector_FiresOnRegexMatch(t *testing.T) {
	kind, spec := seedTriggerSpec(t, "process", "runaway_superlative_detector")
	fired, err := EvaluateTrigger(kind, spec, fixtureSuperlativeState())
	if err != nil {
		t.Fatalf("EvaluateTrigger error: %v", err)
	}
	if !fired {
		t.Fatalf("runaway_superlative_detector MUST fire on the full conjunction; got fired=false")
	}
}

// TestRunawaySuperlativeDetector_DoesNotFireOnSinglePass — critical
// FU-30 contract test. A single regex match in the window is NOT
// enough; we require growth + zero tool calls too. This state has the
// regex but no output growth.
func TestRunawaySuperlativeDetector_DoesNotFireOnSinglePass(t *testing.T) {
	kind, spec := seedTriggerSpec(t, "process", "runaway_superlative_detector")
	fired, err := EvaluateTrigger(kind, spec, fixtureSingleSuperlativeState())
	if err != nil {
		t.Fatalf("EvaluateTrigger error: %v", err)
	}
	if fired {
		t.Fatalf("runaway_superlative_detector MUST NOT fire without sustained growth; got fired=true")
	}
}

func TestCleanStatusWithoutTools_FiresOnUngroundedAllClear(t *testing.T) {
	kind, spec := seedTriggerSpec(t, "process", "clean_status_without_tools")
	st := State{Messages: []MessageSignal{
		{Content: "All clear. Nothing new in the system.", ToolCalls: 0},
		{Content: "Healthy and nominal.", ToolCalls: 0},
	}}
	fired, err := EvaluateTrigger(kind, spec, st)
	if err != nil {
		t.Fatalf("EvaluateTrigger error: %v", err)
	}
	if !fired {
		t.Fatalf("clean_status_without_tools should fire on clean-status claims with zero tools")
	}

	st.Messages[0].ToolCalls = 2
	fired, err = EvaluateTrigger(kind, spec, st)
	if err != nil {
		t.Fatalf("EvaluateTrigger with tools error: %v", err)
	}
	if fired {
		t.Fatalf("clean_status_without_tools must not fire when recent turns used tools")
	}
}

func TestRepeatedPlanningWithoutAction_FiresOnPlanningLoop(t *testing.T) {
	kind, spec := seedTriggerSpec(t, "process", "repeated_planning_without_action")
	st := State{Messages: []MessageSignal{
		{Content: "I will continue with the next pass.", ToolCalls: 0},
		{Content: "The plan is to proceed carefully.", ToolCalls: 0},
		{Content: "Going to review the queue next.", ToolCalls: 0},
		{Content: "I should proceed after this check.", ToolCalls: 0},
	}}
	fired, err := EvaluateTrigger(kind, spec, st)
	if err != nil {
		t.Fatalf("EvaluateTrigger error: %v", err)
	}
	if !fired {
		t.Fatalf("repeated_planning_without_action should fire on a zero-tool planning loop")
	}

	st.Messages[2].ToolCalls = 1
	fired, err = EvaluateTrigger(kind, spec, st)
	if err != nil {
		t.Fatalf("EvaluateTrigger with tool call error: %v", err)
	}
	if fired {
		t.Fatalf("repeated_planning_without_action must not fire when a turn used tools")
	}
}

func TestEvidenceClaimWithoutTools_FiresOnUngroundedCurrentness(t *testing.T) {
	kind, spec := seedTriggerSpec(t, "advisor", "evidence_claim_without_tools")
	st := State{Messages: []MessageSignal{
		{Content: "I confirmed the latest status today.", ToolCalls: 0},
		{Content: "Current state appears stable.", ToolCalls: 0},
	}}
	fired, err := EvaluateTrigger(kind, spec, st)
	if err != nil {
		t.Fatalf("EvaluateTrigger error: %v", err)
	}
	if !fired {
		t.Fatalf("evidence_claim_without_tools should fire on currentness claims with zero tools")
	}

	st.Messages[1].ToolCalls = 1
	fired, err = EvaluateTrigger(kind, spec, st)
	if err != nil {
		t.Fatalf("EvaluateTrigger with tools error: %v", err)
	}
	if fired {
		t.Fatalf("evidence_claim_without_tools must not fire when the window includes tool grounding")
	}
}

// TestEvaluator_NumericWindowKinds exercises the basic per-signal
// predicate kinds in isolation.
func TestEvaluator_NumericWindowKinds(t *testing.T) {
	signals := []MessageSignal{
		{ToolCalls: 0, CacheRead: 0, InputTokens: 3},
		{ToolCalls: 0, CacheRead: 0, InputTokens: 5},
		{ToolCalls: 0, CacheRead: 0, InputTokens: 7},
	}
	st := State{Messages: signals}

	cases := []struct {
		name string
		spec string
		want bool
	}{
		{"toolcalls_eq_0", `{"kind":"tool_calls_window","window":3,"op":"=","value":0}`, true},
		{"toolcalls_lt_1", `{"kind":"tool_calls_window","window":3,"op":"<","value":1}`, true},
		{"input_lt_10", `{"kind":"input_tokens_window","window":3,"op":"<","value":10}`, true},
		{"input_lt_4", `{"kind":"input_tokens_window","window":3,"op":"<","value":4}`, false},
		{"cache_eq_0", `{"kind":"cache_read_window","window":3,"op":"=","value":0}`, true},
		{"window_too_large_returns_false", `{"kind":"tool_calls_window","window":99,"op":"=","value":0}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := EvaluateTrigger("predicate", c.spec, st)
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != c.want {
				t.Errorf("got %v want %v", got, c.want)
			}
		})
	}
}

// TestEvaluator_IdenticalOutputWindow_ByteIdentity confirms the helper.
func TestEvaluator_IdenticalOutputWindow_ByteIdentity(t *testing.T) {
	st := State{Messages: []MessageSignal{
		{Content: "hello"}, {Content: "hello"}, {Content: "hello"},
	}}
	got, _ := EvaluateTrigger("predicate", `{"kind":"identical_output_window","window":3}`, st)
	if !got {
		t.Errorf("identical bodies should fire identical_output_window; got false")
	}
	st.Messages[1].Content = "world"
	got, _ = EvaluateTrigger("predicate", `{"kind":"identical_output_window","window":3}`, st)
	if got {
		t.Errorf("differing bodies should NOT fire; got true")
	}
}

// TestEvaluator_OutputGrowthWindow validates the 1.5× growth check.
func TestEvaluator_OutputGrowthWindow(t *testing.T) {
	growing := State{Messages: []MessageSignal{
		{OutputTokens: 40}, {OutputTokens: 20}, {OutputTokens: 10},
	}}
	got, _ := EvaluateTrigger("predicate", `{"kind":"output_growth_window","window":3,"factor":1.5}`, growing)
	if !got {
		t.Errorf("growing 10→20→40 should fire 1.5× growth; got false")
	}
	flat := State{Messages: []MessageSignal{
		{OutputTokens: 10}, {OutputTokens: 10}, {OutputTokens: 10},
	}}
	got, _ = EvaluateTrigger("predicate", `{"kind":"output_growth_window","window":3,"factor":1.5}`, flat)
	if got {
		t.Errorf("flat 10/10/10 should NOT fire growth; got true")
	}
}

// TestEvaluator_RegexMatchWindow_AtLeastOne validates "any match in window."
func TestEvaluator_RegexMatchWindow_AtLeastOne(t *testing.T) {
	st := State{Messages: []MessageSignal{
		{Content: "ok"},
		{Content: "Beyond Emergency"},
		{Content: "fine"},
	}}
	spec := `{"kind":"regex_match_window","window":3,"pattern":"(?i)(Beyond|Absolute|Ultimate|Maximum)\\s+(Emergency|Crisis|Unprecedented)"}`
	got, err := EvaluateTrigger("predicate", spec, st)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !got {
		t.Errorf("expected match (Beyond Emergency); got false")
	}
}

// TestEvaluator_EventTrigger_MailReceived exercises the event path.
func TestEvaluator_EventTrigger_MailReceived(t *testing.T) {
	st := State{MailUnreadCount: 0}
	got, _ := EvaluateTrigger("event", `{"name":"mail_received"}`, st)
	if got {
		t.Errorf("no unread → should not fire")
	}
	st.MailUnreadCount = 2
	got, _ = EvaluateTrigger("event", `{"name":"mail_received"}`, st)
	if !got {
		t.Errorf("unread=2 → should fire")
	}
}

// TestEvaluator_IntervalTrigger_FiresOnMultiple exercises interval kind.
func TestEvaluator_IntervalTrigger_FiresOnMultiple(t *testing.T) {
	st := State{TickN: 20}
	got, _ := EvaluateTrigger("interval", `{"every_n_ticks":10}`, st)
	if !got {
		t.Errorf("tick=20 every_n=10 should fire; got false")
	}
	st.TickN = 15
	got, _ = EvaluateTrigger("interval", `{"every_n_ticks":10}`, st)
	if got {
		t.Errorf("tick=15 should NOT fire every-10; got true")
	}
}

// TestEvaluator_PrefixPressure exercises the prefix_pressure node.
func TestEvaluator_PrefixPressure(t *testing.T) {
	// 170k of 200k window = 0.85 ratio — at the threshold.
	st := State{PrefixTokens: 170000}
	spec := `{"kind":"prefix_pressure","ratio":0.85,"context_window":200000}`
	got, _ := EvaluateTrigger("predicate", spec, st)
	if !got {
		t.Errorf("170k/200k should fire prefix_pressure(0.85); got false")
	}
	st.PrefixTokens = 100000
	got, _ = EvaluateTrigger("predicate", spec, st)
	if got {
		t.Errorf("100k/200k should NOT fire prefix_pressure(0.85); got true")
	}
}

func TestEvaluator_UserRegexWindow(t *testing.T) {
	st := State{UserMessages: []MessageSignal{
		{Role: "user", Content: "Let's document that and create a task for it."},
	}}
	spec := `{"kind":"user_regex_window","window":1,"pattern":"(?i)document that.*create a task"}`
	got, err := EvaluateTrigger("predicate", spec, st)
	if err != nil {
		t.Fatalf("EvaluateTrigger error: %v", err)
	}
	if !got {
		t.Fatalf("user_regex_window should match recent user intent")
	}
}

func TestEvaluator_EntityMentionWindow(t *testing.T) {
	st := State{UserMessages: []MessageSignal{
		{Role: "user", Content: "Can you check NAN-128 and docs/promptrouter-authoring.md?"},
	}}
	got, err := EvaluateTrigger("predicate", `{"kind":"entity_mention_window","entity":"task_id","scope":"user","window":1}`, st)
	if err != nil {
		t.Fatalf("task id EvaluateTrigger error: %v", err)
	}
	if !got {
		t.Fatalf("entity_mention_window should match task ids")
	}
	got, err = EvaluateTrigger("predicate", `{"kind":"entity_mention_window","entity":"path","scope":"user","window":1}`, st)
	if err != nil {
		t.Fatalf("path EvaluateTrigger error: %v", err)
	}
	if !got {
		t.Fatalf("entity_mention_window should match file paths")
	}
}

func TestEvaluator_ToolAndEnvelopeNameWindows(t *testing.T) {
	st := State{Messages: []MessageSignal{
		{Role: "assistant", ToolNames: []string{"memory_write", "torque_task_create"}, EnvelopeTypes: []string{"options"}},
		{Role: "assistant", ToolNames: []string{"relay_send"}, EnvelopeTypes: []string{"status"}},
	}}
	got, err := EvaluateTrigger("predicate", `{"kind":"tool_name_window","window":2,"names":["torque_task_create"],"mode":"any"}`, st)
	if err != nil {
		t.Fatalf("tool_name_window error: %v", err)
	}
	if !got {
		t.Fatalf("tool_name_window should match named tool")
	}
	got, err = EvaluateTrigger("predicate", `{"kind":"envelope_type_window","window":2,"names":["approval"],"mode":"none"}`, st)
	if err != nil {
		t.Fatalf("envelope_type_window error: %v", err)
	}
	if !got {
		t.Fatalf("envelope_type_window mode=none should fire when absent")
	}
}

func TestEvaluator_MailUnreadCountPredicate(t *testing.T) {
	st := State{MailUnreadCount: 3}
	got, err := EvaluateTrigger("predicate", `{"kind":"mail_unread_count","op":">=","value":2}`, st)
	if err != nil {
		t.Fatalf("EvaluateTrigger error: %v", err)
	}
	if !got {
		t.Fatalf("mail_unread_count should compare unread mailbox count")
	}
}

// TestEvaluator_UnknownKindReturnsError keeps the engine honest.
func TestEvaluator_UnknownKindReturnsError(t *testing.T) {
	_, err := EvaluateTrigger("predicate", `{"kind":"nonexistent"}`, State{})
	if err == nil || !strings.Contains(err.Error(), "unknown predicate kind") {
		t.Errorf("expected unknown-kind error; got %v", err)
	}
}
