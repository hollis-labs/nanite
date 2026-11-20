package chatrunner

import (
	"testing"

	"github.com/hollis-labs/nanite/agent"
)

func TestAutoApproveActivity(t *testing.T) {
	x := &run{}
	got := x.translate(agent.AutoApproved{Conversation: "chat", CallID: "call", Tool: "deploy", InputSummary: "[redacted input]"})
	if len(got) != 1 || got[0].Kind != "auto-approved" {
		t.Fatalf("activity %+v", got)
	}
}
