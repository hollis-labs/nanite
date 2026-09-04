package workflowhost

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/go-workflow/values"
)

func TestArtifactStoreStreamsPreservesTypedMetadataAndSurvivesReopen(t *testing.T) {
	root := canonicalArtifactTestRoot(t)
	store := newArtifactTestStore(t, root, allowArtifactAuthorizer{})
	content := bytes.Repeat([]byte("streamed-artifact-"), 140_000)
	request := artifactTestPutRequest(values.RetentionRun, values.RedactionSecret, "run-sensitive")
	wantSize := int64(len(content))
	request.ExpectedSize = &wantSize
	request.ExpectedDigest = values.SHA256Digest(content)
	request.MaxBytes = wantSize + 1

	metadata, err := store.Put(t.Context(), request, bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if metadata.Ref.Store != ArtifactStoreName || metadata.Ref.Digest != request.ExpectedDigest ||
		metadata.Ref.SizeBytes != wantSize || metadata.Ref.Producer != request.Metadata.Producer ||
		metadata.Ref.Redaction != request.Metadata.Redaction || metadata.Ref.Retention != request.Metadata.Retention ||
		metadata.Owner != request.Owner {
		t.Fatalf("typed metadata changed: %#v", metadata)
	}
	if strings.Contains(metadata.Ref.URI, request.Owner.ID) || strings.Contains(metadata.Ref.URI, root) {
		t.Fatalf("URI leaks owner or local root: %q", metadata.Ref.URI)
	}

	stat, err := store.Stat(t.Context(), request.Access, metadata.Ref)
	if err != nil || !reflect.DeepEqual(stat, metadata) {
		t.Fatalf("Stat=%#v err=%v, want %#v", stat, err, metadata)
	}
	reader, err := store.Open(t.Context(), request.Access, metadata.Ref)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	got, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(got, content) || !reader.Verified() {
		t.Fatalf("ReadAll bytes=%d err=%v verified=%t", len(got), err, reader.Verified())
	}
	if closeErr := reader.Close(); closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}
	if closeErr := reader.Close(); closeErr != nil {
		t.Fatalf("second Close: %v", closeErr)
	}

	locator, err := parseArtifactRef(metadata.Ref)
	if err != nil {
		t.Fatal(err)
	}
	directory := store.artifactDirectory(locator)
	assertArtifactMode(t, directory, 0o700)
	assertArtifactMode(t, filepath.Join(directory, artifactPayloadName), 0o600)
	assertArtifactMode(t, filepath.Join(directory, artifactManifestName), 0o600)

	reopened := newArtifactTestStore(t, root, allowArtifactAuthorizer{})
	reopenedMetadata, err := reopened.Stat(t.Context(), request.Access, metadata.Ref)
	if err != nil || !reflect.DeepEqual(reopenedMetadata, metadata) {
		t.Fatalf("reopened Stat=%#v err=%v, want %#v", reopenedMetadata, err, metadata)
	}
	replayed, err := reopened.Put(t.Context(), request, bytes.NewReader(content))
	if err != nil || !reflect.DeepEqual(replayed, metadata) {
		t.Fatalf("reopened idempotent Put=%#v err=%v, want %#v", replayed, err, metadata)
	}
}

func TestEngineExposesProductionArtifactCapturePath(t *testing.T) {
	_, state := openWorkflowStateTest(t, filepath.Join(t.TempDir(), "artifact-composition.db"))
	configuredRoot := canonicalArtifactTestRoot(t)
	artifactStore := newArtifactTestStore(t, WorkflowArtifactRoot(configuredRoot), ArtifactAccessAuthorizer{})
	if artifactStore.root != filepath.Join(configuredRoot, workflowArtifactDirectory) {
		t.Fatalf("artifact root=%q, want namespaced configured root", artifactStore.root)
	}
	configuredEntries, err := os.ReadDir(configuredRoot)
	if err != nil || len(configuredEntries) != 1 || configuredEntries[0].Name() != workflowArtifactDirectory {
		t.Fatalf("configured root entries=%v err=%v, want only %q", configuredEntries, err, workflowArtifactDirectory)
	}
	engine, err := NewEngine(state)
	if err != nil {
		t.Fatal(err)
	}
	if got := engine.WithArtifactStore(artifactStore); got != engine || engine.ArtifactStore() != artifactStore {
		t.Fatal("engine did not expose its configured artifact adapter")
	}
	request := artifactTestPutRequest(values.RetentionRun, values.RedactionPrivate, "run-composed")
	request.Access.RunID = request.Owner.ID
	value, err := values.CaptureValue(t.Context(), engine.ArtifactStore(), request, strings.NewReader("captured"), values.CapturePolicy{
		Mode: values.CaptureArtifactOnly, InlineLimit: values.DefaultInlineLimit,
	})
	if err != nil {
		t.Fatalf("CaptureValue through Engine.ArtifactStore: %v", err)
	}
	if err := value.Validate(); err != nil || value.Type != values.TypeArtifact || value.Artifact == nil || value.Artifact.Store != ArtifactStoreName {
		t.Fatalf("captured typed value=%#v err=%v", value, err)
	}
}

func TestArtifactStoreBoundsWritesRejectsTamperAndEnforcesExpiry(t *testing.T) {
	store := newArtifactTestStore(t, canonicalArtifactTestRoot(t), allowArtifactAuthorizer{})
	content := []byte("content beyond cap")
	request := artifactTestPutRequest(values.RetentionRun, values.RedactionPrivate, "run-bounds")
	request.MaxBytes = int64(len(content) - 1)
	counting := &artifactCountingReader{reader: bytes.NewReader(content)}
	if _, err := store.Put(t.Context(), request, counting); !errors.Is(err, values.ErrArtifactSizeLimit) {
		t.Fatalf("bounded Put error=%v", err)
	}
	if counting.read > request.MaxBytes+1 {
		t.Fatalf("bounded Put consumed %d bytes, max allowed observation %d", counting.read, request.MaxBytes+1)
	}
	staging, err := os.ReadDir(filepath.Join(store.root, "staging"))
	if err != nil || len(staging) != 0 {
		t.Fatalf("failed Put left staging entries=%v err=%v", staging, err)
	}

	request = artifactTestPutRequest(values.RetentionRun, values.RedactionPrivate, "run-tamper")
	request.ExpiresAt = request.CreatedAt.Add(time.Hour)
	metadata, err := store.Put(t.Context(), request, bytes.NewReader([]byte("original")))
	if err != nil {
		t.Fatal(err)
	}
	locator, _ := parseArtifactRef(metadata.Ref)
	payload := filepath.Join(store.artifactDirectory(locator), artifactPayloadName)
	if err := os.WriteFile(payload, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stat(t.Context(), request.Access, metadata.Ref); !errors.Is(err, values.ErrArtifactDigest) {
		t.Fatalf("Stat did not reject same-size tamper: %v", err)
	}
	if _, err := store.Open(t.Context(), request.Access, metadata.Ref); !errors.Is(err, values.ErrArtifactDigest) {
		t.Fatalf("Open did not reject same-size tamper: %v", err)
	}

	expiredAccess := request.Access
	expiredAccess.At = request.ExpiresAt
	if _, err := store.Stat(t.Context(), expiredAccess, metadata.Ref); !errors.Is(err, values.ErrArtifactExpired) {
		t.Fatalf("expired Stat error=%v", err)
	}
}

func TestArtifactStoreAuthorizesBeforeResolutionAndConfinesPaths(t *testing.T) {
	root := canonicalArtifactTestRoot(t)
	recorder := &artifactRecordingAuthorizer{}
	store := newArtifactTestStore(t, root, recorder)
	request := artifactTestPutRequest(values.RetentionRun, values.RedactionSecret, "run-auth")
	metadata, err := store.Put(t.Context(), request, strings.NewReader("secret material"))
	if err != nil {
		t.Fatal(err)
	}

	malformed := metadata.Ref
	malformed.URI = strings.Replace(malformed.URI, "/run/", "/../", 1)
	baseline := recorder.callCount()
	if _, statErr := store.Stat(t.Context(), request.Access, malformed); !errors.Is(statErr, values.ErrArtifactInvalid) {
		t.Fatalf("malformed URI error=%v", statErr)
	}
	if recorder.callCount() != baseline {
		t.Fatal("malformed URI reached authorization")
	}

	recorder.setDeny(errors.New("private policy detail"))
	originalObjects := filepath.Join(root, "objects")
	movedObjects := originalObjects + ".moved"
	if renameErr := os.Rename(originalObjects, movedObjects); renameErr != nil {
		t.Fatal(renameErr)
	}
	redirect := canonicalArtifactTestRoot(t)
	if symlinkErr := os.Symlink(redirect, originalObjects); symlinkErr != nil {
		t.Fatal(symlinkErr)
	}
	_, err = store.Open(t.Context(), request.Access, metadata.Ref)
	if !errors.Is(err, values.ErrArtifactUnauthorized) || err.Error() != "artifact open failed: unauthorized" {
		t.Fatalf("authorization did not precede replaced path resolution: %v", err)
	}
	entries, readErr := os.ReadDir(redirect)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("denied resolution touched redirect: entries=%v err=%v", entries, readErr)
	}
}

func TestArtifactStoreRejectsIntermediateOwnerSymlink(t *testing.T) {
	store := newArtifactTestStore(t, canonicalArtifactTestRoot(t), allowArtifactAuthorizer{})
	request := artifactTestPutRequest(values.RetentionRun, values.RedactionPrivate, "run-symlink")
	metadata, err := store.Put(t.Context(), request, strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	locator, _ := parseArtifactRef(metadata.Ref)
	ownerDirectory := filepath.Join(store.root, "objects", string(locator.scope), locator.ownerHash)
	movedOwner := ownerDirectory + ".moved"
	if renameErr := os.Rename(ownerDirectory, movedOwner); renameErr != nil {
		t.Fatal(renameErr)
	}
	if symlinkErr := os.Symlink(movedOwner, ownerDirectory); symlinkErr != nil {
		t.Fatal(symlinkErr)
	}
	if _, statErr := store.Stat(t.Context(), request.Access, metadata.Ref); !errors.Is(statErr, values.ErrArtifactInvalid) {
		t.Fatalf("intermediate owner symlink Stat error=%v", statErr)
	}
}

func TestArtifactAccessAuthorizerEnforcesExactOwnerClaims(t *testing.T) {
	store := newArtifactTestStore(t, canonicalArtifactTestRoot(t), ArtifactAccessAuthorizer{})
	request := artifactTestPutRequest(values.RetentionRun, values.RedactionPrivate, "run-owner")
	request.Access.RunID = request.Owner.ID
	metadata, err := store.Put(t.Context(), request, strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	wrong := request.Access
	wrong.RunID = "another-run"
	if _, err := store.Stat(t.Context(), wrong, metadata.Ref); !errors.Is(err, values.ErrArtifactUnauthorized) {
		t.Fatalf("wrong run owner Stat error=%v", err)
	}
	if _, err := store.Open(t.Context(), wrong, metadata.Ref); !errors.Is(err, values.ErrArtifactUnauthorized) {
		t.Fatalf("wrong run owner Open error=%v", err)
	}
	if _, err := store.Delete(t.Context(), values.ArtifactDeleteRequest{Access: wrong, Ref: metadata.Ref}); !errors.Is(err, values.ErrArtifactUnauthorized) {
		t.Fatalf("wrong run owner Delete error=%v", err)
	}
	if _, err := store.Stat(t.Context(), request.Access, metadata.Ref); err != nil {
		t.Fatalf("unauthorized delete changed artifact: %v", err)
	}
}

func TestArtifactStoreConcurrentIdenticalPutConvergesAndDeleteIsIdempotent(t *testing.T) {
	store := newArtifactTestStore(t, canonicalArtifactTestRoot(t), allowArtifactAuthorizer{})
	request := artifactTestPutRequest(values.RetentionProject, values.RedactionPrivate, "project-concurrent")
	content := bytes.Repeat([]byte("concurrent"), 10_000)
	const workers = 12
	refs := make(chan values.ArtifactRef, workers)
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			metadata, err := store.Put(context.Background(), request, bytes.NewReader(content))
			if err != nil {
				errs <- err
				return
			}
			refs <- metadata.Ref
		}()
	}
	group.Wait()
	close(refs)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Put: %v", err)
	}
	var first values.ArtifactRef
	for ref := range refs {
		if first.Store == "" {
			first = ref
		} else if ref != first {
			t.Fatalf("concurrent refs differ: %#v != %#v", ref, first)
		}
	}

	result, err := store.Delete(t.Context(), values.ArtifactDeleteRequest{Access: request.Access, Ref: first})
	if err != nil || result.Outcome != values.ArtifactCleanupDeleted || result.DeletedCount != 1 || result.Validate() != nil {
		t.Fatalf("Delete=%#v err=%v", result, err)
	}
	repeated, err := store.Delete(t.Context(), values.ArtifactDeleteRequest{Access: request.Access, Ref: first})
	if err != nil || repeated.Outcome != values.ArtifactCleanupAlreadyAbsent || repeated.Validate() != nil {
		t.Fatalf("repeated Delete=%#v err=%v", repeated, err)
	}
}

func TestArtifactStoreCleanupRetentionBoundaries(t *testing.T) {
	store := newArtifactTestStore(t, canonicalArtifactTestRoot(t), allowArtifactAuthorizer{})
	runRequest := artifactTestPutRequest(values.RetentionRun, values.RedactionSecret, "run-clean")
	runMetadata, err := store.Put(t.Context(), runRequest, strings.NewReader("secret"))
	if err != nil {
		t.Fatal(err)
	}
	projectRequest := artifactTestPutRequest(values.RetentionProject, values.RedactionPrivate, "project-clean")
	projectMetadata, err := store.Put(t.Context(), projectRequest, strings.NewReader("project"))
	if err != nil {
		t.Fatal(err)
	}

	runCleanup := values.ArtifactCleanupRequest{Access: runRequest.Access, Kind: values.ArtifactCleanupRun, Owner: runRequest.Owner}
	result, err := store.Cleanup(t.Context(), runCleanup)
	if err != nil || result.Outcome != values.ArtifactCleanupDeleted || result.DeletedCount != 1 || result.Validate() != nil {
		t.Fatalf("run Cleanup=%#v err=%v", result, err)
	}
	if strings.Contains(string(result.Outcome), runMetadata.Ref.Digest) {
		t.Fatal("cleanup result exposed digest")
	}
	repeated, err := store.Cleanup(t.Context(), runCleanup)
	if err != nil || repeated.Outcome != values.ArtifactCleanupAlreadyAbsent || repeated.Validate() != nil {
		t.Fatalf("repeated run Cleanup=%#v err=%v", repeated, err)
	}
	if _, statErr := store.Stat(t.Context(), projectRequest.Access, projectMetadata.Ref); statErr != nil {
		t.Fatalf("run cleanup deleted project artifact: %v", statErr)
	}

	expiring := artifactTestPutRequest(values.RetentionRun, values.RedactionPrivate, "run-expiring")
	expiring.ExpiresAt = expiring.CreatedAt.Add(time.Minute)
	if _, putErr := store.Put(t.Context(), expiring, strings.NewReader("expiring")); putErr != nil {
		t.Fatal(putErr)
	}
	expired, err := store.Cleanup(t.Context(), values.ArtifactCleanupRequest{
		Access: expiring.Access, Kind: values.ArtifactCleanupExpired, Before: expiring.ExpiresAt,
	})
	if err != nil || expired.Outcome != values.ArtifactCleanupDeleted || expired.DeletedCount != 1 {
		t.Fatalf("expired Cleanup=%#v err=%v", expired, err)
	}

	partial := filepath.Join(store.root, "staging", "partial-crash")
	if mkdirErr := os.Mkdir(partial, 0o700); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	old := runRequest.CreatedAt.Add(-time.Hour)
	if timeErr := os.Chtimes(partial, old, old); timeErr != nil {
		t.Fatal(timeErr)
	}
	partials, err := store.Cleanup(t.Context(), values.ArtifactCleanupRequest{
		Access: runRequest.Access, Kind: values.ArtifactCleanupPartials, Before: runRequest.CreatedAt,
	})
	if err != nil || partials.Outcome != values.ArtifactCleanupDeleted || partials.DeletedCount != 1 {
		t.Fatalf("partials Cleanup=%#v err=%v", partials, err)
	}

	none, err := store.Cleanup(t.Context(), values.ArtifactCleanupRequest{Access: runRequest.Access, Kind: values.ArtifactCleanupNone})
	if err != nil || none.Outcome != values.ArtifactCleanupNotStored || none.Validate() != nil {
		t.Fatalf("none Cleanup=%#v err=%v", none, err)
	}
	externalRef := artifactExternalTestRef()
	preserved, err := store.Cleanup(t.Context(), values.ArtifactCleanupRequest{
		Access: runRequest.Access, Kind: values.ArtifactCleanupExternal, Ref: &externalRef,
	})
	if err != nil || preserved.Outcome != values.ArtifactCleanupPreservedExternal || preserved.Validate() != nil {
		t.Fatalf("external Cleanup=%#v err=%v", preserved, err)
	}
	preserved, err = store.Delete(t.Context(), values.ArtifactDeleteRequest{Access: runRequest.Access, Ref: externalRef})
	if err != nil || preserved.Outcome != values.ArtifactCleanupPreservedExternal || preserved.Validate() != nil {
		t.Fatalf("external Delete=%#v err=%v", preserved, err)
	}
}

type allowArtifactAuthorizer struct{}

func (allowArtifactAuthorizer) AuthorizeArtifact(context.Context, values.ArtifactAuthorization) error {
	return nil
}

type artifactRecordingAuthorizer struct {
	mu    sync.Mutex
	deny  error
	calls []values.ArtifactAuthorization
}

func (authorizer *artifactRecordingAuthorizer) AuthorizeArtifact(_ context.Context, request values.ArtifactAuthorization) error {
	authorizer.mu.Lock()
	defer authorizer.mu.Unlock()
	authorizer.calls = append(authorizer.calls, request)
	return authorizer.deny
}

func (authorizer *artifactRecordingAuthorizer) callCount() int {
	authorizer.mu.Lock()
	defer authorizer.mu.Unlock()
	return len(authorizer.calls)
}

func (authorizer *artifactRecordingAuthorizer) setDeny(err error) {
	authorizer.mu.Lock()
	defer authorizer.mu.Unlock()
	authorizer.deny = err
}

type artifactCountingReader struct {
	reader io.Reader
	read   int64
}

func (reader *artifactCountingReader) Read(buffer []byte) (int, error) {
	n, err := reader.reader.Read(buffer)
	reader.read += int64(n)
	return n, err
}

func canonicalArtifactTestRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func newArtifactTestStore(t *testing.T, root string, authorizer values.ArtifactAuthorizer) *ArtifactStore {
	t.Helper()
	store, err := NewArtifactStore(root, authorizer)
	if err != nil {
		t.Fatalf("NewArtifactStore: %v", err)
	}
	return store
}

func artifactTestAccess() values.ArtifactAccess {
	return values.ArtifactAccess{
		Principal: "worker-1", RunID: "run-1", ProjectID: "project-1",
		At: time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC),
	}
}

func artifactTestPutRequest(retention values.RetentionClass, redaction values.RedactionClass, ownerID string) values.ArtifactPutRequest {
	scope := values.ArtifactOwnerRun
	if retention == values.RetentionProject {
		scope = values.ArtifactOwnerProject
	}
	return values.ArtifactPutRequest{
		Store: ArtifactStoreName,
		Owner: values.ArtifactOwner{Scope: scope, ID: ownerID},
		Metadata: values.Metadata{
			Producer:  values.Producer{Kind: "node_output", Reference: "invocation-1", Output: "result"},
			MediaType: "application/octet-stream", Redaction: redaction, Retention: retention,
		},
		MaxBytes: 8 << 20, CreatedAt: artifactTestAccess().At, Access: artifactTestAccess(),
	}
}

func artifactExternalTestRef() values.ArtifactRef {
	content := []byte("external")
	return values.ArtifactRef{
		Store: "external-vault", URI: "s3://bucket/object", Digest: values.SHA256Digest(content),
		MediaType: "application/octet-stream", SizeBytes: int64(len(content)),
		Producer:  values.Producer{Kind: "external", Reference: "object-1"},
		Redaction: values.RedactionPrivate, Retention: values.RetentionExternal,
	}
}

func assertArtifactMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode=%o want=%o", filepath.Base(path), got, want)
	}
}

var _ values.ArtifactAuthorizer = allowArtifactAuthorizer{}
