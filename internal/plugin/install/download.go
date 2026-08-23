package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	neturl "net/url"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/hollis-labs/nanite/internal/ssrf"
)

// DefaultMaxArchiveBytes caps plugin archive size at 100 MiB. Matches the
// existing signature/archive handling in internal/plugin.
const DefaultMaxArchiveBytes = int64(100 * 1024 * 1024)

// DefaultDownloadTimeout bounds a single download. 2 minutes is generous for
// a 100MiB archive on a slow link, and short enough to fail fast on a stall.
const DefaultDownloadTimeout = 2 * time.Minute

// HTTPDownloader fetches a plugin archive over HTTP(S) with a size cap,
// overall-request timeout, and progress events.
type HTTPDownloader struct {
	Client         *http.Client
	MaxBytes       int64
	Timeout        time.Duration
	Resolver       ssrf.Resolver
	Dialer         func(ctx context.Context, network, addr string) (net.Conn, error)
	AllowLocalhost bool
}

// Download fetches url into destDir as "<basename(url)>" (or "plugin.tar.gz"
// if the URL has no usable basename) and returns the absolute path to the
// downloaded file. Emits progress events as bytes arrive.
func (d *HTTPDownloader) Download(ctx context.Context, url, destDir, pluginID string, emit EventFunc) (string, error) {
	if url == "" {
		return "", errors.New("download: empty url")
	}
	if destDir == "" {
		return "", errors.New("download: empty destDir")
	}

	parsed, err := neturl.Parse(url)
	if err != nil {
		return "", fmt.Errorf("download: parse url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("download: unsupported scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", errors.New("download: url missing host")
	}

	client := d.secureHTTPClient()
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = DefaultDownloadTimeout
	}
	maxBytes := d.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxArchiveBytes
	}

	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(rctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("download: new request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download: get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("download: http status %s", resp.Status)
	}

	// Reject early if Content-Length says the archive exceeds our cap.
	if resp.ContentLength > maxBytes {
		return "", fmt.Errorf("download: content-length %d exceeds cap %d", resp.ContentLength, maxBytes)
	}
	// ContentLength may be -1 (unknown); that's fine — LimitReader handles
	// the runtime cap.

	fileName := deriveArchiveName(url)
	dest := filepath.Join(destDir, fileName)

	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("download: create %s: %w", dest, err)
	}
	cleanup := func() { _ = os.Remove(dest) }
	closed := false
	defer func() {
		if !closed {
			_ = out.Close()
		}
	}()

	// LimitReader to a single byte past the cap so we can detect overrun.
	limited := io.LimitReader(resp.Body, maxBytes+1)
	pw := &progressWriter{
		out:      out,
		total:    resp.ContentLength,
		emit:     emit,
		pluginID: pluginID,
		state:    StateDownloading,
	}

	n, err := io.Copy(pw, limited)
	if err != nil {
		_ = out.Close()
		closed = true
		cleanup()
		return "", fmt.Errorf("download: copy: %w", err)
	}
	if n > maxBytes {
		_ = out.Close()
		closed = true
		cleanup()
		return "", fmt.Errorf("download: body exceeds cap %d", maxBytes)
	}
	if err := out.Close(); err != nil {
		closed = true
		cleanup()
		return "", fmt.Errorf("download: close: %w", err)
	}
	closed = true
	return dest, nil
}

// secureHTTPClient preserves non-transport client settings supplied by a
// caller, but always installs the shared SSRF policy at the actual dial seam.
// Each DNS answer is checked once and the returned IP literal is what gets
// dialed, so redirects are re-checked and DNS cannot rebind after validation.
func (d *HTTPDownloader) secureHTTPClient() *http.Client {
	client := &http.Client{}
	if d.Client != nil {
		*client = *d.Client
	}
	resolver := d.Resolver
	if resolver == nil {
		resolver = ssrf.DefaultResolver
	}
	innerDial := d.Dialer
	if innerDial == nil {
		dialer := &net.Dialer{Timeout: 10 * time.Second}
		innerDial = dialer.DialContext
	}
	client.Transport = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			pinned, err := ssrf.ResolveAndPin(ctx, resolver, host, d.AllowLocalhost)
			if err != nil {
				return nil, err
			}
			return innerDial(ctx, network, net.JoinHostPort(pinned.String(), port))
		},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		DisableKeepAlives:     true,
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("download: too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return fmt.Errorf("download: redirect to unsupported scheme %q", req.URL.Scheme)
		}
		return nil
	}
	return client
}

// deriveArchiveName extracts a safe file name from rawURL, ignoring query
// strings and fragments. Falls back to "plugin.tar.gz" when the URL has
// no usable path component.
func deriveArchiveName(rawURL string) string {
	u, err := neturl.Parse(rawURL)
	if err == nil && u.Path != "" {
		b := path.Base(u.Path)
		if b != "" && b != "." && b != "/" {
			return b
		}
	}
	return "plugin.tar.gz"
}

// progressWriter forwards writes to an underlying file and emits progress
// events keyed off optional Content-Length.
type progressWriter struct {
	out      io.Writer
	total    int64
	wrote    int64
	lastPct  int
	emit     EventFunc
	pluginID string
	state    State
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.out.Write(b)
	p.wrote += int64(n)
	if p.emit != nil && p.total > 0 {
		pct := int(float64(p.wrote) / float64(p.total) * 100)
		// Throttle to every 10%.
		if pct/10 != p.lastPct/10 {
			p.lastPct = pct
			p.emit(Event{
				PluginID: p.pluginID,
				State:    p.state,
				Progress: float64(p.wrote) / float64(p.total),
				Message:  fmt.Sprintf("downloading %d/%d bytes", p.wrote, p.total),
			})
		}
	}
	return n, err
}
