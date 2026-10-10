package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/agentpolicy"
	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/harnessprofile"
	"github.com/hollis-labs/nanite/internal/writeclaim"
	permissionlib "github.com/hollis-labs/substrate/harness/interception/permission"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

const (
	invented = "01M2SFA0ZQ3K4N6P7R8T9V0WXY" // never returned by any tool
	returned = "01M3QSTVA1AMA53J6FTSDEZPN3" // returned by the fixture's write tool
)

func claim(id string) string {
	return "The Tesseract write succeeded and I saved the record as " + id + "."
}

// ---- the hook itself -------------------------------------------------------

func TestWriteClaimHookDecisions(t *testing.T) {
	stop := func(msg string) string { return msg }
	none := writeClaimFacts{}

	// A backed claim, no claim at all, and a disabled guard all allow.
	for name, tc := range map[string]struct {
		mode  harnessprofile.GuardMode
		reply string
		facts writeClaimFacts
		want  string
	}{
		"no claim":                        {harnessprofile.GuardDeny, "Task CW-20260919-0011 is in review.", none, wcNoClaim},
		"guard off":                       {harnessprofile.GuardOff, claim(invented), none, wcOff},
		"write succeeded this turn":       {harnessprofile.GuardDeny, claim(returned), writeClaimFacts{WroteThisTurn: true, GroundedIDs: map[string]bool{returned: true}}, wcWriteSucceeded},
		"wrote X, claims fabricated Y":    {harnessprofile.GuardDeny, claim(invented), writeClaimFacts{WroteThisTurn: true, GroundedIDs: map[string]bool{returned: true}}, wcUnbackedClaim},
		"wrote, result had no id":         {harnessprofile.GuardDeny, claim(invented), writeClaimFacts{WroteThisTurn: true}, wcUnbackedClaim},
		"recap of an earlier write":       {harnessprofile.GuardDeny, claim(returned), writeClaimFacts{GroundedIDs: map[string]bool{returned: true}}, wcGroundedInWrite},
		"invented id, nothing ran":        {harnessprofile.GuardDeny, claim(invented), none, wcUnbackedClaim},
		"an id only a READ result showed": {harnessprofile.GuardDeny, claim(returned), writeClaimFacts{ToolsRan: []string{"kb_read:ok"}}, wcUnbackedClaim},
		"a failed write, then success":    {harnessprofile.GuardDeny, claim(invented), writeClaimFacts{ToolsRan: []string{"kb_write:error"}}, wcUnbackedClaim},
		"only discovery tools ran (c395)": {harnessprofile.GuardDeny, claim(invented), writeClaimFacts{ToolsRan: []string{"whoami:ok", "tool_describe:ok"}}, wcUnbackedClaim},
		"one grounded, one invented id":   {harnessprofile.GuardDeny, "Saved " + returned + " and also wrote " + invented + ".", writeClaimFacts{GroundedIDs: map[string]bool{returned: true}}, wcUnbackedClaim},
	} {
		out, d := writeClaimHook(tc.mode, stop(tc.reply), tc.facts)
		if d.Reason != tc.want {
			t.Errorf("%s: reason %q, want %q", name, d.Reason, tc.want)
		}
		if wantFired := tc.want == wcUnbackedClaim; d.Fired != wantFired {
			t.Errorf("%s: fired = %v", name, d.Fired)
		}
		if !d.Fired && out.Decision != permissionlib.DecisionAllow {
			t.Errorf("%s: an unfired guard returned %q", name, out.Decision)
		}
	}

	// What each mode does when it fires.
	deny, d := writeClaimHook(harnessprofile.GuardDeny, stop(claim(invented)), none)
	if deny.Decision != permissionlib.DecisionDeny || deny.Continue == nil || *deny.Continue || deny.StopReason == "" || !strings.Contains(deny.Reason, invented) || len(d.Finding.Ungrounded) != 1 {
		t.Errorf("deny output = %+v", deny)
	}
	warn, _ := writeClaimHook(harnessprofile.GuardWarn, stop(claim(invented)), none)
	if warn.Decision != permissionlib.DecisionAllow || warn.SystemMessage == "" || warn.Continue != nil {
		t.Errorf("warn output = %+v", warn)
	}
	ask, _ := writeClaimHook(harnessprofile.GuardAsk, stop(claim(invented)), none)
	if ask.Decision != permissionlib.DecisionAsk || ask.Continue != nil {
		t.Errorf("ask output = %+v", ask)
	}
	for _, o := range []writeClaimHookResult{deny, warn, ask} {
		switch o.Decision {
		case permissionlib.DecisionAllow, permissionlib.DecisionDeny, permissionlib.DecisionAsk:
		default:
			t.Errorf("invalid guard decision: %q", o.Decision)
		}
	}
}

// ---- which tools count as a write -----------------------------------------

type metaStub struct {
	characterizationTools
	readOnly map[string]bool
}

func (m *metaStub) GetToolMeta(_ context.Context, name string) (ToolMetaInfo, bool) {
	return ToolMetaInfo{IsReadOnly: m.readOnly[name]}, true
}

func TestIsWriteCapable(t *testing.T) {
	svc := &chatServiceImpl{tools: &destructiveStub{
		metaStub:    metaStub{readOnly: map[string]bool{"kb_read": true, "write_preview": true}},
		destructive: map[string]bool{"opaque_mutator": true},
	}}
	ctx := context.Background()
	for name, want := range map[string]bool{
		// recognizable writers
		"kb_write": true, "torque_task_transition": true, "tether_group_post": true, "knowledgeWrite": true,
		"context_ingest": true, "mux_message_send": true, "cerberus_forge_deploy": true, "torque_comment_add": true,
		// declared destructive, name says nothing
		"opaque_mutator": true,
		// declared read-only wins over a write verb in the name
		"write_preview": false, "kb_read": false,
		// discovery, cache and scratch
		"request_tools": false, "tool_describe": false, "whoami": false, "fetch_tool_result": false,
		"search_tool_result": false, "scratchpad_write": false,
		// pure / computing tools must not silence the guard or ground an id
		"think": false, "math_eval": false, "datetime": false, "calc": false, "base64_encode": false,
		// unclassified and not a writer by name
		"unknown_tool": false, "tesseract_recall": false, "torque_task_get": false,
	} {
		if got := svc.isWriteCapable(ctx, name); got != want {
			t.Errorf("isWriteCapable(%q) = %v, want %v", name, got, want)
		}
	}
	// With no tool service the name still decides.
	bare := &chatServiceImpl{}
	if !bare.isWriteCapable(ctx, "kb_write") || bare.isWriteCapable(ctx, "think") || bare.isWriteCapable(ctx, "opaque_mutator") {
		t.Error("name-only classification is wrong")
	}
}

// metaTable serves fixed ToolMetaInfo per name, standing in for declared and
// heuristic metadata.
type metaTable struct {
	characterizationTools
	meta map[string]ToolMetaInfo
}

func (m *metaTable) GetToolMeta(_ context.Context, name string) (ToolMetaInfo, bool) {
	mi, ok := m.meta[name]
	return mi, ok
}

// Representative names per verb class, not the catalog: the classifier is a
// fallback for tools that declare nothing, and the cases here pin its rules.
func TestIsWriteCapableNameHeuristic(t *testing.T) {
	svc := &chatServiceImpl{}
	ctx := context.Background()
	for name, want := range map[string]bool{
		// writers with no CRUD verb, one or two per token class
		"handoff_stash": true, "handoff_approve": true, "subagent_spawn": true, "builder_step": true,
		"install_home": true, "mux_message_notify": true, "mux_message_consume": true,
		"mux_message_mark_read": true, "tether_group_mark_read": true, "tether_group_leave": true,
		"torque_session_checkpoint": true, "torque_task_checkpoint_emit": true, "torque_task_subtodo_done": true,
		"cerberus_docker_up": true, "cerberus_docker_down": true, "cerberus_resource_reload": true,
		"loom_compile_request": true, "tangent_session_advance": true, "tangent_surface_open": true,
		"tangent_hitl_withdraw": true, "mux_session_resize": true, "reanalyze_fragment_attachments": true,
		// whole-name writers
		"mux_call": true, "message_resolve": true,
		// a trailing noun that is also a read word must not hide the verb
		"context_status_set": true, "torque_collection_inbox_add": true,
		// a read verb wins over a write token
		"cerberus_get_dns_record_set": false, "preview_ingest": false, "validate_ingest": false,
		"loom_compile_job_get": false, "loom_compile_job_list": false, "torque_task_checkpoint_get": false,
		"torque_task_checkpoint_list": false, "tangent_interaction_list_kinds": false,
		// readers, and lookups that share a word with a writer
		"torque_task_get": false, "torque_task_list": false, "tesseract_recall": false, "tesseract_history": false,
		"tesseract_ref_resolve": false, "tangent_interaction_resolve_definition": false,
		"tether_group_read": false, "tether_registry_lookup": false, "mux_message_inbox": false,
		"mux_message_thread": false, "mux_message_trace": false, "cerberus_resource_status": false,
		"context_estimate": false, "tangent_health_report": false, "mux_ai_chat": false, "mux_ai_embeddings": false,
	} {
		if got := svc.isWriteCapable(ctx, name); got != want {
			t.Errorf("isWriteCapable(%q) = %v, want %v", name, got, want)
		}
	}
}

// Declared hints beat the name in both directions; an absent hint is not a
// declaration and falls through to the name.
func TestIsWriteCapableDeclaredHintsWin(t *testing.T) {
	svc := &chatServiceImpl{tools: &metaTable{meta: map[string]ToolMetaInfo{
		"sync_records":     {ReadOnlyDeclared: true},                   // write-looking name, declared read
		"quietly_changes":  {WriteDeclared: true},                      // no verb, declared write
		"drop_table_x":     {IsDestructive: true, WriteDeclared: true}, // declared destructive
		"kb_write_preview": {ReadOnlyDeclared: true},                   // declared read beats write verb
		"post_thing":       {},                                         // annotations without hints: name decides
		"mystery_op":       {},                                         // nothing declared, nothing in the name
	}}}
	ctx := context.Background()
	for name, want := range map[string]bool{
		"sync_records": false, "quietly_changes": true, "drop_table_x": true,
		"kb_write_preview": false, "post_thing": true, "mystery_op": false,
	} {
		if got := svc.isWriteCapable(ctx, name); got != want {
			t.Errorf("isWriteCapable(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestMarkReadIsNotHeuristicallyReadOnly(t *testing.T) {
	svc := &toolServiceImpl{}
	for name, want := range map[string]bool{
		"mux_message_mark_read": false, "tether_group_mark_read": false, "kb_read": true, "tether_group_read": true,
	} {
		meta, _ := svc.GetToolMeta(context.Background(), name)
		if meta.IsReadOnly != want {
			t.Errorf("GetToolMeta(%q).IsReadOnly = %v, want %v", name, meta.IsReadOnly, want)
		}
	}
}

type destructiveStub struct {
	metaStub
	destructive map[string]bool
}

func (d *destructiveStub) GetToolMeta(ctx context.Context, name string) (ToolMetaInfo, bool) {
	m, ok := d.metaStub.GetToolMeta(ctx, name)
	m.IsDestructive = d.destructive[name]
	return m, ok
}

func TestNameTokens(t *testing.T) {
	for in, want := range map[string]string{
		"torque_task_transition": "torque task transition",
		"knowledgeWrite":         "knowledge write",
		"mux-message.send":       "mux message send",
		"HTTPPost":               "http post",
		"":                       "",
	} {
		if got := strings.Join(nameTokens(in), " "); got != want {
			t.Errorf("nameTokens(%q) = %q, want %q", in, got, want)
		}
	}
}

// ---- through the real loop -------------------------------------------------

// guardTools is the fixture's tool stub with real read/write classification and
// outputs that carry ids.
type guardTools struct {
	characterizationTools
	failWrite bool
}

func (g *guardTools) GetToolMeta(_ context.Context, name string) (ToolMetaInfo, bool) {
	return ToolMetaInfo{IsReadOnly: strings.HasSuffix(name, "_read")}, true
}

func (g *guardTools) Execute(_ context.Context, _ string, name string, _ map[string]any) (*ToolResult, error) {
	g.mu.Lock()
	g.executed = append(g.executed, name)
	g.mu.Unlock()
	switch name {
	case "kb_write":
		if g.failWrite {
			return &ToolResult{Output: "Error: write refused", IsError: true}, nil
		}
		return &ToolResult{Output: `{"ok":true,"id":"` + returned + `"}`}, nil
	case "think":
		return &ToolResult{Output: "noted: the record id is " + invented}, nil
	case "kb_read":
		return &ToolResult{Output: `{"id":"` + returned + `","body":"existing entry"}`}, nil
	}
	return &ToolResult{Output: name + " ok"}, nil
}

func newGuardFixture(t *testing.T, steps []characterizationProviderStep, tools *guardTools) *characterizationFixture {
	return newGuardFixtureWithPolicy(t, steps, tools, "deny")
}

func newGuardFixtureWithPolicy(t *testing.T, steps []characterizationProviderStep, tools *guardTools, mode string) *characterizationFixture {
	t.Helper()
	f := newCharacterizationFixtureWithPolicy(t, steps, "Private guard instructions", &agentpolicy.NativePolicy{WriteClaimGuard: mode}, "kb_write", "kb_read", "whoami", "think")
	tools.definitions = f.tools.definitions
	f.svc.tools = tools
	return f
}

func toolUse(id, name string) llmtypes.ToolUseBlock {
	return llmtypes.ToolUseBlock{ID: id, Name: name, Input: map[string]any{}}
}

func guardEvents(t *testing.T, f *characterizationFixture) []map[string]any {
	t.Helper()
	rows, err := f.st.DB.Query(`SELECT metadata FROM event_log WHERE session_id = ? AND event_type = 'write_claim_guard' ORDER BY id`, f.session)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var meta string
		_ = rows.Scan(&meta)
		var m map[string]any
		_ = json.Unmarshal([]byte(meta), &m)
		out = append(out, m)
	}
	return out
}

func lastAssistantText(t *testing.T, f *characterizationFixture) string {
	t.Helper()
	var content string
	if err := f.st.DB.QueryRow(`SELECT content FROM messages WHERE session_id = ? AND role = 'assistant' ORDER BY created_at DESC, rowid DESC LIMIT 1`, f.session).Scan(&content); err != nil {
		t.Fatal(err)
	}
	return content
}

// The c395 failure: no tool ran and the reply invents a write. It is sent back
// once, the model corrects itself, and the invented id never reaches history.
func TestWriteClaimGuardDenySendsBackOnce(t *testing.T) {
	f := newGuardFixture(t, []characterizationProviderStep{
		{events: doneEvents(claim(invented))},
		{events: doneEvents("Correction: nothing was written; I have no write result to report.")},
	}, &guardTools{})
	events := f.run(t, "deny-once")

	if got := len(f.provider.requestsSnapshot()); got != 2 {
		t.Fatalf("provider requests = %d, want the original and one correction", got)
	}
	if findEvent(events, "replace_content") == nil {
		t.Error("the bad reply was not cleared from the client")
	}
	nudged := false
	for _, m := range f.provider.requestsSnapshot()[1].Messages {
		for _, b := range m.ContentBlocks {
			if strings.Contains(b.Text, "System check") && strings.Contains(b.Text, invented) {
				nudged = true
			}
		}
	}
	if !nudged {
		t.Error("the correction request did not carry the guard's message")
	}
	if text := lastAssistantText(t, f); strings.Contains(text, invented) || !strings.Contains(text, "nothing was written") {
		t.Errorf("stored reply = %q", text)
	}
	ev := guardEvents(t, f)
	if len(ev) != 1 || ev[0]["action"] != "sent_back" || ev[0]["reason"] != wcUnbackedClaim || ev[0]["mode"] != "deny" {
		t.Errorf("decision log = %+v", ev)
	}
}

// A model that repeats the claim is not looped: one retry, then the reply is
// finalized with a visible correction.
func TestWriteClaimGuardCannotLoop(t *testing.T) {
	f := newGuardFixture(t, []characterizationProviderStep{
		{events: doneEvents(claim(invented))},
		{events: doneEvents(claim(invented))},
		{events: doneEvents("a third attempt that must never be requested")},
	}, &guardTools{})
	f.run(t, "no-loop")

	if got := len(f.provider.requestsSnapshot()); got != 2 {
		t.Fatalf("provider requests = %d, want exactly 2", got)
	}
	text := lastAssistantText(t, f)
	if !strings.Contains(text, "Unverified claim") || !strings.Contains(text, invented) {
		t.Errorf("stored reply lacks the correction footer: %q", text)
	}
	ev := guardEvents(t, f)
	if len(ev) != 2 || ev[0]["action"] != "sent_back" || ev[1]["action"] != "footer_after_retry" {
		t.Errorf("decision log = %+v", ev)
	}
}

// The dev profile warns and does not block.
func TestWriteClaimGuardPinnedWarnWithDevHostWarns(t *testing.T) {
	f := newGuardFixtureWithPolicy(t, []characterizationProviderStep{{events: doneEvents(claim(invented))}}, &guardTools{}, "warn")
	if err := f.st.UpdateSessionMetadata(context.Background(), f.session, `{"harness_profile":"dev"}`); err != nil {
		t.Fatal(err)
	}
	events := f.run(t, "dev-warn")

	if got := len(f.provider.requestsSnapshot()); got != 1 {
		t.Fatalf("dev must not send the reply back: %d requests", got)
	}
	if findEvent(events, "replace_content") != nil {
		t.Error("dev cleared the reply")
	}
	warned := false
	for _, e := range events {
		if e.Type == "status" && strings.Contains(e.Content, "write-claim guard") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no warning status event: %v", eventTypes(events))
	}
	if ev := guardEvents(t, f); len(ev) != 1 || ev[0]["action"] != "warned" || ev[0]["mode"] != "warn" {
		t.Errorf("decision log = %+v", ev)
	}
	if !strings.Contains(lastAssistantText(t, f), invented) {
		t.Error("warn mode must leave the reply as written")
	}
}

// Session metadata cannot weaken the guard requested by the verified definition.
func TestWriteClaimGuardDefinitionCannotBeWeakenedByMetadata(t *testing.T) {
	for _, tc := range []struct {
		meta     string
		requests int
	}{
		{`{"harness_profile":"dev","harness_overrides":{"hooks":{"write_claim_guard":"deny"}}}`, 2},
		{`{"harness_overrides":{"hooks":{"write_claim_guard":"off"}}}`, 2},
		{`{"harness_overrides":{"hooks":{"write_claim_guard":"ask"}}}`, 2},
	} {
		f := newGuardFixture(t, []characterizationProviderStep{
			{events: doneEvents(claim(invented))}, {events: doneEvents("ok, nothing was written")},
		}, &guardTools{})
		if err := f.st.UpdateSessionMetadata(context.Background(), f.session, tc.meta); err != nil {
			t.Fatal(err)
		}
		f.run(t, "override")
		if got := len(f.provider.requestsSnapshot()); got != tc.requests {
			t.Errorf("%s: %d provider requests, want %d", tc.meta, got, tc.requests)
		}
	}
}

// A write that succeeded backs the claim; the returned id is grounded.
func TestWriteClaimGuardAllowsABackedClaim(t *testing.T) {
	f := newGuardFixture(t, []characterizationProviderStep{
		{events: toolTurnEvents(toolUse("w1", "kb_write"))},
		{events: doneEvents(claim(returned))},
	}, &guardTools{})
	events := f.run(t, "backed")
	if got := len(f.provider.requestsSnapshot()); got != 2 {
		t.Fatalf("requests = %d, want the tool turn and the reply", got)
	}
	if findEvent(events, "replace_content") != nil || len(guardEvents(t, f)) != 1 || guardEvents(t, f)[0]["reason"] != wcWriteSucceeded {
		t.Errorf("a backed claim was interfered with: %+v", guardEvents(t, f))
	}
	// The ids the write produced are logged for later turns' recaps.
	var n int
	_ = f.st.DB.QueryRow(`SELECT COUNT(*) FROM event_log WHERE session_id = ? AND event_type = 'write_result_ids' AND metadata LIKE ?`, f.session, "%"+returned+"%").Scan(&n)
	if n != 1 {
		t.Errorf("write_result_ids rows = %d", n)
	}
}

// A READ that showed an id does not license a write claim about it.
func TestWriteClaimGuardReadDoesNotBackAWriteClaim(t *testing.T) {
	f := newGuardFixture(t, []characterizationProviderStep{
		{events: toolTurnEvents(toolUse("r1", "kb_read"))},
		{events: doneEvents(claim(returned))},
		{events: doneEvents("I only read " + returned + "; I did not write anything.")},
	}, &guardTools{})
	f.run(t, "read-then-claim")
	if got := len(f.provider.requestsSnapshot()); got != 3 {
		t.Fatalf("requests = %d, want tool turn, claim, correction", got)
	}
	if ev := guardEvents(t, f); len(ev) != 1 || ev[0]["action"] != "sent_back" {
		t.Errorf("a read-backed claim was not caught: %+v", ev)
	}
}

// A failed write does not back a success claim.
func TestWriteClaimGuardFailedWriteDoesNotBackASuccessClaim(t *testing.T) {
	f := newGuardFixture(t, []characterizationProviderStep{
		{events: toolTurnEvents(toolUse("w1", "kb_write"))},
		{events: doneEvents(claim(invented))},
		{events: doneEvents("The write was refused; nothing was saved.")},
	}, &guardTools{failWrite: true})
	f.run(t, "failed-write")
	if got := len(f.provider.requestsSnapshot()); got != 3 {
		t.Fatalf("requests = %d", got)
	}
	if ev := guardEvents(t, f); len(ev) != 1 || ev[0]["action"] != "sent_back" {
		t.Errorf("decision log = %+v", ev)
	}
}

// Having written something this turn does not license citing another id: the
// model wrote X (returned) and claims Y (invented).
func TestWriteClaimGuardWroteXThenClaimsFabricatedY(t *testing.T) {
	f := newGuardFixture(t, []characterizationProviderStep{
		{events: toolTurnEvents(toolUse("w1", "kb_write"))},
		{events: doneEvents(claim(invented))},
		{events: doneEvents("I saved " + returned + " only.")},
	}, &guardTools{})
	f.run(t, "x-then-y")
	if got := len(f.provider.requestsSnapshot()); got != 3 {
		t.Fatalf("requests = %d, want tool turn, fabricated claim, correction", got)
	}
	ev := guardEvents(t, f)
	if len(ev) == 0 || ev[0]["reason"] != wcUnbackedClaim || ev[0]["action"] != "sent_back" {
		t.Errorf("a fabricated id after a real write was not caught: %+v", ev)
	}
}

// Wrote X, claims X: allowed.
func TestWriteClaimGuardWroteXClaimsXIsAllowed(t *testing.T) {
	f := newGuardFixture(t, []characterizationProviderStep{
		{events: toolTurnEvents(toolUse("w1", "kb_write"))},
		{events: doneEvents(claim(returned))},
	}, &guardTools{})
	f.run(t, "x-then-x")
	if got := len(f.provider.requestsSnapshot()); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
}

// An id that appears only in the user's message grounds nothing.
func TestWriteClaimGuardUserMessageIDDoesNotGround(t *testing.T) {
	f := newGuardFixture(t, []characterizationProviderStep{
		{events: doneEvents(claim(invented))},
		{events: doneEvents("Nothing was written.")},
	}, &guardTools{})
	f.userContent = "Please save a note about " + invented
	f.run(t, "user-id")
	if got := len(f.provider.requestsSnapshot()); got != 2 {
		t.Errorf("requests = %d: an id copied from the user's message must not ground a claim", got)
	}
	if ev := guardEvents(t, f); len(ev) == 0 || ev[0]["reason"] != wcUnbackedClaim {
		t.Errorf("decision log = %+v", ev)
	}
}

// A write the permission layer denied never ran and grounds nothing.
func TestWriteClaimGuardDeniedToolDoesNotGround(t *testing.T) {
	f := newGuardFixture(t, []characterizationProviderStep{
		{events: toolTurnEvents(toolUse("w1", "kb_write"))},
		{events: doneEvents(claim(returned))},
		{events: doneEvents("The write was denied; nothing was saved.")},
	}, &guardTools{})
	f.svc.permissions = permissionlib.NewEngine(permissionlib.ModePlan, nil)
	f.run(t, "denied")
	if got := len(f.provider.requestsSnapshot()); got != 3 {
		t.Fatalf("requests = %d, want tool turn, claim, correction", got)
	}
	if ev := guardEvents(t, f); len(ev) == 0 || ev[0]["reason"] != wcUnbackedClaim {
		t.Errorf("decision log = %+v", ev)
	}
}

// The c395 pattern: discovery calls ran, no write, and the reply claims one.
func TestWriteClaimGuardDiscoveryCallsAreNotWrites(t *testing.T) {
	f := newGuardFixture(t, []characterizationProviderStep{
		{events: toolTurnEvents(toolUse("d1", "whoami"))},
		{events: doneEvents(claim(invented))},
		{events: doneEvents("Nothing was written.")},
	}, &guardTools{})
	f.run(t, "c395")
	if got := len(f.provider.requestsSnapshot()); got != 3 {
		t.Fatalf("requests = %d, want tool turn, claim, correction", got)
	}
	ev := guardEvents(t, f)
	if len(ev) != 1 || ev[0]["reason"] != wcUnbackedClaim {
		t.Errorf("decision log = %+v", ev)
	}
}

// A recap of work an earlier turn really did is not an invented write.
func TestWriteClaimGuardAllowsARecapOfAnEarlierWrite(t *testing.T) {
	f := newGuardFixture(t, []characterizationProviderStep{{events: doneEvents("Recap: I saved the record as " + returned + " earlier.")}}, &guardTools{})
	f.st.LogEvent(context.Background(), f.session, "write_result_ids", "write_claim_guard", "kb_write", `{"tool":"kb_write","ids":["`+returned+`"]}`)
	events := f.run(t, "recap")
	if got := len(f.provider.requestsSnapshot()); got != 1 || findEvent(events, "replace_content") != nil {
		t.Fatalf("a recap was blocked: %d requests", got)
	}
	if ev := guardEvents(t, f); len(ev) != 1 || ev[0]["reason"] != wcGroundedInWrite || ev[0]["action"] != "allowed" {
		t.Errorf("decision log = %+v", ev)
	}
}

// The same recap in a different session is not grounded there.
func TestWriteClaimGuardGroundingIsPerSession(t *testing.T) {
	f := newGuardFixture(t, []characterizationProviderStep{
		{events: doneEvents("Recap: I saved the record as " + returned + " earlier.")},
		{events: doneEvents("I have no record of writing that here.")},
	}, &guardTools{})
	f.st.LogEvent(context.Background(), "some-other-session", "write_result_ids", "write_claim_guard", "kb_write", `{"tool":"kb_write","ids":["`+returned+`"]}`)
	f.run(t, "other-session")
	if got := len(f.provider.requestsSnapshot()); got != 2 {
		t.Errorf("requests = %d: another session's write must not ground this claim", got)
	}
}

// Replies with no claim leave no log row.
func TestWriteClaimGuardQuietWithoutAClaim(t *testing.T) {
	f := newGuardFixture(t, []characterizationProviderStep{{events: doneEvents("Task CW-20260919-0011 is in review.")}}, &guardTools{})
	f.run(t, "quiet")
	if ev := guardEvents(t, f); len(ev) != 0 || len(f.provider.requestsSnapshot()) != 1 {
		t.Errorf("guard acted on an ordinary reply: %+v", ev)
	}
}

var _ = chat.StreamEvent{}

// A tool that only computes cannot ground an id even when it echoes one: the
// reasoning tool repeats the model's invented id and the claim still fires.
func TestWriteClaimGuardComputeToolsDoNotGround(t *testing.T) {
	f := newGuardFixture(t, []characterizationProviderStep{
		{events: toolTurnEvents(toolUse("t1", "think"))},
		{events: doneEvents(claim(invented))},
		{events: doneEvents("Nothing was written.")},
	}, &guardTools{})
	f.run(t, "think-echo")
	if got := len(f.provider.requestsSnapshot()); got != 3 {
		t.Fatalf("requests = %d: a compute tool's echo grounded the id", got)
	}
	if ev := guardEvents(t, f); len(ev) != 1 || ev[0]["action"] != "sent_back" {
		t.Errorf("decision log = %+v", ev)
	}
	var n int
	_ = f.st.DB.QueryRow(`SELECT COUNT(*) FROM event_log WHERE session_id = ? AND event_type = 'write_result_ids'`, f.session).Scan(&n)
	if n != 0 {
		t.Errorf("a compute tool's output was logged as write ids: %d rows", n)
	}
}

// ---- prose in iterations that also call tools ---------------------------------

func narratedToolTurn(text string, tu llmtypes.ToolUseBlock) []llmtypes.StreamEvent {
	return []llmtypes.StreamEvent{
		{Type: "delta", Content: text},
		{Type: "tool_use", ToolUse: &tu},
		{Type: "usage", Usage: &llmtypes.Usage{StopReason: "tool_use"}},
		{Type: "done"},
	}
}

// A claim made while calling tools cannot be sent back (it is already on screen
// and the tools have run). It is flagged at the end of the turn: the reply gets
// a footer under deny, and the decision is logged.
func TestWriteClaimGuardFlagsUnbackedNarration(t *testing.T) {
	f := newGuardFixture(t, []characterizationProviderStep{
		{events: narratedToolTurn("I saved it as "+invented+" and I am checking it now. ", toolUse("r1", "kb_read"))},
		{events: doneEvents("Checked: the record exists.")},
	}, &guardTools{})
	f.run(t, "narrated")
	if got := len(f.provider.requestsSnapshot()); got != 2 {
		t.Fatalf("narration must not trigger a retry: %d requests", got)
	}
	text := lastAssistantText(t, f)
	if !strings.Contains(text, "Unverified claim") || !strings.Contains(text, invented) {
		t.Errorf("reply lacks the footer for the narrated claim: %q", text)
	}
	ev := guardEvents(t, f)
	if len(ev) != 1 || ev[0]["action"] != "narration_flagged" {
		t.Errorf("decision log = %+v", ev)
	}
}

// ...but narration about a write the same turn then really performed is fine.
func TestWriteClaimGuardNarrationBackedByTheTurnsOwnWrite(t *testing.T) {
	f := newGuardFixture(t, []characterizationProviderStep{
		{events: narratedToolTurn("Saving the record now, it will be "+returned+". I saved "+returned+". ", toolUse("w1", "kb_write"))},
		{events: doneEvents("Done.")},
	}, &guardTools{})
	f.run(t, "narrated-backed")
	if strings.Contains(lastAssistantText(t, f), "Unverified") {
		t.Errorf("a backed narration claim was flagged: %q", lastAssistantText(t, f))
	}
	for _, e := range guardEvents(t, f) {
		if e["action"] == "narration_flagged" {
			t.Errorf("flagged: %+v", e)
		}
	}
}

// Under warn the narrated claim is reported, not appended to the reply.
func TestWriteClaimGuardNarrationUnderWarn(t *testing.T) {
	f := newGuardFixtureWithPolicy(t, []characterizationProviderStep{
		{events: narratedToolTurn("I saved it as "+invented+". ", toolUse("r1", "kb_read"))},
		{events: doneEvents("Checked.")},
	}, &guardTools{}, "warn")
	if err := f.st.UpdateSessionMetadata(context.Background(), f.session, `{"harness_profile":"dev"}`); err != nil {
		t.Fatal(err)
	}
	events := f.run(t, "narrated-warn")
	if strings.Contains(lastAssistantText(t, f), "Unverified") {
		t.Error("warn mode altered the reply")
	}
	warned := false
	for _, e := range events {
		if e.Type == "status" && strings.Contains(e.Content, "write-claim guard") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no status event: %v", eventTypes(events))
	}
}

func TestWriteClaimNudgePreservesAnswerAndDoesNotDemandWrites(t *testing.T) {
	nudge := writeClaimNudge(writeClaimDecision{Finding: writeclaim.Finding{Ungrounded: []string{"CW-20260919-0011"}}})
	for _, want := range []string{"CW-20260919-0011", "does not establish that no write happened", "Keep the user's requested answer", "Do not repeat a successful write"} {
		if !strings.Contains(nudge, want) {
			t.Errorf("correction missing %q: %s", want, nudge)
		}
	}
}

func TestWriteClaimGuardPreservesReadActivitySummary(t *testing.T) {
	reply := "Observed: tasks under PRJ-20260416-0001 were created or updated yesterday. I saved the summary as " + returned + "."
	f := newGuardFixture(t, []characterizationProviderStep{
		{events: toolTurnEvents(toolUse("w1", "kb_write"))},
		{events: doneEvents(reply)},
	}, &guardTools{})
	events := f.run(t, "activity-summary")
	if got := len(f.provider.requestsSnapshot()); got != 2 {
		t.Fatalf("activity summary triggered a correction: %d requests", got)
	}
	var stored struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(lastAssistantText(t, f)), &stored); err != nil {
		t.Fatal(err)
	}
	if findEvent(events, "replace_content") != nil || stored.Text != reply {
		t.Fatalf("the guard changed the requested summary: %q", stored.Text)
	}
	if ev := guardEvents(t, f); len(ev) != 1 || ev[0]["reason"] != wcWriteSucceeded || ev[0]["fired"] != false {
		t.Fatalf("read activity was rejected: %+v", ev)
	}
}
