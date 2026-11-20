package chatrunner

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/agent"
)

func TestAuthDecidedValidatesBeforeTakingWait(t *testing.T) {
	r := openRunner(t)
	for _, status := range []string{"", "pending", "granted", "approve", "APPROVED", " Granted ", " DENIED ", " pending ", "unknown", "approved", "denied", "expired", "withdrawn"} {
		t.Run(status, func(t *testing.T) {
			wait := &authWait{RequestID: "req-status", Chats: map[string]string{}}
			r.auth.waits = map[string]*authWait{wait.RequestID: wait}
			r.auth.watching = map[string]bool{wait.RequestID: true}
			normalized := normalizeAuthStatus(status)
			valid := normalized == "approved" || normalized == "denied" || normalized == "expired" || normalized == "withdrawn"
			err := r.AuthDecided(t.Context(), wait.RequestID, status)
			if (err == nil) != valid {
				t.Fatalf("status %q: error %v", status, err)
			}
			if got := r.auth.waits[wait.RequestID]; valid && got != nil || !valid && got != wait {
				t.Fatalf("status %q consumed wrong wait: %+v", status, got)
			}
			if !valid && !r.auth.watching[wait.RequestID] {
				t.Fatal("invalid decision stopped watching")
			}
			if !valid && r.AuthDecided(t.Context(), "unknown-request", status) == nil {
				t.Fatal("unknown request bypassed status validation")
			}
		})
	}
}

func TestAutoModeAuthWaitingGuard(t *testing.T) {
	for _, auto := range []bool{false, true} {
		t.Run(fmt.Sprint(auto), func(t *testing.T) {
			r := openRunner(t)
			r.opts.Agent.AutoApprove = auto
			callbacks := 0
			r.opts.Agent.OnAuthWaiting = func(agent.AuthWaiting) { callbacks++ }
			// Inject at the runner boundary; no Warden source can suppress this call.
			r.onAuthWaiting(agent.AuthWaiting{RequestID: "request", Audience: "fixture"})
			want := 1
			if auto {
				want = 0
			}
			if callbacks != want {
				t.Fatalf("callbacks=%d want=%d", callbacks, want)
			}
		})
	}
}

func TestAutoModeDoesNotRestoreGrantWatches(t *testing.T) {
	for _, auto := range []bool{false, true} {
		t.Run(fmt.Sprint(auto), func(t *testing.T) {
			r := openRunner(t)
			r.opts.Agent.AutoApprove = auto
			saved := map[string]*authWait{"request": {RequestID: "request", Audience: "fixture", Chats: map[string]string{"nanite": "chat"}, Since: time.Now().UTC()}}
			raw, err := json.Marshal(saved)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.agent.SetHostState(t.Context(), authWaitsKey, string(raw)); err != nil {
				t.Fatal(err)
			}
			// An existing watcher avoids launching a polling goroutine in the control.
			r.auth.watching = map[string]bool{"request": true}
			r.loadAuthWaits(t.Context())
			r.auth.mu.Lock()
			got := len(r.auth.waits)
			r.auth.mu.Unlock()
			want := 1
			if auto {
				want = 0
			}
			if got != want {
				t.Fatalf("restored waits=%d want=%d", got, want)
			}
			after, err := r.agent.HostState(t.Context(), authWaitsKey)
			if err != nil || after != string(raw) {
				t.Fatalf("saved waits changed: %q %v", after, err)
			}
		})
	}
}
