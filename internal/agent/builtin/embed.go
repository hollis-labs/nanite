package builtin

import (
	_ "embed"

	"github.com/hollis-labs/nanite/internal/agent"
)

//go:embed default.md
var defaultAgentMD []byte

//go:embed mux-orchestrator.md
var muxOrchestratorMD []byte

// DefaultAgent returns the parsed built-in default agent definition.
func DefaultAgent() (*agent.Definition, error) {
	def, err := agent.ParseMD(defaultAgentMD)
	if err != nil {
		return nil, err
	}
	def.Source = "builtin"
	def.SourceRef = "embedded:default.md"
	return def, nil
}

// MuxOrchestratorAgent returns the built-in mux-orchestrator profile.
// POC — CW-20260420-0047.
func MuxOrchestratorAgent() (*agent.Definition, error) {
	def, err := agent.ParseMD(muxOrchestratorMD)
	if err != nil {
		return nil, err
	}
	def.Source = "builtin"
	def.SourceRef = "embedded:mux-orchestrator.md"
	return def, nil
}
