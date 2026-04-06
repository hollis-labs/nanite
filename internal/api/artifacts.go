package api

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"github.com/hollis-labs/nanite/internal/store"
)

func (a *API) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	artifacts, err := a.Services.Store.ListArtifacts(sessionID)
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

	artifact, err := a.Services.Store.GetArtifact(id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "artifact not found")
		return
	}

	f, err := os.Open(artifact.StoragePath)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "file not found on disk")
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", artifact.MimeType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, artifact.Name))
	io.Copy(w, f)
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
	defer file.Close()

	// Create storage directory.
	storageDir := filepath.Join("data", "artifacts", sessionID)
	if err := os.MkdirAll(storageDir, 0o755); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to create storage directory")
		return
	}

	// Write file to disk.
	storagePath := filepath.Join(storageDir, header.Filename)
	dst, err := os.Create(storagePath)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to create file")
		return
	}
	defer dst.Close()

	written, err := io.Copy(dst, file)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to write file")
		return
	}

	// Detect MIME type.
	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" || mimeType == "application/octet-stream" {
		ext := filepath.Ext(header.Filename)
		mimeType = mime.TypeByExtension(ext)
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
	}

	artifact := &store.Artifact{
		SessionID:   sessionID,
		MessageID:   messageID,
		Name:        header.Filename,
		MimeType:    mimeType,
		SizeBytes:   written,
		StoragePath: storagePath,
	}
	if err := a.Services.Store.CreateArtifact(artifact); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Emit artifact.created plugin event.
	if a.Services.Plugins != nil {
		go a.Services.Plugins.EmitArtifactCreated(sessionID, artifact.ID, mimeType, store.ArtifactOriginUploaded)
	}

	a.jsonResp(w, http.StatusCreated, artifact)
}

// handlePlaceArtifact creates an artifact with origin="placed" for tools/plugins
// that want to deliberately surface a file to the user. The file must already
// exist on disk at storage_path.
func (a *API) handlePlaceArtifact(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID   string `json:"session_id"`
		MessageID   string `json:"message_id"`
		Name        string `json:"name"`
		MimeType    string `json:"mime_type"`
		StoragePath string `json:"storage_path"`
		AgentID     string `json:"agent_id"`
		PluginID    string `json:"plugin_id"`
	}
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.SessionID == "" || req.Name == "" || req.StoragePath == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id, name, and storage_path are required")
		return
	}
	if req.MimeType == "" {
		ext := filepath.Ext(req.Name)
		req.MimeType = mime.TypeByExtension(ext)
		if req.MimeType == "" {
			req.MimeType = "application/octet-stream"
		}
	}

	// Get file size if the file exists.
	var sizeBytes int64
	if info, err := os.Stat(req.StoragePath); err == nil {
		sizeBytes = info.Size()
	}

	artifact := &store.Artifact{
		SessionID:      req.SessionID,
		MessageID:      req.MessageID,
		Name:           req.Name,
		MimeType:       req.MimeType,
		SizeBytes:      sizeBytes,
		StoragePath:    req.StoragePath,
		Origin:         store.ArtifactOriginPlaced,
		SourceAgentID:  req.AgentID,
		SourcePluginID: req.PluginID,
	}
	if err := a.Services.Store.CreateArtifact(artifact); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Emit artifact.created plugin event.
	if a.Services.Plugins != nil {
		go a.Services.Plugins.EmitArtifactCreated(req.SessionID, artifact.ID, req.MimeType, string(store.ArtifactOriginPlaced))
	}

	a.jsonResp(w, http.StatusCreated, artifact)
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

	artifacts, err := a.Services.Store.ListArtifactsByOrigin(sessionID, origin)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if artifacts == nil {
		artifacts = []store.Artifact{}
	}
	a.jsonResp(w, http.StatusOK, artifacts)
}
