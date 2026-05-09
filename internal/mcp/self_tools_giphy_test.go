package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// withGiphyEndpoint swaps the package-level GIPHY endpoint for the duration
// of the test and restores it after. Tests must run sequentially in a
// package by default, so a plain defer restore is safe.
func withGiphyEndpoint(t *testing.T, url string) {
	t.Helper()
	orig := giphySearchEndpoint
	giphySearchEndpoint = url
	t.Cleanup(func() { giphySearchEndpoint = orig })
}

// giphyHappyResponse mimics the relevant subset of the GIPHY API payload.
const giphyHappyResponse = `{
  "data": [
    {
      "title": "Celebration GIF",
      "alt_text": "A celebratory gif",
      "images": {
        "original": {"url": "https://media.giphy.com/media/test/giphy.gif"},
        "fixed_width": {"url": "https://media.giphy.com/media/test/200w.gif"}
      }
    }
  ]
}`

// giphyTwoHitResponse covers the limit>1 array form.
const giphyTwoHitResponse = `{
  "data": [
    {
      "title": "First",
      "alt_text": "first",
      "images": {"original": {"url": "https://media.giphy.com/media/a/giphy.gif"}, "fixed_width": {"url":""}}
    },
    {
      "title": "Second",
      "alt_text": "second",
      "images": {"original": {"url": "https://media.giphy.com/media/b/giphy.gif"}, "fixed_width": {"url":""}}
    }
  ]
}`

const giphyEmptyResponse = `{"data": []}`

// TestCallGiphySearch_LiveHappyPath_SingleHit covers the limit=1 default
// shape: a flat object with gif_url + title + attribution + alt_text.
func TestCallGiphySearch_LiveHappyPath_SingleHit(t *testing.T) {
	t.Setenv("GIPHY_API_KEY", "fake-key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("api_key"); got != "fake-key" {
			t.Errorf("api_key query: want fake-key got %q", got)
		}
		if got := r.URL.Query().Get("q"); got != "celebration" {
			t.Errorf("q query: want celebration got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, giphyHappyResponse)
	}))
	defer srv.Close()
	withGiphyEndpoint(t, srv.URL)

	st := newSelfTools(t)
	res, err := st.callGiphySearch(map[string]any{"query": "celebration"})
	if err != nil {
		t.Fatalf("callGiphySearch returned transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error result: %s", readToolText(t, res))
	}

	var hit giphySearchHit
	if err := json.Unmarshal([]byte(readToolText(t, res)), &hit); err != nil {
		t.Fatalf("result not parseable as giphySearchHit: %v body=%s", err, readToolText(t, res))
	}
	if hit.GifURL != "https://media.giphy.com/media/test/giphy.gif" {
		t.Errorf("gif_url: got %q", hit.GifURL)
	}
	if hit.Title != "Celebration GIF" {
		t.Errorf("title: got %q", hit.Title)
	}
	if hit.Attribution != "GIPHY" {
		t.Errorf("attribution: want GIPHY got %q", hit.Attribution)
	}
	if hit.AltText != "A celebratory gif" {
		t.Errorf("alt_text: got %q", hit.AltText)
	}
}

// TestCallGiphySearch_LiveLimitGreaterThanOne_ReturnsArrayShape verifies the
// limit>1 path emits {results: [...]} so the agent has a stable shape it
// can iterate without indexing.
func TestCallGiphySearch_LiveLimitGreaterThanOne_ReturnsArrayShape(t *testing.T) {
	t.Setenv("GIPHY_API_KEY", "fake-key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("limit"); got != "2" {
			t.Errorf("limit query: want 2 got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, giphyTwoHitResponse)
	}))
	defer srv.Close()
	withGiphyEndpoint(t, srv.URL)

	st := newSelfTools(t)
	res, err := st.callGiphySearch(map[string]any{"query": "x", "limit": float64(2)})
	if err != nil {
		t.Fatalf("transport err: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(readToolText(t, res)), &got); err != nil {
		t.Fatalf("result not parseable: %v body=%s", err, readToolText(t, res))
	}
	results, ok := got["results"].([]any)
	if !ok {
		t.Fatalf("expected results array, got %v", got)
	}
	if len(results) != 2 {
		t.Fatalf("len(results) = %d, want 2", len(results))
	}
	first, _ := results[0].(map[string]any)
	if first["gif_url"] != "https://media.giphy.com/media/a/giphy.gif" {
		t.Errorf("results[0].gif_url: got %v", first["gif_url"])
	}
}

// TestCallGiphySearch_NoResults_StructuredError covers the empty-data path.
// The agent gets {error: "no_results", query: ...} so it can branch
// programmatically — IsError stays false because the tool *call* succeeded.
func TestCallGiphySearch_NoResults_StructuredError(t *testing.T) {
	t.Setenv("GIPHY_API_KEY", "fake-key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, giphyEmptyResponse)
	}))
	defer srv.Close()
	withGiphyEndpoint(t, srv.URL)

	st := newSelfTools(t)
	res, err := st.callGiphySearch(map[string]any{"query": "obscure"})
	if err != nil {
		t.Fatalf("transport err: %v", err)
	}
	if res.IsError {
		t.Fatalf("structured no_results should not flip IsError; body=%s", readToolText(t, res))
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(readToolText(t, res)), &got); err != nil {
		t.Fatalf("result not parseable: %v body=%s", err, readToolText(t, res))
	}
	if got["error"] != "no_results" {
		t.Errorf("error: want no_results got %v", got["error"])
	}
	if got["query"] != "obscure" {
		t.Errorf("query: want obscure got %v", got["query"])
	}
}

// TestCallGiphySearch_HTTPError_StructuredError covers the upstream-failure
// path (5xx, in this case).
func TestCallGiphySearch_HTTPError_StructuredError(t *testing.T) {
	t.Setenv("GIPHY_API_KEY", "fake-key")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()
	withGiphyEndpoint(t, srv.URL)

	st := newSelfTools(t)
	res, err := st.callGiphySearch(map[string]any{"query": "x"})
	if err != nil {
		t.Fatalf("transport err: %v", err)
	}
	if res.IsError {
		t.Fatalf("http_error result should be structured, not IsError; body=%s", readToolText(t, res))
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(readToolText(t, res)), &got); err != nil {
		t.Fatalf("result not parseable: %v body=%s", err, readToolText(t, res))
	}
	if got["error"] != "http_error" {
		t.Errorf("error: want http_error got %v", got["error"])
	}
	if details, _ := got["details"].(string); !strings.Contains(details, "502") {
		t.Errorf("details should mention status 502: %v", got["details"])
	}
}

// TestCallGiphySearch_DemoMode_KeywordHit covers the no-API-key fallback.
// Picks a deterministic curated GIF when the query contains a known keyword.
func TestCallGiphySearch_DemoMode_KeywordHit(t *testing.T) {
	t.Setenv("GIPHY_API_KEY", "")
	st := newSelfTools(t)
	res, err := st.callGiphySearch(map[string]any{"query": "huge celebration tonight"})
	if err != nil {
		t.Fatalf("transport err: %v", err)
	}
	if res.IsError {
		t.Fatalf("demo mode should succeed; body=%s", readToolText(t, res))
	}
	var hit giphySearchHit
	if err := json.Unmarshal([]byte(readToolText(t, res)), &hit); err != nil {
		t.Fatalf("result not parseable: %v body=%s", err, readToolText(t, res))
	}
	if hit.GifURL != giphyDemoGifs["celebration"] {
		t.Errorf("expected celebration demo URL, got %q", hit.GifURL)
	}
	if hit.Attribution != "GIPHY (demo mode)" {
		t.Errorf("attribution: want demo mode got %q", hit.Attribution)
	}
}

// TestCallGiphySearch_DemoMode_NoKeyword_DeterministicFallback verifies the
// hash-based pick stays stable across calls so demo CI is repeatable.
func TestCallGiphySearch_DemoMode_NoKeyword_DeterministicFallback(t *testing.T) {
	t.Setenv("GIPHY_API_KEY", "")
	st := newSelfTools(t)

	first, _ := st.callGiphySearch(map[string]any{"query": "zztop xyzzy"})
	second, _ := st.callGiphySearch(map[string]any{"query": "zztop xyzzy"})

	if readToolText(t, first) != readToolText(t, second) {
		t.Fatalf("demo-mode result for the same query should be deterministic; got divergent payloads")
	}
}

// TestCallGiphySearch_DemoMode_LimitGreaterThanOne_ReturnsArrayShape covers
// the limit>1 demo-mode case.
func TestCallGiphySearch_DemoMode_LimitGreaterThanOne_ReturnsArrayShape(t *testing.T) {
	t.Setenv("GIPHY_API_KEY", "")
	st := newSelfTools(t)
	res, err := st.callGiphySearch(map[string]any{"query": "celebration", "limit": float64(3)})
	if err != nil {
		t.Fatalf("transport err: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(readToolText(t, res)), &got); err != nil {
		t.Fatalf("result not parseable: %v body=%s", err, readToolText(t, res))
	}
	results, ok := got["results"].([]any)
	if !ok {
		t.Fatalf("expected results array, got %v", got)
	}
	if len(results) != 3 {
		t.Errorf("len(results) = %d, want 3", len(results))
	}
}

// TestCallGiphySearch_RejectsEmptyQuery covers the input-validation gate.
func TestCallGiphySearch_RejectsEmptyQuery(t *testing.T) {
	st := newSelfTools(t)
	res, err := st.callGiphySearch(map[string]any{})
	if err != nil {
		t.Fatalf("transport err: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected IsError for missing query, got: %s", readToolText(t, res))
	}
}

// TestCallGiphySearch_LimitClampedToMax checks the limit cap so a runaway
// agent can't request a hundred GIFs.
func TestCallGiphySearch_LimitClampedToMax(t *testing.T) {
	t.Setenv("GIPHY_API_KEY", "fake-key")
	var seenLimit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenLimit = r.URL.Query().Get("limit")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, giphyHappyResponse)
	}))
	defer srv.Close()
	withGiphyEndpoint(t, srv.URL)

	st := newSelfTools(t)
	if _, err := st.callGiphySearch(map[string]any{"query": "x", "limit": float64(99)}); err != nil {
		t.Fatalf("transport err: %v", err)
	}
	if seenLimit != fmt.Sprint(giphyMaxLimit) {
		t.Errorf("limit clamp: server saw %q, want %d", seenLimit, giphyMaxLimit)
	}
}

// TestCallGiphySearch_ListedAsTool covers the registry surface so the
// dispatcher and discovery layers see the new name.
func TestCallGiphySearch_ListedAsTool(t *testing.T) {
	st := newSelfTools(t)
	tools, err := st.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range tools {
		if tool.Name == "giphy_search" {
			return
		}
	}
	t.Errorf("giphy_search not registered in ListTools output")
}
