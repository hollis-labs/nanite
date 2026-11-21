package warden

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/libs/security/secrets/tokenfile"
	"github.com/hollis-labs/nanite/internal/warden/wardentest"
)

// TestRecordFixtures records the Warden exchanges in wardentest/testdata
// from the live Warden, through a loopback proxy that keeps a scrubbed copy
// of each. It is skipped unless NANITE_RECORD_FIXTURES=1. It needs
// NANITE_RECORD_WARDEN_URL, NANITE_RECORD_CREDENTIAL and
// NANITE_RECORD_PRINCIPAL for an enrolled identity granted llm-gateway.chat.
//
// The parent credential rotates on every successful mint, and the Source
// saves each rotation itself: run one process at a time, and stop at the
// first ErrReenroll (a lost answer leaves the .inflight marker; Warden must
// re-enroll the identity, never present the spent parent again). Error
// answers come from requests that cannot spend the parent: a refusal Warden
// decides before rotating (invalid_scope), a throwaway parent that matches
// nothing (invalid_grant), and lookups.
func TestRecordFixtures(t *testing.T) {
	if os.Getenv("NANITE_RECORD_FIXTURES") != "1" {
		t.Skip("set NANITE_RECORD_FIXTURES=1 to record live fixtures")
	}
	env := func(key string) string {
		v := os.Getenv(key)
		if v == "" {
			t.Fatalf("%s is required", key)
		}
		return v
	}
	proxy := wardentest.NewRecordingProxy(t, env("NANITE_RECORD_WARDEN_URL"))
	src, err := New(Config{URL: proxy.URL, CredentialFile: env("NANITE_RECORD_CREDENTIAL"), Principal: env("NANITE_RECORD_PRINCIPAL"),
		Purpose: "Record Nanite Warden test fixtures", FailFastGrants: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	save := func(name string, want int) {
		t.Helper()
		taken := proxy.Take()
		if len(taken) != want {
			t.Fatalf("%s: %d exchanges, want %d", name, len(taken), want)
		}
		for i, f := range taken {
			n := name
			if want > 1 {
				n = name + "_" + string(rune('a'+i))
			}
			wardentest.CheckClean(t, n, f)
			if err := wardentest.Save(n, f); err != nil {
				t.Fatal(err)
			}
		}
	}
	stopOnReenroll := func(step string, err error) {
		t.Helper()
		if errors.Is(err, ErrReenroll) {
			t.Fatalf("%s: %v. STOP: the identity needs re-enrollment through Cerberus; do not retry", step, err)
		}
	}

	// A token for the gateway, and one for Warden itself (ground tier).
	if _, err := src.Token(ctx, "llm-gateway", []string{"llm-gateway.chat"}); err != nil {
		stopOnReenroll("mint llm-gateway", err)
		t.Fatalf("mint llm-gateway: %v", err)
	}
	save("mint_ok", 1)
	if _, err := src.Token(ctx, wardenAudience, requestScopes); err != nil {
		stopOnReenroll("mint warden", err)
		t.Fatalf("mint warden: %v", err)
	}
	save("mint_ok_warden", 1)

	// A grant request that does not exist (the Warden token is cached).
	_, err = src.Request(ctx, "req_nanite_fixture_missing")
	t.Logf("lookup of a missing request: %v", err)
	save("lookup_missing", 1)

	// No token at all for a lookup.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, proxy.URL+"/api/v1/requests/req_nanite_fixture_missing", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer not-a-token")
	if resp, err := http.DefaultClient.Do(req); err == nil {
		_ = resp.Body.Close()
	}
	save("lookup_unauthorized", 1)

	// A parent that matches nothing: invalid_grant, with no real credential
	// involved.
	junk := filepath.Join(t.TempDir(), "junk.cred")
	if err := tokenfile.Write(junk, "nanite-fixture-not-a-parent-credential"); err != nil {
		t.Fatal(err)
	}
	junkSrc, err := New(Config{URL: proxy.URL, CredentialFile: junk, Principal: "agent:nanite-fixture-junk", FailFastGrants: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = junkSrc.mint(ctx, "llm-gateway", []string{"llm-gateway.chat"})
	t.Logf("mint with a junk parent: %v", err)
	save("mint_invalid_grant", 1)

	// A scope the identity has no grant for: Warden refuses before it
	// rotates, so the parent is not spent. Mint directly, so no grant
	// request is filed.
	_, err = src.mint(ctx, "llm-gateway", []string{"llm-gateway.nanite-fixture-ungranted"})
	t.Logf("mint of an ungranted scope: %v", err)
	stopOnReenroll("mint ungranted scope", err)
	save("mint_invalid_scope", 1)
}
