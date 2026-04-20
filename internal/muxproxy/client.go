package muxproxy

import (
	"sync"

	agentmux "github.com/hollis-labs/go-agentmux-client"
)

// daemonEndpoint is hardcoded for the POC. Any consumer promoting this
// package beyond throwaway must replace with config-schema/env-var.
const daemonEndpoint = "unix:~/.agent-mux/run/muxd.sock"

// orchestratorProfileSlug is the agent-profile slug that activates
// the mux_* tool allowlist. Kept constant here for revert-friendliness.
// Referenced by Task 10 (agent profile wiring); the POC-wide revert
// benefit outweighs the brief unused-linter suppression.
const orchestratorProfileSlug = "mux-orchestrator" //nolint:unused // referenced by later task

var (
	clientOnce sync.Once
	clientVal  *agentmux.Client
)

// Client returns the singleton agent-mux daemon client. First call
// constructs the client; subsequent calls return the cached value.
func Client() *agentmux.Client {
	clientOnce.Do(func() {
		clientVal = agentmux.MustNew(daemonEndpoint)
	})
	return clientVal
}
