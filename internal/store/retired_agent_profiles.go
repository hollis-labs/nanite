package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrAgentProfileRetired = errors.New("agent profile has been retired")

type RetiredAgentProfile struct {
	ID        string
	Slug      string
	Name      string
	Source    string
	PluginID  string
	Class     string
	ExportID  string
	Digest    string
	Actor     string
	Reason    string
	RetiredAt string
}

type RetireAgentProfileAudit struct {
	ExportID string
	Digest   string
	Actor    string
	Reason   string
}

func (s *Store) IsAgentProfileRetired(ctx context.Context, id, slug string) (bool, error) {
	var n int
	switch {
	case id != "" && slug != "":
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM retired_agent_profiles WHERE id = ? OR slug = ?`, id, slug).Scan(&n); err != nil {
			return false, fmt.Errorf("check retired agent profile: %w", err)
		}
	case id != "":
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM retired_agent_profiles WHERE id = ?`, id).Scan(&n); err != nil {
			return false, fmt.Errorf("check retired agent profile: %w", err)
		}
	case slug != "":
		if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM retired_agent_profiles WHERE slug = ?`, slug).Scan(&n); err != nil {
			return false, fmt.Errorf("check retired agent profile: %w", err)
		}
	default:
		return false, nil
	}
	return n > 0, nil
}

func insertRetiredAgentProfileTx(ctx context.Context, tx *sql.Tx, p *AgentProfile, audit RetireAgentProfileAudit) error {
	if p == nil || p.ID == "" || p.Slug == "" || audit.ExportID == "" || audit.Digest == "" {
		return ErrProfileRetirementConflict
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := tx.ExecContext(ctx, `INSERT INTO retired_agent_profiles
		(id, slug, name, source, plugin_id, class, export_id, digest, actor, reason, retired_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Slug, p.Name, p.Source, p.PluginID, p.Class, audit.ExportID, audit.Digest, audit.Actor, audit.Reason, now)
	if err != nil {
		return fmt.Errorf("record retired agent profile: %w", err)
	}
	return nil
}

func (s *Store) GetRetiredAgentProfileBySlug(ctx context.Context, slug string) (*RetiredAgentProfile, error) {
	var r RetiredAgentProfile
	err := s.DB.QueryRowContext(ctx, `SELECT id, slug, name, source, plugin_id, class, export_id, digest, actor, reason, retired_at
		FROM retired_agent_profiles WHERE slug = ?`, slug).Scan(&r.ID, &r.Slug, &r.Name, &r.Source, &r.PluginID, &r.Class, &r.ExportID, &r.Digest, &r.Actor, &r.Reason, &r.RetiredAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get retired agent profile: %w", err)
	}
	return &r, nil
}
