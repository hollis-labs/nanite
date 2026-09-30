package anthropic

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/hollis-labs/nanite/internal/llm/keycheck"
)

func TestVerifyKey(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		wantErr      bool
		wantRejected bool
	}{
		{"accepted", http.StatusOK, false, false},
		{"401 is rejected", http.StatusUnauthorized, true, true},
		{"403 is rejected", http.StatusForbidden, true, true},
		{"500 is not a rejection", http.StatusInternalServerError, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath, gotKey string
			c := newStreamingTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				gotPath, gotKey = r.URL.Path, r.Header.Get("X-Api-Key")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				if tc.status == http.StatusOK {
					_, _ = w.Write([]byte(`{"data":[],"has_more":false,"first_id":null,"last_id":null}`))
					return
				}
				_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"nope"}}`))
			})

			err := c.VerifyKey(context.Background())
			if (err != nil) != tc.wantErr {
				t.Fatalf("VerifyKey err = %v, wantErr %v", err, tc.wantErr)
			}
			if got := errors.Is(err, keycheck.ErrRejected); got != tc.wantRejected {
				t.Fatalf("errors.Is(err, ErrRejected) = %v, want %v (err %v)", got, tc.wantRejected, err)
			}
			if gotPath != "/v1/models" || gotKey != "test-key" {
				t.Fatalf("request = %s key %q, want /v1/models with the configured key", gotPath, gotKey)
			}
		})
	}
}

func TestVerifyKey_UnreachableIsNotARejection(t *testing.T) {
	c := newStreamingTestClient(t, func(http.ResponseWriter, *http.Request) {})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := c.VerifyKey(ctx)
	if err == nil || errors.Is(err, keycheck.ErrRejected) {
		t.Fatalf("VerifyKey with no reachable API = %v, want a non-rejection error", err)
	}
}
