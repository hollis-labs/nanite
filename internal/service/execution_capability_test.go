package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
)

func rootCap(t *testing.T, reg *capabilityRegistry, view string, narrow ToolNarrowing) *executionCapability {
	t.Helper()
	c, err := reg.issueRoot(view, "turn-"+view, "run-"+view, "view-a", "rev-1", "operator", narrow)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var testLimits = store.CodeModeContextLimits{DefaultLastN: 1, MaxLastN: 8, MaxMessages: 32, MaxBytes: 8192, HistoryMaxMessages: 64, HistoryMaxBytes: 65536}

// --- negative cases first ---------------------------------------------------

func TestACapabilityCannotBeForgedOrKeptAfterItEnds(t *testing.T) {
	reg := newCapabilityRegistry()
	real := rootCap(t, reg, "parent", nil)
	forged := &executionCapability{id: real.id, subjectViewID: "parent", turnID: "t", hostSettingsID: "view-a", admittedRevision: "rev-1", submittedBy: "operator", reg: reg}
	var zero executionCapability
	for name, c := range map[string]*executionCapability{"copy with the same id": forged, "zero value": &zero, "nil": nil} {
		if c.isLive() {
			t.Errorf("%s is live", name)
		}
		if _, ok := executionCapabilityFrom(withExecutionCapability(context.Background(), c)); ok {
			t.Errorf("%s accepted from context", name)
		}
	}
	other := newCapabilityRegistry()
	if (&executionCapability{id: real.id, reg: other}).isLive() {
		t.Error("a capability is live in a registry that never issued it")
	}
	real.end()
	if real.isLive() {
		t.Fatal("ended capability is still live")
	}
	if _, ok := executionCapabilityFrom(withExecutionCapability(context.Background(), real)); ok {
		t.Fatal("ended capability accepted from context")
	}
	if _, err := reg.issueRoot("", "t", "r", "h", "rev", "c", nil); err == nil {
		t.Fatal("issued a capability with no view")
	}
	if _, err := reg.issueRoot("v", "t", "r", "h", "rev", "", nil); err == nil {
		t.Fatal("issued a capability with no submitting credential")
	}
}

func TestChildCapabilityRulesNarrowOnlyNoNestingNeedsLiveParent(t *testing.T) {
	reg := newCapabilityRegistry()
	parent := rootCap(t, reg, "parent", ToolNarrowing{"dev_read": true, "dev_grep": true})
	if _, err := parent.child("fork", "t", "r", nil); !errors.Is(err, ErrCapabilityNarrowed) {
		t.Fatalf("child with no narrowing: %v", err)
	}
	if _, err := parent.child("fork", "t", "r", ToolNarrowing{"dev_read": true, "dev_bash": true}); !errors.Is(err, ErrCapabilityNarrowed) {
		t.Fatalf("child wider than its parent: %v", err)
	}
	if _, err := parent.child("parent", "t", "r", ToolNarrowing{"dev_read": true}); err == nil {
		t.Fatal("child of the same view")
	}
	child, err := parent.child("fork", "t-fork", "r-fork", ToolNarrowing{"dev_read": true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = child.child("grandchild", "t", "r", ToolNarrowing{"dev_read": true}); !errors.Is(err, ErrCapabilityNested) {
		t.Fatalf("grandchild: %v", err)
	}
	parent.end()
	if _, err = parent.child("fork2", "t", "r", ToolNarrowing{"dev_read": true}); !errors.Is(err, ErrCapabilityEnded) {
		t.Fatalf("child of an ended parent: %v", err)
	}
	// An unrestricted parent still cannot hand out an unrestricted child.
	open := rootCap(t, reg, "open-parent", nil)
	if _, err = open.child("open-fork", "t", "r", nil); !errors.Is(err, ErrCapabilityNarrowed) {
		t.Fatalf("unrestricted child: %v", err)
	}
}

func TestVerifierAuthenticatesTheCapabilityNotTheString(t *testing.T) {
	reg := newCapabilityRegistry()
	v := newNativeForkVerifier(testLimits)
	parent := rootCap(t, reg, "parent", nil)
	ctx := withExecutionCapability(context.Background(), parent)
	if _, err := v.VerifyCaller(context.Background(), "parent"); !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("no capability in ctx: %v", err)
	}
	for _, claim := range []string{"", "someone-elses-view", parent.id, parent.turnID} {
		if _, err := v.VerifyCaller(ctx, claim); !errors.Is(err, store.ErrVerifiedActorRequired) {
			t.Errorf("claimed %q was accepted", claim)
		}
	}
	if limits, err := v.VerifyCaller(ctx, "parent"); err != nil || limits != testLimits {
		t.Fatalf("own view: %+v %v", limits, err)
	}
	parent.end()
	if _, err := v.VerifyCaller(ctx, "parent"); !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("ended capability: %v", err)
	}
}

func TestVerifierForkAndHistoryRulesAndDistinctProvenance(t *testing.T) { // R2
	reg := newCapabilityRegistry()
	v := newNativeForkVerifier(testLimits)
	parent := rootCap(t, reg, "parent", nil)
	child, err := parent.child("fork", "t-fork", "r-fork", ToolNarrowing{"dev_read": true})
	if err != nil {
		t.Fatal(err)
	}
	if child.parentCapabilityID != parent.id || child.parentViewID != "parent" || child.parentCapabilityID == child.parentViewID {
		t.Fatalf("provenance not recorded as two distinct identifiers: %+v", child)
	}
	pctx, cctx := withExecutionCapability(context.Background(), parent), withExecutionCapability(context.Background(), child)
	snap := store.CodeModeParentSnapshot{ParentViewID: "parent"}
	hist := func(fork, parentView string) store.CodeModeHistorySnapshot {
		return store.CodeModeHistorySnapshot{ForkViewID: fork, Parent: store.CodeModeParentSnapshot{ParentViewID: parentView}}
	}
	// Fork: the parent's own root capability only; a child may not fork; another view's may not.
	if err = v.VerifyFork(pctx, snap, store.CodeModeForkRequest{}); err != nil {
		t.Fatalf("root fork of its own view: %v", err)
	}
	if err = v.VerifyFork(cctx, snap, store.CodeModeForkRequest{}); err == nil {
		t.Fatal("a child capability forked its parent")
	}
	// The real nested case: the child forks its own view.
	if err = v.VerifyFork(cctx, store.CodeModeParentSnapshot{ParentViewID: "fork"}, store.CodeModeForkRequest{}); !errors.Is(err, store.ErrCodeModeNested) {
		t.Fatalf("a child capability forking its own view: %v", err)
	}
	if err = v.VerifyFork(pctx, store.CodeModeParentSnapshot{ParentViewID: "other"}, store.CodeModeForkRequest{}); err == nil {
		t.Fatal("forked a view the capability is not for")
	}
	if err = v.VerifyFork(context.Background(), snap, store.CodeModeForkRequest{}); err == nil {
		t.Fatal("forked with no capability")
	}
	// History: the child's own capability, for its recorded parent view.
	if err = v.VerifyHistory(cctx, hist("fork", "parent"), store.CodeModeHistoryRequest{}); err != nil {
		t.Fatalf("child reading its parent's history: %v", err)
	}
	for name, h := range map[string]store.CodeModeHistorySnapshot{
		"another fork":               hist("other-fork", "parent"),
		"another parent":             hist("fork", "other-parent"),
		"parent capability identity": hist("fork", parent.id), // never accepted where a view id belongs
	} {
		if err = v.VerifyHistory(cctx, h, store.CodeModeHistoryRequest{}); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if err = v.VerifyHistory(pctx, hist("parent", "parent"), store.CodeModeHistoryRequest{}); err == nil {
		t.Error("the parent's capability read history as if it were a fork")
	}
	if err = v.VerifyHistory(pctx, hist("fork", "parent"), store.CodeModeHistoryRequest{}); err == nil {
		t.Error("the parent's capability read the fork's history")
	}
	// A child's lifetime is its own: it survives the parent's turn ending.
	parent.end()
	if err = v.VerifyHistory(cctx, hist("fork", "parent"), store.CodeModeHistoryRequest{}); err != nil {
		t.Fatalf("child capability died with its parent's turn: %v", err)
	}
	child.end()
	if err = v.VerifyHistory(cctx, hist("fork", "parent"), store.CodeModeHistoryRequest{}); err == nil {
		t.Fatal("ended child still read history")
	}
}

func TestCapabilityDispatchRechecksCredentialGrantsAndNarrowing(t *testing.T) { // R1 for root and child
	ng, grants, callers := nativeGrantsFixture() // view-a: dev_grep, dev_read; operator valid+tools
	reg := newCapabilityRegistry()
	ctx := context.Background()
	parent := rootCap(t, reg, "parent", nil)
	child, err := parent.child("fork", "t", "r", ToolNarrowing{"dev_read": true})
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := ng.AuthorizeCapability(ctx, parent, "dev_grep"); !ok {
		t.Fatal("setup: root should run dev_grep")
	}
	if ok, why := ng.AuthorizeCapability(ctx, child, "dev_grep"); ok || why != nativeDenyNarrowed {
		t.Fatalf("child ran a tool outside its narrowing: %v %q", ok, why)
	}
	if ok, _ := ng.AuthorizeCapability(ctx, child, "dev_read"); !ok {
		t.Fatal("child refused a tool it kept")
	}
	// The parent's turn ending does not end the child, and does not revoke authority.
	parent.end()
	if ok, why := ng.AuthorizeCapability(ctx, parent, "dev_read"); ok || why == "" {
		t.Fatalf("ended parent dispatched: %v %q", ok, why)
	}
	if ok, _ := ng.AuthorizeCapability(ctx, child, "dev_read"); !ok {
		t.Fatal("child refused after its parent turn ended")
	}
	// Revoking the grant, then the credential, refuses the child's next call.
	grants.names["view-a"] = []string{"dev_grep"}
	if ok, _ := ng.AuthorizeCapability(ctx, child, "dev_read"); ok {
		t.Fatal("child honored a revoked grant")
	}
	grants.names["view-a"] = []string{"dev_grep", "dev_read"}
	callers.standing["operator"] = CallerStanding{Valid: false, Tools: true}
	if ok, why := ng.AuthorizeCapability(ctx, child, "dev_read"); ok || why != nativeDenyCaller {
		t.Fatalf("child dispatched for a revoked credential: %v %q", ok, why)
	}
	callers.standing["operator"] = CallerStanding{Valid: true, Tools: true}
	child.end()
	if ok, _ := ng.AuthorizeCapability(ctx, child, "dev_read"); ok {
		t.Fatal("ended child dispatched")
	}
}

// --- through the real store -------------------------------------------------

func nativeForkDefinition(id string) []byte {
	return []byte(fmt.Sprintf("---\nschema_version: \"2\"\ndefinition_id: %s\nrevision: \"1\"\nname: test-definition\ndescription: Storage fixture.\nbehavior:\n  purpose: Complete the requested work.\nrequirements: {}\nharness_profile:\n  context: {}\n  permissions:\n    profile: default\ncontinuity:\n  mode: ephemeral\n---\nPinned parent instructions.\n", id))
}

func TestCapabilityVerifierThroughTheRealForkAndHistoryPaths(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	pin, err := st.InstallAgentDefinition(ctx, nativeForkDefinition("def:native-fork-parent"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := json.Marshal(map[string]string{"definition_id": pin.ID, "revision": pin.Revision, "semantic_digest": pin.Digest})
	parent := &store.Session{Provider: "private-host-model", Model: "parent-model", Metadata: `{}`}
	cfg := `{"instructions":"Pinned parent instructions.","permission_profile":"default","model":{"provider":"private-host-model","model":"parent-model"}}`
	if err = st.CreateDefinedCognitiveSession(ctx, parent, "", store.CognitiveViewRecord{DefinitionRefJSON: string(ref), ChatConfigJSON: cfg}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		for _, m := range []*store.Message{{Role: "user", Content: fmt.Sprintf("needle goal %d", i)}, {Role: "assistant", Content: fmt.Sprintf(`{"v":1,"text":"answer %d"}`, i)}} {
			m.SessionID = parent.ID
			if err = st.CreateMessage(ctx, m); err != nil {
				t.Fatal(err)
			}
		}
	}
	reg := newCapabilityRegistry()
	v := newNativeForkVerifier(testLimits)
	root, err := reg.issueRoot(parent.ID, "turn", "run", "view-a", "rev-1", "operator", nil)
	if err != nil {
		t.Fatal(err)
	}
	sessions := func() int {
		var n int
		_ = st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&n)
		return n
	}
	before := sessions()
	opts := store.CodeModeForkOptions{Verifier: v, Request: store.CodeModeForkRequest{Goal: "bounded task"}}
	// No capability, an ended one and a forged one create nothing.
	if _, err = st.ForkCodeModeSession(ctx, parent.ID, opts); !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("fork with no capability: %v", err)
	}
	forged := withExecutionCapability(ctx, &executionCapability{id: root.id, subjectViewID: parent.ID, reg: reg})
	if _, err = st.ForkCodeModeSession(forged, parent.ID, opts); !errors.Is(err, store.ErrVerifiedActorRequired) {
		t.Fatalf("fork with a forged capability: %v", err)
	}
	if sessions() != before {
		t.Fatal("a refused fork created a session")
	}
	fork, err := st.ForkCodeModeSession(withExecutionCapability(ctx, root), parent.ID, opts)
	if err != nil {
		t.Fatalf("root fork: %v", err)
	}
	child, err := root.child(fork.ID, "turn-fork", "run-fork", ToolNarrowing{"dev_read": true})
	if err != nil {
		t.Fatal(err)
	}
	// The child cannot fork again (nesting) and the parent cannot read the fork's history as itself.
	if _, err = st.ForkCodeModeSession(withExecutionCapability(ctx, child), fork.ID, opts); err == nil {
		t.Fatal("a child capability forked")
	}
	req := store.CodeModeHistoryRequest{Query: "needle", Limit: 4}
	if _, err = st.SearchCodeModeParentHistory(withExecutionCapability(ctx, root), fork.ID, req, v); err == nil {
		t.Fatal("the parent's capability read the fork's history")
	}
	page, err := st.SearchCodeModeParentHistory(withExecutionCapability(ctx, child), fork.ID, req, v)
	if err != nil || len(page.Matches) == 0 {
		t.Fatalf("child history: %+v %v", page, err)
	}
	// Parent turn over: the child still reads; the child over: it no longer does.
	root.end()
	if _, err = st.SearchCodeModeParentHistory(withExecutionCapability(ctx, child), fork.ID, req, v); err != nil {
		t.Fatalf("child history after the parent turn ended: %v", err)
	}
	child.end()
	if _, err = st.SearchCodeModeParentHistory(withExecutionCapability(ctx, child), fork.ID, req, v); err == nil {
		t.Fatal("ended child read history")
	}
}

// A generation ends the capability its launch issued before it closes its
// stream, so a consumer that sees the close never observes the capability live.
// The hook is scoped to the launch's own turn: a capability that merely rides
// the same ctx is left alone, and so is the parent's when a child generation
// (a delegated worker runs under context.WithCancel of its caller's ctx)
// inherits the hook through ctx values.
func TestEndCapabilityBeforeCloseEndsOnlyTheOwningTurnsCapability(t *testing.T) {
	reg := newCapabilityRegistry()
	issued, err := reg.issueRoot("view-1", "turn-1", "run-1", "hs-1", "rev-1", "operator", nil)
	if err != nil {
		t.Fatal(err)
	}
	other, err := reg.issueRoot("view-2", "turn-2", "run-2", "hs-1", "rev-1", "operator", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := withExecutionCapability(t.Context(), other)

	endCapabilityBeforeClose(ctx, "turn-1") // no hook registered: nothing ends
	if !issued.isLive() || !other.isLive() {
		t.Fatal("ending without a registered hook must not end any capability")
	}

	parentCtx := withEndBeforeClose(ctx, "turn-1", issued.end)

	// A child generation derived from the parent's ctx inherits the hook, but
	// runs a different turn: it must not end the parent's capability.
	childCtx, cancel := context.WithCancel(parentCtx)
	defer cancel()
	endCapabilityBeforeClose(childCtx, "child-msg")
	if !issued.isLive() {
		t.Fatal("a child generation that inherited the hook ended the parent's capability")
	}

	endCapabilityBeforeClose(parentCtx, "turn-1")
	if issued.isLive() {
		t.Fatal("the owning turn's capability must be ended before its stream closes")
	}
	if !other.isLive() {
		t.Fatal("a capability that only rides ctx must not be ended by the launch's hook")
	}
	endCapabilityBeforeClose(parentCtx, "turn-1") // idempotent with the launch's deferred end
}
