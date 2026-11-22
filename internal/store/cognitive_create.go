package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// CreateCognitiveSession commits the view, native-subagent policy and primary
// profile binding together. It creates no actor identity or process record.
func (s *Store) CreateCognitiveSession(ctx context.Context, view *Session, profileID string) error {
	return s.createCognitiveSession(ctx, view, profileID, nil)
}

type HostSettingsAdmission struct{ ID, Revision string }

func (s *Store) CreateDefinedSessionWithHost(ctx context.Context, view *Session, record CognitiveViewRecord, host HostSettingsAdmission) error {
	return s.createCognitiveSession(ctx, view, "", &record, host)
}

func (s *Store) createCognitiveSession(ctx context.Context, view *Session, profileID string, record *CognitiveViewRecord, hosts ...HostSettingsAdmission) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollbackUnlessCommitted(tx)
	if profileID != "" {
		return ErrVerifiedActorRequired
	}
	if len(hosts) != 0 {
		var revision string
		var enabled bool
		if hostErr := tx.QueryRowContext(ctx, `SELECT revision,enabled FROM agent_host_settings WHERE id=?`, hosts[0].ID).Scan(&revision, &enabled); hostErr != nil {
			return hostErr
		}
		if !enabled || revision != hosts[0].Revision {
			return ErrAgentHostRevisionConflict
		}
	}
	if view.ID == "" {
		view.ID = uuid.NewString()
	}
	if view.Metadata == "" {
		view.Metadata = "{}"
	}
	now := time.Now().UTC().Format(time.RFC3339)
	err = tx.QueryRowContext(ctx, `INSERT INTO sessions
 (id,short_code,title,project_id,provider,model,status,metadata,subagent_runtime,last_activity,created_at,updated_at)
 VALUES (?,`+nextShortCodeSQL+`,?,?,?,?, 'active',?, 'api',?,?,?) RETURNING short_code`,
		view.ID, nullIfEmpty(view.Title), nullIfEmpty(view.ProjectID), nullIfEmpty(view.Provider), nullIfEmpty(view.Model), view.Metadata, now, now, now).Scan(&view.ShortCode)
	if err != nil {
		return fmt.Errorf("create cognitive view: %w", err)
	}
	if record != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO cognitive_views(session_view_id,definition_ref_json,chat_config_json) VALUES(?,?,?)`, view.ID, record.DefinitionRefJSON, record.ChatConfigJSON); err != nil {
			return fmt.Errorf("bind cognitive definition: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	view.Status = "active"
	view.CreatedAt = now
	view.UpdatedAt = now
	view.LastActivity = now
	return nil
}
