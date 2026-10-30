package apiauth

import (
	"testing"
	"time"
)

func TestAgentCredentialTTLs(t *testing.T) {
	cases := []struct {
		name, ttl, max   string
		wantTTL, wantMax time.Duration
	}{
		{"default", "", "", time.Hour, 24 * time.Hour},
		{"override", "2h", "48h", 2 * time.Hour, 48 * time.Hour},
		{"never", "never", "never", neverAgentCredentialTTL, neverAgentCredentialTTL},
		{"invalid", "bogus", "-1h", time.Hour, 24 * time.Hour},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(AgentCredentialTTLEnv, c.ttl)
			t.Setenv(AgentCredentialMaxTTLEnv, c.max)
			ttl, max := agentCredentialTTLs()
			if ttl != c.wantTTL || max != c.wantMax {
				t.Fatalf("got %v/%v want %v/%v", ttl, max, c.wantTTL, c.wantMax)
			}
		})
	}
}
