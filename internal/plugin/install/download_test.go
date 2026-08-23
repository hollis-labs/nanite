package install

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/nanite/internal/ssrf"
)

func TestDownload_HappyPath(t *testing.T) {
	body := []byte("archive body bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	d := &HTTPDownloader{AllowLocalhost: true}
	path, err := d.Download(context.Background(), srv.URL+"/plugin.tar.gz", dir, "giphy", nil)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if filepath.Base(path) != "plugin.tar.gz" {
		t.Errorf("base = %q, want plugin.tar.gz", filepath.Base(path))
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("body = %q, want %q", got, body)
	}
}

func TestDownload_ContentLengthOverCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "99999999")
		_, _ = w.Write(make([]byte, 10))
	}))
	defer srv.Close()
	d := &HTTPDownloader{MaxBytes: 100, AllowLocalhost: true}
	_, err := d.Download(context.Background(), srv.URL+"/a.tgz", t.TempDir(), "x", nil)
	if err == nil || !strings.Contains(err.Error(), "content-length") {
		t.Fatalf("err = %v", err)
	}
}

func TestDownload_BodyOverCap(t *testing.T) {
	// Unknown Content-Length; stream exceeds cap.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Don't set Content-Length → chunked.
		for i := 0; i < 10; i++ {
			_, _ = w.Write(make([]byte, 100))
		}
	}))
	defer srv.Close()
	d := &HTTPDownloader{MaxBytes: 250, AllowLocalhost: true}
	_, err := d.Download(context.Background(), srv.URL+"/a.tgz", t.TempDir(), "x", nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds cap") {
		t.Fatalf("err = %v", err)
	}
}

func TestDownload_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer srv.Close()
	d := &HTTPDownloader{AllowLocalhost: true}
	_, err := d.Download(context.Background(), srv.URL+"/a.tgz", t.TempDir(), "x", nil)
	if err == nil || !strings.Contains(err.Error(), "http status") {
		t.Fatalf("err = %v", err)
	}
}

func TestDownload_Timeout(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10")
		f, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("x"))
		if f != nil {
			f.Flush()
		}
		time.Sleep(500 * time.Millisecond)
	}))
	defer slow.Close()
	d := &HTTPDownloader{Timeout: 50 * time.Millisecond, AllowLocalhost: true}
	_, err := d.Download(context.Background(), slow.URL+"/a.tgz", t.TempDir(), "x", nil)
	if err == nil {
		t.Fatal("want timeout error")
	}
}

func TestDownload_EmitsProgress(t *testing.T) {
	body := make([]byte, 2000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		// Write in chunks so the progress writer sees multiple calls.
		for i := 0; i < len(body); i += 200 {
			_, _ = w.Write(body[i : i+200])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer srv.Close()

	var events []Event
	emit := func(e Event) { events = append(events, e) }
	d := &HTTPDownloader{AllowLocalhost: true}
	_, err := d.Download(context.Background(), srv.URL+"/a.tgz", t.TempDir(), "giphy", emit)
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(events) == 0 {
		t.Error("want at least one progress event")
	}
	for _, e := range events {
		if e.State != StateDownloading {
			t.Errorf("event state = %q, want %q", e.State, StateDownloading)
		}
		if e.PluginID != "giphy" {
			t.Errorf("event plugin id = %q, want giphy", e.PluginID)
		}
	}
}

func TestDownload_BlocksPrivateAndIMDSDestinations(t *testing.T) {
	tests := map[string]string{
		"rfc1918":          "10.0.0.1",
		"ipv4-loopback":    "127.0.0.1",
		"imds-link-local":  "169.254.169.254",
		"ipv6-link-local":  "fe80::1",
		"cgnat":            "100.64.0.1",
		"ipv6-ula":         "fd00::1",
		"ipv4-unspecified": "0.0.0.0",
		"ipv6-unspecified": "::",
	}
	for name, blocked := range tests {
		t.Run(name, func(t *testing.T) {
			dialed := false
			d := &HTTPDownloader{
				Resolver: func(context.Context, string) ([]net.IP, error) {
					return []net.IP{net.ParseIP(blocked)}, nil
				},
				Dialer: func(context.Context, string, string) (net.Conn, error) {
					dialed = true
					return nil, errors.New("unexpected dial")
				},
			}
			_, err := d.Download(context.Background(), "https://catalog.example/plugin.tar.gz", t.TempDir(), "x", nil)
			if !errors.Is(err, ssrf.ErrBlocked) {
				t.Fatalf("Download error = %v, want ssrf.ErrBlocked", err)
			}
			if dialed {
				t.Fatal("blocked destination reached the dialer")
			}
		})
	}
}

func TestDownload_PinsValidatedDNSAnswer(t *testing.T) {
	var dialAddr string
	dialErr := errors.New("stop after pinned dial")
	d := &HTTPDownloader{
		Resolver: func(context.Context, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("203.0.113.25")}, nil
		},
		Dialer: func(_ context.Context, _ string, addr string) (net.Conn, error) {
			dialAddr = addr
			return nil, dialErr
		},
	}
	_, err := d.Download(context.Background(), "https://catalog.example/plugin.tar.gz", t.TempDir(), "x", nil)
	if !errors.Is(err, dialErr) {
		t.Fatalf("Download error = %v, want dial sentinel", err)
	}
	if dialAddr != "203.0.113.25:443" {
		t.Fatalf("dial address = %q, want pinned literal", dialAddr)
	}
}
