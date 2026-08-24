package api

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/hollis-labs/nanite/internal/pathsafe"
	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/store"
)

// defaultArtifactsStorageDir matches config.DefaultAppConfig().Artifacts.StorageDir.
// Used as a fallback when AppConfig is nil (bare API construction in tests).
const defaultArtifactsStorageDir = "data/artifacts"

type artifactTempFile interface {
	io.Writer
	io.Closer
	Name() string
}

type artifactTempFileFactory func(dir, pattern string) (artifactTempFile, error)

func defaultArtifactTempFile(dir, pattern string) (artifactTempFile, error) {
	return os.CreateTemp(dir, pattern)
}

// artifactsStorageDir returns the configured artifacts storage root, falling
// back to the default when AppConfig is unset.
func (a *API) artifactsStorageDir() string {
	if a.Services != nil && a.Services.AppConfig != nil {
		if dir := a.Services.AppConfig.Artifacts.StorageDir; dir != "" {
			return dir
		}
	}
	return defaultArtifactsStorageDir
}

// sanitizeUploadFilename enforces a single-segment, separator-free, non-empty
// filename. It returns an error rather than silently rewriting the input —
// callers that expected a specific filename should see the rejection and
// surface it to the uploader.
//
// Rules:
//   - reject null bytes
//   - reject path separators (/, \) and ".." sequences
//   - reject empty / whitespace-only inputs
//   - the result of filepath.Base(input) must equal input after TrimSpace
//     (i.e. no directory segments were stripped)
//
// This function is the single authority for upload-filename validation. If a
// new upload surface is added elsewhere it should call this.
func sanitizeUploadFilename(name string) (string, error) {
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

func (a *API) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	artifacts, err := a.Services.Store.ListArtifacts(r.Context(), sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if artifacts == nil {
		artifacts = []store.Artifact{}
	}
	a.jsonResp(w, http.StatusOK, artifacts)
}

func (a *API) handleDownloadArtifact(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	artifact, err := a.Services.Store.GetArtifact(r.Context(), id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "artifact not found")
		return
	}

	// Confine the stored path under the configured artifacts root. This
	// preserves defense in depth for legacy/corrupted rows even though
	// handlePlaceArtifact now applies the same check at write time.
	root := a.artifactsStorageDir()
	resolved, err := resolveArtifactStoragePath(root, artifact.StoragePath)
	if err != nil {
		var escape *pathsafe.EscapeError
		if errors.As(err, &escape) {
			a.errorResp(w, http.StatusBadRequest, "artifact path outside storage root")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, "resolve artifact path: "+err.Error())
		return
	}

	f, err := os.Open(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			a.errorResp(w, http.StatusNotFound, "file not found on disk")
			return
		}
		a.errorResp(w, http.StatusInternalServerError, "open artifact: "+err.Error())
		return
	}
	defer func() {
		_ = f.Close() // Read-only file close is best-effort cleanup; read errors are handled separately.
	}()

	// Sanitize the downloaded filename in Content-Disposition so a filename
	// stored earlier (e.g. with quotes or CRLF) cannot inject headers.
	safeName := sanitizeContentDispositionName(artifact.Name)
	w.Header().Set("Content-Type", artifact.MimeType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, safeName))
	_, _ = io.Copy(w, f) // The response is already streaming; a client disconnect has no recovery response path.
}

// sanitizeContentDispositionName strips characters that could break out of a
// quoted Content-Disposition value (quotes, CR, LF, null). Non-ASCII and
// other printable characters pass through unchanged.
func sanitizeContentDispositionName(name string) string {
	repl := strings.NewReplacer("\"", "", "\r", "", "\n", "", "\x00", "")
	cleaned := repl.Replace(name)
	if cleaned == "" {
		return "download"
	}
	return cleaned
}

func (a *API) handleUploadArtifact(w http.ResponseWriter, r *http.Request) {
	// Parse multipart form (max 32 MB).
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		a.errorResp(w, http.StatusBadRequest, "failed to parse multipart form: "+err.Error())
		return
	}

	sessionID := r.FormValue("session_id")
	messageID := r.FormValue("message_id")
	if sessionID == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id is required")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, "file is required: "+err.Error())
		return
	}
	defer func() {
		_ = file.Close() // Multipart input close is best-effort cleanup; read errors are handled separately.
	}()

	// Sanitize the uploader-supplied filename. Reject — don't silently
	// rename — so the caller is never surprised by a different filename on
	// download.
	safeName, err := sanitizeUploadFilename(header.Filename)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid filename: "+err.Error())
		return
	}

	// Similarly sanitize session_id: the session segment becomes a
	// directory name, so path separators or traversal sequences here
	// would escape the artifacts root just as effectively as a bad
	// filename.
	safeSession, err := sanitizeUploadFilename(sessionID)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid session_id: "+err.Error())
		return
	}

	// Create storage directory, confined under the configured artifacts
	// root via pathsafe.ResolveUnder.
	artifactsRoot := a.artifactsStorageDir()
	storageDir, err := pathsafe.ResolveUnder(artifactsRoot, safeSession)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, "resolve session storage dir: "+err.Error())
		return
	}
	if err := os.MkdirAll(storageDir, 0o755); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to create storage directory")
		return
	}

	// Resolve the final file path under the session directory. With the
	// sanitation above this is belt-and-suspenders, but keeps a single
	// source of truth for path confinement.
	storagePath, err := pathsafe.ResolveUnder(storageDir, safeName)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, "resolve storage path: "+err.Error())
		return
	}
	written, err := a.writeArtifactAtomically(storageDir, storagePath, file)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to store file: "+err.Error())
		return
	}

	// Detect MIME type.
	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" || mimeType == "application/octet-stream" {
		ext := filepath.Ext(safeName)
		mimeType = mime.TypeByExtension(ext)
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
	}

	artifact := &store.Artifact{
		SessionID:   safeSession,
		MessageID:   messageID,
		Name:        safeName,
		MimeType:    mimeType,
		SizeBytes:   written,
		StoragePath: storagePath,
	}
	if err := a.Services.Store.CreateArtifact(r.Context(), artifact); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Emit artifact.created plugin event.
	if a.Services.Plugins != nil {
		safego.Go(r.Context(), "api.artifacts.emit.artifact-created", func() {
			a.Services.Plugins.EmitArtifactCreated(safeSession, artifact.ID, mimeType, store.ArtifactOriginUploaded)
		})
	}

	a.jsonResp(w, http.StatusCreated, artifact)
}

// writeArtifactAtomically stages an upload beside its final path, closes it,
// then atomically promotes it. Copy, close, and rename failures leave any
// existing final-path artifact untouched.
func (a *API) writeArtifactAtomically(storageDir, storagePath string, src io.Reader) (int64, error) {
	tmp, err := a.createArtifactTemp(storageDir, ".nanite-artifact-*")
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

// handlePlaceArtifact creates an artifact with origin="placed" for tools/plugins
// that want to deliberately surface a file to the user. The file must already
// exist on disk at storage_path.
func (a *API) handlePlaceArtifact(w http.ResponseWriter, r *http.Request) {
	var req PlaceArtifactRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.SessionID == "" || req.Name == "" || req.StoragePath == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id, name, and storage_path are required")
		return
	}
	storagePath, err := resolveArtifactStoragePath(a.artifactsStorageDir(), req.StoragePath)
	if err != nil {
		var escape *pathsafe.EscapeError
		if errors.As(err, &escape) {
			a.errorResp(w, http.StatusBadRequest, "artifact path outside storage root")
			return
		}
		a.errorResp(w, http.StatusBadRequest, "invalid storage_path: "+err.Error())
		return
	}
	if req.MimeType == "" {
		ext := filepath.Ext(req.Name)
		req.MimeType = mime.TypeByExtension(ext)
		if req.MimeType == "" {
			req.MimeType = "application/octet-stream"
		}
	}

	info, err := os.Stat(storagePath)
	if err != nil {
		a.errorResp(w, http.StatusBadRequest, "storage_path must name an existing file: "+err.Error())
		return
	}
	if !info.Mode().IsRegular() {
		a.errorResp(w, http.StatusBadRequest, "storage_path must name a regular file")
		return
	}

	artifact := &store.Artifact{
		SessionID:      req.SessionID,
		MessageID:      req.MessageID,
		Name:           req.Name,
		MimeType:       req.MimeType,
		SizeBytes:      info.Size(),
		StoragePath:    storagePath,
		Origin:         store.ArtifactOriginPlaced,
		SourceAgentID:  req.AgentID,
		SourcePluginID: req.PluginID,
	}
	if err := a.Services.Store.CreateArtifact(r.Context(), artifact); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Emit artifact.created plugin event.
	if a.Services.Plugins != nil {
		safego.Go(r.Context(), "api.artifacts.emit.artifact-placed", func() {
			a.Services.Plugins.EmitArtifactCreated(req.SessionID, artifact.ID, req.MimeType, string(store.ArtifactOriginPlaced))
		})
	}

	a.jsonResp(w, http.StatusCreated, artifact)
}

// resolveArtifactStoragePath composes absolute-path callers with
// pathsafe.ResolveUnder correctly. ResolveUnder intentionally treats an
// absolute userPath as root-relative, so an already-canonical artifact path
// must first be converted to a path relative to the artifacts root. Relative
// callers can delegate directly.
func resolveArtifactStoragePath(root, storagePath string) (string, error) {
	if !filepath.IsAbs(storagePath) {
		return pathsafe.ResolveUnder(root, storagePath)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve artifacts root: %w", err)
	}
	if canonical, evalErr := filepath.EvalSymlinks(absRoot); evalErr == nil {
		absRoot = canonical
	}
	absTarget, err := filepath.Abs(storagePath)
	if err != nil {
		return "", fmt.Errorf("resolve artifact path: %w", err)
	}
	if canonical, evalErr := filepath.EvalSymlinks(absTarget); evalErr == nil {
		absTarget = canonical
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return "", fmt.Errorf("resolve artifact path relative to root: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", &pathsafe.EscapeError{
			Root:     absRoot,
			Attempt:  storagePath,
			Resolved: absTarget,
			Cause:    errors.New("resolved path outside root"),
		}
	}
	return pathsafe.ResolveUnder(absRoot, rel)
}

// handleListArtifactsByOrigin returns artifacts filtered by origin type.
func (a *API) handleListArtifactsByOrigin(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	origin := r.URL.Query().Get("origin")

	if origin == "" {
		// Fall through to regular list.
		a.handleListArtifacts(w, r)
		return
	}

	artifacts, err := a.Services.Store.ListArtifactsByOrigin(r.Context(), sessionID, origin)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if artifacts == nil {
		artifacts = []store.Artifact{}
	}
	a.jsonResp(w, http.StatusOK, artifacts)
}

// handleListArtifactsByProject returns artifacts whose owning session belongs
// to the given project. F4 (CW-20260429-0004): backs the right-rail
// "This Project" inherited-artifacts section.
//
// Optional ?exclude_session_id= query param filters out artifacts from a
// specific session — used by the FE to avoid double-counting the active
// session (which is shown in its own "This Session" list).
func (a *API) handleListArtifactsByProject(w http.ResponseWriter, r *http.Request) {
	projectID := r.PathValue("id")
	if projectID == "" {
		a.errorResp(w, http.StatusBadRequest, "project id is required")
		return
	}
	excludeSessionID := r.URL.Query().Get("exclude_session_id")

	artifacts, err := a.Services.Store.ListArtifactsByProject(r.Context(), projectID, excludeSessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if artifacts == nil {
		artifacts = []store.Artifact{}
	}
	a.jsonResp(w, http.StatusOK, artifacts)
}
