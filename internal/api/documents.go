package api

import (
	"net/http"
	"strings"

	"github.com/hollis-labs/nanite/internal/store"
)

// --- Documents (J10, CW-20260426-0008) ---

type CreateDocumentRequest struct {
	Name        string `json:"name"`
	Content     string `json:"content"`
	MimeType    string `json:"mime_type"`
	Summary     string `json:"summary"`
	Included    bool   `json:"included"`
	FullContent bool   `json:"full_content"`
}

type UpdateDocumentRequest struct {
	Included    *bool   `json:"included"`
	FullContent *bool   `json:"full_content"`
	Summary     *string `json:"summary"`
}

type SetSessionContextPromptRequest struct {
	Prompt string `json:"prompt"`
}

// handleListDocuments — GET /api/sessions/{id}/documents
func (a *API) handleListDocuments(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	docs, err := a.Services.Store.ListDocuments(sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to list documents: "+err.Error())
		return
	}
	if docs == nil {
		docs = []store.Document{}
	}
	a.jsonResp(w, http.StatusOK, docs)
}

// handleCreateDocument — POST /api/sessions/{id}/documents
func (a *API) handleCreateDocument(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	var req CreateDocumentRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		a.errorResp(w, http.StatusBadRequest, "name is required")
		return
	}

	doc := &store.Document{
		SessionID:   sessionID,
		Name:        req.Name,
		Content:     req.Content,
		MimeType:    req.MimeType,
		Summary:     req.Summary,
		Included:    req.Included,
		FullContent: req.FullContent,
	}
	if err := a.Services.Store.CreateDocument(doc); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to create document: "+err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, doc)
}

// handleGetDocument — GET /api/documents/{id}
func (a *API) handleGetDocument(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	doc, err := a.Services.Store.GetDocument(id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "document not found")
		return
	}
	a.jsonResp(w, http.StatusOK, doc)
}

// handleUpdateDocument — PUT /api/documents/{id}
func (a *API) handleUpdateDocument(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req UpdateDocumentRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	doc, err := a.Services.Store.GetDocument(id)
	if err != nil {
		a.errorResp(w, http.StatusNotFound, "document not found")
		return
	}

	included := doc.Included
	fullContent := doc.FullContent
	summary := doc.Summary
	if req.Included != nil {
		included = *req.Included
	}
	if req.FullContent != nil {
		fullContent = *req.FullContent
	}
	if req.Summary != nil {
		summary = *req.Summary
	}

	if err := a.Services.Store.UpdateDocumentToggles(id, included, fullContent, summary); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to update document: "+err.Error())
		return
	}
	doc.Included = included
	doc.FullContent = fullContent
	doc.Summary = summary
	a.jsonResp(w, http.StatusOK, doc)
}

// handleDeleteDocument — DELETE /api/documents/{id}
func (a *API) handleDeleteDocument(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := a.Services.Store.DeleteDocument(id); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to delete document: "+err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleGetSessionContextPrompt — GET /api/sessions/{id}/context-prompt
func (a *API) handleGetSessionContextPrompt(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	prompt, err := a.Services.Store.GetSessionContextPrompt(sessionID)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to get context prompt: "+err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"prompt": prompt})
}

// handleSetSessionContextPrompt — PUT /api/sessions/{id}/context-prompt
func (a *API) handleSetSessionContextPrompt(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	var req SetSessionContextPromptRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if err := a.Services.Store.SetSessionContextPrompt(sessionID, req.Prompt); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "failed to set context prompt: "+err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"prompt": req.Prompt})
}
