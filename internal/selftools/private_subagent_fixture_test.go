package selftools

import (
	"context"

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
