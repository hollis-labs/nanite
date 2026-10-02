package pluginapi

import (
	"context"
	"fmt"

	"github.com/hollis-labs/plugin-sdk/manifest"
)

// CoreReference retains core IDs as references, never a copy of core messages.
// Deleted messages leave plugin data intact; ResolveReference lets a plugin
// explicitly handle missing references rather than deleting its own records.
type CoreReference struct {
	SessionID string `json:"session_id"`
	MessageID string `json:"message_id"`
}

type QueryMessageReferencesData struct {
	References []QueryMessageReference `json:"references"`
	More       bool                    `json:"more"`
}

type QueryMessageReference struct {
	SessionID string `json:"session_id"`
	MessageID string `json:"message_id"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
}

func (client *QueryClient) ResolveReference(ctx context.Context, reference CoreReference) (QueryMessageReference, error) {
	if !validQuerySession(reference.SessionID) || !validQuerySession(reference.MessageID) {
		return QueryMessageReference{}, fmt.Errorf("pluginapi: invalid core reference")
	}
	response, err := client.Query(ctx, QueryRequest{Resource: QueryMessageReferences, SessionID: reference.SessionID, MessageID: reference.MessageID, Limit: 1})
	if err != nil {
		return QueryMessageReference{}, err
	}
	var data QueryMessageReferencesData
	if err := manifest.DecodeExtension(response.Data, &data); err != nil {
		return QueryMessageReference{}, err
	}
	if len(data.References) != 1 || data.More || data.References[0].SessionID != reference.SessionID || data.References[0].MessageID != reference.MessageID {
		return QueryMessageReference{}, fmt.Errorf("pluginapi: core reference unavailable or mismatched")
	}
	return data.References[0], nil
}
