package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/apiauth"
)

func TestCredentialStandingReadsTheStoreOnEveryCall(t *testing.T) {
	ctx := context.Background()
	st, err := apiauth.Open(ctx, filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	mk := func(kind apiauth.Kind, scopes ...apiauth.Scope) apiauth.Credential {
		c, _, cerr := st.Create(ctx, apiauth.CreateParams{Kind: kind, Label: string(kind) + "-test", Scopes: scopes})
		if cerr != nil {
			t.Fatal(cerr)
		}
		return c
	}
	op := mk(apiauth.KindOperator)
	withTools := mk(apiauth.KindClient, apiauth.ScopeRead, apiauth.ScopeOperate, apiauth.ScopeTools)
	operateOnly := mk(apiauth.KindClient, apiauth.ScopeRead, apiauth.ScopeOperate)
	reader := credentialStanding{store: st}
	stand := func(id string) (bool, bool) {
		s, serr := reader.CallerStanding(ctx, id)
		if serr != nil {
			t.Fatalf("%s: %v", id, serr)
		}
		return s.Valid, s.Tools
	}
	if v, tl := stand(op.ID); !v || !tl {
		t.Fatalf("operator = valid %v tools %v", v, tl)
	}
	if v, tl := stand(withTools.ID); !v || !tl {
		t.Fatalf("client with tools = valid %v tools %v", v, tl)
	}
	if v, tl := stand(operateOnly.ID); !v || tl {
		t.Fatalf("operate-only client = valid %v tools %v, want valid without tools", v, tl)
	}
	if v, tl := stand("no-such-credential"); v || tl {
		t.Fatalf("unknown credential = valid %v tools %v", v, tl)
	}
	// Revoking is seen by the very next call: nothing is cached.
	if _, err = st.Revoke(ctx, withTools.ID); err != nil {
		t.Fatal(err)
	}
	if v, tl := stand(withTools.ID); v || tl {
		t.Fatalf("revoked credential = valid %v tools %v", v, tl)
	}
	// Expiry is judged against the clock at the time of asking.
	expiring, _, err := st.Create(ctx, apiauth.CreateParams{Kind: apiauth.KindClient, Label: "short", Scopes: []apiauth.Scope{apiauth.ScopeOperate, apiauth.ScopeTools}, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := stand(expiring.ID); !v {
		t.Fatal("unexpired credential reported invalid")
	}
	later := credentialStanding{store: st, now: func() time.Time { return time.Now().Add(2 * time.Hour) }}
	if s, _ := later.CallerStanding(ctx, expiring.ID); s.Valid || s.Tools {
		t.Fatalf("expired credential = %+v", s)
	}
	// A failing lookup is an error, never a verdict of "valid".
	_ = st.Close()
	if s, serr := reader.CallerStanding(ctx, op.ID); serr == nil || s.Valid {
		t.Fatalf("lookup failure produced %+v, %v", s, serr)
	}
}
