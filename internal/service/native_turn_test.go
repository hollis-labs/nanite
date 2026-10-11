package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/chat"
	"github.com/hollis-labs/nanite/internal/lifecycle"
	"github.com/hollis-labs/nanite/internal/store"
	permissionlib "github.com/hollis-labs/substrate/harness/interception/permission"
	llmtypes "github.com/hollis-labs/substrate/llm-core/llmtypes"
)

// NativeToolDefinitions lets the characterization tool service answer the native
// offering path the way toolServiceImpl does.
func (f *characterizationTools) NativeToolDefinitions(names []string) []llmtypes.ToolDefinition {
	var out []llmtypes.ToolDefinition
	for _, d := range f.definitions {
		for _, n := range names {
			if d.Name == n {
				out = append(out, d)
			}
		}
	}
	return out
}

type nativeE2E struct {
	f         *characterizationFixture
	callers   *fakeCallers
	la        *store.LocalAuthority
	host      store.AgentHostSettings
	authority store.NativeAuthority
}

func toolUseThenAnswer(onFirst func()) []characterizationProviderStep {
	return []characterizationProviderStep{
		// A native turn needs the provider's terminal marker after the tool use.
		{events: append(terminationToolEvents("fixture_tool"), llmtypes.StreamEvent{Type: "done"}), beforeReturn: onFirst},
		{events: doneEvents("done")},
	}
}

// newNativeE2E builds a real native view admitted under real host settings, a
// reviewed authority granting fixture_tool, and the decider wired to a fake
// credential store (the apiauth adapter is covered separately).
func newNativeE2E(t *testing.T, steps []characterizationProviderStep) *nativeE2E {
	t.Helper()
	ctx := t.Context()
	f := newCharacterizationFixture(t, steps, "fixture_tool")
	f.svc.activeGen = make(map[string]*inFlightGen)
	f.svc.lifecycle = lifecycle.NewManager("test.native-turn")
	t.Cleanup(func() { _ = f.svc.lifecycle.Shutdown(5 * time.Second) })
	bindTestDefinedConfiguration(t, f)
	verified, err := EmbeddedDefinition()
	if err != nil {
		t.Fatal(err)
	}
	host, err := f.st.CreateAgentHostSettings(ctx, store.AgentHostSettings{Slug: "native-e2e", Title: "Native e2e", DefinitionRef: verified.Ref.MeshRef(),
		Settings: store.NativeHostSettings{Version: "1", Runtime: "api", Provider: "characterization", Model: "characterization-model"},
		Enabled:  true, Source: "explicit-private-test-authoring"})
	if err != nil {
		t.Fatal(err)
	}
	e := &nativeE2E{f: f, host: host, la: f.st.LocalAuthority(), callers: &fakeCallers{standing: map[string]CallerStanding{
		"operator": {Valid: true, Tools: true},
		"embedded": {Valid: true, Tools: false},
	}}}
	e.admit(t, host.Revision)
	e.authority, err = e.la.CreateAuthority(ctx, "cred-admin", host.ID, "reviewed")
	if err != nil {
		t.Fatal(err)
	}
	if err = e.la.GrantTool(ctx, "cred-admin", e.authority.ID, "fixture_tool", "workspace helper"); err != nil {
		t.Fatal(err)
	}
	f.svc.nativeGrants = NewNativeGrants(e.la, e.callers)
	f.svc.capabilities = newCapabilityRegistry()
	f.svc.permissions = permissionlib.NewEngine(permissionlib.ModeDefault, &permissionlib.RuleSet{Rules: []permissionlib.Rule{{Tool: "fixture_tool", Behavior: permissionlib.DecisionAllow}}})
	return e
}

// admit records the host settings revision the view was admitted under.
func (e *nativeE2E) admit(t *testing.T, revision string) {
	t.Helper()
	ref := `{"id":"` + e.host.ID + `","revision":"` + revision + `"}`
	if _, err := e.f.st.DB.ExecContext(t.Context(), `UPDATE cognitive_views SET chat_config_json=json_set(chat_config_json,'$.host_settings_ref',json(?)) WHERE session_view_id=?`, ref, e.f.session); err != nil {
		t.Fatal(err)
	}
}

func (e *nativeE2E) turn(t *testing.T, submittedBy string) {
	t.Helper()
	ctx := chat.WithDeltaMode(t.Context(), chat.DeltaModeLive)
	if submittedBy != "" {
		ctx = WithSubmittedBy(ctx, submittedBy)
	}
	id, err := e.f.svc.SubmitCognitiveTurn(ctx, e.f.session, "use the tool")
	if err != nil {
		t.Fatal(err)
	}
	stream, _ := e.f.svc.streams.GetStream(id)
	drainStream(stream)
}

func (e *nativeE2E) offered() []string {
	var names []string
	if reqs := e.f.provider.requestsSnapshot(); len(reqs) > 0 {
		for _, d := range reqs[0].Tools {
			names = append(names, d.Name)
		}
	}
	return names
}

func (e *nativeE2E) lastToolResult() string {
	reqs := e.f.provider.requestsSnapshot()
	if len(reqs) < 2 {
		return ""
	}
	var out []string
	for _, m := range reqs[1].Messages {
		for _, b := range m.ContentBlocks {
			if b.Type == "tool_result" {
				out = append(out, b.Content)
			}
		}
	}
	return strings.Join(out, "|")
}

// --- negative cases first -----------------------------------------------------

func TestNativeTurnWithoutASubmittingCredentialIsToolFree(t *testing.T) {
	e := newNativeE2E(t, toolUseThenAnswer(nil))
	e.turn(t, "")
	if got := e.offered(); len(got) != 0 {
		t.Fatalf("offered %v with no submitting credential", got)
	}
	if calls := e.f.tools.calls(); len(calls) != 0 {
		t.Fatalf("a tool ran with no submitting credential: %v", calls)
	}
	if !strings.Contains(e.lastToolResult(), "EXECUTION_RULES_DENIED") {
		t.Fatalf("a model-emitted tool call was not refused: %q", e.lastToolResult())
	}
}

func TestNativeTurnOfAnOperateOnlyCallerIsToolFree(t *testing.T) { // R3
	e := newNativeE2E(t, toolUseThenAnswer(nil))
	e.turn(t, "embedded")
	if got := e.offered(); len(got) != 0 {
		t.Fatalf("operate-only caller was offered %v", got)
	}
	if calls := e.f.tools.calls(); len(calls) != 0 {
		t.Fatalf("a tool ran for an operate-only caller: %v", calls)
	}
}

func TestNativeTurnRefusesAfterTheSubmittingCredentialIsRevokedMidTurn(t *testing.T) { // R1
	var e *nativeE2E
	e = newNativeE2E(t, toolUseThenAnswer(func() {
		e.callers.standing["operator"] = CallerStanding{Valid: false, Tools: true}
	}))
	e.turn(t, "operator")
	if got := e.offered(); len(got) != 1 || got[0] != "fixture_tool" {
		t.Fatalf("setup: the tool should have been offered before the revocation: %v", got)
	}
	if calls := e.f.tools.calls(); len(calls) != 0 {
		t.Fatalf("a tool ran after its credential was revoked: %v", calls)
	}
}

func TestNativeTurnRefusesAfterTheGrantIsRevokedMidTurn(t *testing.T) {
	var e *nativeE2E
	e = newNativeE2E(t, toolUseThenAnswer(func() {
		if err := e.la.RevokeTool(context.Background(), "cred-admin", e.authority.ID, "fixture_tool", "no longer needed"); err != nil {
			t.Error(err)
		}
	}))
	e.turn(t, "operator")
	if calls := e.f.tools.calls(); len(calls) != 0 {
		t.Fatalf("a tool ran after its grant was revoked: %v", calls)
	}
}

func TestNativeTurnForAViewAdmittedUnderAnOlderRevisionIsToolFree(t *testing.T) {
	e := newNativeE2E(t, toolUseThenAnswer(nil))
	e.admit(t, "an-older-revision")
	e.turn(t, "operator")
	if got := e.offered(); len(got) != 0 {
		t.Fatalf("a view admitted under another revision was offered %v", got)
	}
	if calls := e.f.tools.calls(); len(calls) != 0 {
		t.Fatalf("a tool ran: %v", calls)
	}
}

func TestNativeTurnWithNoReviewedAuthorityIsToolFree(t *testing.T) {
	e := newNativeE2E(t, toolUseThenAnswer(nil))
	if err := e.la.RevokeAuthority(t.Context(), "cred-admin", e.authority.ID, "retired"); err != nil {
		t.Fatal(err)
	}
	e.turn(t, "operator")
	if got := e.offered(); len(got) != 0 {
		t.Fatalf("a revoked authority still offered %v", got)
	}
	if calls := e.f.tools.calls(); len(calls) != 0 {
		t.Fatalf("a tool ran under a revoked authority: %v", calls)
	}
}

func TestNativeTurnWithoutWiredGrantsStaysToolFree(t *testing.T) {
	e := newNativeE2E(t, toolUseThenAnswer(nil))
	e.f.svc.nativeGrants = nil
	e.turn(t, "operator")
	if got := e.offered(); len(got) != 0 {
		t.Fatalf("unwired grants offered %v", got)
	}
	if calls := e.f.tools.calls(); len(calls) != 0 {
		t.Fatalf("a tool ran with grants unwired: %v", calls)
	}
}

// --- positive path ---------------------------------------------------------------

func TestNativeTurnRunsAGrantedToolForACredentialThatMayRunTools(t *testing.T) {
	e := newNativeE2E(t, toolUseThenAnswer(nil))
	e.turn(t, "operator")
	if got := e.offered(); len(got) != 1 || got[0] != "fixture_tool" {
		t.Fatalf("offered = %v, want the granted tool", got)
	}
	if calls := e.f.tools.calls(); len(calls) != 1 || calls[0] != "fixture_tool" {
		t.Fatalf("calls = %v, want the granted tool to run once", calls)
	}
	if !strings.Contains(e.lastToolResult(), "fixture_tool-result") {
		t.Fatalf("tool result not returned to the model: %q", e.lastToolResult())
	}
	// The capability ended with the run.
	e.f.svc.capabilities.mu.Lock()
	live := len(e.f.svc.capabilities.live)
	e.f.svc.capabilities.mu.Unlock()
	if live != 0 {
		t.Fatalf("%d capabilities still live after the turn", live)
	}
}
