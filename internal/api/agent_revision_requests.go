package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
)

// Supplied fields must be exact supported keys with non-null values. Ordinary
// struct decoding treats null as omission and accepts duplicate/wrapped keys.
func decodeAgentObject(r *http.Request, dst any, requireEdit bool) error {
	allowed := make(map[string]bool)
	typ := reflect.TypeOf(dst).Elem()
	for i := 0; i < typ.NumField(); i++ {
		allowed[typ.Field(i).Tag.Get("json")] = true
	}
	d := json.NewDecoder(r.Body)
	start, err := d.Token()
	if err != nil {
		return fmt.Errorf("a non-empty bare JSON object is required: %w", err)
	}
	if start != json.Delim('{') {
		return fmt.Errorf("a bare JSON object is required; wrapped, array and null bodies are unsupported")
	}
	fields := make(map[string]json.RawMessage)
	edits := 0
	for d.More() {
		key, tokenErr := d.Token()
		if tokenErr != nil {
			return tokenErr
		}
		name, ok := key.(string)
		if !ok || !allowed[name] {
			return fmt.Errorf("unsupported field %q; send only editable fields in a bare object", name)
		}
		if _, exists := fields[name]; exists {
			return fmt.Errorf("duplicate field %q", name)
		}
		var raw json.RawMessage
		if err = d.Decode(&raw); err != nil {
			return err
		}
		if string(raw) == "null" {
			return fmt.Errorf("field %q must not be null; omit it to preserve the current value", name)
		}
		fields[name] = raw
		if name != "revision" {
			edits++
		}
	}
	if _, err = d.Token(); err != nil {
		return err
	}
	var extra json.RawMessage
	if err = d.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return fmt.Errorf("invalid trailing JSON: %w", err)
		}
		return fmt.Errorf("only one JSON object is allowed")
	}
	if len(fields) == 0 || (requireEdit && edits == 0) {
		return fmt.Errorf("at least one supported editable field is required")
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}

func (a *API) handleListAgentRevisions(w http.ResponseWriter, r *http.Request) {
	limit, offset := 50, 0
	for name, target := range map[string]*int{"limit": &limit, "offset": &offset} {
		if raw := r.URL.Query().Get(name); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil {
				a.errorResp(w, http.StatusBadRequest, "invalid revision page")
				return
			}
			*target = value
		}
	}
	if limit < 1 || limit > 100 || offset < 0 {
		a.errorResp(w, http.StatusBadRequest, "limit must be 1..100 and offset non-negative")
		return
	}
	rows, err := a.Services.AgentConfig.ListRevisions(r.Context(), r.PathValue("id"), limit, offset)
	if err != nil {
		a.serviceError(w, r, err)
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{"revisions": rows, "limit": limit, "offset": offset, "restore_scope": "partial_profile_and_assignments"})
}

type restoreAgentRevisionRequest struct {
	Revision string `json:"revision"`
}

func (a *API) handleRestoreAgentRevision(w http.ResponseWriter, r *http.Request) {
	var req restoreAgentRevisionRequest
	if err := decodeAgentObject(r, &req, false); err != nil {
		a.errorResp(w, http.StatusBadRequest, "invalid restore request: "+err.Error())
		return
	}
	if req.Revision == "" {
		a.errorResp(w, http.StatusBadRequest, "current revision is required")
		return
	}
	result, err := a.Services.AgentConfig.RestoreRevision(r.Context(), r.PathValue("id"), r.PathValue("revisionId"), req.Revision)
	if err != nil {
		a.serviceError(w, r, err)
		return
	}
	a.jsonResp(w, http.StatusOK, map[string]any{
		"agent": a.agentView(*result.Profile), "restore_scope": "partial_profile_and_assignments",
		"restored_from": r.PathValue("revisionId"), "grants_restored": false,
	})
}
