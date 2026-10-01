package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/go-safefs/pathsafe"
	"github.com/hollis-labs/nanite/internal/artifactstore"
	"github.com/hollis-labs/nanite/internal/store"
)

// DefaultArtifactsStorageDir matches config.DefaultAppConfig().Artifacts.StorageDir.
// It is the artifacts root when none is configured.
const DefaultArtifactsStorageDir = artifactstore.DefaultStorageDir

// ArtifactTempFile is an upload staging file.
type ArtifactTempFile interface {
	io.Writer
	io.Closer
	Name() string
}

// ArtifactTempFileFactory creates an upload staging file in dir.
type ArtifactTempFileFactory func(dir, pattern string) (ArtifactTempFile, error)

func defaultArtifactTempFile(dir, pattern string) (ArtifactTempFile, error) {
	return os.CreateTemp(dir, pattern)
}

// ArtifactErrorKind classifies an ArtifactError.
type ArtifactErrorKind int

const (
	// ArtifactInvalid is a request the storage rules reject.
	ArtifactInvalid ArtifactErrorKind = iota
	// ArtifactNotFound is a missing artifact row or file.
	ArtifactNotFound
	// ArtifactStorageFailure is a filesystem failure on the server's side.
	ArtifactStorageFailure
)

// ArtifactError reports why an artifact operation was refused or failed.
// Msg is meant for the caller.
type ArtifactError struct {
	Kind ArtifactErrorKind
	Msg  string
}

func (e *ArtifactError) Error() string { return e.Msg }

func artifactErr(kind ArtifactErrorKind, msg string) error {
	return &ArtifactError{Kind: kind, Msg: msg}
}

// ArtifactService owns session artifacts: their rows and the rules for the
// files behind them. Every file an artifact names lives under the artifacts
// root; uploads are written there, placed files must already be there, and
// downloads re-check it, so a legacy or corrupted row cannot serve a file
// from elsewhere. Store errors come back unwrapped; rule failures are
// *ArtifactError.
type ArtifactService struct {
	store      ArtifactStore
	root       func() string
	createTemp ArtifactTempFileFactory
}

// NewArtifactService builds the service. root returns the artifacts storage
// root at call time; nil or an empty result means DefaultArtifactsStorageDir.
func NewArtifactService(st ArtifactStore, root func() string) *ArtifactService {
	return &ArtifactService{store: st, root: root, createTemp: defaultArtifactTempFile}
}

// SetTempFileFactory replaces how upload staging files are created. Tests
// use it to inject write failures.
func (s *ArtifactService) SetTempFileFactory(f ArtifactTempFileFactory) {
	s.createTemp = f
}

// StorageDir returns the artifacts storage root.
func (s *ArtifactService) StorageDir() string {
	if s.root != nil {
		if dir := s.root(); dir != "" {
			return dir
		}
	}
	return DefaultArtifactsStorageDir
}

// List returns a session's artifacts.
func (s *ArtifactService) List(ctx context.Context, sessionID string) ([]store.Artifact, error) {
	return s.store.ListArtifacts(ctx, sessionID)
}

// ListByOrigin returns a session's artifacts of one origin.
func (s *ArtifactService) ListByOrigin(ctx context.Context, sessionID, origin string) ([]store.Artifact, error) {
	return s.store.ListArtifactsByOrigin(ctx, sessionID, origin)
}

// ListByProject returns the artifacts of a project's sessions, leaving out
// excludeSessionID's when it is set.
func (s *ArtifactService) ListByProject(ctx context.Context, projectID, excludeSessionID string) ([]store.Artifact, error) {
	return s.store.ListArtifactsByProject(ctx, projectID, excludeSessionID)
}

// UploadInput is an uploaded file to store as a session artifact.
type UploadInput struct {
	SessionID string
	MessageID string
	// Filename is the uploader's name for the file; it must be a single
	// path segment.
	Filename string
	// ContentType is the uploader's declared MIME type; empty or
	// application/octet-stream means detect it from the filename.
	ContentType string
	Content     io.Reader
}

// Upload writes an uploaded file under the artifacts root, at
// <root>/<session>/<filename>, and records it. The filename and session id
// must each be a single safe path segment (SanitizeUploadFilename). The file
// is staged beside its final path and renamed into place, so a failed write
// leaves any existing file at that path untouched and records nothing.
func (s *ArtifactService) Upload(ctx context.Context, in UploadInput) (*store.Artifact, error) {
	safeName, err := SanitizeUploadFilename(in.Filename)
	if err != nil {
		return nil, artifactErr(ArtifactInvalid, "invalid filename: "+err.Error())
	}
	// The session segment becomes a directory name, so a separator or
	// traversal sequence here would escape the root just as a bad filename
	// would.
	safeSession, err := SanitizeUploadFilename(in.SessionID)
	if err != nil {
		return nil, artifactErr(ArtifactInvalid, "invalid session_id: "+err.Error())
	}

	storageDir, err := pathsafe.ResolveUnder(s.StorageDir(), safeSession)
	if err != nil {
		return nil, artifactErr(ArtifactInvalid, "resolve session storage dir: "+err.Error())
	}
	// #nosec G301 -- the artifacts directory mode is unchanged from the handler this moved out of.
	if err = os.MkdirAll(storageDir, 0o755); err != nil {
		return nil, artifactErr(ArtifactStorageFailure, "failed to create storage directory")
	}
	// Belt-and-suspenders after the sanitation above, but it keeps a single
	// source of truth for path confinement.
	storagePath, err := pathsafe.ResolveUnder(storageDir, safeName)
	if err != nil {
		return nil, artifactErr(ArtifactInvalid, "resolve storage path: "+err.Error())
	}
	written, err := s.writeAtomically(storageDir, storagePath, in.Content)
	if err != nil {
		return nil, artifactErr(ArtifactStorageFailure, "failed to store file: "+err.Error())
	}

	mimeType := in.ContentType
	if mimeType == "" || mimeType == "application/octet-stream" {
		mimeType = mime.TypeByExtension(filepath.Ext(safeName))
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
	}

	artifact := &store.Artifact{
		SessionID:   safeSession,
		MessageID:   in.MessageID,
		Name:        safeName,
		MimeType:    mimeType,
		SizeBytes:   written,
		StoragePath: storagePath,
	}
	if err := s.store.CreateArtifact(ctx, artifact); err != nil {
		return nil, err
	}
	return artifact, nil
}

// writeAtomically stages content beside storagePath, closes it, then
// renames it into place. Copy, close and rename failures leave any existing
// file at storagePath untouched; a copy failure is reported over the
// staging file's close failure.
func (s *ArtifactService) writeAtomically(storageDir, storagePath string, src io.Reader) (int64, error) {
	tmp, err := s.createTemp(storageDir, ".nanite-artifact-*")
	if err != nil {
		return 0, fmt.Errorf("create upload temp file: %w", err)
	}
	tmpPath := tmp.Name()
	promoted := false
	defer func() {
		if !promoted {
			_ = os.Remove(tmpPath) // Staging cleanup must not replace the authoritative copy, close, or rename error.
		}
	}()

	written, err := io.Copy(tmp, src)
	if err != nil {
		_ = tmp.Close() // Preserve the copy failure; close is cleanup for the incomplete staging file.
		return written, fmt.Errorf("copy upload: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return written, fmt.Errorf("close upload temp file: %w", err)
	}
	// #nosec G703 -- both paths are created/resolved inside the same confined storage directory above.
	if err := os.Rename(tmpPath, storagePath); err != nil {
		return written, fmt.Errorf("promote upload: %w", err)
	}
	promoted = true
	return written, nil
}

// PlaceInput is an existing file to surface as a session artifact.
type PlaceInput struct {
	SessionID string
	MessageID string
	Name      string
	// MimeType empty means detect it from Name.
	MimeType       string
	StoragePath    string
	SourceAgentID  string
	SourcePluginID string
}

// Place records an existing file as an artifact with origin "placed". The
// file must be a regular file under the artifacts root
// (ResolveArtifactStoragePath); the recorded path is the resolved one and
// the size is read from the file.
func (s *ArtifactService) Place(ctx context.Context, in PlaceInput) (*store.Artifact, error) {
	file, err := artifactstore.InspectPlaced(s.StorageDir(), in.StoragePath, in.Name, in.MimeType)
	if err != nil {
		return nil, artifactErr(ArtifactInvalid, err.Error())
	}

	artifact := &store.Artifact{
		SessionID:      in.SessionID,
		MessageID:      in.MessageID,
		Name:           in.Name,
		MimeType:       file.MIME,
		SizeBytes:      file.Size,
		StoragePath:    file.Path,
		Origin:         store.ArtifactOriginPlaced,
		SourceAgentID:  in.SourceAgentID,
		SourcePluginID: in.SourcePluginID,
	}
	if err := s.store.CreateArtifact(ctx, artifact); err != nil {
		return nil, err
	}
	return artifact, nil
}

// Open returns an artifact and its file, opened for reading; the caller
// closes it. The stored path is re-checked against the artifacts root
// first, as defense in depth for legacy or corrupted rows.
func (s *ArtifactService) Open(ctx context.Context, id string) (*store.Artifact, *os.File, error) {
	artifact, err := s.store.GetArtifact(ctx, id)
	if err != nil {
		return nil, nil, artifactErr(ArtifactNotFound, "artifact not found")
	}
	resolved, err := ResolveArtifactStoragePath(s.StorageDir(), artifact.StoragePath)
	if err != nil {
		var escape *pathsafe.EscapeError
		if errors.As(err, &escape) {
			return nil, nil, artifactErr(ArtifactInvalid, "artifact path outside storage root")
		}
		return nil, nil, artifactErr(ArtifactStorageFailure, "resolve artifact path: "+err.Error())
	}
	f, err := os.Open(resolved) // #nosec G304 -- resolved is confined under the artifacts root just above
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, artifactErr(ArtifactNotFound, "file not found on disk")
		}
		return nil, nil, artifactErr(ArtifactStorageFailure, "open artifact: "+err.Error())
	}
	return artifact, f, nil
}

// SanitizeUploadFilename enforces a single-segment, separator-free,
// non-empty filename. It returns an error rather than silently rewriting the
// input, so an uploader expecting a specific filename sees the rejection.
//
// Rules:
//   - reject null bytes
//   - reject path separators (/, \) and ".." sequences
//   - reject empty / whitespace-only inputs
//   - the result of filepath.Base(input) must equal input after TrimSpace
//     (i.e. no directory segments were stripped)
//
// This function is the single authority for upload-filename validation.
func SanitizeUploadFilename(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", errors.New("filename is empty")
	}
	if strings.ContainsRune(trimmed, 0) {
		return "", errors.New("filename contains null byte")
	}
	if strings.ContainsAny(trimmed, `/\`) {
		return "", errors.New("filename contains path separator")
	}
	// Block ".." as a segment or sequence. filepath.Base(..) returns "..",
	// which Base alone would happily accept, so treat it explicitly.
	if trimmed == "." || trimmed == ".." || strings.Contains(trimmed, "..") {
		return "", errors.New("filename contains disallowed '..' sequence")
	}
	base := filepath.Base(trimmed)
	if base != trimmed {
		return "", errors.New("filename must be a single path segment")
	}
	if base == "." || base == string(filepath.Separator) {
		return "", errors.New("filename resolves to a directory marker")
	}
	return base, nil
}

// ResolveArtifactStoragePath applies the shared artifact storage confinement
// rule. Relative paths are relative to root; absolute paths must resolve under
// root. Download re-checks this rule for legacy or externally modified rows.
func ResolveArtifactStoragePath(root, storagePath string) (string, error) {
	return artifactstore.Resolve(root, storagePath)
}
