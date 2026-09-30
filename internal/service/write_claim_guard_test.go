package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	hooks "github.com/hollis-labs/go-hooks"
	llmtypes "github.com/hollis-labs/go-llm-types"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/harnessprofile"
	"github.com/hollis-labs/nanite/internal/permission"
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
	stop := func(msg string) hooks.StopInput { return hooks.StopInput{LastAssistantMessage: msg} }
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
		if !d.Fired && out.Decision != hooks.DecisionAllow {
			t.Errorf("%s: an unfired guard returned %q", name, out.Decision)
		}
	}

	// What each mode does when it fires.
	deny, d := writeClaimHook(harnessprofile.GuardDeny, stop(claim(invented)), none)
	if deny.Decision != hooks.DecisionDeny || deny.Continue == nil || *deny.Continue || deny.StopReason == "" || !strings.Contains(deny.Reason, invented) || len(d.Finding.Ungrounded) != 1 {
		t.Errorf("deny output = %+v", deny)
	}
	warn, _ := writeClaimHook(harnessprofile.GuardWarn, stop(claim(invented)), none)
	if warn.Decision != hooks.DecisionAllow || warn.SystemMessage == "" || warn.Continue != nil {
		t.Errorf("warn output = %+v", warn)
	}
	ask, _ := writeClaimHook(harnessprofile.GuardAsk, stop(claim(invented)), none)
	if ask.Decision != hooks.DecisionAsk || ask.Continue != nil {
		t.Errorf("ask output = %+v", ask)
	}
	for _, o := range []hooks.Output{deny, warn, ask} {
		if err := o.Validate(); err != nil {
			t.Errorf("output does not validate: %v", err)
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
	svc := &chatServiceImpl{tools: &metaStub{readOnly: map[string]bool{"kb_read": true}}}
	ctx := context.Background()
	for name, want := range map[string]bool{
		"kb_write": true, "unknown_tool": true, "kb_read": false,
		"request_tools": false, "tool_describe": false, "whoami": false, "fetch_tool_result": false,
		"search_tool_result": false, "scratchpad_write": false,
	} {
		if got := svc.isWriteCapable(ctx, name); got != want {
			t.Errorf("isWriteCapable(%q) = %v, want %v", name, got, want)
		}
	}
	// With no tool service every unclassified tool counts as a write.
	if !(&chatServiceImpl{}).isWriteCapable(ctx, "kb_read") {
		t.Error("an unclassifiable tool must count as write-capable")
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
	case "kb_read":
		return &ToolResult{Output: `{"id":"` + returned + `","body":"existing entry"}`}, nil
	}
	return &ToolResult{Output: name + " ok"}, nil
}

func newGuardFixture(t *testing.T, steps []characterizationProviderStep, tools *guardTools) *characterizationFixture {
	t.Helper()
	f := newCharacterizationFixture(t, steps, "kb_write", "kb_read", "whoami")
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
func TestWriteClaimGuardDevProfileWarns(t *testing.T) {
	f := newGuardFixture(t, []characterizationProviderStep{{events: doneEvents(claim(invented))}}, &guardTools{})
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

// ...and the profile can be tightened or loosened either way.
func TestWriteClaimGuardModeIsOverridable(t *testing.T) {
	for _, tc := range []struct {
		meta     string
		requests int
	}{
		{`{"harness_profile":"dev","harness_overrides":{"hooks":{"write_claim_guard":"deny"}}}`, 2},
		{`{"harness_overrides":{"hooks":{"write_claim_guard":"off"}}}`, 1},
		{`{"harness_overrides":{"hooks":{"write_claim_guard":"ask"}}}`, 1},
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
	f.svc.permissions = permission.NewEngine(permission.ModePlan, nil)
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
