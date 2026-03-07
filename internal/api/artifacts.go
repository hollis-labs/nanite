package api

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"github.com/hollis-labs/mentat-chat/internal/store"
)

func (a *API) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	artifacts, err := a.Store.ListArtifacts(sessionID)
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

	artifact, err := a.Store.GetArtifact(id)
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
	if err := a.Store.CreateArtifact(artifact); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	a.jsonResp(w, http.StatusCreated, artifact)
}
