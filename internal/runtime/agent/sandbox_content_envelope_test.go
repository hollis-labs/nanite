package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	envelopes "github.com/hollis-labs/go-envelopes"
	"github.com/hollis-labs/nanite/internal/chat"
)

// TestEnvelopeSchemaContent_NoRegistry pins the nil-registry fallback: with
// no chat.SetEnvelopeRegistry/InitCoreTypes call made (the state this
// package's other bootdir tests run under), envelopeSchemaContent must not
// panic and must still emit the static wire-format/field-reference
// sections, just with a placeholder instead of a type table.
func TestEnvelopeSchemaContent_NoRegistry(t *testing.T) {
	restore := snapshotEnvelopeRegistryForTest(t)
	defer restore()
	chat.SetEnvelopeRegistry(nil)

	got := envelopeSchemaContent()
	if !strings.Contains(got, "# Nanite Envelope Schema") {
		t.Error("missing top-level header")
	}
	if !strings.Contains(got, "## Registered Envelope Types") {
		t.Error("missing Registered Envelope Types section")
	}
	if strings.Contains(got, "| document-viewer |") {
		t.Error("should not list any types when the registry is unpopulated")
	}
}

// TestEnvelopeSchemaContent_ReflectsLiveRegistry is the core Phase 6 task
// 06 assertion: envelopeSchemaContent's "Registered Envelope Types" table
// must be built from the live registry at call time, not a hardcoded
// list — so it must (a) list every currently-registered type, including
// ones added after this file was last edited, and (b) NOT list any type
// that isn't registered (the giphy-modal/kb-result/ticket-* family cut in
// Phase 0).
func TestEnvelopeSchemaContent_ReflectsLiveRegistry(t *testing.T) {
	restore := snapshotEnvelopeRegistryForTest(t)
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reg, err := envelopes.LoadCore(ctx)
	if err != nil {
		t.Fatalf("envelopes.LoadCore: %v", err)
	}
	chat.SetEnvelopeRegistry(reg)
	chat.InitCoreTypes(reg.Names())

	got := envelopeSchemaContent()

	for _, name := range reg.Names() {
		if !strings.Contains(got, "| "+name+" |") {
			t.Errorf("envelopeSchemaContent missing live-registered type %q", name)
		}
	}

	// Phase 0 cut these five types (TASKS/phase-0/15a-cut-giphy.md,
	// 15c-cut-support-ticket.md) — they must never reappear in planted
	// boot content again, which is the exact staleness this task fixed.
	for _, cut := range []string{"giphy-modal", "kb-result", "ticket-confirmation", "ticket-form", "resolution-capture"} {
		if strings.Contains(got, "| "+cut+" |") {
			t.Errorf("envelopeSchemaContent lists cut type %q — hardcoded/stale list regressed", cut)
		}
	}
}

// TestClaudeMDBody_ReflectsLiveRegistry pins the CLAUDE.md addendum's
// registered-types paragraph to the same live source, and confirms it no
// longer points at the two nonexistent paths (config/envelopes.yaml,
// internal/envelope/schemas/*.schema.json).
func TestClaudeMDBody_ReflectsLiveRegistry(t *testing.T) {
	restore := snapshotEnvelopeRegistryForTest(t)
	defer restore()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reg, err := envelopes.LoadCore(ctx)
	if err != nil {
		t.Fatalf("envelopes.LoadCore: %v", err)
	}
	chat.SetEnvelopeRegistry(reg)
	chat.InitCoreTypes(reg.Names())

	got := claudeMDBody()

	if strings.Contains(got, "config/envelopes.yaml") {
		t.Error("claudeMDBody still points at nonexistent config/envelopes.yaml")
	}
	if strings.Contains(got, "internal/envelope/schemas") {
		t.Error("claudeMDBody still points at nonexistent internal/envelope/schemas/")
	}
	for _, name := range reg.Names() {
		if !strings.Contains(got, name) {
			t.Errorf("claudeMDBody missing live-registered type %q", name)
		}
	}
}

// snapshotEnvelopeRegistryForTest saves chat's current global envelope-
// registry state and returns a func that restores it, so these tests
// don't leak SetEnvelopeRegistry/InitCoreTypes state into the rest of
// this package's tests (which run against the nil-registry default).
func snapshotEnvelopeRegistryForTest(t *testing.T) func() {
	t.Helper()
	prevReg := chat.EnvelopeRegistry()
	prevNames := chat.RegisteredEnvelopeTypeNames()
	prevSet := make(map[string]bool, len(prevNames))
	for _, n := range prevNames {
		prevSet[n] = true
	}
	return func() {
		chat.SetEnvelopeRegistry(prevReg)
		// Remove anything this test added that wasn't there before, then
		// restore anything that was there before (both no-ops if the test
		// didn't touch the registered-types set).
		for _, n := range chat.RegisteredEnvelopeTypeNames() {
			if !prevSet[n] {
				chat.UnregisterEnvelopeType(n)
			}
		}
		for _, n := range prevNames {
			chat.RegisterEnvelopeType(n)
		}
	}
}
