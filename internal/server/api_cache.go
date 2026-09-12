package server

import (
	"net/http"
	"strings"
)

// apiNoStore is the cache policy every JSON API response carries.
//
// `no-store` rather than `no-cache`: Nanite is a single-user desktop
// application served over loopback, so there is no bandwidth pressure to trade
// against, and correctness is the only axis that matters. `no-cache` without an
// `ETag` or `Last-Modified` to revalidate against buys nothing here, and it
// reads to most people as "do not cache" when it actually means "cache, but
// revalidate" — an invitation to misread later.
const apiNoStore = "no-store"

// apiCacheMiddleware sets a DEFAULT Cache-Control on /api/ responses.
//
// # Why this exists
//
// No JSON API handler set a cache header. A browser cached an empty
// /api/start-surface/capabilities from a window when that endpoint really was
// empty, and kept serving it after the endpoint was fixed — so the new-chat
// launcher showed no providers and no models on every path while the same
// endpoint returned full data to curl. Deploying a correct server does not
// clear that; only a hard refresh nobody knows to perform does.
//
// A transient bad response therefore became permanent per-browser, invisibly,
// with no way for a fix to reach the user. That is the defect, and it is
// systemic rather than about one endpoint.
//
// # Why a default rather than a list
//
// This is deliberately not a per-handler fix and not an exclusion list. A list
// is a thing the next endpoint is not on, which is how the gap appeared in the
// first place: five handlers DO set `Cache-Control: no-cache`
// (host_runtime_feed, messages, presence, plugins_events, workflows) and all
// five are SSE — they set it because streaming requires it, not for freshness.
// Their presence made the gap look covered.
//
// Setting the header BEFORE the handler runs makes this a default that a
// handler may override, because http.Header.Set replaces. The five SSE
// handlers set their own value and win, with no list here naming them and no
// coordination needed if a sixth appears. A new JSON endpoint inherits
// no-store without being registered anywhere.
//
// # What this does not touch
//
// The SPA layer, which got this right independently: setSPACacheHeaders
// (spa.go) has three deliberate policies — revalidate index.html so a rebuilt
// asset manifest takes effect, cache content-hashed assets hard — pinned by
// TestSetSPACacheHeaders. This middleware is scoped to /api/ and leaves them
// alone. The same reasoning was simply never applied one layer over.
func (s *Server) apiCacheMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if apiCachePolicyFor(r.URL.Path) != "" {
			w.Header().Set("Cache-Control", apiNoStore)
		}
		next.ServeHTTP(w, r)
	})
}

// apiCachePolicyFor returns the default Cache-Control for a request path, or
// "" when this middleware has no opinion about it.
//
// Split out from the middleware so the policy is testable as a function rather
// than only through a live handler, matching how setSPACacheHeaders is pinned.
func apiCachePolicyFor(path string) string {
	if strings.HasPrefix(path, "/api/") {
		return apiNoStore
	}
	return ""
}
