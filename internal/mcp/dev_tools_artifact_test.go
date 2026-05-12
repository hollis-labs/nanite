package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubArtifactResolver is a tiny in-memory ArtifactResolver for testing
// dev_read's stash-pointer entrypoint without spinning up the full store.
type stubArtifactResolver struct {
	rows map[string]*ArtifactMeta
	err  error
}

func (s *stubArtifactResolver) GetArtifact(id string) (*ArtifactMeta, error) {
	if s.err != nil {
		return nil, s.err
	}
	row, ok := s.rows[id]
	if !ok {
		return nil, errArtifactNotFound
	}
	return row, nil
}

func TestDevRead_ArtifactID_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	artifactsRoot := tmp + "/artifacts/sess-x"
	if err := os.MkdirAll(artifactsRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := "line A\nline B\nline C\n"
	storagePath := artifactsRoot + "/art-stash-deadbeef12345678.txt"
	if err := os.WriteFile(storagePath, []byte(body), 0o644); err != nil {
		t.Fatalf("write artifact body: %v", err)
	}

	resolver := &stubArtifactResolver{rows: map[string]*ArtifactMeta{
		"art-stash-deadbeef12345678": {
			ID:          "art-stash-deadbeef12345678",
			StoragePath: storagePath,
			MimeType:    "text/plain",
			SizeBytes:   int64(len(body)),
		},
	}}
	dev := NewDevToolsTransport(nil).WithArtifactResolver(resolver, tmp+"/artifacts")

	res, err := dev.CallTool(context.Background(), "dev_read", map[string]any{
		"artifact_id": "art-stash-deadbeef12345678",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %+v", res)
	}
	got := res.Content[0].Text
	for _, want := range []string{"line A", "line B", "line C"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q in %q", want, got)
		}
	}
}

func TestDevRead_ArtifactID_PathAndArtifactIDMutuallyExclusive(t *testing.T) {
	dev := NewDevToolsTransport(nil)
	res, err := dev.CallTool(context.Background(), "dev_read", map[string]any{
		"artifact_id": "art-x",
		"path":        "/some/path",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error when both path and artifact_id are passed")
	}
	if !strings.Contains(res.Content[0].Text, "mutually exclusive") {
		t.Errorf("error message should explain mutual exclusion, got %q", res.Content[0].Text)
	}
}

func TestDevRead_NeitherPathNorArtifactID(t *testing.T) {
	dev := NewDevToolsTransport(nil)
	res, err := dev.CallTool(context.Background(), "dev_read", map[string]any{})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error when neither path nor artifact_id is passed")
	}
}

func TestDevRead_ArtifactID_NoResolverConfigured(t *testing.T) {
	// When the transport was constructed without a resolver, dev_read
	// with artifact_id returns a clear error rather than panicking.
	dev := NewDevToolsTransport(nil)
	res, err := dev.CallTool(context.Background(), "dev_read", map[string]any{
		"artifact_id": "art-x",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error when no resolver is configured")
	}
	if !strings.Contains(res.Content[0].Text, "not configured") {
		t.Errorf("error should mention missing config, got %q", res.Content[0].Text)
	}
}

func TestDevRead_ArtifactID_NotFound(t *testing.T) {
	resolver := &stubArtifactResolver{rows: map[string]*ArtifactMeta{}}
	dev := NewDevToolsTransport(nil).WithArtifactResolver(resolver, "/tmp/artifacts")
	res, err := dev.CallTool(context.Background(), "dev_read", map[string]any{
		"artifact_id": "art-nonexistent",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for missing artifact")
	}
	if !strings.Contains(res.Content[0].Text, "not found") {
		t.Errorf("error should mention not-found, got %q", res.Content[0].Text)
	}
}

func TestDevRead_ArtifactID_BlocksStoragePathOutsideRoot(t *testing.T) {
	// Defense in depth: a corrupted artifact row with a storage_path
	// outside the configured artifacts root must NOT be served via
	// dev_read. The artifact stasher confines paths at write time, but
	// dev_read re-checks at read time.
	tmp := t.TempDir()
	rogueFile := tmp + "/rogue.txt"
	if err := os.WriteFile(rogueFile, []byte("secret"), 0o644); err != nil {
		t.Fatalf("create rogue: %v", err)
	}
	artifactsRoot := tmp + "/artifacts"
	if err := os.MkdirAll(artifactsRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	resolver := &stubArtifactResolver{rows: map[string]*ArtifactMeta{
		"art-bad": {
			ID:          "art-bad",
			StoragePath: rogueFile, // outside artifactsRoot
		},
	}}
	dev := NewDevToolsTransport(nil).WithArtifactResolver(resolver, artifactsRoot)
	res, err := dev.CallTool(context.Background(), "dev_read", map[string]any{
		"artifact_id": "art-bad",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error for storage_path outside artifacts root")
	}
	if !strings.Contains(res.Content[0].Text, "outside artifacts root") {
		t.Errorf("error should mention root-escape, got %q", res.Content[0].Text)
	}
}

func TestDevRead_ArtifactID_PropagatesResolverError(t *testing.T) {
	// A non-found-error from the resolver (e.g. SQL backend failure)
	// should surface as a tool-level error, not panic.
	resolver := &stubArtifactResolver{err: errors.New("backend exploded")}
	dev := NewDevToolsTransport(nil).WithArtifactResolver(resolver, "/tmp/artifacts")
	res, err := dev.CallTool(context.Background(), "dev_read", map[string]any{
		"artifact_id": "art-x",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error when resolver fails")
	}
	if !strings.Contains(res.Content[0].Text, "backend exploded") {
		t.Errorf("error should propagate backend message, got %q", res.Content[0].Text)
	}
}

func TestStoreArtifactResolver_NilStoreReturnsNil(t *testing.T) {
	// Construction guard: passing a nil store should produce a nil
	// resolver so callers can pass directly into WithArtifactResolver
	// without nil-checks at the call site.
	r := NewStoreArtifactResolver(nil)
	if r != nil {
		t.Error("NewStoreArtifactResolver(nil) should return nil")
	}
}

// Quick sanity: WithArtifactResolver returns the same transport so
// callers can fluent-chain it inline. This is the construction shape
// used in cmd/nanite/main.go and mcpserver/server.go.
func TestWithArtifactResolver_FluentChainable(t *testing.T) {
	d := NewDevToolsTransport(nil)
	got := d.WithArtifactResolver(nil, "/tmp")
	if got != d {
		t.Error("WithArtifactResolver should return same transport (fluent shape)")
	}
	if d.ArtifactsRoot != "/tmp" {
		t.Errorf("ArtifactsRoot not set: got %q", d.ArtifactsRoot)
	}
}

// Compile-time guard: filepath import is used by stash-path checks; if
// future edits drop the dependency, this test catches the unused-import.
var _ = filepath.Separator
