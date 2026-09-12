package store

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// naniteActorURN is the shape a2a.GenerateAgentURN produces. Written out here
// rather than imported so this test fails if the generator's authority changes
// silently — the whole point of CW-20260912-0017 was that a minter drifted into
// another product's authority and nothing noticed for the life of the feature.
var naniteActorURN = regexp.MustCompile(`^msg://agent/nanite/agt_[a-z2-7]{10}$`)

// TestCreateDurableAgentInstance_MintsNaniteActorURN covers the identity half
// of migration 159: the actor is the INSTANCE, and it is addressed under
// Nanite's own authority.
func TestCreateDurableAgentInstance_MintsNaniteActorURN(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	agent := makeTestAgent(t, s, "actor-urn")

	inst := &DurableAgentInstance{Name: "Actor A", Slug: "actor-a", ProfileID: agent.ID}
	if err := s.CreateDurableAgentInstance(ctx, inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}

	if !naniteActorURN.MatchString(inst.URN) {
		t.Errorf("URN = %q, want %s", inst.URN, naniteActorURN.String())
	}
	if strings.Contains(inst.URN, "agent-mux") {
		t.Errorf("URN = %q minted into Tether's authority; that is the defect "+
			"CW-20260912-0017 retired", inst.URN)
	}

	// It is persisted, not recomputed on read. Tether's
	// messaging-integration.md §1: an app that re-derives on boot orphans
	// every message addressed to the old value.
	got, err := s.GetDurableAgentInstance(ctx, inst.ID)
	if err != nil {
		t.Fatalf("GetDurableAgentInstance: %v", err)
	}
	if got.URN != inst.URN {
		t.Errorf("round-trip URN = %q, want the persisted %q", got.URN, inst.URN)
	}
}

// TestCreateDurableAgentInstance_TwoInstancesOfOneProfileGetDistinctURNs is the
// live case that proved the profile could never have been the actor: the
// production database already held two instances sharing one profile, and
// therefore one profile URN, against the architecture's "sharing a definition
// does not share identity".
func TestCreateDurableAgentInstance_TwoInstancesOfOneProfileGetDistinctURNs(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	agent := makeTestAgent(t, s, "shared-profile")

	a := &DurableAgentInstance{Name: "First", Slug: "shared-a", ProfileID: agent.ID}
	b := &DurableAgentInstance{Name: "Second", Slug: "shared-b", ProfileID: agent.ID}
	for _, inst := range []*DurableAgentInstance{a, b} {
		if err := s.CreateDurableAgentInstance(ctx, inst); err != nil {
			t.Fatalf("CreateDurableAgentInstance(%s): %v", inst.Slug, err)
		}
	}
	if a.URN == "" || b.URN == "" {
		t.Fatalf("empty URN: a=%q b=%q", a.URN, b.URN)
	}
	if a.URN == b.URN {
		t.Errorf("two instances of profile %s share URN %q — they are two correspondents, "+
			"not one", agent.ID, a.URN)
	}
}

// TestCreateDurableAgentInstance_SuppliedURNIsKept protects a restore and a
// deliberate re-home: minting is a default, not an override.
func TestCreateDurableAgentInstance_SuppliedURNIsKept(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	agent := makeTestAgent(t, s, "supplied-urn")

	const supplied = "msg://agent/nanite/agt_restored01"
	inst := &DurableAgentInstance{
		Name: "Restored", Slug: "restored", ProfileID: agent.ID, URN: supplied,
	}
	if err := s.CreateDurableAgentInstance(ctx, inst); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}
	if inst.URN != supplied {
		t.Errorf("URN = %q, want the supplied %q — a restore must not be re-minted", inst.URN, supplied)
	}
}

// TestSyncDurableAgentInstanceConfig_DoesNotRehomeActor is the guard that
// matters most in this file. SyncDurableAgentInstanceConfig upserts on slug and
// runs whenever config is reconciled; if it carried urn into its DO UPDATE SET,
// an ordinary config sync would silently move an actor's address and orphan
// every message already sent to it.
func TestSyncDurableAgentInstanceConfig_DoesNotRehomeActor(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	agent := makeTestAgent(t, s, "sync-urn")

	first := &DurableAgentInstance{Name: "Sync", Slug: "sync-target", ProfileID: agent.ID}
	if err := s.CreateDurableAgentInstance(ctx, first); err != nil {
		t.Fatalf("CreateDurableAgentInstance: %v", err)
	}
	original := first.URN
	if original == "" {
		t.Fatal("no URN minted; the re-home assertion would be vacuous")
	}

	// Re-sync the same slug with a different name and a freshly-minted URN in
	// the incoming struct — the shape an ordinary reconcile takes.
	incoming := &DurableAgentInstance{Name: "Sync Renamed", Slug: "sync-target", ProfileID: agent.ID}
	if _, err := s.SyncDurableAgentInstanceConfig(ctx, incoming); err != nil {
		t.Fatalf("SyncDurableAgentInstanceConfig: %v", err)
	}

	got, err := s.GetDurableAgentInstanceBySlug(ctx, "sync-target")
	if err != nil {
		t.Fatalf("GetDurableAgentInstanceBySlug: %v", err)
	}
	if got.URN != original {
		t.Errorf("config sync re-homed the actor: URN %q -> %q. Every message addressed to "+
			"the old URN is now orphaned.", original, got.URN)
	}
	if got.Name != "Sync Renamed" {
		t.Errorf("Name = %q, want the sync to have applied; the URN guard must not freeze "+
			"the whole row", got.Name)
	}
}
