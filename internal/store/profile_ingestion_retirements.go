package store

import (
	"context"
	"errors"
)

var ErrProfileIngestionRetired = errors.New("profile identity is retired from ingestion")

// ProfileIngestionRetired reads the durable retirement control before ingestion
// performs secondary effects. The row writer also checks inside its transaction.
func (s *Store) ProfileIngestionRetired(ctx context.Context, id, slug string) (bool, error) {
	return profileIngestionRetired(ctx, s.DB, id, slug)
}

func profileIngestionRetired(ctx context.Context, db agentConfigDB, id, slug string) (bool, error) {
	var retired bool
	err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM profile_ingestion_retirements WHERE profile_id = ? OR slug = ?)`, id, slug).Scan(&retired)
	return retired, err
}

func checkProfileIngestionRetired(ctx context.Context, db agentConfigDB, id, slug string) error {
	retired, err := profileIngestionRetired(ctx, db, id, slug)
	if err != nil {
		return err
	}
	if retired {
		return ErrProfileIngestionRetired
	}
	return nil
}
