package storetest

import (
	"context"

	"github.com/google/uuid"
	"github.com/hollis-labs/nanite/internal/store"
)

// HistoricalProfile supplies retained legacy data to a private test database.
// It deliberately bypasses retired production writers. It is not conversion,
// enrollment, or a fixture for runtime profile selection.
func HistoricalProfile(ctx context.Context, st *store.Store, p *store.AgentProfile) error {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	if p.Source == "" {
		p.Source = "user"
	}
	_, err := st.DB.ExecContext(ctx, `INSERT INTO agent_profiles(id,name,slug,system_prompt,source,plugin_id) VALUES(?,?,?,?,?,?)`, p.ID, p.Name, p.Slug, p.SystemPrompt, p.Source, p.PluginID)
	if err != nil {
		return err
	}
	saved, err := st.GetHistoricalAgentProfile(ctx, p.ID)
	if err != nil {
		return err
	}
	*p = *saved
	return nil
}
