package envelope

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/go-envelopes"
	"github.com/hollis-labs/go-envelopes/envelopestest"
)

// TestGoEnvelopesContract runs the lib's downstream-consumer contract
// against a registry built the same way Nanite builds it at startup
// (LoadCore + RegisterOrphans). Catches regressions when the lib
// version bumps so we know the consumption invariants still hold.
//
// Built fresh per run rather than reusing the package-level registry
// installed by main_test.go because the contract roundtrip-registers
// "envelopestest.contract-tmp" and we don't want a parallel-test race
// against the shared registry.
func TestGoEnvelopesContract(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reg, err := envelopes.LoadCore(ctx)
	if err != nil {
		t.Fatalf("LoadCore: %v", err)
	}
	if _, err := RegisterOrphans(reg); err != nil {
		t.Fatalf("RegisterOrphans: %v", err)
	}
	envelopestest.RunContract(t, reg)
}

// TestOrphansRegisteredUnderLegacyNamespace verifies that the five
// schemas extracted into the lib's manifest/schemas/ but absent from
// the YAML manifest land in the registry under nanite-legacy.<bare>
// after RegisterOrphans. Belt-and-suspenders coverage for the locked
// decision in the implementer prompt — catalog cleanup will eventually
// promote the live ones into the manifest, at which point this test
// updates to assert the bare-name path instead.
func TestOrphansRegisteredUnderLegacyNamespace(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reg, err := envelopes.LoadCore(ctx)
	if err != nil {
		t.Fatalf("LoadCore: %v", err)
	}
	registered, err := RegisterOrphans(reg)
	if err != nil {
		t.Fatalf("RegisterOrphans: %v", err)
	}
	if registered != len(OrphanTypes) {
		t.Fatalf("expected all %d orphans registered, got %d", len(OrphanTypes), registered)
	}
	for _, bare := range OrphanTypes {
		ns := LegacyTypeName(bare)
		spec, ok := reg.Lookup(ns)
		if !ok {
			t.Errorf("orphan %q not found under %q after RegisterOrphans", bare, ns)
			continue
		}
		if spec.PluginID != LegacyPluginID {
			t.Errorf("orphan %q plugin id = %q, want %q", bare, spec.PluginID, LegacyPluginID)
		}
		if spec.DataSchema == nil {
			t.Errorf("orphan %q registered without DataSchema (lib's manifest/schemas/%s.schema.json missing?)", bare, bare)
		}
	}
}
