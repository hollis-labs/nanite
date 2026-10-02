package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	permissionlib "github.com/hollis-labs/go-permission"
	"github.com/hollis-labs/nanite/internal/service"
)

func respondApprovalForTest(a *testAPI, session, requestID, decision, scope string) int {
	r := httptest.NewRequest(http.MethodPost, "/approvals", strings.NewReader(fmt.Sprintf(`{"decision":%q,"scope":%q}`, decision, scope)))
	r.SetPathValue("id", session)
	r.SetPathValue("requestId", requestID)
	w := httptest.NewRecorder()
	a.handleRespondApproval(w, r)
	return w.Code
}

func TestRespondApproval_SharedEngineSessionBinding(t *testing.T) {
	e := permissionlib.NewEngine(permissionlib.ModeDefault, nil)
	a := &testAPI{API: &API{Services: &service.Container{Permissions: e}}}
	req := e.RequestApproval("owner", "shell", nil, "test")
	for _, session := range []string{"", "other"} {
		if code := respondApprovalForTest(a, session, req.ID, "allow", "session"); code != http.StatusNotFound {
			t.Fatalf("session %q answered another session's request: %d", session, code)
		}
	}
	for _, tc := range []struct{ decision, scope string }{{"ask", "once"}, {"allow", "project"}} {
		if code := respondApprovalForTest(a, "owner", req.ID, tc.decision, tc.scope); code != http.StatusBadRequest {
			t.Fatalf("invalid answer accepted: %+v, %d", tc, code)
		}
	}
	if code := respondApprovalForTest(a, "owner", req.ID, "allow", "session"); code != http.StatusOK {
		t.Fatalf("valid owner answer rejected: %d", code)
	}
	if resp := e.WaitForApproval(t.Context(), req); resp.Decision != permissionlib.DecisionAllow || resp.Scope != permissionlib.ScopeSession {
		t.Fatalf("approval response: %+v", resp)
	}
	for _, tc := range []struct {
		session string
		want    permissionlib.Decision
	}{{"owner", permissionlib.DecisionAllow}, {"other", permissionlib.DecisionAsk}} {
		if got := e.Check(t.Context(), tc.session, "shell", nil, permissionlib.ToolMeta{IsDestructive: true}); got.Decision != tc.want {
			t.Fatalf("session grant leaked: session=%s, %+v", tc.session, got)
		}
	}
}

func TestRespondApproval_ConcurrentDenialsCannotBecomeGrant(t *testing.T) {
	e := permissionlib.NewEngine(permissionlib.ModeDefault, nil)
	a := &testAPI{API: &API{Services: &service.Container{Permissions: e}}}
	req := e.RequestApproval("owner", "shell", nil, "test")
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if code := respondApprovalForTest(a, "owner", req.ID, "deny", "once"); code == http.StatusOK {
				accepted.Add(1)
			} else if code != http.StatusNotFound {
				t.Errorf("concurrent answer status: %d", code)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatalf("request accepted %d answers", accepted.Load())
	}
	// A later answer must not write a grant while the winning denial is still
	// buffered and the request remains in the engine's pending map.
	if code := respondApprovalForTest(a, "owner", req.ID, "allow", "session"); code != http.StatusNotFound {
		t.Fatalf("duplicate answer accepted: %d", code)
	}
	if resp := e.WaitForApproval(t.Context(), req); resp.Decision != permissionlib.DecisionDeny {
		t.Fatalf("denial changed: %+v", resp)
	}
	if got := e.Check(t.Context(), "owner", "shell", nil, permissionlib.ToolMeta{IsDestructive: true}); got.Decision != permissionlib.DecisionAsk {
		t.Fatalf("losing answer recorded a session grant: %+v", got)
	}
}

func TestRespondApproval_CanceledRequestCannotGrant(t *testing.T) {
	e := permissionlib.NewEngine(permissionlib.ModeDefault, nil)
	a := &testAPI{API: &API{Services: &service.Container{Permissions: e}}}
	req := e.RequestApproval("owner", "shell", nil, "test")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if resp := e.WaitForApproval(ctx, req); resp.Decision != permissionlib.DecisionDeny || resp.TimedOut {
		t.Fatalf("canceled request: %+v", resp)
	}
	if code := respondApprovalForTest(a, "owner", req.ID, "allow", "session"); code != http.StatusNotFound {
		t.Fatalf("canceled answer accepted: %d", code)
	}
	if got := e.Check(t.Context(), "owner", "shell", nil, permissionlib.ToolMeta{IsDestructive: true}); got.Decision != permissionlib.DecisionAsk {
		t.Fatalf("canceled request recorded a grant: %+v", got)
	}
}
