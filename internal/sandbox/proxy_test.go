package sandbox

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestProxy_AllowedHTTP(t *testing.T) {
	// Start a target server pretending to be httpbin.org.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok from target")
	}))
	defer target.Close()

	targetURL, _ := url.Parse(target.URL)

	proxy := NewProxy([]string{targetURL.Hostname()})
	if err := proxy.Start(); err != nil {
		t.Fatalf("proxy.Start() error: %v", err)
	}
	defer proxy.Stop()

	// Send a plain HTTP request through the proxy.
	proxyURL, _ := url.Parse("http://" + proxy.Addr)
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get(target.URL + "/get")
	if err != nil {
		t.Fatalf("GET through proxy: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok from target" {
		t.Errorf("body = %q, want %q", body, "ok from target")
	}
}

func TestProxy_DeniedHTTP(t *testing.T) {
	proxy := NewProxy([]string{"allowed.example.com"})
	if err := proxy.Start(); err != nil {
		t.Fatalf("proxy.Start() error: %v", err)
	}
	defer proxy.Stop()

	proxyURL, _ := url.Parse("http://" + proxy.Addr)
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get("http://denied.example.com/foo")
	if err != nil {
		t.Fatalf("GET through proxy: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestProxy_CONNECTAllowed(t *testing.T) {
	// Start a TLS target server.
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "tls ok")
	}))
	defer target.Close()

	targetURL, _ := url.Parse(target.URL)
	targetHost := targetURL.Host

	// Allow the target's hostname (without port for domain check).
	proxy := NewProxy([]string{targetURL.Hostname()})
	if err := proxy.Start(); err != nil {
		t.Fatalf("proxy.Start() error: %v", err)
	}
	defer proxy.Stop()

	// Manually send a CONNECT request.
	conn, err := net.DialTimeout("tcp", proxy.Addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()

	// Send CONNECT.
	fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", targetHost, targetHost)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status = %d, want 200", resp.StatusCode)
	}

	// Now upgrade to TLS and make a request.
	tlsConn := tls.Client(conn, &tls.Config{
		InsecureSkipVerify: true,
	})
	defer tlsConn.Close()

	req, _ := http.NewRequest("GET", "/", nil)
	req.Host = targetURL.Hostname()
	if err := req.Write(tlsConn); err != nil {
		t.Fatalf("write request: %v", err)
	}

	tlsResp, err := http.ReadResponse(bufio.NewReader(tlsConn), req)
	if err != nil {
		t.Fatalf("read TLS response: %v", err)
	}
	defer tlsResp.Body.Close()

	body, _ := io.ReadAll(tlsResp.Body)
	if string(body) != "tls ok" {
		t.Errorf("body = %q, want %q", body, "tls ok")
	}
}

func TestProxy_CONNECTDenied(t *testing.T) {
	proxy := NewProxy([]string{"allowed.example.com"})
	if err := proxy.Start(); err != nil {
		t.Fatalf("proxy.Start() error: %v", err)
	}
	defer proxy.Stop()

	conn, err := net.DialTimeout("tcp", proxy.Addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()

	fmt.Fprintf(conn, "CONNECT denied.example.com:443 HTTP/1.1\r\nHost: denied.example.com:443\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("CONNECT denied status = %d, want 403", resp.StatusCode)
	}
}

func TestProxy_WildcardMatching(t *testing.T) {
	tests := []struct {
		name    string
		domains []string
		host    string
		want    bool
	}{
		{"exact match", []string{"api.github.com"}, "api.github.com", true},
		{"exact mismatch", []string{"api.github.com"}, "evil.github.com", false},
		{"wildcard match", []string{"*.github.com"}, "api.github.com", true},
		{"wildcard deep match", []string{"*.github.com"}, "sub.api.github.com", true},
		{"wildcard root denied", []string{"*.github.com"}, "github.com", false},
		{"wildcard mismatch", []string{"*.github.com"}, "api.gitlab.com", false},
		{"case insensitive", []string{"API.GitHub.COM"}, "api.github.com", true},
		{"multiple patterns", []string{"httpbin.org", "*.github.com"}, "api.github.com", true},
		{"multiple patterns exact", []string{"httpbin.org", "*.github.com"}, "httpbin.org", true},
		{"multiple patterns denied", []string{"httpbin.org", "*.github.com"}, "evil.com", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Proxy{AllowedDomains: tt.domains}
			got := p.domainAllowed(tt.host)
			if got != tt.want {
				t.Errorf("domainAllowed(%q) = %v, want %v", tt.host, got, tt.want)
			}
		})
	}
}

func TestProxy_StopCleansUp(t *testing.T) {
	proxy := NewProxy([]string{"example.com"})
	if err := proxy.Start(); err != nil {
		t.Fatalf("proxy.Start() error: %v", err)
	}

	addr := proxy.Addr

	// Verify it's listening.
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("could not connect to proxy: %v", err)
	}
	conn.Close()

	// Stop it.
	if err := proxy.Stop(); err != nil {
		t.Fatalf("proxy.Stop() error: %v", err)
	}

	// Verify it's no longer listening.
	conn, err = net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Error("proxy still accepting connections after Stop")
	}
}

func TestProxy_MissingHost(t *testing.T) {
	proxy := NewProxy([]string{"example.com"})
	if err := proxy.Start(); err != nil {
		t.Fatalf("proxy.Start() error: %v", err)
	}
	defer proxy.Stop()

	// Send a non-proxy request (no absolute URL).
	req, _ := http.NewRequest("GET", "http://"+proxy.Addr+"/foo", nil)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want 400; body: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}
