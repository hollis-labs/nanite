package service

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeGrants struct {
	names map[string][]string
	err   error
	calls int
}

func (f *fakeGrants) ResolveToolNames(_ context.Context, host string) ([]string, error) {
	f.calls++
	return append([]string(nil), f.names[host]...), f.err
}

type fakeCallers struct {
	standing map[string]CallerStanding
	err      error
}

func (f *fakeCallers) CallerStanding(_ context.Context, id string) (CallerStanding, error) {
	return f.standing[id], f.err // an unknown credential is the zero value: not valid
}

func nativeGrantsFixture() (*NativeGrants, *fakeGrants, *fakeCallers) {
	g := &fakeGrants{names: map[string][]string{"view-a": {"dev_grep", "dev_read"}, "view-b": {"think"}}}
	c := &fakeCallers{standing: map[string]CallerStanding{
		"operator": {Valid: true, Tools: true},
		"embedded": {Valid: true, Tools: false}, // operate only
	}}
	return &NativeGrants{Grants: g, Callers: c}, g, c
}

var subjA = NativeSubject{HostSettingsID: "view-a", SubmittedBy: "operator"}

func TestNativeGrantsRefuseWhatTheFixtureDoesNotGrant(t *testing.T) {
	ng, _, _ := nativeGrantsFixture()
	ctx := context.Background()
	if ok, why := ng.AuthorizeDispatch(ctx, subjA, nil, "dev_bash"); ok || why != nativeDenyGrant {
		t.Fatalf("ungranted tool: %v %q", ok, why)
	}
	// A grant for view A is not honored for view B.
	if ok, _ := ng.AuthorizeDispatch(ctx, NativeSubject{HostSettingsID: "view-b", SubmittedBy: "operator"}, nil, "dev_read"); ok {
		t.Fatal("view A's grant honored for view B")
	}
	// An unknown view has nothing.
	if ok, _ := ng.AuthorizeDispatch(ctx, NativeSubject{HostSettingsID: "nobody", SubmittedBy: "operator"}, nil, "dev_read"); ok {
		t.Fatal("unknown view authorized")
	}
}

func TestNativeGrantsOperateOnlyCallerGetsToolFreeTurns(t *testing.T) { // R3
	ng, _, _ := nativeGrantsFixture()
	ctx := context.Background()
	subj := NativeSubject{HostSettingsID: "view-a", SubmittedBy: "embedded"}
	if got := ng.Offered(ctx, subj, nil); len(got) != 0 {
		t.Fatalf("operate-only caller was offered tools: %v", got)
	}
	if ok, why := ng.AuthorizeDispatch(ctx, subj, nil, "dev_read"); ok || why != nativeDenyScope {
		t.Fatalf("operate-only dispatch: %v %q", ok, why)
	}
}

func TestNativeGrantsRevokedOrUnknownCredentialRefusedAtEveryDispatch(t *testing.T) { // R1
	ng, _, callers := nativeGrantsFixture()
	ctx := context.Background()
	if ok, _ := ng.AuthorizeDispatch(ctx, subjA, nil, "dev_read"); !ok {
		t.Fatal("setup: operator should be allowed")
	}
	// Revoked between two calls of one turn: the second is refused.
	callers.standing["operator"] = CallerStanding{Valid: false, Tools: true}
	if ok, why := ng.AuthorizeDispatch(ctx, subjA, nil, "dev_read"); ok || why != nativeDenyCaller {
		t.Fatalf("revoked credential: %v %q", ok, why)
	}
	if got := ng.Offered(ctx, subjA, nil); len(got) != 0 {
		t.Fatalf("revoked credential still offered tools: %v", got)
	}
	// A credential id the host does not know is not valid.
	if ok, _ := ng.AuthorizeDispatch(ctx, NativeSubject{HostSettingsID: "view-a", SubmittedBy: "made-up"}, nil, "dev_read"); ok {
		t.Fatal("unknown credential authorized")
	}
}

func TestNativeGrantsRevokedGrantTakesEffectOnTheNextCall(t *testing.T) {
	ng, grants, _ := nativeGrantsFixture()
	ctx := context.Background()
	if ok, _ := ng.AuthorizeDispatch(ctx, subjA, nil, "dev_read"); !ok {
		t.Fatal("setup")
	}
	grants.names["view-a"] = []string{"dev_grep"} // operator revoked dev_read mid-turn
	if ok, _ := ng.AuthorizeDispatch(ctx, subjA, nil, "dev_read"); ok {
		t.Fatal("a revoked grant was still honored (grants must not be cached for the turn)")
	}
	if ok, _ := ng.AuthorizeDispatch(ctx, subjA, nil, "dev_grep"); !ok {
		t.Fatal("remaining grant lost")
	}
}

func TestNativeGrantsFailClosed(t *testing.T) {
	ng, grants, callers := nativeGrantsFixture()
	ctx := context.Background()
	callers.err = errors.New("auth db unavailable")
	if ok, why := ng.AuthorizeDispatch(ctx, subjA, nil, "dev_read"); ok || why != nativeDenyUnready {
		t.Fatalf("caller lookup failure: %v %q", ok, why)
	}
	if got := ng.Offered(ctx, subjA, nil); len(got) != 0 {
		t.Fatalf("offered tools while the caller lookup failed: %v", got)
	}
	callers.err = nil
	grants.err = errors.New("store unavailable")
	if ok, _ := ng.AuthorizeDispatch(ctx, subjA, nil, "dev_read"); ok {
		t.Fatal("authorized while the grant lookup failed")
	}
	grants.err = nil
	for name, bad := range map[string]*NativeGrants{"nil": nil, "no grants": {Callers: callers}, "no callers": {Grants: grants}} {
		if ok, _ := bad.AuthorizeDispatch(ctx, subjA, nil, "dev_read"); ok {
			t.Errorf("%s: authorized by an unwired resolver", name)
		}
	}
	for name, subj := range map[string]NativeSubject{"no host": {SubmittedBy: "operator"}, "no caller": {HostSettingsID: "view-a"}} {
		if ok, _ := ng.AuthorizeDispatch(ctx, subj, nil, "dev_read"); ok {
			t.Errorf("%s: authorized", name)
		}
	}
}

func TestNativeGrantsNarrowingOnlyNarrows(t *testing.T) {
	ng, _, _ := nativeGrantsFixture()
	ctx := context.Background()
	narrow := ToolNarrowing{"dev_read": true, "dev_bash": true} // dev_bash was never granted
	if ok, _ := ng.AuthorizeDispatch(ctx, subjA, narrow, "dev_read"); !ok {
		t.Fatal("granted and narrowed-in tool refused")
	}
	if ok, why := ng.AuthorizeDispatch(ctx, subjA, narrow, "dev_grep"); ok || why != nativeDenyNarrowed {
		t.Fatalf("narrowed-out tool: %v %q", ok, why)
	}
	if ok, why := ng.AuthorizeDispatch(ctx, subjA, narrow, "dev_bash"); ok || why != nativeDenyGrant {
		t.Fatalf("narrowing must not grant: %v %q", ok, why)
	}
	if got := ng.Offered(ctx, subjA, ToolNarrowing{}); len(got) != 0 {
		t.Fatalf("empty narrowing offered %v", got)
	}
	if got := strings.Join(ng.Offered(ctx, subjA, nil), ","); got != "dev_grep,dev_read" {
		t.Fatalf("nil narrowing = %q", got)
	}
}
