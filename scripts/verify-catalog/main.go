// verify-catalog is a one-off utility used during the Track F.4 exit gate to
// confirm the live-deployed catalog.yaml verifies against the embedded
// CatalogRootKeyPEM. It is not shipped — callers run it via `go run`.
//
// Usage: go run ./scripts/verify-catalog <catalog-url> <sig-url>
// Defaults fetch from the Cloudflare Pages wildcard URL. After the CNAME for
// plugins.nanite.hollislabs.dev lands, prefer that URL (BLG follow-up).
package main

import (
	"crypto/ed25519"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/hollis-labs/nanite/internal/plugin/catalog"
)

const (
	defaultCatalogURL = "https://nanite-plugins-catalog.pages.dev/catalog.yaml"
	defaultSigURL     = "https://nanite-plugins-catalog.pages.dev/catalog.yaml.sig"
)

// After CNAME for plugins.nanite.hollislabs.dev lands, prefer that URL.

var httpClient = &http.Client{Timeout: 30 * time.Second}

func main() {
	catURL := defaultCatalogURL
	sigURL := defaultSigURL
	if len(os.Args) >= 2 {
		catURL = os.Args[1]
	}
	if len(os.Args) >= 3 {
		sigURL = os.Args[2]
	}

	blob := must(fetch(catURL))
	sig := must(fetch(sigURL))

	ok := ed25519.Verify(catalog.RootKey(), blob, sig)
	fmt.Printf("catalog_url=%s\nsig_url=%s\ncatalog_bytes=%d sig_bytes=%d verify=%v\n",
		catURL, sigURL, len(blob), len(sig), ok)
	if !ok {
		os.Exit(1)
	}
}

func fetch(rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse URL %q: %w", rawURL, err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("refusing non-https URL %q", rawURL)
	}
	// #nosec G107 -- one-off operator tool, URLs typed by user
	resp, err := httpClient.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: HTTP %d", rawURL, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
