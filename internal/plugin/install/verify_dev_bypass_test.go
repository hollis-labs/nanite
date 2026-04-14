//go:build devmode

package install

import (
	"context"
	"testing"
)

// TestVerify_DevmodeAllowsUnsigned asserts that in a devmode build, when
// AllowUnsigned is set (wired from user_settings.allow_unsigned_plugins),
// an archive without a signature is accepted as long as sha256 integrity
// still matches.
func TestVerify_DevmodeAllowsUnsigned(t *testing.T) {
	body := []byte("archive bytes")
	archive := writeTempArchive(t, body)

	v := &SignatureVerifier{AllowUnsigned: true}
	err := v.Verify(context.Background(), Handle{
		Kind:           "archive",
		Path:           archive,
		ExpectedSHA256: sum(body),
	})
	if err != nil {
		t.Fatalf("expected Verify to succeed under devmode+AllowUnsigned, got %v", err)
	}
}

// TestVerify_DevmodeShaMismatchStillFails asserts that archive integrity
// (sha256) is enforced even when signature verification is bypassed.
func TestVerify_DevmodeShaMismatchStillFails(t *testing.T) {
	body := []byte("archive bytes")
	archive := writeTempArchive(t, body)

	v := &SignatureVerifier{AllowUnsigned: true}
	err := v.Verify(context.Background(), Handle{
		Kind:           "archive",
		Path:           archive,
		ExpectedSHA256: sum([]byte("different bytes")),
	})
	if err == nil {
		t.Fatal("expected sha256 mismatch to fail even with AllowUnsigned")
	}
}
