package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

var (
	ErrProtectedRetirementAuthority = errors.New("protected retirement requires owner attribution and an exact retained logical profile")
	ErrProfileRetirementActive      = errors.New("profile runtime must be stopped before retirement")
)

// ProtectedRetirementAuthority records the owner's deliberate operation. Actor
// is an audit label, never authentication; HTTP authentication remains host-owned.
type ProtectedRetirementAuthority struct {
	Actor         string `json:"actor"`
	Reason        string `json:"reason"`
	KeepProfileID string `json:"keep_profile_id"`
	KeepRevision  string `json:"keep_revision"`
}

func (a ProtectedRetirementAuthority) Validate(id string) error {
	if strings.TrimSpace(a.Actor) == "" || len(a.Actor) > 256 || strings.TrimSpace(a.Reason) == "" || len(a.Reason) > 2048 || a.KeepProfileID == "" || a.KeepProfileID == id || a.KeepRevision == "" {
		return ErrProtectedRetirementAuthority
	}
	return nil
}

func protectedRetirementSnapshot(ctx context.Context, tx *sql.Tx, id string, authority ProtectedRetirementAuthority) (ProfileRetirementExport, error) {
	if checkErr := authority.Validate(id); checkErr != nil {
		return ProfileRetirementExport{}, checkErr
	}
	keep, err := getHistoricalAgentProfile(ctx, tx, authority.KeepProfileID)
	if err != nil {
		return ProfileRetirementExport{}, err
	}
	if keep.Revision != authority.KeepRevision || !retirementEditable(keep) {
		return ProfileRetirementExport{}, ErrProfileRetirementConflict
	}
	p, err := getHistoricalAgentProfile(ctx, tx, id)
	if err != nil {
		return ProfileRetirementExport{}, err
	}
	if retirementEditable(p) {
		return ProfileRetirementExport{}, ErrProfileRetirementProtected
	}
	var active bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM durable_agent_instances WHERE profile_id = ? AND status NOT IN ('stopped','failed','archived'))`, id).Scan(&active)
	if err != nil {
		return ProfileRetirementExport{}, err
	}
	if active {
		return ProfileRetirementExport{}, ErrProfileRetirementActive
	}
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_runtime WHERE (agent_profile = ? OR agent_profile = ?) AND state IN ('launching','running'))`, p.ID, p.Slug).Scan(&active)
	if err != nil {
		return ProfileRetirementExport{}, err
	}
	if active {
		return ProfileRetirementExport{}, ErrProfileRetirementActive
	}
	snapshot, err := profileRetirementSnapshotData(ctx, tx, p)
	if err != nil {
		return snapshot, err
	}
	// These shared parents are part of the class's exported definition, but
	// retirement does not modify or delete them. In particular, a plugin's
	// instructions may live entirely in its linked role.
	for _, parent := range []struct{ table, id string }{{"roles", p.RoleID}, {"consumers", p.ConsumerID}, {"models", p.ModelID}} {
		if parent.id == "" {
			continue
		}
		if checkErr := addProtectedRetirementParent(ctx, tx, &snapshot, parent.table, parent.id); checkErr != nil {
			return snapshot, checkErr
		}
	}
	rows := 0
	for _, table := range snapshot.Tables {
		rows += len(table.Rows)
	}
	if rows > profileExportMaxRows {
		return snapshot, ErrProfileRetirementBound
	}
	_, err = snapshot.Digest()
	return snapshot, err
}

func addProtectedRetirementParent(ctx context.Context, tx *sql.Tx, snapshot *ProfileRetirementExport, table, id string) error {
	// Table names are a closed set above, and every column name comes from
	// SQLite. Expressions preserve exact storage classes, as in format 2.
	// #nosec G202 -- fixed parent tables above, quoted schema identifiers.
	rows, err := tx.QueryContext(ctx, "SELECT * FROM "+quoteRetirementName(table)+" LIMIT 0")
	if err != nil {
		return err
	}
	columns, err := rows.Columns()
	closeErr := rows.Close()
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	expressions := make([]string, len(columns))
	for i, col := range columns {
		expressions[i] = "CASE WHEN 1 THEN " + quoteRetirementName(col) + " END"
	}
	// #nosec G202 -- fixed table names and quoted schema columns; ID is bound.
	row := tx.QueryRowContext(ctx, "SELECT "+strings.Join(expressions, ",")+" FROM "+quoteRetirementName(table)+" WHERE id = ?", id)
	values, dest := make([]any, len(columns)), make([]any, len(columns))
	for i := range values {
		dest[i] = &values[i]
	}
	if checkErr := row.Scan(dest...); checkErr != nil {
		return checkErr
	}
	encoded, err := encodeProfileExportRow(values)
	if err != nil {
		return err
	}
	snapshot.Tables[table] = ProfileExportTable{Columns: columns, Rows: []json.RawMessage{encoded}}
	return nil
}

func (s *Store) ExportProtectedProfileRetirement(ctx context.Context, id string, authority ProtectedRetirementAuthority) (ProfileRetirementExport, error) {
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ProfileRetirementExport{}, err
	}
	defer rollbackUnlessCommitted(tx)
	snapshot, err := protectedRetirementSnapshot(ctx, tx, id, authority)
	if err != nil {
		return ProfileRetirementExport{}, err
	}
	return snapshot, tx.Commit()
}

// RetireExportedProtectedProfile commits suppression, audit attribution and the
// existing relational cleanup together, only after the exact typed export matches.
func (s *Store) RetireExportedProtectedProfile(ctx context.Context, id, exportID, digest string, authority ProtectedRetirementAuthority) error {
	if exportID == "" || digest == "" {
		return ErrProfileRetirementConflict
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	snapshot, err := protectedRetirementSnapshot(ctx, tx, id, authority)
	if err != nil {
		return err
	}
	actual, err := snapshot.Digest()
	if err != nil {
		return err
	}
	if actual != digest {
		return ErrProfileRetirementConflict
	}
	p, err := getHistoricalAgentProfile(ctx, tx, id)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO profile_ingestion_retirements(profile_id,slug,source,plugin_id,export_id,digest,actor,reason,keep_profile_id,keep_revision) VALUES(?,?,?,?,?,?,?,?,?,?)`, id, p.Slug, p.Source, p.PluginID, exportID, digest, authority.Actor, authority.Reason, authority.KeepProfileID, authority.KeepRevision)
	if err != nil {
		return err
	}
	if err = deleteAgentTx(ctx, tx, p); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ProtectedProfileRetirementCommitted(ctx context.Context, id, exportID, digest string, a ProtectedRetirementAuthority) (bool, error) {
	var committed bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM profile_ingestion_retirements WHERE profile_id=? AND export_id=? AND digest=? AND actor=? AND reason=? AND keep_profile_id=? AND keep_revision=?)`, id, exportID, digest, a.Actor, a.Reason, a.KeepProfileID, a.KeepRevision).Scan(&committed)
	return committed, err
}
