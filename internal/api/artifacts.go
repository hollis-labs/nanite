package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/hollis-labs/nanite/internal/safego"
	"github.com/hollis-labs/nanite/internal/service"
	"github.com/hollis-labs/nanite/internal/store"
)

// artifactError maps an ArtifactService error: a rejected request is a 400,
// a missing row or file a 404, a server-side file failure or a store error a
// 500. The body is the service's message.
func (a *API) artifactError(w http.ResponseWriter, err error) {
	var ae *service.ArtifactError
	if errors.As(err, &ae) {
		switch ae.Kind {
		case service.ArtifactInvalid:
			a.errorResp(w, http.StatusBadRequest, ae.Msg)
		case service.ArtifactNotFound:
			a.errorResp(w, http.StatusNotFound, ae.Msg)
		default:
			a.errorResp(w, http.StatusInternalServerError, ae.Msg)
		}
		return
	}
	a.errorResp(w, http.StatusInternalServerError, err.Error())
}

func (a *API) handleListArtifacts(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")

	artifacts, err := a.Services.Artifacts.List(r.Context(), sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, artifactsToView(artifacts))
}

func (a *API) handleDownloadArtifact(w http.ResponseWriter, r *http.Request) {
	artifact, f, err := a.Services.Artifacts.Open(r.Context(), r.PathValue("id"))
	if err != nil {
		a.artifactError(w, err)
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

	artifact, err := a.Services.Artifacts.Upload(r.Context(), service.UploadInput{
		SessionID:   sessionID,
		MessageID:   messageID,
		Filename:    header.Filename,
		ContentType: header.Header.Get("Content-Type"),
		Content:     file,
	})
	if err != nil {
		a.artifactError(w, err)
		return
	}

	// Emit artifact.created plugin event.
	if a.Services.Plugins != nil {
		safego.Go(r.Context(), "api.artifacts.emit.artifact-created", func() {
			a.Services.Plugins.EmitArtifactCreated(artifact.SessionID, artifact.ID, artifact.MimeType, store.ArtifactOriginUploaded)
		})
	}

	a.jsonResp(w, http.StatusCreated, artifactToView(artifact))
}

// handlePlaceArtifact creates an artifact with origin="placed" for tools/plugins
// that want to deliberately surface a file to the user. The file must already
// exist on disk at storage_path, under the artifacts root.
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

	artifact, err := a.Services.Artifacts.Place(r.Context(), service.PlaceInput{
		SessionID:      req.SessionID,
		MessageID:      req.MessageID,
		Name:           req.Name,
		MimeType:       req.MimeType,
		StoragePath:    req.StoragePath,
		SourceAgentID:  req.AgentID,
		SourcePluginID: req.PluginID,
	})
	if err != nil {
		a.artifactError(w, err)
		return
	}

	// Emit artifact.created plugin event.
	if a.Services.Plugins != nil {
		safego.Go(r.Context(), "api.artifacts.emit.artifact-placed", func() {
			a.Services.Plugins.EmitArtifactCreated(req.SessionID, artifact.ID, artifact.MimeType, string(store.ArtifactOriginPlaced))
		})
	}

	a.jsonResp(w, http.StatusCreated, artifactToView(artifact))
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

	artifacts, err := a.Services.Artifacts.ListByOrigin(r.Context(), sessionID, origin)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, artifactsToView(artifacts))
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

	artifacts, err := a.Services.Artifacts.ListByProject(r.Context(), projectID, excludeSessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, artifactsToView(artifacts))
}
