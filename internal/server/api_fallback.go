package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

// Fallback patterns. Every real /api route is registered with a method
// prefix, so these method-less patterns only answer what nothing else
// matches. They must stay out of the method probe below, or every path would
// look routable.
const (
	apiFallbackPattern     = "/api/"
	apiRootFallbackPattern = "/api"
	spaFallbackPattern     = "/"
)

// apiProbeMethods are the methods handleAPINotFound tries when deciding
// between 404 and 405. HEAD needs no probe: a GET pattern also serves HEAD.
var apiProbeMethods = []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// registerFallbacks installs the catch-alls, after every other route.
//
// Before CW-20260930-0109 the SPA's "/" answered every unregistered path,
// /api included, with 200 text/html, so a client calling a missing or
// removed route saw a JSON parse error instead of a 404 — and a client that
// special-cased 404 never saw one.
func (s *Server) registerFallbacks() {
	s.mux.HandleFunc(apiFallbackPattern, s.handleAPINotFound)
	s.mux.HandleFunc(apiRootFallbackPattern, s.handleAPINotFound)
	s.mux.HandleFunc(spaFallbackPattern, s.handleSPA)
}

// handleAPINotFound answers an /api request no route matched: 405 with an
// Allow header when the path is registered under other methods, else 404.
// Both use the API's {"error": msg} body and Cache-Control: no-store, as
// apiCacheMiddleware sets for /api/ (it does not match the bare /api), where
// handleSPA would have overwritten it with no-cache.
func (s *Server) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	if allow := s.allowedAPIMethods(r); len(allow) > 0 {
		w.Header().Set("Allow", strings.Join(allow, ", "))
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeAPIError(w, http.StatusNotFound, "not found")
}

// allowedAPIMethods returns the methods some real route serves r's path
// under, excluding r's own method (which already matched nothing) and the
// fallbacks.
func (s *Server) allowedAPIMethods(r *http.Request) []string {
	var allow []string
	for _, method := range apiProbeMethods {
		if method == r.Method {
			continue
		}
		probe := &http.Request{Method: method, Host: r.Host, URL: r.URL, Header: http.Header{}}
		_, pattern := s.mux.Handler(probe)
		if isFallbackPattern(pattern) {
			continue
		}
		allow = append(allow, method)
		if method == http.MethodGet {
			allow = append(allow, http.MethodHead)
		}
	}
	return allow
}

func isFallbackPattern(pattern string) bool {
	switch pattern {
	case "", apiFallbackPattern, apiRootFallbackPattern, spaFallbackPattern:
		return true
	}
	return false
}

func writeAPIError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
		slog.Debug("server: write API error response failed", "err", err)
	}
}
