package apiauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

// Handlers serves the credential API under /api/auth/. Scope requirements
// are enforced by the Guard through routes.go; the handlers re-check the
// principal where the decision depends on it.
type Handlers struct {
	Guard *Guard
	// OperatorTokenPath, when set, is rewritten when the operator
	// credential is rotated through the API.
	OperatorTokenPath string
	// SecureCookies sets the Secure attribute on the session cookie (the
	// operator's statement that browsers reach Nanite over HTTPS).
	SecureCookies bool
	// PublicURL, when set, is the origin browsers reach Nanite on (behind a
	// proxy this differs from the bind address). Minted login links carry a
	// ready-to-open "url" built on it.
	PublicURL string
}

// Register mounts the credential API on mux.
func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/login", h.login)
	mux.HandleFunc("GET /api/auth/link", h.link)
	mux.HandleFunc("GET /api/auth/session", h.session)
	mux.HandleFunc("POST /api/auth/logout", h.logout)
	mux.HandleFunc("GET /api/auth/credentials", h.list)
	mux.HandleFunc("POST /api/auth/credentials", h.create)
	mux.HandleFunc("DELETE /api/auth/credentials/{id}", h.revoke)
	mux.HandleFunc("POST /api/auth/credentials/{id}/rotate", h.rotate)
	mux.HandleFunc("POST /api/auth/login-links", h.loginLink)
}

type credentialView struct {
	ID             string  `json:"id"`
	Kind           Kind    `json:"kind"`
	Label          string  `json:"label"`
	Scopes         []Scope `json:"scopes"`
	BoundSessionID string  `json:"bound_session_id,omitempty"`
	CreatedAt      string  `json:"created_at"`
	ExpiresAt      string  `json:"expires_at,omitempty"`
	LastUsedAt     string  `json:"last_used_at,omitempty"`
	RevokedAt      string  `json:"revoked_at,omitempty"`
	CreatedBy      string  `json:"created_by,omitempty"`
	Secret         string  `json:"secret,omitempty"`
	WrittenTo      string  `json:"written_to,omitempty"`
}

func viewOf(c Credential) credentialView {
	return credentialView{ID: c.ID, Kind: c.Kind, Label: c.Label, Scopes: c.Scopes, BoundSessionID: c.BoundSessionID,
		CreatedAt: ts(c.CreatedAt), ExpiresAt: ts(c.ExpiresAt), LastUsedAt: ts(c.LastUsedAt), RevokedAt: ts(c.RevokedAt), CreatedBy: c.CreatedBy}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil || dec.More() {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Invalid request body.")
		return false
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Invalid request body.")
		return false
	}
	return true
}

func (h *Handlers) setCookie(w http.ResponseWriter, value string, expires time.Time) {
	// Secure follows the operator's http.secure_cookies setting, not the
	// request: Nanite serves plain HTTP behind a proxy and does not trust
	// forwarded headers, so it cannot infer an external HTTPS scheme.
	// #nosec G124 -- Secure is operator-controlled for the reason above
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: value, Path: "/", HttpOnly: true, Secure: h.SecureCookies, SameSite: http.SameSiteStrictMode, Expires: expires})
}

// login exchanges an operator or client credential for a UI session cookie.
func (h *Handlers) login(w http.ResponseWriter, r *http.Request) {
	if !originMatchesHost(r) {
		writeError(w, r, http.StatusForbidden, "forbidden", "Cross-origin request refused.")
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	c, err := h.Guard.Store.Authenticate(r.Context(), strings.TrimSpace(body.Token))
	if err != nil || c.Kind == KindAgent {
		time.Sleep(150 * time.Millisecond)
		h.Guard.deny(r, "bad-login")
		writeUnauthorized(w, r)
		return
	}
	u, cookie, err := h.Guard.Store.CreateUISession(r.Context(), c)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Could not start a session.")
		return
	}
	h.setCookie(w, cookie, u.ExpiresAt)
	h.Guard.Store.Audit(r.Context(), c.ID, c.Kind, r.Method, "/api/auth/login", "login", remoteHost(r))
	writeJSON(w, http.StatusOK, map[string]any{"csrf_token": u.CSRF, "expires_at": ts(u.ExpiresAt), "principal": viewOf(c)})
}

// link spends a one-time login link, starts a session and redirects home.
func (h *Handlers) link(w http.ResponseWriter, r *http.Request) {
	c, err := h.Guard.Store.ConsumeLoginLink(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		h.Guard.deny(r, "bad-link")
		http.Error(w, "This login link is invalid, expired or already used.", http.StatusUnauthorized)
		return
	}
	u, cookie, err := h.Guard.Store.CreateUISession(r.Context(), c)
	if err != nil {
		http.Error(w, "Could not start a session.", http.StatusInternalServerError)
		return
	}
	h.setCookie(w, cookie, u.ExpiresAt)
	h.Guard.Store.Audit(r.Context(), c.ID, c.Kind, r.Method, "/api/auth/link", "login-link", remoteHost(r))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handlers) session(w http.ResponseWriter, r *http.Request) {
	p, ok := FromContext(r.Context())
	if !ok {
		writeUnauthorized(w, r)
		return
	}
	out := map[string]any{"credential_id": p.CredentialID, "kind": p.Kind, "label": p.Label, "scopes": scopesOf(p), "cookie": p.Cookie}
	if p.Cookie {
		out["csrf_token"] = p.CSRF
	}
	writeJSON(w, http.StatusOK, out)
}

func scopesOf(p Principal) []Scope {
	if p.Kind == KindOperator {
		return AllScopes()
	}
	return p.Scopes
}

func (h *Handlers) logout(w http.ResponseWriter, r *http.Request) {
	p, ok := FromContext(r.Context())
	if ok && p.Cookie {
		_ = h.Guard.EndUISession(context.WithoutCancel(r.Context()), p.UISessionID)
	}
	h.setCookie(w, "", time.Unix(0, 0))
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) list(w http.ResponseWriter, r *http.Request) {
	creds, err := h.Guard.Store.List(r.Context(), r.URL.Query().Get("include_revoked") == "true")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Could not list credentials.")
		return
	}
	views := make([]credentialView, 0, len(creds))
	for _, c := range creds {
		views = append(views, viewOf(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": views})
}

// clientTTL is the default lifetime of a client credential.
const clientTTL = 30 * 24 * time.Hour

func (h *Handlers) create(w http.ResponseWriter, r *http.Request) {
	p, _ := FromContext(r.Context())
	var body struct {
		Kind    string   `json:"kind"`
		Label   string   `json:"label"`
		Scopes  []string `json:"scopes"`
		TTLDays int      `json:"ttl_days"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Kind != string(KindClient) {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Only client credentials can be created here; rotate the operator credential, and agent credentials are minted by the host.")
		return
	}
	scopes, err := ParseScopes(body.Scopes)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	ttl := clientTTL
	if body.TTLDays > 0 {
		ttl = time.Duration(body.TTLDays) * 24 * time.Hour
	}
	c, secret, err := h.Guard.Store.Create(r.Context(), CreateParams{Kind: KindClient, Label: strings.TrimSpace(body.Label), Scopes: scopes, TTL: ttl, CreatedBy: p.Actor()})
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	v := viewOf(c)
	v.Secret = secret
	writeJSON(w, http.StatusCreated, v)
}

func (h *Handlers) revoke(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	target, err := h.Guard.Store.Get(r.Context(), id)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "not_found", "No such credential.")
		return
	}
	if target.Kind == KindOperator {
		creds, _ := h.Guard.Store.List(r.Context(), false)
		active := 0
		for _, c := range creds {
			if c.Kind == KindOperator {
				active++
			}
		}
		if active <= 1 {
			writeError(w, r, http.StatusConflict, "conflict", "This is the last operator credential; rotate it instead of revoking it.")
			return
		}
	}
	ok, err := h.Guard.Store.Revoke(context.WithoutCancel(r.Context()), id)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal", "Could not revoke the credential.")
		return
	}
	if !ok {
		writeError(w, r, http.StatusNotFound, "not_found", "No live credential with that id.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) rotate(w http.ResponseWriter, r *http.Request) {
	p, _ := FromContext(r.Context())
	fresh, secret, err := h.Guard.Store.Rotate(context.WithoutCancel(r.Context()), r.PathValue("id"), p.Actor())
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	v := viewOf(fresh)
	v.Secret = secret
	if fresh.Kind == KindOperator && h.OperatorTokenPath != "" {
		if err := WriteTokenFile(h.OperatorTokenPath, secret); err == nil {
			v.WrittenTo = h.OperatorTokenPath
		}
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *Handlers) loginLink(w http.ResponseWriter, r *http.Request) {
	p, _ := FromContext(r.Context())
	c, err := h.Guard.Store.Get(r.Context(), p.CredentialID)
	if err != nil {
		writeError(w, r, http.StatusForbidden, "forbidden", "Only an operator credential can mint a login link.")
		return
	}
	code, expires, err := h.Guard.Store.CreateLoginLink(r.Context(), c)
	if err != nil {
		writeError(w, r, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	out := map[string]any{"path": "/api/auth/link?code=" + code, "expires_at": ts(expires)}
	if base := strings.TrimRight(strings.TrimSpace(h.PublicURL), "/"); base != "" {
		out["url"] = base + out["path"].(string)
	}
	writeJSON(w, http.StatusCreated, out)
}

// AdminAuthenticator adapts the request's principal to the
// func(*http.Request)(actor, ok) shape the admin and workflow-responder
// handlers take. It requires the scope and returns the credential label as the
// principal reference.
func AdminAuthenticator(need Scope) func(*http.Request) (string, bool) {
	return func(r *http.Request) (string, bool) {
		if r == nil {
			return "", false
		}
		p, ok := FromContext(r.Context())
		if !ok || !p.Has(need) {
			return "", false
		}
		// The reference is the credential's label ("operator" for the
		// bootstrap credential), the stable name workflow wait authorities and
		// responder provenance record; never the secret or its hash.
		if p.Label != "" {
			return p.Label, true
		}
		return p.CredentialID, true
	}
}
