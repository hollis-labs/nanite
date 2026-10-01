// Package modelsdevtest keeps tests that boot a service container off the
// network: they get a local models.dev endpoint instead of the live ~5 MB
// catalog (CW-20260930-0105).
//
// The endpoint serves an empty catalog. A refresh therefore succeeds and
// changes nothing — tests see pkg/models' built-in defaults, exactly as with
// no network — and no fixture model leaks into the process-global model
// overlay that other tests in the same package read.
package modelsdevtest

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hollis-labs/go-modelsdev/modelsdev"
)

// emptyCatalog is a valid models.dev response with no providers.
const emptyCatalog = `{}`

// NewServer starts a local models.dev endpoint serving the empty catalog,
// closed when the test ends. Use its URL where only a URL can be passed,
// such as NANITE_MODELSDEV_URL for a whole `nanite serve`.
func NewServer(t testing.TB) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, emptyCatalog)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Options returns modelsdev options for ContainerConfig.ModelCatalogOptions:
// the local endpoint, its HTTP client, and a per-test cache directory.
func Options(t testing.TB) []modelsdev.Option {
	t.Helper()
	srv := NewServer(t)
	return []modelsdev.Option{
		modelsdev.WithURL(srv.URL),
		modelsdev.WithHTTPClient(srv.Client()),
		modelsdev.WithCacheDir(t.TempDir()),
	}
}

// LingerAfterCancel returns options whose fetch completes only d after its
// request is canceled, so the refresher outlives a cancel by d. It is how a
// test observes whether a stop path waits for the refresher or merely
// cancels it. No network is involved.
func LingerAfterCancel(t testing.TB, d time.Duration) []modelsdev.Option {
	t.Helper()
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       &lingeringBody{done: r.Context().Done(), delay: d, data: bytes.NewReader([]byte(emptyCatalog))},
			Request:    r,
		}, nil
	})}
	return []modelsdev.Option{
		modelsdev.WithURL("http://modelsdev.invalid/api.json"),
		modelsdev.WithHTTPClient(client),
		modelsdev.WithCacheDir(t.TempDir()),
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// lingeringBody blocks its first Read until the request is canceled, then
// waits delay more before serving data. It ignores the cancellation itself,
// standing in for a fetch that had already completed when the cancel landed.
type lingeringBody struct {
	done    <-chan struct{}
	delay   time.Duration
	data    *bytes.Reader
	started bool
}

func (b *lingeringBody) Read(p []byte) (int, error) {
	if !b.started {
		b.started = true
		<-b.done
		time.Sleep(b.delay)
	}
	return b.data.Read(p)
}

func (b *lingeringBody) Close() error { return nil }
