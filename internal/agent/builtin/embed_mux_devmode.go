//go:build devmode

package builtin

import (
	_ "embed"

	"github.com/hollis-labs/nanite/internal/agent"
)

//go:embed mux-orchestrator.md
var muxOrchestratorMD []byte

// MuxOrchestratorAgent returns the built-in mux-orchestrator profile.
// POC — CW-20260420-0047. Only available in devmode builds.
func MuxOrchestratorAgent() (*agent.Definition, error) {
	def, err := agent.ParseMD(muxOrchestratorMD)
	if err != nil {
		return nil, err
	}
	def.Source = "builtin"
	def.SourceRef = "embedded:mux-orchestrator.md"
	return def, nil
}
