package broker

// TASKS/agent-host-acp/11-nanite-per-agent-protocol-transport-config.md:
// 2026-08-21 fix-up addendum. internal/runtime/agent's acp_session.go
// (Finding 2 fix) constructs exactly &agentsessions.ExitError{Code: -1}
// for a genuine, unprompted ACP subprocess crash -- ACP's wire protocol
// carries no structured exit-code/signal info, so this is the minimal
// shape that (a) satisfies errors.As(err, &xe) so the broker even sees
// it, and (b) has a real chance of engaging Classify's crash-handling
// path rather than falling into its "exit code 0, not a recovery
// candidate" bottom branch. This test pins that exact shape's real
// classification, and drives it through the whole OnSessionExit pipeline
// (not just Classify in isolation) so a future change to either side of
// this contract fails a test instead of silently regressing back to the
// "ACP crashes are invisible to recovery" bug this fix closes.

import (
	"testing"

	agentsessions "github.com/hollis-labs/agentkit/agentsessions"
)

// acpUnclassifiedExit is the exact *agentsessions.ExitError shape
// acp_session.go's handleProcessExited constructs today.
func acpUnclassifiedExit() *agentsessions.ExitError {
	return &agentsessions.ExitError{Code: -1}
}

func TestClassify_ACPUnclassifiedCrashShape(t *testing.T) {
	got := Classify(&FailureEvent{Exit: acpUnclassifiedExit(), Attempt: 1})
	if got.Class != ClassTransient {
		t.Fatalf("Classify(ACP crash shape, attempt 1) = %+v, want Class=ClassTransient -- "+
			"an ACP crash with Code=-1 must route through the 'unclassified non-zero exit' "+
			"branch, not the Code==0 'not a recovery candidate' bottom branch", got)
	}

	gotRetry := Classify(&FailureEvent{Exit: acpUnclassifiedExit(), Attempt: 2})
	if gotRetry.Class != ClassPermanent {
		t.Fatalf("Classify(ACP crash shape, attempt 2) = %+v, want Class=ClassPermanent (escalation "+
			"after repeat unclassified failures)", gotRetry)
	}
}

// TestOnSessionExit_ACPCrashShapeEngagesBroker drives the exact ExitError
// shape acp_session.go's Finding 2 fix produces through the real
// Broker.OnSessionExit pipeline end to end -- confirming an ACP session's
// crash is no longer silently swallowed (the pre-fix bug: Wait() always
// returned nil, so OnSessionExit was never even called) but instead
// dispatches a real replacement session, exactly like a native-path
// crash would.
func TestOnSessionExit_ACPCrashShapeEngagesBroker(t *testing.T) {
	boot := &fakeAgentBoot{}
	store := &fakeStore{}
	envSink := newFakeEnvelope()
	b := NewBroker(Dependencies{
		AgentBoot: boot,
		Store:     store,
		Envelope:  envSink,
	})

	b.OnSessionExit("acp-sess-1", acpUnclassifiedExit(), map[string]any{
		MetaKeyAgentProfile: "opencode-acp-agent",
		MetaKeyProvider:     "opencode",
		MetaKeyMode:         "long_lived",
		MetaKeyWorkdir:      "/proj",
	})

	if len(boot.gotOpts) != 1 {
		t.Fatalf("AgentBoot.Boot calls: got %d, want 1 -- the broker must dispatch a replacement "+
			"session for a genuine ACP crash", len(boot.gotOpts))
	}
	if boot.gotOpts[0].SessionID != "acp-sess-1" {
		t.Errorf("dispatch SessionID = %q, want acp-sess-1", boot.gotOpts[0].SessionID)
	}

	if len(store.breadcrumbs) != 1 {
		t.Fatalf("breadcrumbs: got %d, want 1", len(store.breadcrumbs))
	}
	if store.breadcrumbs[0].Class != ClassTransient {
		t.Errorf("breadcrumb Class = %v, want ClassTransient", store.breadcrumbs[0].Class)
	}
}
