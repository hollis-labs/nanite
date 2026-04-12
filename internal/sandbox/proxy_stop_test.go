package sandbox

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestProxy_Stop_DrainsStalledCONNECT regression-tests BLG-008 / audit
// finding 04: a CONNECT tunnel to a stalled upstream must not wedge
// Proxy.Stop. Before the fix, Stop would call http.Server.Shutdown
// (which explicitly does NOT close hijacked conns), then block forever
// on the serve waitgroup because the copy goroutines were both parked
// in io.Copy. The fix tracks in-flight conns, force-closes them in
// Stop, and waits on lifecycle.Manager with a bounded window.
func TestProxy_Stop_DrainsStalledCONNECT(t *testing.T) {
	// Upstream that accepts but never writes — simulates a server that
	// completes TLS handshake then stalls, which is a realistic
	// anti-scraping / slow-API behavior.
	stalled, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen stalled upstream: %v", err)
	}
	defer stalled.Close()
	var accepted atomic.Int32
	go func() {
		for {
			c, err := stalled.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			// Hold the conn open without reading or writing.
			go func(c net.Conn) {
				defer c.Close()
				time.Sleep(30 * time.Second)
			}(c)
		}
	}()

	stalledHost, stalledPortStr, _ := net.SplitHostPort(stalled.Addr().String())

	proxy := NewProxy([]string{"stalled.example.com"})
	proxy.AllowLocalhost = true
	proxy.ExtraCONNECTPorts = []string{stalledPortStr}
	// Use a tiny deadline so tunnel goroutines would exit on their own
	// even without Stop — belt + suspenders.
	proxy.connectDeadline = 200 * time.Millisecond
	// Resolve the allowlisted hostname to the stalled upstream's IP.
	proxy.Resolver = testProxyResolver(stalledHost)
	if err := proxy.Start(); err != nil {
		t.Fatalf("proxy.Start: %v", err)
	}

	// Open a CONNECT tunnel so a hijacked conn is parked in the proxy.
	conn, err := net.DialTimeout("tcp", proxy.Addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "CONNECT stalled.example.com:%s HTTP/1.1\r\nHost: stalled.example.com:%s\r\n\r\n",
		stalledPortStr, stalledPortStr)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("CONNECT status = %d, want 200", resp.StatusCode)
	}

	// Give the proxy a beat to register the hijacked conns.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		proxy.connsMu.Lock()
		n := len(proxy.conns)
		proxy.connsMu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Stop must return within the drain window. Before the fix this hung.
	start := time.Now()
	if err := proxy.Stop(); err != nil {
		t.Fatalf("proxy.Stop: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > proxyStopDrainWindow+2*time.Second {
		t.Fatalf("Stop took %v, want <= %v", elapsed, proxyStopDrainWindow+2*time.Second)
	}

	// Give spawned tunnel goroutines a moment to exit, then assert the
	// lifecycle manager shows zero active goroutines. This is a direct
	// assertion that no tunnel leaked.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if proxy.lc.Active() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := proxy.lc.Active(); n != 0 {
		// Emit the current stacks to aid debugging if this regresses.
		buf := make([]byte, 1<<16)
		n2 := runtime.Stack(buf, true)
		t.Fatalf("lifecycle.Active = %d after Stop, want 0\n%s", n, buf[:n2])
	}
}

// TestProxy_HTTP_HostHeaderNotForwarded regression-tests audit finding
// 04c: the proxy must not forward the client's arbitrary `Host` header
// to upstream. Upstream should see `Host: <resolved target host>`, not
// whatever the client sent.
func TestProxy_HTTP_HostHeaderNotForwarded(t *testing.T) {
	var upstreamHost atomic.Value // string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHost.Store(r.Host)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	targetURL, _ := url.Parse(target.URL)

	proxy := NewProxy([]string{targetURL.Hostname()})
	proxy.AllowLocalhost = true
	if err := proxy.Start(); err != nil {
		t.Fatalf("proxy.Start: %v", err)
	}
	defer proxy.Stop()

	proxyURL, _ := url.Parse("http://" + proxy.Addr)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   5 * time.Second,
	}

	// Client sends a bogus Host header — a naive proxy would propagate
	// "internal.example" to upstream, leaking an internal hostname and
	// potentially confusing vhost-based routing on the upstream server.
	// Using Header directly (not req.Host) so the proxy's URL-based
	// allowlist check still resolves to the real target host; the
	// assertion is only about what upstream receives, not about proxy
	// allowlist enforcement.
	req, _ := http.NewRequest("GET", target.URL+"/", nil)
	req.Header.Set("Host", "internal.example")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body=%s", resp.StatusCode, body)
	}

	got, _ := upstreamHost.Load().(string)
	if got == "internal.example" {
		t.Fatalf("upstream saw client-supplied Host %q, expected proxy to scrub it", got)
	}
	if !strings.Contains(got, targetURL.Host) {
		t.Fatalf("upstream Host = %q, want it to contain the real target host %q", got, targetURL.Host)
	}
}

// TestProxy_Stop_Idempotent verifies Stop can be called twice without
// error or panic (AgentExec defers proxy.Stop() after user code may
// have already called it).
func TestProxy_Stop_Idempotent(t *testing.T) {
	proxy := NewProxy([]string{"example.com"})
	if err := proxy.Start(); err != nil {
		t.Fatalf("proxy.Start: %v", err)
	}
	if err := proxy.Stop(); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	// Second Stop should be a no-op. We guard against a panic by
	// running it in a goroutine with a timeout.
	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("panic: %v", r)
				return
			}
			done <- nil
		}()
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = proxy.Stop()
		}()
		wg.Wait()
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("second Stop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second Stop hung")
	}
}
