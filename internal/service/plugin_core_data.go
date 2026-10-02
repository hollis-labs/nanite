package service

import (
	"context"
	"fmt"
	"time"

	"github.com/hollis-labs/nanite/internal/brand"
	"github.com/hollis-labs/nanite/internal/plugin/dataexport"
	"github.com/hollis-labs/nanite/internal/store"
)

// PluginCoreData owns the explicit, released feature-to-table allowlist. The
// core APIs have been removed before this binary ships. Operators must restart
// old table readers before deploying it, as with any destructive schema change.
type PluginCoreData struct {
	store   *store.Store
	dataDir func(string) (string, error)
}

func NewPluginCoreData(st *store.Store) *PluginCoreData {
	return &PluginCoreData{store: st, dataDir: brand.PluginDataDir}
}
func (adopter *PluginCoreData) AdoptPluginCoreData(ctx context.Context, owner string) error {
	features := map[string]string{"nanite.bookmarks": "bookmarks", "nanite.reminders": "reminders"}
	feature, known := features[owner]
	if !known {
		return nil
	}
	if adopter.store == nil {
		return fmt.Errorf("plugin core data: store unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	source, err := dataexport.SourceID(adopter.store.DBPath(ctx))
	if err != nil {
		return err
	}
	directory, err := adopter.dataDir(owner)
	if err != nil {
		return err
	}
	tx, err := adopter.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, feature).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		// A moved DB retains its receipt's original SourceID. Never create another
		// source or acknowledge an absent table with no committed export.
		var count int
		err = tx.QueryRowContext(ctx, `SELECT count(*) FROM plugin_core_exports WHERE plugin_id=? AND feature=?`, owner, feature).Scan(&count)
		if err != nil {
			return fmt.Errorf("plugin core data: retired %s receipt unavailable: %w", feature, err)
		}
		if count != 1 {
			return fmt.Errorf("plugin core data: exactly one %s receipt required", feature)
		}
		return nil
	}
	// Reject inconsistent state rather than exporting an empty/recreated table
	// over the source the plugin already imported.
	var ledger bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='plugin_core_exports')`).Scan(&ledger); err != nil {
		return err
	}
	if ledger {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM plugin_core_exports WHERE plugin_id=? AND feature=?`, owner, feature).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("plugin core data: %s table exists beside committed receipt", feature)
		}
	}
	if _, err = dataexport.ExportAndDrop(ctx, tx, dataexport.Spec{PluginID: owner, Feature: feature, SourceID: source, Table: feature}, directory); err != nil {
		return err
	}
	return tx.Commit()
}
