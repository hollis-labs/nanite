//go:build devmode

package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/nanite/internal/plugin/catalog"
	"github.com/hollis-labs/nanite/internal/plugin/install"
	"github.com/hollis-labs/nanite/internal/storetest"
)

// Regression coverage for AD-25
// (TASKS/audit-remediation/01-plugin-install-convergence/02-wire-allow-
// unsigned-plugins-setting.md): user_settings.allow_unsigned_plugins must
// reach the SignatureVerifier buildInstaller constructs, in a devmode-tagged
// build. These tests exercise resolveAllowUnsignedPlugins — the exact
// function buildInstaller calls — against an explicit, scratch database
// path, never resolveDBPath()'s real XDG-resolved default (which this
// package has no existing test coverage sandboxing; see
// docs/engineering/EXECUTION-PROCESS.md's live-verification-writes
// guidance on always targeting an explicit scratch path).

// TestBuildInstaller_ResolveAllowUnsignedPlugins_ThreadsSetting asserts that
// once user_settings.allow_unsigned_plugins is true, resolveAllowUnsignedPlugins
// reads it back and that value reaches the SignatureVerifier constructed via
// the same install.NewInstaller/BuildOptions path buildInstaller itself uses.
func TestBuildInstaller_ResolveAllowUnsignedPlugins_ThreadsSetting(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	s, err := storetest.New(t, context.Background(), dbPath)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	// store.New (unlike cmdServe's boot path) does not run Store.Seed — the
	// user_settings singleton row (id=1) only exists once something has
	// inserted it. Insert it directly here rather than calling Seed(),
	// which also seeds a real external "official" catalog_sources row this
	// test has no reason to touch.
	if _, err := s.DB.Exec(`INSERT OR IGNORE INTO user_settings (id) VALUES (1)`); err != nil {
		t.Fatalf("seed user_settings row: %v", err)
	}
	us, err := s.GetUserSettings()
	if err != nil {
		t.Fatalf("GetUserSettings: %v", err)
	}
	us.AllowUnsignedPlugins = true
	if err := s.UpdateUserSettings(us); err != nil {
		t.Fatalf("UpdateUserSettings: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if !resolveAllowUnsignedPlugins(dbPath) {
		t.Fatal("expected resolveAllowUnsignedPlugins to read true from user_settings.allow_unsigned_plugins")
	}

	// Mirrors buildInstaller's own install.NewInstaller call, so this
	// asserts the setting reaches the actual construction path, not just
	// the settings read in isolation.
	ring := catalog.NewKeyRing()
	inst, _ := install.NewInstaller(install.BuildOptions{
		KeyLookup:     ring.LookupFunc(),
		AllowUnsigned: resolveAllowUnsignedPlugins(dbPath),
		Extractor:     &install.TarGzExtractor{},
		Loader:        noopLoader{},
		StagingRoot:   t.TempDir(),
		PluginsRoot:   t.TempDir(),
	})
	v, ok := inst.Verifier.(*install.SignatureVerifier)
	if !ok {
		t.Fatalf("Verifier is %T, want *install.SignatureVerifier", inst.Verifier)
	}
	if !v.AllowUnsigned {
		t.Error("expected AllowUnsigned=true to reach the SignatureVerifier when user_settings.allow_unsigned_plugins=true")
	}
}

// TestBuildInstaller_ResolveAllowUnsignedPlugins_DefaultsFalse asserts a
// freshly-migrated database with no user_settings row at all — a real,
// reachable state for `nanite plugin install` run before `nanite serve` has
// ever seeded the singleton row — resolves to AllowUnsigned=false via the
// same fail-safe path as a read error: the feature is opt-in, not opt-out.
func TestBuildInstaller_ResolveAllowUnsignedPlugins_DefaultsFalse(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	if resolveAllowUnsignedPlugins(dbPath) {
		t.Error("expected resolveAllowUnsignedPlugins to default to false on a fresh database")
	}
}

// TestBuildInstaller_ResolveAllowUnsignedPlugins_FailsSafeWhenStoreCannotOpen
// asserts that a dbPath the store cannot open (here: a path that is itself an
// existing directory, not a SQLite file) fails SAFE to false, never true —
// "couldn't read the setting" must never be silently treated as "allow
// unsigned."
func TestBuildInstaller_ResolveAllowUnsignedPlugins_FailsSafeWhenStoreCannotOpen(t *testing.T) {
	dbPath := t.TempDir() // a directory, not a file -- store.New must fail to open it as SQLite.
	if resolveAllowUnsignedPlugins(dbPath) {
		t.Error("expected resolveAllowUnsignedPlugins to fail safe to false when the store cannot be opened")
	}
}
