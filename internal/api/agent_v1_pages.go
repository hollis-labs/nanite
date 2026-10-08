package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/hollis-labs/nanite/internal/service"
)

type agentPageCursor struct {
	Scope   string `json:"scope"`
	Created string `json:"created"`
	ID      string `json:"id"`
}

func (a *API) encodeAgentCursor(scope, created, id string) string {
	body, _ := json.Marshal(agentPageCursor{scope, created, id})
	mac := hmac.New(sha256.New, a.agentCursorKey)
	mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (a *API) decodeAgentCursor(token, scope string) (agentPageCursor, error) {
	var cursor agentPageCursor
	if token == "" {
		return cursor, nil
	}
	if len(token) > 2048 {
		return cursor, errors.New("invalid pagination cursor")
	}
	pieces := strings.Split(token, ".")
	if len(pieces) != 2 {
		return cursor, errors.New("invalid pagination cursor")
	}
	body, err := base64.RawURLEncoding.DecodeString(pieces[0])
	if err != nil {
		return cursor, errors.New("invalid pagination cursor")
	}
	signature, err := base64.RawURLEncoding.DecodeString(pieces[1])
	if err != nil {
		return cursor, errors.New("invalid pagination cursor")
	}
	mac := hmac.New(sha256.New, a.agentCursorKey)
	mac.Write(body)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return cursor, errors.New("invalid pagination cursor")
	}
	if json.Unmarshal(body, &cursor) != nil || cursor.Scope != scope || cursor.ID == "" || cursor.Created == "" {
		return cursor, errors.New("pagination cursor belongs to a different view or query")
	}
	return cursor, nil
}
func agentPageLimit(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 50, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 200 {
		return 0, errors.New("limit must be between 1 and 200")
	}
	return n, nil
}
func (a *API) handleAgentV1ListSessions(w http.ResponseWriter, r *http.Request) {
	limit, err := agentPageLimit(r)
	if err != nil {
		a.agentV1Error(w, http.StatusBadRequest, err.Error())
		return
	}
	archived := r.URL.Query().Get("include_archived") == "true"
	scope := fmt.Sprintf("operator:views:%t", archived)
	cursor, err := a.decodeAgentCursor(r.URL.Query().Get("cursor"), scope)
	if err != nil {
		a.agentV1Error(w, http.StatusBadRequest, err.Error())
		return
	}
	pages, ok := a.Services.Sessions.(service.CognitivePages)
	if !ok {
		a.agentV1Error(w, http.StatusServiceUnavailable, "bounded views are unavailable")
		return
	}
	views, err := pages.ListCognitiveSessionPage(r.Context(), cursor.Created, cursor.ID, limit+1, archived)
	if err != nil {
		a.agentV1Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	var next *string
	if len(views) > limit {
		views = views[:limit]
		last := views[len(views)-1]
		token := a.encodeAgentCursor(scope, last.CreatedAt, last.ID)
		next = &token
	}
	a.jsonResp(w, http.StatusOK, struct {
		Items []SessionView `json:"items"`
		Next  *string       `json:"next_cursor"`
	}{sessionsToView(views), next})
}
func (a *API) handleAgentV1History(w http.ResponseWriter, r *http.Request) {
	view := r.PathValue("id")
	if _, err := a.Services.Sessions.Get(r.Context(), view); err != nil {
		a.agentV1LookupError(w, err)
		return
	}
	limit, err := agentPageLimit(r)
	if err != nil {
		a.agentV1Error(w, http.StatusBadRequest, err.Error())
		return
	}
	scope := "operator:history:" + view
	cursor, err := a.decodeAgentCursor(r.URL.Query().Get("older_cursor"), scope)
	if err != nil {
		a.agentV1Error(w, http.StatusBadRequest, err.Error())
		return
	}
	pages, ok := a.Services.Sessions.(service.CognitivePages)
	if !ok {
		a.agentV1Error(w, http.StatusServiceUnavailable, "bounded history is unavailable")
		return
	}
	messages, err := pages.ListCognitiveMessagePage(r.Context(), view, cursor.Created, cursor.ID, limit+1)
	if err != nil {
		a.agentV1Error(w, http.StatusInternalServerError, err.Error())
		return
	}
	var older *string
	if len(messages) > limit {
		messages = messages[1:]
		first := messages[0]
		token := a.encodeAgentCursor(scope, first.CreatedAt, first.ID)
		older = &token
	}
	a.jsonResp(w, http.StatusOK, struct {
		Items []MessageView `json:"items"`
		Older *string       `json:"older_cursor"`
	}{messagesToView(messages), older})
}
