package api

import (
	"bytes"
	"net/http"
	"text/template"
	"time"

	"github.com/hollis-labs/nanite/internal/store"
)

func (a *API) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := a.Services.Store.ListTemplates()
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, templates)
}

func (a *API) handleGetTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	t, err := a.Services.Store.GetTemplate(name)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if t == nil {
		a.errorResp(w, http.StatusNotFound, "template not found")
		return
	}
	a.jsonResp(w, http.StatusOK, t)
}

func (a *API) handleCreateTemplate(w http.ResponseWriter, r *http.Request) {
	var req CreateTemplateRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" || req.Template == "" {
		a.errorResp(w, http.StatusBadRequest, "name and template are required")
		return
	}

	// Validate template syntax.
	if _, err := template.New("").Parse(req.Template); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid template syntax: "+err.Error())
		return
	}

	t := &store.Template{Name: req.Name, Template: req.Template}
	if err := a.Services.Store.CreateTemplate(t); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusCreated, t)
}

func (a *API) handleUpdateTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var req UpdateTemplateRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Template == "" {
		a.errorResp(w, http.StatusBadRequest, "template is required")
		return
	}

	if _, err := template.New("").Parse(req.Template); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid template syntax: "+err.Error())
		return
	}

	if err := a.Services.Store.UpdateTemplate(name, req.Template); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (a *API) handleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := a.Services.Store.DeleteTemplate(name); err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (a *API) handleApplyTemplate(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	var req ApplyTemplateRequest
	if err := a.decode(r, &req); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.SessionID == "" {
		a.errorResp(w, http.StatusBadRequest, "session_id is required")
		return
	}

	tmpl, err := a.Services.Store.GetTemplate(name)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tmpl == nil {
		a.errorResp(w, http.StatusNotFound, "template not found")
		return
	}

	// Get the last assistant message from the session.
	messages, err := a.Services.Store.ListMessages(req.SessionID, 20)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, err.Error())
		return
	}

	var lastContent string
	var lastTitle string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			lastContent = messages[i].Content
			break
		}
	}

	// Get session title.
	sess, err := a.Services.Store.GetSession(req.SessionID)
	if err == nil && sess != nil {
		lastTitle = sess.Title
	}

	// Execute the template.
	t, err := template.New(name).Parse(tmpl.Template)
	if err != nil {
		a.errorResp(w, http.StatusInternalServerError, "template parse error: "+err.Error())
		return
	}

	data := map[string]string{
		"Content": lastContent,
		"Title":   lastTitle,
		"Date":    time.Now().Format("2006-01-02"),
		"Status":  "complete",
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		a.errorResp(w, http.StatusInternalServerError, "template exec error: "+err.Error())
		return
	}

	a.jsonResp(w, http.StatusOK, map[string]string{
		"template": name,
		"output":   buf.String(),
	})
}
