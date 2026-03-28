package store

import (
	"crypto/sha256"
	"encoding/hex"
)

// ComputeAgentHash returns a 16-character hex hash of the agent's content fields.
// This hash is stable across edits to non-content fields (name, tags, status)
// and changes only when the agent's functional definition changes.
func ComputeAgentHash(systemPrompt, tools, toolPermissions string) string {
	h := sha256.New()
	h.Write([]byte(systemPrompt))
	h.Write([]byte{0}) // separator
	h.Write([]byte(tools))
	h.Write([]byte{0})
	h.Write([]byte(toolPermissions))
	return hex.EncodeToString(h.Sum(nil))[:16]
}
