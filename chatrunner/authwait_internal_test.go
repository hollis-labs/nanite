package chatrunner

import "testing"

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
