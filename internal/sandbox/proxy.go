package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hollis-labs/nanite/internal/safego"
)

// ProxyResolver resolves a hostname to IP addresses. Tests replace this to
// control what IPs the proxy pins against without real DNS lookups. Matches
// the signature used by internal/mcp web_fetch so the pattern stays
// consistent across the codebase.
type ProxyResolver func(ctx context.Context, host string) ([]net.IP, error)

// defaultProxyResolver uses the system resolver.
func defaultProxyResolver(ctx context.Context, host string) ([]net.IP, error) {
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// proxyDeniedCIDRs is the set of IP ranges the proxy refuses to dial.
// Mirrors internal/mcp general_tools.go:ssrfDeniedCIDRs so both network
// egress paths enforce the same policy: loopback, link-local / cloud IMDS,
// RFC1918, CGNAT, unspecified, IPv6 loopback, ULA, and IPv6 link-local.
// Loopback is gated separately so the AllowLocalhost flag can permit it
// without weakening the rest.
var proxyDeniedCIDRs = mustParseSandboxCIDRs([]string{
	"169.254.0.0/16", // link-local incl. cloud IMDS
	"10.0.0.0/8",     // RFC1918
	"172.16.0.0/12",  // RFC1918
	"192.168.0.0/16", // RFC1918
	"0.0.0.0/8",      // unspecified
	"100.64.0.0/10",  // CGNAT
	"fc00::/7",       // IPv6 ULA
	"fe80::/10",      // IPv6 link-local
	"::/128",         // IPv6 unspecified
})

// proxyLoopbackCIDRs covers 127.0.0.0/8 and ::1/128. Separated so the
// AllowLocalhost flag can gate them.
var proxyLoopbackCIDRs = mustParseSandboxCIDRs([]string{
	"127.0.0.0/8",
	"::1/128",
})

func mustParseSandboxCIDRs(cidrs []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, block, err := net.ParseCIDR(c)
		if err != nil {
			panic(fmt.Sprintf("sandbox: invalid CIDR %q: %v", c, err))
		}
		out = append(out, block)
	}
	return out
}

// errProxySSRFBlocked is the sentinel used for SSRF-class rejections so
// tests can distinguish validator errors from transport errors via
// errors.Is.
var errProxySSRFBlocked = errors.New("proxy: blocked destination")

// allowedCONNECTPorts restricts CONNECT to the TLS ports sandboxed tools
// legitimately need. Everything else is refused before any dial.
var allowedCONNECTPorts = map[string]bool{
	"443":  true,
	"8443": true,
}

// Proxy is a domain-allowlisted HTTP proxy that listens on localhost TCP.
// Sandboxed processes connect to it via HTTP_PROXY / HTTPS_PROXY env vars.
// The proxy checks each request's target domain against an allowlist and
// its resolved IP against the SSRF denylist before forwarding. Denied
// requests receive a 403 response.
type Proxy struct {
	AllowedDomains []string // exact "api.github.com" or wildcard "*.example.com"
	Addr           string   // "127.0.0.1:<assigned-port>" — set after Start

	// AllowLocalhost permits connections to 127.0.0.0/8 and ::1/128 once the
	// resolver returns one. Off by default; callers that legitimately need
	// to reach localhost services set this explicitly via config.
	AllowLocalhost bool

	// Resolver is the DNS hook used by the SSRF guard. Nil falls back to
	// the system resolver. Tests install a stub here.
	Resolver ProxyResolver

	// Dialer is the low-level dial function used once the IP has been
	// validated. Nil uses net.Dialer with a short connect timeout. Tests
	// install a stub that records the dial target without opening a socket.
	Dialer func(ctx context.Context, network, addr string) (net.Conn, error)

	// ExtraCONNECTPorts augments the built-in CONNECT port allowlist
	// (443, 8443). Used by tests that need to tunnel to httptest TLS
	// servers on dynamic ports; callers normally leave this empty.
	ExtraCONNECTPorts []string

	listener net.Listener
	server   *http.Server
	wg       sync.WaitGroup
}

// NewProxy creates a proxy that will listen on 127.0.0.1:0 (random port).
func NewProxy(allowedDomains []string) *Proxy {
	return &Proxy{
		AllowedDomains: allowedDomains,
	}
}

// Start begins listening on a random localhost port. The assigned address
// is available via p.Addr after Start returns.
func (p *Proxy) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("proxy: listen: %w", err)
	}
	p.listener = ln
	p.Addr = ln.Addr().String()

	p.server = &http.Server{
		Handler: http.HandlerFunc(p.handleRequest),
	}

	p.wg.Add(1)
	safego.Go(context.Background(), "sandbox.proxy.serve", func() {
		defer p.wg.Done()
		if err := p.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("proxy: serve error: %v", err)
		}
	})

	return nil
}

// Stop gracefully shuts down the proxy listener.
func (p *Proxy) Stop() error {
	if p.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := p.server.Shutdown(ctx)
	p.wg.Wait()
	return err
}

// handleRequest dispatches HTTP CONNECT (HTTPS tunneling) and plain HTTP requests.
func (p *Proxy) handleRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
	} else {
		p.handleHTTP(w, r)
	}
}

// resolveAndPin resolves host via the proxy's resolver (or the system
// resolver when unset), rejects every returned IP that falls in the SSRF
// CIDR set (and loopback unless AllowLocalhost is true), and returns the
// first validated IP. The caller then dials the IP literal so DNS cannot
// rebind between validation and dial.
func (p *Proxy) resolveAndPin(ctx context.Context, host string) (net.IP, error) {
	if isLocalhostName(host) && !p.AllowLocalhost {
		return nil, fmt.Errorf("%w: localhost name %q", errProxySSRFBlocked, host)
	}
	resolver := p.Resolver
	if resolver == nil {
		resolver = defaultProxyResolver
	}
	ips, err := resolver(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%w: no IPs for %q", errProxySSRFBlocked, host)
	}
	for _, ip := range ips {
		if !p.AllowLocalhost {
			for _, block := range proxyLoopbackCIDRs {
				if block.Contains(ip) {
					return nil, fmt.Errorf("%w: loopback %s", errProxySSRFBlocked, ip)
				}
			}
		}
		for _, block := range proxyDeniedCIDRs {
			if block.Contains(ip) {
				return nil, fmt.Errorf("%w: %s in %s", errProxySSRFBlocked, ip, block)
			}
		}
		if ip.IsUnspecified() {
			return nil, fmt.Errorf("%w: unspecified %s", errProxySSRFBlocked, ip)
		}
	}
	// Pin to the first validated IP. The caller supplies a literal address
	// to the dialer, so DNS rebinding between this check and the dial is
	// impossible.
	return ips[0], nil
}

func (p *Proxy) innerDial(ctx context.Context, network, addr string) (net.Conn, error) {
	if p.Dialer != nil {
		return p.Dialer(ctx, network, addr)
	}
	d := &net.Dialer{Timeout: 10 * time.Second}
	return d.DialContext(ctx, network, addr)
}

// connectPortAllowed reports whether port is in the built-in TLS
// allowlist or in ExtraCONNECTPorts.
func (p *Proxy) connectPortAllowed(port string) bool {
	if allowedCONNECTPorts[port] {
		return true
	}
	for _, extra := range p.ExtraCONNECTPorts {
		if extra == port {
			return true
		}
	}
	return false
}

// isLocalhostName matches "localhost" and any subdomain of ".localhost"
// (RFC 6761 reserves both). Case-insensitive.
func isLocalhostName(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	return h == "localhost" || strings.HasSuffix(h, ".localhost")
}

// handleConnect handles HTTPS tunneling via the CONNECT method.
func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	host, port, err := splitHostPort(r.Host)
	if err != nil {
		http.Error(w, "bad host", http.StatusBadRequest)
		return
	}
	// Restrict CONNECT to TLS ports. Sandboxed tools that legitimately need
	// another port can layer it on top of HTTPS, or the operator can extend
	// this list after review.
	if !p.connectPortAllowed(port) {
		log.Printf("proxy: denied CONNECT to %s (port %s not in TLS allowlist)", r.Host, port)
		http.Error(w, "CONNECT only allowed to TLS ports (443, 8443)", http.StatusForbidden)
		return
	}

	if !p.domainAllowed(host) {
		log.Printf("proxy: denied CONNECT to %s", r.Host)
		http.Error(w, "domain not allowed", http.StatusForbidden)
		return
	}

	// Resolve the host once and pin the dial to the validated IP. Rejects
	// DNS-rebinding to RFC1918 / loopback / IMDS targets.
	pinned, err := p.resolveAndPin(r.Context(), host)
	if err != nil {
		log.Printf("proxy: blocked CONNECT to %s: %v", r.Host, err)
		http.Error(w, fmt.Sprintf("blocked: %v", err), http.StatusForbidden)
		return
	}

	dialCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	targetConn, err := p.innerDial(dialCtx, "tcp", net.JoinHostPort(pinned.String(), port))
	if err != nil {
		http.Error(w, fmt.Sprintf("dial target: %v", err), http.StatusBadGateway)
		return
	}
	defer targetConn.Close()

	// Hijack the client connection to pipe bytes bidirectionally.
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, fmt.Sprintf("hijack: %v", err), http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	// Send 200 Connection Established.
	_, _ = clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))

	// Bidirectional copy.
	var copyWg sync.WaitGroup
	copyWg.Add(2)
	safego.Go(context.Background(), "sandbox.proxy.connect.copy-to-target", func() {
		defer copyWg.Done()
		_, _ = io.Copy(targetConn, clientConn)
	})
	safego.Go(context.Background(), "sandbox.proxy.connect.copy-to-client", func() {
		defer copyWg.Done()
		_, _ = io.Copy(clientConn, targetConn)
	})
	copyWg.Wait()
}

// handleHTTP forwards plain HTTP requests after checking the domain allowlist.
func (p *Proxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Host == "" {
		http.Error(w, "missing host in request", http.StatusBadRequest)
		return
	}

	// Reject non-http(s) schemes outright. Go's default transport already
	// refuses `file://`, but surface it explicitly so nothing reaches the
	// dialer.
	if r.URL.Scheme != "" && r.URL.Scheme != "http" && r.URL.Scheme != "https" {
		http.Error(w, fmt.Sprintf("unsupported scheme %q", r.URL.Scheme), http.StatusForbidden)
		return
	}

	host, _, err := splitHostPort(r.URL.Host)
	if err != nil {
		http.Error(w, "bad host", http.StatusBadRequest)
		return
	}

	if !p.domainAllowed(host) {
		log.Printf("proxy: denied %s to %s", r.Method, r.URL.Host)
		http.Error(w, "domain not allowed", http.StatusForbidden)
		return
	}

	// Custom transport whose DialContext resolves the hostname once, checks
	// every returned IP against the SSRF denylist, and pins the connection
	// to the validated IP. Matches the pattern used by web_fetch so both
	// egress paths enforce the same policy.
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialHost, dialPort, splitErr := net.SplitHostPort(addr)
		if splitErr != nil {
			return nil, splitErr
		}
		pinned, err := p.resolveAndPin(ctx, dialHost)
		if err != nil {
			return nil, err
		}
		return p.innerDial(ctx, network, net.JoinHostPort(pinned.String(), dialPort))
	}
	transport := &http.Transport{
		DialContext:           dial,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		DisableKeepAlives:     true,
	}

	// Build the outgoing request.
	outReq, err := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("build request: %v", err), http.StatusInternalServerError)
		return
	}
	outReq.Header = r.Header.Clone()
	// Remove hop-by-hop headers.
	outReq.Header.Del("Proxy-Connection")
	outReq.Header.Del("Proxy-Authorization")

	client := &http.Client{
		Transport: transport,
		// Don't follow redirects — let the sandboxed process handle them.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 60 * time.Second,
	}

	resp, err := client.Do(outReq)
	if err != nil {
		if errors.Is(err, errProxySSRFBlocked) {
			http.Error(w, fmt.Sprintf("blocked: %v", err), http.StatusForbidden)
			return
		}
		http.Error(w, fmt.Sprintf("upstream: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers.
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// domainAllowed checks the host against the allowlist.
// Supports exact match ("api.github.com") and wildcard ("*.example.com").
// A wildcard "*.example.com" matches "sub.example.com" but NOT "example.com".
func (p *Proxy) domainAllowed(host string) bool {
	host = strings.ToLower(host)
	for _, pattern := range p.AllowedDomains {
		pattern = strings.ToLower(pattern)
		if strings.HasPrefix(pattern, "*.") {
			// Wildcard: *.example.com matches sub.example.com
			suffix := pattern[1:] // ".example.com"
			if strings.HasSuffix(host, suffix) && host != suffix[1:] {
				return true
			}
		} else {
			if host == pattern {
				return true
			}
		}
	}
	return false
}

// splitHostPort splits a host:port string. If no port is present, returns
// the host as-is with an empty port string.
func splitHostPort(hostport string) (host, port string, err error) {
	host, port, err = net.SplitHostPort(hostport)
	if err != nil {
		// Maybe no port — treat the whole thing as host.
		if !strings.Contains(hostport, ":") {
			return hostport, "", nil
		}
		return "", "", err
	}
	return host, port, nil
}
