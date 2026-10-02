package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestPreferencesTransactionPreservesRowAndDurability(t *testing.T) {
	s := newSeededStore(t)
	ctx := context.Background()
	execPreferences(t, s, `UPDATE user_settings SET default_model='unrelated', updated_at='2001-01-01' WHERE id=1`)
	before := privateSettingsRow(t, s.DB)
	initial := adminPreferences(t, s)
	var staged *AdminPreferences
	calls := 0
	err := s.WithAdminPreferencesTransaction(ctx, func(tx *PreferencesTransaction) error {
		calls++
		current, err := tx.Current()
		if err != nil {
			return err
		}
		if *current != *initial {
			t.Fatal("wrong transaction snapshot")
		}
		staged, err = tx.Stage(AdminPreferences{ToolStreamBehavior: "hidden", ToolDrawerRetention: 60})
		if err == nil && staged.Version == initial.Version {
			t.Fatal("no staged generation")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || *adminPreferences(t, s) != *staged {
		t.Fatal("callback replay or incorrect commit")
	}
	after := privateSettingsRow(t, s.DB)
	for _, key := range []string{"tool_stream_behavior", "tool_drawer_retention", "updated_at", "admin_preferences_version"} {
		delete(before, key)
		delete(after, key)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("unrelated singleton columns changed (values omitted)")
	}
	noop := privateSettingsRow(t, s.DB)
	err = s.WithAdminPreferencesTransaction(ctx, func(tx *PreferencesTransaction) error { _, stageErr := tx.Stage(*staged); return stageErr })
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(noop, privateSettingsRow(t, s.DB)) {
		t.Fatal("no-op changed row")
	}
	other, err := New(ctx, s.DBPath(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close(ctx)
	if *adminPreferences(t, other) != *staged {
		t.Fatal("committed preferences not durable")
	}
}

func TestPreferencesTransactionFailure(t *testing.T) {
	for _, mode := range []string{"callback", "cancel", "commit", "stage"} {
		t.Run(mode, func(t *testing.T) {
			s := newSeededStore(t)
			before := privateSettingsRow(t, s.DB)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			err := s.WithAdminPreferencesTransaction(ctx, func(tx *PreferencesTransaction) error {
				calls++
				if mode == "stage" {
					if _, err := tx.tx.ExecContext(ctx, `CREATE TEMP TRIGGER fail_preference BEFORE UPDATE ON user_settings BEGIN SELECT RAISE(ABORT,'fixture stage failure'); END`); err != nil {
						return err
					}
				}
				if _, err := tx.Stage(AdminPreferences{ToolStreamBehavior: "persist", ToolDrawerRetention: 30}); err != nil {
					return err
				}
				switch mode {
				case "callback":
					return errors.New("fixture post-stage failure")
				case "cancel":
					cancel()
				case "commit":
					return tx.tx.Rollback()
				}
				return nil
			})
			if err == nil || calls != 1 {
				t.Fatal("failure admitted or callback replayed")
			}
			// Wait for database/sql's cancellation rollback before using its one connection.
			if !reflect.DeepEqual(before, privateSettingsRow(t, s.DB)) {
				t.Fatal("failure changed row/version (values omitted)")
			}
		})
	}
}

func TestPreferencesTransactionSerializesExternalWriter(t *testing.T) {
	s := newSeededStore(t)
	ctx := context.Background()
	other, err := New(ctx, s.DBPath(ctx))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close(ctx)
	started, done := make(chan struct{}), make(chan error, 1)
	var staged *AdminPreferences
	err = s.WithAdminPreferencesTransaction(ctx, func(tx *PreferencesTransaction) error {
		current, currentErr := tx.Current()
		if currentErr != nil {
			return currentErr
		}
		go func() {
			close(started)
			_, writeErr := other.DB.ExecContext(ctx, `UPDATE user_settings SET tool_drawer_retention=60 WHERE id=1`)
			done <- writeErr
		}()
		<-started
		select {
		case earlyErr := <-done:
			t.Fatalf("external writer escaped BEGIN IMMEDIATE: %v", earlyErr)
		case <-time.After(50 * time.Millisecond):
		}
		current.ToolStreamBehavior = "hidden"
		staged, currentErr = tx.Stage(*current)
		return currentErr
	})
	if err != nil {
		t.Fatal(err)
	}
	if writeErr := <-done; writeErr != nil {
		t.Fatal(writeErr)
	}
	final := adminPreferences(t, s)
	if final.ToolStreamBehavior != "hidden" || final.ToolDrawerRetention != 60 || final.Version == staged.Version {
		t.Fatal("external writer did not serialize/invalidate")
	}
}
