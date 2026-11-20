package engine

import (
	"github.com/hollis-labs/nanite/internal/provider/fake"
	"testing"
)

func TestRuntimeCacheTTLHints(t *testing.T) {
	for _, ttl := range []string{"", "5m", "1h"} {
		t.Run("ttl="+ttl, func(t *testing.T) {
			p := &watchedProvider{Provider: fake.New(fake.Text("done"))}
			e, _, id := setupEngine(t, p, "runtime:\n  cache_ttl: '"+ttl+"'\n")
			run, err := e.Send(t.Context(), id, Input{Text: "go"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = run.Wait(); err != nil {
				t.Fatal(err)
			}
			if len(p.requests) != 1 {
				t.Fatalf("requests=%d", len(p.requests))
			}
			for _, hint := range p.requests[0].CacheHints {
				want := ""
				if hint.Position == "system" || hint.Position == "tools" {
					want = ttl
				}
				if hint.TTL != want {
					t.Fatalf("hint %+v want TTL %q", hint, want)
				}
			}
		})
	}
}
