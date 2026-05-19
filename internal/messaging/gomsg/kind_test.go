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

func TestFromGoKind_SharedOnlyKindsFoldToNotification(t *testing.T) {
	for _, k := range []messaging.Kind{messaging.MsgKindStatusUpdate, messaging.MsgKindEscalation} {
		if got := gomsg.FromGoKind(k); got != "notification" {
			t.Errorf("FromGoKind(%q) = %q, want notification", k, got)
		}
	}
}
