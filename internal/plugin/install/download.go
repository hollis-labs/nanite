package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"time"
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
	Client   *http.Client
	MaxBytes int64
	Timeout  time.Duration
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

	client := d.Client
	if client == nil {
		client = http.DefaultClient
	}
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
		out:       out,
		total:     resp.ContentLength,
		emit:      emit,
		pluginID:  pluginID,
		state:     StateDownloading,
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

// deriveArchiveName extracts a safe file name from rawURL, ignoring query
// strings and fragments. Falls back to "plugin.tar.gz" when the URL has
// no usable path component.
func deriveArchiveName(rawURL string) string {
	u, err := url.Parse(rawURL)
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
