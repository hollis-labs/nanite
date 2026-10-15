package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Local authority for native views (CW-20261011-0037). A native view is hosted
// by Nanite and is not a fabric actor, so its tool grants live in their own
// tables and never in agent_actor_bindings / actor_granted_tools.
//
// LocalAuthority is the only writer. It does not authenticate anyone: the
// caller (an operator-authenticated API handler) supplies grantedBy, the
// credential id it already verified, and the writer records it. Receipts are
// provenance. Neither the receipt nor the database triggers prove who wrote a
// row, so nothing on the dispatch path may read a receipt to authenticate.

const NativeAuthorityName = "nanite-local"

var (
	ErrNativeAuthorityNotFound = errors.New("native authority not found")
	ErrNativeAuthorityExists   = errors.New("a live native authority already exists for this reviewed host settings revision")
	ErrNativeAuthorityStale    = errors.New("host settings changed since they were reviewed")
	ErrNativeAuthorityRevoked  = errors.New("native authority is revoked")
	ErrNativeAuthorityActor    = errors.New("a grant requires the granting credential id")
	ErrNativeToolNotGrantable  = errors.New("tool is not an available known tool")
)

// NativeAuthority is one reviewed (host settings revision, definition pin).
type NativeAuthority struct {
	ID                   string `json:"id"`
	HostSettingsID       string `json:"host_settings_id"`
	HostSettingsRevision string `json:"host_settings_revision"`
	DefinitionID         string `json:"definition_id"`
	DefinitionRevision   string `json:"definition_revision"`
	SemanticDigest       string `json:"semantic_digest"`
	Enabled              bool   `json:"enabled"`
	CreatedAt            string `json:"created_at"`
	RevokedAt            string `json:"revoked_at,omitempty"`
}

// NativeAuthorityTool is one granted tool by name.
type NativeAuthorityTool struct {
	ToolID    string `json:"tool_id"`
	Name      string `json:"name"`
	GrantedAt string `json:"granted_at"`
}

// LocalAuthority writes native-view authority. Construct it only in the API
// layer that has authenticated an operator.
type LocalAuthority struct{ s *Store }

// LocalAuthority returns the writer for this store.
func (s *Store) LocalAuthority() *LocalAuthority { return &LocalAuthority{s: s} }

type receipt struct {
	Authority            string `json:"authority"`
	V                    int    `json:"v"`
	GrantedBy            string `json:"granted_by"`
	HostSettingsRevision string `json:"host_settings_revision,omitempty"`
	DefinitionDigest     string `json:"definition_digest,omitempty"`
	Reason               string `json:"reason,omitempty"`
	At                   string `json:"at"`
}

func newReceipt(grantedBy, hostRevision, digest, reason string) (string, error) {
	grantedBy = strings.TrimSpace(grantedBy)
	if grantedBy == "" {
		return "", ErrNativeAuthorityActor
	}
	b, err := json.Marshal(receipt{Authority: NativeAuthorityName, V: 1, GrantedBy: grantedBy, HostSettingsRevision: hostRevision, DefinitionDigest: digest, Reason: reason, At: time.Now().UTC().Format(time.RFC3339Nano)})
	return string(b), err
}

func auditNative(ctx context.Context, tx *sql.Tx, authorityID, action, toolID, grantedBy, reason string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO native_authority_audit(authority_id,action,tool_id,granted_by,reason) VALUES(?,?,?,?,?)`, authorityID, action, toolID, strings.TrimSpace(grantedBy), reason)
	return err
}

// CreateAuthority records the operator's review of the host settings revision
// currently in force. It refuses disabled host settings, and a second live
// authority for the same reviewed revision and pin.
func (l *LocalAuthority) CreateAuthority(ctx context.Context, grantedBy, hostSettingsID, reason string) (NativeAuthority, error) {
	var out NativeAuthority
	host, err := l.s.GetAgentHostSettings(ctx, hostSettingsID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrNativeAuthorityNotFound
		}
		return out, err
	}
	if !host.Enabled {
		return out, errors.New("host settings are disabled")
	}
	rc, err := newReceipt(grantedBy, host.Revision, host.DefinitionRef.Digest, reason)
	if err != nil {
		return out, err
	}
	out = NativeAuthority{ID: uuid.NewString(), HostSettingsID: host.ID, HostSettingsRevision: host.Revision,
		DefinitionID: host.DefinitionRef.ID, DefinitionRevision: host.DefinitionRef.Revision, SemanticDigest: host.DefinitionRef.Digest, Enabled: true}
	tx, err := l.s.DB.BeginTx(ctx, nil)
	if err != nil {
		return NativeAuthority{}, err
	}
	defer func() { _ = tx.Rollback() }()
	// The revision is read inside the transaction's view of the write so a
	// settings update between the read above and this insert is not blessed.
	var current string
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM agent_host_settings WHERE id=? AND enabled=1`, host.ID).Scan(&current); err != nil || current != host.Revision {
		return NativeAuthority{}, ErrNativeAuthorityStale
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO native_authorities(id,host_settings_id,host_settings_revision,definition_id,definition_revision,semantic_digest,receipt) VALUES(?,?,?,?,?,?,?)`,
		out.ID, out.HostSettingsID, out.HostSettingsRevision, out.DefinitionID, out.DefinitionRevision, out.SemanticDigest, rc); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return NativeAuthority{}, ErrNativeAuthorityExists
		}
		return NativeAuthority{}, err
	}
	if err = auditNative(ctx, tx, out.ID, "authority_created", "", grantedBy, reason); err != nil {
		return NativeAuthority{}, err
	}
	if err = tx.Commit(); err != nil {
		return NativeAuthority{}, err
	}
	return l.GetAuthority(ctx, out.ID)
}

// RevokeAuthority ends an authority permanently. Its grants stay as history but
// stop resolving, because a grant requires a live authority.
func (l *LocalAuthority) RevokeAuthority(ctx context.Context, grantedBy, authorityID, reason string) error {
	if strings.TrimSpace(grantedBy) == "" {
		return ErrNativeAuthorityActor
	}
	tx, err := l.s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE native_authorities SET enabled=0, revoked_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=? AND revoked_at IS NULL`, authorityID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNativeAuthorityNotFound
	}
	if err = auditNative(ctx, tx, authorityID, "authority_revoked", "", grantedBy, reason); err != nil {
		return err
	}
	return tx.Commit()
}

// GrantTool grants one available known tool, by name, to a live authority.
func (l *LocalAuthority) GrantTool(ctx context.Context, grantedBy, authorityID, toolName, reason string) error {
	a, err := l.GetAuthority(ctx, authorityID)
	if err != nil {
		return err
	}
	if !a.Enabled || a.RevokedAt != "" {
		return ErrNativeAuthorityRevoked
	}
	rc, err := newReceipt(grantedBy, a.HostSettingsRevision, a.SemanticDigest, reason)
	if err != nil {
		return err
	}
	tx, err := l.s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var toolID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM known_tools WHERE name=? AND status='available'`, toolName).Scan(&toolID)
	if errors.Is(err, sql.ErrNoRows) {
		// Tool names use underscores (dev_read); accept the dotted spelling.
		if alias := strings.ReplaceAll(toolName, ".", "_"); alias != toolName {
			err = tx.QueryRowContext(ctx, `SELECT id FROM known_tools WHERE name=? AND status='available'`, alias).Scan(&toolID)
		}
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %q; list grantable names with `nanite authority tools`", ErrNativeToolNotGrantable, toolName)
		}
		return err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO native_authority_tools(authority_id,tool_id,receipt) VALUES(?,?,?) ON CONFLICT(authority_id,tool_id) DO NOTHING`, authorityID, toolID, rc)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // already granted: keep the original receipt and write no audit noise
	}
	if err = auditNative(ctx, tx, authorityID, "tool_granted", toolID, grantedBy, reason); err != nil {
		return err
	}
	return tx.Commit()
}

// RevokeTool removes one grant; revoking a tool that is not granted is a no-op.
func (l *LocalAuthority) RevokeTool(ctx context.Context, grantedBy, authorityID, toolName, reason string) error {
	if strings.TrimSpace(grantedBy) == "" {
		return ErrNativeAuthorityActor
	}
	tx, err := l.s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var toolID string
	granted := func(name string) error {
		return tx.QueryRowContext(ctx, `SELECT t.tool_id FROM native_authority_tools t JOIN known_tools k ON k.id=t.tool_id WHERE t.authority_id=? AND k.name=?`, authorityID, name).Scan(&toolID)
	}
	err = granted(toolName)
	if errors.Is(err, sql.ErrNoRows) {
		// Same dotted-alias rule as GrantTool, whether or not the tool is
		// still available.
		if alias := strings.ReplaceAll(toolName, ".", "_"); alias != toolName {
			err = granted(alias)
		}
	}
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM native_authority_tools WHERE authority_id=? AND tool_id=?`, authorityID, toolID); err != nil {
		return err
	}
	if err = auditNative(ctx, tx, authorityID, "tool_revoked", toolID, grantedBy, reason); err != nil {
		return err
	}
	return tx.Commit()
}

// GetAuthority reads one authority.
func (l *LocalAuthority) GetAuthority(ctx context.Context, id string) (NativeAuthority, error) {
	var a NativeAuthority
	var revoked sql.NullString
	err := l.s.DB.QueryRowContext(ctx, `SELECT id,host_settings_id,host_settings_revision,definition_id,definition_revision,semantic_digest,enabled,created_at,revoked_at FROM native_authorities WHERE id=?`, id).
		Scan(&a.ID, &a.HostSettingsID, &a.HostSettingsRevision, &a.DefinitionID, &a.DefinitionRevision, &a.SemanticDigest, &a.Enabled, &a.CreatedAt, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNativeAuthorityNotFound
	}
	a.RevokedAt = revoked.String
	return a, err
}

// ListTools returns the tools granted to an authority, by name.
func (l *LocalAuthority) ListTools(ctx context.Context, authorityID string) ([]NativeAuthorityTool, error) {
	rows, err := l.s.DB.QueryContext(ctx, `SELECT k.id,k.name,t.granted_at FROM native_authority_tools t JOIN known_tools k ON k.id=t.tool_id WHERE t.authority_id=? ORDER BY k.name`, authorityID)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	out := make([]NativeAuthorityTool, 0)
	for rows.Next() {
		var t NativeAuthorityTool
		if err = rows.Scan(&t.ToolID, &t.Name, &t.GrantedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ResolveToolNames returns the tools a native view may be offered or run: the
// grants of the live authority whose reviewed host settings revision AND
// definition pin still equal what is in force now, and only when the view was
// admitted with that same revision (a view created under older settings keeps
// running under those settings and does not inherit a later review). Settings that changed since
// the review, a disabled host, a revoked authority and a tool that is no longer
// available all yield nothing for that tool. It is read on every dispatch, so a
// revocation reaches a turn already running.
func (l *LocalAuthority) ResolveToolNames(ctx context.Context, hostSettingsID, admittedRevision string) ([]string, error) {
	if hostSettingsID == "" || admittedRevision == "" {
		return []string{}, nil
	}
	rows, err := l.s.DB.QueryContext(ctx, `
		SELECT k.name
		  FROM agent_host_settings h
		  JOIN native_authorities a ON a.host_settings_id = h.id
		   AND a.host_settings_revision = h.revision
		   AND a.definition_id = h.definition_id
		   AND a.definition_revision = h.definition_revision
		   AND a.semantic_digest = h.semantic_digest
		  JOIN native_authority_tools t ON t.authority_id = a.id
		  JOIN known_tools k ON k.id = t.tool_id
		 WHERE h.id = ? AND h.revision = ? AND h.enabled = 1 AND a.enabled = 1 AND a.revoked_at IS NULL AND k.status = 'available'
		 ORDER BY k.name`, hostSettingsID, admittedRevision)
	if err != nil {
		return nil, fmt.Errorf("resolve native tool grants: %w", err)
	}
	defer closeRows(rows)
	out := make([]string, 0)
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// ListAuthorities returns every authority for a host settings id, newest first,
// revoked ones included (history stays readable).
func (l *LocalAuthority) ListAuthorities(ctx context.Context, hostSettingsID string) ([]NativeAuthority, error) {
	rows, err := l.s.DB.QueryContext(ctx, `SELECT id FROM native_authorities WHERE host_settings_id=? ORDER BY created_at DESC, rowid DESC`, hostSettingsID)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]NativeAuthority, 0, len(ids))
	for _, id := range ids {
		a, gerr := l.GetAuthority(ctx, id)
		if gerr != nil {
			return nil, gerr
		}
		out = append(out, a)
	}
	return out, nil
}

// GrantableTool is one tool name a grant can name.
type GrantableTool struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Description string `json:"description,omitempty"`
}

// GrantableTools lists the available known tools, by the exact names GrantTool
// accepts.
func (l *LocalAuthority) GrantableTools(ctx context.Context) ([]GrantableTool, error) {
	known, err := l.s.ListAvailableKnownTools(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]GrantableTool, 0, len(known))
	for _, k := range known {
		out = append(out, GrantableTool{Name: k.Name, Source: k.Source, Description: k.Description})
	}
	return out, nil
}
