package store

import (
	"context"
	"strings"
	"testing"
)

func TestListPluginSettingsRejectsMalformedJSON(t *testing.T) {
	tests := []struct {
		name         string
		settingsJSON string
		schemaJSON   string
		wantError    string
	}{
		{
			name:         "settings",
			settingsJSON: `{not-json`,
			schemaJSON:   `[]`,
			wantError:    "parse plugin settings",
		},
		{
			name:         "schema",
			settingsJSON: `{}`,
			schemaJSON:   `[not-json`,
			wantError:    "parse plugin schema",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			s := newTestStore(t)
			pluginID := "malformed-" + tt.name
			if _, err := s.DB.ExecContext(ctx,
				`INSERT INTO plugin_settings (plugin_id, settings, schema) VALUES (?, ?, ?)`,
				pluginID, tt.settingsJSON, tt.schemaJSON,
			); err != nil {
				t.Fatalf("insert malformed plugin settings: %v", err)
			}

			if _, err := s.GetPluginSettings(ctx, pluginID); err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("GetPluginSettings error = %v, want error containing %q", err, tt.wantError)
			}
			if _, err := s.ListPluginSettings(ctx); err == nil ||
				!strings.Contains(err.Error(), tt.wantError) ||
				!strings.Contains(err.Error(), pluginID) {
				t.Fatalf("ListPluginSettings error = %v, want error containing %q and %q", err, tt.wantError, pluginID)
			}
		})
	}
}
