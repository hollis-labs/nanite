// verify-catalog is a one-off utility used during the Track F.4 exit gate to
// confirm the live-deployed catalog.yaml verifies against the embedded
// CatalogRootKeyPEM. It is not shipped — callers run it via `go run`.
//
// Usage: go run ./scripts/verify-catalog <catalog-url> <sig-url>
// Defaults fetch from plugins.nanite.hollislabs.dev.
package main

import (
	"crypto/ed25519"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/hollis-labs/nanite/internal/plugin/catalog"
)

func main() {
	catURL := "https://plugins.nanite.hollislabs.dev/catalog.yaml"
	sigURL := "https://plugins.nanite.hollislabs.dev/catalog.yaml.sig"
	if len(os.Args) >= 2 { catURL = os.Args[1] }
	if len(os.Args) >= 3 { sigURL = os.Args[2] }

	blob := must(fetch(catURL))
	sig  := must(fetch(sigURL))

	ok := ed25519.Verify(catalog.RootKey(), blob, sig)
	fmt.Printf("catalog_url=%s\nsig_url=%s\ncatalog_bytes=%d sig_bytes=%d verify=%v\n",
		catURL, sigURL, len(blob), len(sig), ok)
	if !ok { os.Exit(1) }
}

func fetch(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
func must[T any](v T, err error) T { if err != nil { panic(err) }; return v }
