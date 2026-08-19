package subagent

import (
	"context"
	"errors"
	"testing"

	"github.com/hollis-labs/nanite/internal/dispatch"
	"github.com/hollis-labs/nanite/internal/store"
)

// TestSpawn_RealStoreTrustResolver_EndToEnd is the "Done means" manual
// verification for Phase 0 item 20
// (TASKS/phase-0/20-retire-workspaces-and-instance-mechanism.md), promoted
// to a real, permanent regression test rather than a one-off manual check:
// it wires a genuine *store.Store (the same production wiring
// container.go:1169's `subagentSvc.SetTrustResolver(cfg.Store)` uses — not
// a stub) into subagent.Service and confirms the simplified, unconditional
// base-tier ResolveTrust(ctx, agentProfileID) still gates real dispatches
// correctly for all three tiers:
//
//   - untrusted (agent_profiles.default_trust_tier='untrusted', the real
//     default for kind='external'/'cli' per migration 035) refuses
//     outright with ErrUntrustedRole.
//   - trusted (explicit override on the row) bypasses the approval gate.
//   - normal (the real default for kind='internal') falls through to the
//     existing approval-gate flow.
//
// No prior test in this package exercised the real store as the trust
// resolver — internal/subagent/trust_test.go and
// elicitation_orthogonality_test.go both use stubTrustResolver. This
// closes that gap, and is the concrete evidence that
// internal/store/trust.go's ResolveTrust (simplified from a (workspace,
// agent) pair to just agentProfileID after workspace_role_trust's full
// removal, operator-confirmed 2026-08-18) still drives subagent-dispatch
// trust gating correctly end-to-end.
func TestSpawn_RealStoreTrustResolver_EndToEnd(t *testing.T) {
	db, st := newTestDB(t)

	// kind="external" only got 'untrusted' via migration 035's one-time
	// repair UPDATE against rows that existed at migration time — new
	// rows created after migration still get the column's plain SQL
	// DEFAULT 'normal' regardless of kind (confirmed against
	// internal/store/trust_test.go's own TestResolveTrust_ExternalDefaultUntrusted,
	// which patches the column directly for exactly this reason), so this
	// needs the same manual patch.
	untrustedProfile := &store.AgentProfile{
		Name: "Untrusted Plugin", Slug: "untrusted-plugin", SystemPrompt: "x", Kind: "external",
	}
	if err := st.CreateAgent(untrustedProfile); err != nil {
		t.Fatalf("create untrusted profile: %v", err)
	}
	if _, err := st.DB.Exec(`UPDATE agent_profiles SET default_trust_tier = 'untrusted' WHERE id = ?`, untrustedProfile.ID); err != nil {
		t.Fatalf("patch untrusted tier: %v", err)
	}

	// kind="internal" seeds default_trust_tier='normal'.
	normalProfile := &store.AgentProfile{
		Name: "Normal Worker", Slug: "normal-worker", SystemPrompt: "x", Kind: "internal",
	}
	if err := st.CreateAgent(normalProfile); err != nil {
		t.Fatalf("create normal profile: %v", err)
	}

	// An explicit 'trusted' override — no per-row API for this (trust
	// tier is set at creation time via kind-based defaults only), so this
	// mirrors internal/store/trust_test.go's own pattern of patching the
	// column directly for a tier the create path doesn't produce.
	trustedProfile := &store.AgentProfile{
		Name: "Trusted Internal Role", Slug: "trusted-internal-role", SystemPrompt: "x", Kind: "internal",
	}
	if err := st.CreateAgent(trustedProfile); err != nil {
		t.Fatalf("create trusted profile: %v", err)
	}
	if _, err := st.DB.Exec(`UPDATE agent_profiles SET default_trust_tier = 'trusted' WHERE id = ?`, trustedProfile.ID); err != nil {
		t.Fatalf("patch trusted tier: %v", err)
	}

	t.Run("untrusted role refused outright", func(t *testing.T) {
		svc := NewService(db, &notCalledRunner{t: t}, nil, nil, stubSettings{store.UserSettings{SubagentApprovalRequired: false}})
		svc.SetTrustResolver(st) // real store, not a stub

		_, err := svc.Spawn(context.Background(), SpawnRequest{
			ParentSessionID: "sess-real-untrusted",
			Role:            "plugin-agent",
			Prompt:          "do something",
			Mode:            ModeSync,
			AgentProfileID:  untrustedProfile.ID,
		})
		if !errors.Is(err, dispatch.ErrUntrustedRole) {
			t.Fatalf("expected ErrUntrustedRole from real store.ResolveTrust, got: %v", err)
		}
	})

	t.Run("trusted role bypasses approval", func(t *testing.T) {
		poster := &stubPoster{}
		logger := &stubEventLogger{}
		emitter := &stubEmitter{}
		svc := NewService(db, EchoRunner{}, poster, emitter, stubSettings{
			store.UserSettings{SubagentApprovalRequired: true},
		})
		svc.SetTrustResolver(st)
		svc.SetEventLogger(logger)

		id, err := svc.Spawn(context.Background(), SpawnRequest{
			ParentSessionID: "sess-real-trusted",
			Role:            "worker",
			Prompt:          "do work",
			Mode:            ModeSync,
			AgentProfileID:  trustedProfile.ID,
		})
		if err != nil {
			t.Fatalf("Spawn: %v", err)
		}
		run, err := svc.Status(context.Background(), id)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if run.Status != StatusCompleted {
			t.Errorf("expected StatusCompleted (trusted bypass via real store), got %q", run.Status)
		}
		if emitter.Count() != 0 {
			t.Errorf("expected 0 approval emissions for trusted role, got %d", emitter.Count())
		}
	})

	t.Run("normal role still requires approval", func(t *testing.T) {
		emitter := &stubEmitter{}
		svc := NewService(db, &notCalledRunner{t: t}, nil, emitter, stubSettings{
			store.UserSettings{SubagentApprovalRequired: true},
		})
		svc.SetTrustResolver(st)

		id, err := svc.Spawn(context.Background(), SpawnRequest{
			ParentSessionID: "sess-real-normal",
			Role:            "chat",
			Prompt:          "hello",
			Mode:            ModeSync,
			AgentProfileID:  normalProfile.ID,
		})
		if err != nil {
			t.Fatalf("Spawn: %v", err)
		}
		run, err := svc.Status(context.Background(), id)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if run.Status != StatusRequested {
			t.Errorf("expected StatusRequested (normal + approval required, via real store), got %q", run.Status)
		}
		if emitter.Count() != 1 {
			t.Errorf("expected 1 approval emission, got %d", emitter.Count())
		}
	})

	t.Run("unknown agent profile falls back to normal (base-tier default)", func(t *testing.T) {
		// This is the "confirm what the unconditional base-tier fallback
		// actually is" check: an agent_profile_id with no row (e.g. a
		// stale/bad ID) must NOT silently become untrusted-refused or
		// trusted-bypassed — internal/store/trust.go's ResolveTrust
		// returns TrustNormal on a miss, so this must gate exactly like
		// the "normal role" case above.
		emitter := &stubEmitter{}
		svc := NewService(db, &notCalledRunner{t: t}, nil, emitter, stubSettings{
			store.UserSettings{SubagentApprovalRequired: true},
		})
		svc.SetTrustResolver(st)

		id, err := svc.Spawn(context.Background(), SpawnRequest{
			ParentSessionID: "sess-real-unknown",
			Role:            "chat",
			Prompt:          "hello",
			Mode:            ModeSync,
			AgentProfileID:  "does-not-exist",
		})
		if err != nil {
			t.Fatalf("Spawn: %v", err)
		}
		run, err := svc.Status(context.Background(), id)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if run.Status != StatusRequested {
			t.Errorf("expected StatusRequested (unknown profile -> TrustNormal fallback), got %q", run.Status)
		}
	})
}
