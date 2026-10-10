package selftools

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/nanite/internal/store"
	subagenthost "github.com/hollis-labs/nanite/internal/subagent"

	"github.com/hollis-labs/substrate/agent/subagent"
)

// privateSpawnDecision tests the standalone lifecycle and envelope protocol.
// It is not Nanite's host authorizer or an enrollment port.
type privateSpawnDecision struct{}

func (privateSpawnDecision) AuthorizeSpawn(context.Context, string) (subagent.SpawnAuthorization, error) {
	return subagent.SpawnAuthorization{}, nil
}
func newPrivateSubagentService(db subagent.Database, runner subagent.Runner, approver subagent.ApprovalEmitter, settings subagent.SettingsReader) *subagent.Service {
	svc := subagent.NewService(db, runner, nil, approver, settings)
	svc.SetSpawnAuthorizer(privateSpawnDecision{})
	return svc
}

// The host adapter remains a refusal even when an envelope supplies a plausible
// parent and role. Standalone protocol fixtures above cannot become an issuer.
func TestSelfToolSpawnHostRefusesBeforeApprovalAndChildEffects(t *testing.T) {
	st := newTestStore(t)
	runner := &privateCountingRunner{}
	emitter := &gatedApprovalEmitter{}
	settings := gatedSettingsReader{us: store.UserSettings{SubagentApprovalRequired: true}}
	svc := subagenthost.NewService(st.DB, runner, nil, emitter, settings)
	transport := &SelfToolsTransport{Reads: testReadServices(st), Writes: testWriteServices(st), Subagent: svc}
	result, err := transport.callSpawnSubagent(t.Context(), map[string]any{"parent_session_id": "plausible-session", "parent_agent_id": "plausible-host", "role": "worker", "prompt": "execute", "mode": subagent.ModeSync})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatalf("missing host authority accepted: %+v", result)
	}
	envelope := parseEnvelopeFromResult(t, result)
	if envelope.Success || envelope.Error == nil || !strings.Contains(envelope.Error.Message, store.ErrVerifiedActorRequired.Error()) {
		t.Fatalf("host refusal lost: %+v", envelope)
	}
	var rows int
	if err := st.DB.QueryRowContext(t.Context(), `SELECT count(*) FROM subagent_runs`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 || runner.calls != 0 || emitter.count != 0 {
		t.Fatalf("refused host effects: runs=%d runner=%d approvals=%d", rows, runner.calls, emitter.count)
	}
}

type privateCountingRunner struct{ calls int }

func (r *privateCountingRunner) Run(context.Context, *subagent.Run) (*subagent.Result, error) {
	r.calls++
	return &subagent.Result{Summary: "unexpected child"}, nil
}
