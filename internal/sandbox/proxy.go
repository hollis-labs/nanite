package sandbox

import (
	"context"
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

// Proxy is a domain-allowlisted HTTP proxy that listens on localhost TCP.
// Sandboxed processes connect to it via HTTP_PROXY / HTTPS_PROXY env vars.
// The proxy checks each request's target domain against an allowlist before
// forwarding. Denied domains receive a 403 response.
type Proxy struct {
	AllowedDomains []string // exact "api.github.com" or wildcard "*.example.com"
	Addr           string   // "127.0.0.1:<assigned-port>" — set after Start

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

// handleConnect handles HTTPS tunneling via the CONNECT method.
func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	host, _, err := splitHostPort(r.Host)
	if err != nil {
		http.Error(w, "bad host", http.StatusBadRequest)
		return
	}

	if !p.domainAllowed(host) {
		log.Printf("proxy: denied CONNECT to %s", r.Host)
		http.Error(w, "domain not allowed", http.StatusForbidden)
		return
	}

	// Dial the target.
	targetConn, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
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
		// Don't follow redirects — let the sandboxed process handle them.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Timeout: 60 * time.Second,
	}

	resp, err := client.Do(outReq)
	if err != nil {
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
