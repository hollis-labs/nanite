package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// AgentView is the read model of one set-up agent: its host settings (which
// carry the definition pin, runtime, provider, model and directories) and the
// tool authority currently active for the revision those settings are at.
type AgentView struct {
	HostSettings AgentHostSettings `json:"host_settings"`
	// State is "ready" (enabled, authority active), "no_authority" (enabled
	// but nothing is granted for this revision) or "disabled".
	State       string   `json:"state"`
	AuthorityID string   `json:"authority_id,omitempty"`
	Tools       []string `json:"tools"`
}

// ListAgentViews returns every host-settings record with its active authority.
func (s *Store) ListAgentViews(ctx context.Context) ([]AgentView, error) {
	hosts, err := s.ListAgentHostSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]AgentView, 0, len(hosts))
	for _, h := range hosts {
		v, err := s.agentViewFor(ctx, h)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// GetAgentView finds an agent by host-settings id or slug.
func (s *Store) GetAgentView(ctx context.Context, idOrSlug string) (AgentView, error) {
	idOrSlug = strings.TrimSpace(idOrSlug)
	h, err := getAgentHostSettingsWhere(ctx, s.DB, "id", idOrSlug)
	if errors.Is(err, sql.ErrNoRows) {
		h, err = getAgentHostSettingsWhere(ctx, s.DB, "slug", idOrSlug)
	}
	if err != nil {
		return AgentView{}, err
	}
	return s.agentViewFor(ctx, h)
}

func (s *Store) agentViewFor(ctx context.Context, h AgentHostSettings) (AgentView, error) {
	v := AgentView{HostSettings: h, Tools: []string{}}
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM native_authorities WHERE host_settings_id=? AND host_settings_revision=? AND enabled=1 AND revoked_at IS NULL ORDER BY id LIMIT 1`, h.ID, h.Revision).Scan(&v.AuthorityID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return v, err
	}
	if v.AuthorityID != "" {
		rows, err := s.DB.QueryContext(ctx, `SELECT k.name FROM native_authority_tools t JOIN known_tools k ON k.id=t.tool_id WHERE t.authority_id=? ORDER BY k.name`, v.AuthorityID)
		if err != nil {
			return v, err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var n string
			if err = rows.Scan(&n); err != nil {
				return v, err
			}
			v.Tools = append(v.Tools, n)
		}
		if err = rows.Err(); err != nil {
			return v, err
		}
	}
	switch {
	case !h.Enabled:
		v.State = "disabled"
	case v.AuthorityID == "":
		v.State = "no_authority"
	default:
		v.State = "ready"
	}
	return v, nil
}
