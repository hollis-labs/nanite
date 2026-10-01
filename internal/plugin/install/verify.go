package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// ChecksumVerifier enforces archive integrity and bounded verification reads.
// A checksum describes bytes; it does not establish trust in their publisher.
type ChecksumVerifier struct{ MaxArchiveBytes int64 }

func (v *ChecksumVerifier) Verify(ctx context.Context, h Handle) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if h.Kind != "archive" {
		return fmt.Errorf("verify: unsupported handle kind %q", h.Kind)
	}
	if h.Path == "" {
		return errors.New("verify: empty path")
	}
	expected, err := hex.DecodeString(h.ExpectedSHA256)
	if err != nil || len(expected) != sha256.Size {
		return errors.New("verify: expected sha256 must be 64 hex characters")
	}
	max := v.MaxArchiveBytes
	if max <= 0 {
		max = DefaultMaxArchiveBytes
	}
	f, err := os.Open(h.Path)
	if err != nil {
		return fmt.Errorf("verify: open: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("verify: stat: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("verify: archive must be a regular file")
	}
	if info.Size() > max {
		return fmt.Errorf("verify: archive size %d exceeds cap %d", info.Size(), max)
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(&contextReader{ctx: ctx, reader: f}, max+1))
	if err != nil {
		return fmt.Errorf("verify: read: %w", err)
	}
	if n > max {
		return errors.New("verify: archive exceeds cap during read")
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(got, h.ExpectedSHA256) {
		return fmt.Errorf("verify: sha256 mismatch: got %s want %s", got, h.ExpectedSHA256)
	}
	return ctx.Err()
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
