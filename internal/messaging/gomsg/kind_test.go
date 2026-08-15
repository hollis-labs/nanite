package gomsg_test

import (
	"testing"

	messaging "github.com/hollis-labs/go-messaging"
	"github.com/hollis-labs/nanite/internal/messaging/gomsg"
)

func TestKindMapping_Roundtrip(t *testing.T) {
	cases := []struct {
		legacy string
		shared messaging.Kind
	}{
		{"request", messaging.MsgKindRequest},
		{"reply", messaging.MsgKindResponse},
		{"notification", messaging.MsgKindNotice},
		{"handoff", messaging.MsgKindHandoff},
		{"subagent_result", messaging.MsgKindStatusUpdate},
	}
	for _, c := range cases {
		if got := gomsg.ToGoKind(c.legacy); got != c.shared {
			t.Errorf("ToGoKind(%q) = %q, want %q", c.legacy, got, c.shared)
		}
		if got := gomsg.FromGoKind(c.shared); got != c.legacy {
			t.Errorf("FromGoKind(%q) = %q, want %q", c.shared, got, c.legacy)
		}
	}
}

func TestToGoKind_UnknownDefaultsToNotice(t *testing.T) {
	if got := gomsg.ToGoKind("bogus"); got != messaging.MsgKindNotice {
		t.Errorf("ToGoKind(bogus) = %q, want notice", got)
	}
}

// TestFromGoKind_EscalationFoldsToNotification covers the one shared kind
// that still has no legacy equivalent — status_update round-trips onto
// subagent_result now (CW-20260512-0019) and is covered by
// TestKindMapping_Roundtrip instead.
func TestFromGoKind_EscalationFoldsToNotification(t *testing.T) {
	if got := gomsg.FromGoKind(messaging.MsgKindEscalation); got != "notification" {
		t.Errorf("FromGoKind(escalation) = %q, want notification", got)
	}
}
