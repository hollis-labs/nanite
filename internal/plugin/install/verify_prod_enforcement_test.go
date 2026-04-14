//go:build !devmode

package install

import (
	"context"
	"strings"
	"testing"
)

// TestVerify_ProductionRefusesUnsigned asserts that in a production build
// (no `devmode` tag) a SignatureVerifier rejects an archive that has no
// signature even when the operator sets AllowUnsigned=true. This is the
// safe-by-default invariant Track J.2 encodes: user_settings.allow_unsigned_plugins
// is inert in production.
func TestVerify_ProductionRefusesUnsigned(t *testing.T) {
	body := []byte("archive bytes")
	archive := writeTempArchive(t, body)

	v := &SignatureVerifier{
		// No KeyLookup, no signature on the handle. In devmode with
		// AllowUnsigned this would pass integrity-only; in prod it must fail.
		AllowUnsigned: true,
	}
	err := v.Verify(context.Background(), Handle{
		Kind:           "archive",
		Path:           archive,
		ExpectedSHA256: sum(body),
		// Signature, SignerKeyID deliberately empty.
	})
	if err == nil {
		t.Fatal("expected Verify to fail in production build when archive is unsigned, got nil")
	}
	if !strings.Contains(err.Error(), "missing signature") {
		t.Fatalf("expected 'missing signature' error, got %v", err)
	}
}
