package chat

import (
	"context"

	toolresult "github.com/hollis-labs/libs/agent/core/toolresult"
	llmtypes "github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/tool"
)

// ReconcileCachedResults copies the model projection so neither persisted
// messages nor prior request snapshots are mutated as cached bodies expire.
func ReconcileCachedResults(ctx context.Context, cache *toolresult.Cache, sessionID string, messages []llmtypes.ChatMessage) []llmtypes.ChatMessage {
	projection := tool.NewResultProjection(ctx, cache, sessionID)
	out := append([]llmtypes.ChatMessage(nil), messages...)
	for i := range out {
		out[i].Content = projection.Text(out[i].Content)
		out[i].ContentBlocks = append([]llmtypes.ContentBlock(nil), out[i].ContentBlocks...)
		for j := range out[i].ContentBlocks {
			out[i].ContentBlocks[j].Content = projection.Text(out[i].ContentBlocks[j].Content)
			out[i].ContentBlocks[j].Text = projection.Text(out[i].ContentBlocks[j].Text)
		}
	}
	return out
}
