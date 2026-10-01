package agent

import (
	"errors"
	"fmt"
	"testing"

	"github.com/hollis-labs/agentkit/agentsessions"
)

// A resume turn the provider no longer has fails with agentkit's
// SessionLostError. Nanite must forget the persisted id so the next cold
// boot does not resume the dead session again (CW-20260930-0113).
func TestSession_ForgetLostProviderSession(t *testing.T) {
	lost := &agentsessions.SessionLostError{RequestedID: "ses_dead", Err: errors.New("exit status 1")}
	for _, tc := range []struct {
		name    string
		err     error
		wantIDs string
	}{
		{"session lost", lost, ""},
		{"session lost, wrapped by a caller", fmt.Errorf("send input: %w", lost), ""},
		{"other turn failure keeps the id", errors.New("exit status 1"), "ses_dead"},
		{"success keeps the id", nil, "ses_dead"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeRuntimeStore()
			store.provIDs["rt-1"] = "ses_dead"
			s := &Session{ID: "rt-1", deps: &Dependencies{Store: store}}

			s.forgetLostProviderSession(tc.err)

			if got := store.provIDs["rt-1"]; got != tc.wantIDs {
				t.Fatalf("provider session id = %q, want %q", got, tc.wantIDs)
			}
		})
	}
}
