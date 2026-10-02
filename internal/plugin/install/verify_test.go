package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChecksumVerifier(t *testing.T) {
	body := []byte("plugin archive")
	path := filepath.Join(t.TempDir(), "archive.tar.gz")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	sha := hex.EncodeToString(digest[:])
	for _, sum := range []string{sha, strings.ToUpper(sha)} {
		if err := (&ChecksumVerifier{}).Verify(context.Background(), Handle{Kind: "archive", Path: path, ExpectedSHA256: sum}); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name   string
		handle Handle
		max    int64
	}{
		{"wrong catalog size", Handle{Kind: "archive", Path: path, ExpectedSHA256: sha, ExpectedSize: int64(len(body) + 1)}, 0},
		{"wrong digest", Handle{Kind: "archive", Path: path, ExpectedSHA256: strings.Repeat("0", 64)}, 0},
		{"malformed digest", Handle{Kind: "archive", Path: path, ExpectedSHA256: strings.Repeat("x", 64)}, 0},
		{"missing digest", Handle{Kind: "archive", Path: path}, 0},
		{"short digest", Handle{Kind: "archive", Path: path, ExpectedSHA256: "deadbeef"}, 0},
		{"missing path", Handle{Kind: "archive", ExpectedSHA256: sha}, 0},
		{"missing file", Handle{Kind: "archive", Path: path + ".missing", ExpectedSHA256: sha}, 0},
		{"directory", Handle{Kind: "archive", Path: filepath.Dir(path), ExpectedSHA256: sha}, 0},
		{"wrong kind", Handle{Kind: "directory", Path: path, ExpectedSHA256: sha}, 0},
		{"oversized", Handle{Kind: "archive", Path: path, ExpectedSHA256: sha}, int64(len(body) - 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := (&ChecksumVerifier{MaxArchiveBytes: tc.max}).Verify(context.Background(), tc.handle); err == nil {
				t.Fatal("accepted invalid archive")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (&ChecksumVerifier{}).Verify(ctx, Handle{Kind: "archive", Path: path, ExpectedSHA256: sha}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if err := (&ChecksumVerifier{MaxArchiveBytes: int64(len(body))}).Verify(context.Background(), Handle{Kind: "archive", Path: path, ExpectedSHA256: sha}); err != nil {
		t.Fatalf("exact size cap: %v", err)
	}
}
