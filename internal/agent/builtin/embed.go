package builtin

import (
	_ "embed"

	"github.com/hollis-labs/nanite/internal/agent"
)

//go:embed default.md
var defaultAgentMD []byte

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
