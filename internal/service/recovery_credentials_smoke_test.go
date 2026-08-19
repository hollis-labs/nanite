package service

import (
	"context"
	"strings"
	"testing"

	"github.com/hollis-labs/go-providers/provider"
	nllmanthropic "github.com/hollis-labs/nanite/internal/llm/anthropic"
	"github.com/hollis-labs/nanite/internal/recovery/broker"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestSmoke_BrokerRemediateThroughCredentialsAdapter exercises the full
// path from broker.Remediate(RemediationRefreshCredentials) → adapter
// Refresh → SetAPIKey on a registered *anthropic.Client. Closes the
// boot prompt's "force RemediationRefreshCredentials against a session
// — verify the broker's Remediate succeeds (no longer returns
// 'Credentials not wired')" smoke step.
func TestSmoke_BrokerRemediateThroughCredentialsAdapter(t *testing.T) {
	// Build the wired adapter the way BuildAgentDependencies does.
	profiles := map[string]*store.AgentProfile{
		"agent-x": {
			ID:              "agent-x",
			Name:            "agent-x",
			Slug:            "agent-x",
			DefaultProvider: "anthropic",
		},
	}
	reg := provider.NewRegistry()
	reg.Register("anthropic", nllmanthropic.New())

	adapter := newAdapterForTest(profiles, reg, map[string]string{
		"provider-api-key:anthropic-001": "sk-ant-smoke",
	})

	// Build a broker with ONLY Credentials wired — mirrors the
	// "BootDir/MCP still nil" Phase 9 wiring intermediate state to
	// confirm the credentials path lights up independently.
	b := broker.NewBroker(broker.Dependencies{
		Credentials: adapter,
	})

	ev := &broker.FailureEvent{
		SessionID:    "smoke-session",
		AgentProfile: "agent-x",
	}
	c := broker.Classification{
		Class:       broker.ClassConfigPermissions,
		Reason:      "smoke: stderr indicates auth failure",
		Remediation: broker.RemediationRefreshCredentials,
	}

	if err := b.Remediate(context.Background(), ev, c); err != nil {
		t.Fatalf("Remediate(RefreshCredentials): expected success, got: %v", err)
	}
}

// TestSmoke_BrokerRemediateCLIProviderEscalates verifies the CLI-managed
// branch surfaces an error all the way up through Remediate so the
// broker's escalatePermanent path fires correctly. The orchestrator is
// not exercised here; we only confirm Remediate returns a non-nil error.
func TestSmoke_BrokerRemediateCLIProviderEscalates(t *testing.T) {
	profiles := map[string]*store.AgentProfile{
		"agent-cli": {
			ID:              "agent-cli",
			Name:            "agent-cli",
			Slug:            "agent-cli",
			DefaultProvider: "claude",
		},
	}
	adapter := newAdapterForTest(profiles, provider.NewRegistry(), nil)

	b := broker.NewBroker(broker.Dependencies{
		Credentials: adapter,
	})

	ev := &broker.FailureEvent{SessionID: "cli-session", AgentProfile: "agent-cli"}
	c := broker.Classification{
		Class:       broker.ClassConfigPermissions,
		Remediation: broker.RemediationRefreshCredentials,
	}

	err := b.Remediate(context.Background(), ev, c)
	if err == nil {
		t.Fatal("Remediate(RefreshCredentials) on CLI provider: expected non-nil error to drive escalation")
	}
	if !strings.Contains(err.Error(), "manages its own auth") {
		t.Errorf("Remediate error should be the CLI-managed-auth surface, got: %v", err)
	}
}
