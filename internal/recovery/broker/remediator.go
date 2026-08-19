package broker

import (
	"context"
	"fmt"
)

// Remediate runs the remediation action carried on the Classification.
// Bounded by the broker's per-remediation timeout (default 10s).
// Idempotent — every BootDirOps / MCPControl / CredentialOps method is
// expected to be safe to call repeatedly.
//
// Phase 1: signature + dispatch. Phase 3 fleshes out the per-action
// behavior (right now all branches forward straight to the dependency).
func (b *Broker) Remediate(ctx context.Context, ev *FailureEvent, c Classification) error {
	if c.Remediation == RemediationNone {
		return nil
	}
	if ev == nil {
		return fmt.Errorf("broker.Remediate: nil failure event")
	}

	rctx, cancel := context.WithTimeout(ctx, b.remediationTimeout)
	defer cancel()

	switch c.Remediation {
	case RemediationRepopulateSandbox:
		if b.deps.BootDir == nil {
			return fmt.Errorf("broker.Remediate: BootDir not wired")
		}
		return b.deps.BootDir.Repopulate(rctx, ev.SessionID)

	case RemediationRefreshMCPTransport:
		if b.deps.MCP == nil {
			return fmt.Errorf("broker.Remediate: MCP not wired")
		}
		return b.deps.MCP.RestartTransport(rctx, ev.SessionID)

	case RemediationRefreshCredentials:
		if b.deps.Credentials == nil {
			return fmt.Errorf("broker.Remediate: Credentials not wired")
		}
		return b.deps.Credentials.Refresh(rctx, ev.AgentProfile)

	case RemediationRegenerateCLAUDEMD:
		if b.deps.BootDir == nil {
			return fmt.Errorf("broker.Remediate: BootDir not wired")
		}
		return b.deps.BootDir.RegenerateCLAUDEMD(rctx, ev.SessionID)
	}

	return fmt.Errorf("broker.Remediate: unknown remediation %v", c.Remediation)
}
