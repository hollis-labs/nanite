package apiauth

import (
	"log/slog"
	"os"
	"strings"
	"time"
)

const (
	DefaultAgentCredentialTTL    = time.Hour
	DefaultAgentCredentialMaxTTL = 24 * time.Hour
	AgentCredentialTTLEnv        = "NANITE_AGENT_CREDENTIAL_TTL"
	AgentCredentialMaxTTLEnv     = "NANITE_AGENT_CREDENTIAL_MAX_TTL"
	neverAgentCredentialTTL      = 10 * 365 * 24 * time.Hour
)

// agentCredentialTTLs returns the sliding lifetime and the absolute cap of an
// agent credential. Unset keeps the defaults; "never" is ten years; an invalid
// value warns and keeps the default.
func agentCredentialTTLs() (ttl, max time.Duration) {
	return envDuration(AgentCredentialTTLEnv, DefaultAgentCredentialTTL),
		envDuration(AgentCredentialMaxTTLEnv, DefaultAgentCredentialMaxTTL)
}

func envDuration(env string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(env))
	if raw == "" {
		return def
	}
	if strings.EqualFold(raw, "never") {
		return neverAgentCredentialTTL
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		slog.Warn("invalid duration; using default", "env", env, "default", def.String())
		return def
	}
	return d
}
