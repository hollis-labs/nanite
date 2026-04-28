//go:build !devmode

package builtin

import "github.com/hollis-labs/nanite/internal/agent"

// MuxOrchestratorAgent returns nil in non-devmode builds.
// The mux-orchestrator agent profile is not seeded in production binaries.
func MuxOrchestratorAgent() (*agent.Definition, error) {
	return nil, nil
}
