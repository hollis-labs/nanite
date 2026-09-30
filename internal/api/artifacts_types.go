package api

import "github.com/hollis-labs/nanite/internal/store"

// ArtifactView is a session artifact as the API returns it. Field order is
// the store row's; the source_* fields drop out when empty.
type ArtifactView struct {
	ID               string `json:"id"`
	SessionID        string `json:"session_id"`
	MessageID        string `json:"message_id"`
	Name             string `json:"name"`
	MimeType         string `json:"mime_type"`
	SizeBytes        int64  `json:"size_bytes"`
	StoragePath      string `json:"storage_path"`
	Metadata         string `json:"metadata"`
	Origin           string `json:"origin"`
	SourceToolCallID string `json:"source_tool_call_id,omitempty"`
	SourceAgentID    string `json:"source_agent_id,omitempty"`
	SourcePluginID   string `json:"source_plugin_id,omitempty"`
	CreatedAt        string `json:"created_at"`
}

func artifactToView(a *store.Artifact) ArtifactView {
	return ArtifactView{
		ID:               a.ID,
		SessionID:        a.SessionID,
		MessageID:        a.MessageID,
		Name:             a.Name,
		MimeType:         a.MimeType,
		SizeBytes:        a.SizeBytes,
		StoragePath:      a.StoragePath,
		Metadata:         a.Metadata,
		Origin:           a.Origin,
		SourceToolCallID: a.SourceToolCallID,
		SourceAgentID:    a.SourceAgentID,
		SourcePluginID:   a.SourcePluginID,
		CreatedAt:        a.CreatedAt,
	}
}

// artifactsToView never returns nil, so a missing list encodes as [], as the
// handlers have always answered.
func artifactsToView(artifacts []store.Artifact) []ArtifactView {
	out := make([]ArtifactView, len(artifacts))
	for i := range artifacts {
		out[i] = artifactToView(&artifacts[i])
	}
	return out
}
