package engine

import (
	"reflect"
	"testing"

	"github.com/hollis-labs/libs/llm/core/llmtypes"
	"github.com/hollis-labs/nanite/internal/provider/fake"
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
			// The whole hint list, not just whatever hints arrived: no hints
			// at all must fail too.
			want := []llmtypes.CacheHint{{Position: "system", TTL: ttl}, {Position: "tools", TTL: ttl},
				{Position: "recent_message", Index: 0}, {Position: "recent_message", Index: 1}}
			if !reflect.DeepEqual(p.requests[0].CacheHints, want) {
				t.Fatalf("hints %+v, want %+v", p.requests[0].CacheHints, want)
			}
		})
	}
}
