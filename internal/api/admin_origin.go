package api

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// AdminOriginAllowed is the command/credentialed CORS policy, independent of
// legacy CORS. It reads only request headers and actual transport authority.
// Wildcards and forwarded headers never grant authority. Missing Origin fails
// closed, including for CLI clients, which must supply an approved Origin.
func AdminOriginAllowed(r *http.Request, allowed []string) bool {
	origins := r.Header.Values("Origin")
	if len(origins) != 1 {
		return false
	}
	origin := origins[0]
	canonical, ok := adminOrigin(origin)
	if !ok {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	authority, ok := adminOrigin(scheme + "://" + r.Host)
	if ok && canonical == authority {
		return true
	}
	for _, entry := range allowed {
		if entry == origin {
			if _, ok := adminOrigin(entry); ok {
				return true
			}
		}
	}
	return false
}

// adminOrigin accepts only a serialized http(s) origin, never a URL path or
// origin list. Normalize host case and default ports for same-origin comparison.
func adminOrigin(origin string) (string, bool) {
	if strings.ContainsAny(origin, " \t\r\n,\\%") {
		return "", false
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(origin, "#") {
		return "", false
	}
	host := u.Hostname()
	if host == "" || strings.HasSuffix(u.Host, ":") {
		return "", false
	}
	if strings.Contains(host, ":") {
		if net.ParseIP(host) == nil {
			return "", false
		}
	} else {
		for _, c := range host {
			switch {
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-':
			default:
				return "", false
			}
		}
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", false
		}
	}
	if u.Scheme == "http" && port == "80" || u.Scheme == "https" && port == "443" {
		port = ""
	}
	host = strings.ToLower(host)
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	return u.Scheme + "://" + host, true
}
