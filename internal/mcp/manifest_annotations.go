package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/libs/plugin-mcp/plugin-sdk/manifest"
)

// manifestAnnotations snapshots the reviewed optional hints without assigning
// defaults or retaining pointers into a caller's manifest.
func manifestAnnotations(source *manifest.ToolAnnotations) (map[string]any, error) {
	if source == nil {
		return nil, nil
	}
	if source.ReadOnlyHint != nil && *source.ReadOnlyHint && source.DestructiveHint != nil && *source.DestructiveHint {
		return nil, fmt.Errorf("read-only and destructive annotations conflict")
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return nil, err
	}
	var annotations map[string]any
	if err := json.Unmarshal(raw, &annotations); err != nil {
		return nil, err
	}
	return annotations, nil
}
